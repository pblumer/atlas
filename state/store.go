// Package state is Atlas's materialized state store: the queryable fold of
// the event log (ADR-0001), backed by Pebble (ADR-0003).
//
// State is never the source of truth — the WAL is. Durability therefore belongs
// to the WAL's fsync (ADR-0005), so state transactions commit without their own
// fsync (NoSync): after a crash the store may trail the log, and recovery
// replays events from [Store.LastAppliedPosition] forward to catch up. Because
// each transaction commits its mutations and the advanced position atomically,
// state and position can never disagree.
//
// Keys are organized into column-family indexes (see keys.go) so the engine's
// access patterns — "elements of this instance", "open jobs of this type",
// "timers due by now" — are prefix or range scans rather than full scans.
//
// A Store is owned by a single partition goroutine (invariant I3); it holds no
// locks of its own.
package state

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/cockroachdb/pebble"
	"github.com/pblumer/atlas/model"
)

// metaLastApplied keys the highest log position folded into the store.
const metaLastApplied = "last_applied_position"

// Store wraps a Pebble database.
type Store struct {
	db *pebble.DB
	// freeBatch caches one indexed batch for reuse across transactions. The
	// store is single-writer (invariant I3), so at most one transaction is live
	// at a time and a single cached batch suffices — this keeps NewTransaction
	// from allocating a Pebble batch every batch cycle (invariant I1).
	freeBatch *pebble.Batch
}

// Open opens (creating if needed) the state store rooted at dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	db, err := pebble.Open(dir, &pebble.Options{Merger: counterMerger})
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	// The runtime aggregate counters (ADR-0080) are derived state added after the
	// fact. A live store persists across restarts and replays only the tail, so the
	// counters would read zero for instances that already exist. Seed them once from
	// the current state; thereafter applyToState maintains them and replay rebuilds
	// the tail.
	if err := s.backfillRuntimeCountersIfNeeded(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("state: backfill runtime counters: %w", err)
	}
	// The finished-count and last-activity summary counters (ADR-0083) are a later
	// addition, so a store that already ran the ADR-0080 backfill still needs them
	// seeded once from the current active instances and history.
	if err := s.backfillSummaryCountersIfNeeded(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("state: backfill summary counters: %w", err)
	}
	// The engine-wide open-job / pending-timer / subscription counters (ADR-0142) are a
	// later addition again, and a store that already holds open work must not report
	// zero for it and then drift negative as that work finishes.
	if err := s.backfillRuntimeTotalsIfNeeded(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("state: backfill runtime totals: %w", err)
	}
	return s, nil
}

// Close flushes and closes the store.
func (s *Store) Close() error {
	if s.freeBatch != nil {
		s.freeBatch.Close()
		s.freeBatch = nil
	}
	return s.db.Close()
}

// NewTransaction starts a transaction. Reads through it see its own pending
// writes (it is an indexed batch). Mutations become visible only on Commit. The
// underlying batch is drawn from the store's cache when available, so steady-
// state processing does not allocate one (invariant I1).
func (s *Store) NewTransaction() *Tx {
	b := s.freeBatch
	if b != nil {
		s.freeBatch = nil
		b.Reset()
	} else {
		b = s.db.NewIndexedBatch()
	}
	return &Tx{b: b, store: s}
}

// recycle returns a finished batch to the cache for reuse, or closes it if one
// is already cached.
func (s *Store) recycle(b *pebble.Batch) error {
	if s.freeBatch == nil {
		s.freeBatch = b
		return nil
	}
	return b.Close()
}

// LastAppliedPosition returns the highest log position folded into committed
// state, or 0 if none has been applied yet (genesis).
func (s *Store) LastAppliedPosition() (uint64, error) {
	raw, ok, err := getCopy(s.db, keyMeta(metaLastApplied))
	if err != nil || !ok {
		return 0, err
	}
	if len(raw) != 8 {
		return 0, fmt.Errorf("state: corrupt last-applied position (%d bytes)", len(raw))
	}
	return binary.BigEndian.Uint64(raw), nil
}

// ElementInstancesOfProcess calls fn with the key of every element instance
// belonging to the given process instance, via the elByProc index.
func (s *Store) ElementInstancesOfProcess(procKey uint64, fn func(elKey uint64) error) error {
	return s.scanPrefix(elByProcPrefix(procKey), func(k, _ []byte) error {
		return fn(trailingKey(k))
	})
}

// ActivatableJobs calls fn with the key of every open job of the given type,
// via the jobActivatable index — the worker-polling access pattern.
func (s *Store) ActivatableJobs(jobType int32, fn func(jobKey uint64) error) error {
	return s.scanPrefix(jobActivatablePrefix(jobType), func(k, _ []byte) error {
		return fn(trailingKey(k))
	})
}

// ActivatableJobsDesc calls fn with the key of every open job of the given type in
// DESCENDING key order (newest first), starting just below `before` — before == 0
// starts from the newest key. It backs the task inbox's newest-first, cursor-paged
// listing (GET /tasks?before=): a flood parks its oldest tasks at the low keys, so
// paging from the newest downward surfaces the tasks an operator most likely wants
// first while still bounding the scan. Worker polling keeps using ActivatableJobs
// (oldest-first, FIFO) — this is a read-side ordering only.
func (s *Store) ActivatableJobsDesc(jobType int32, before uint64, fn func(jobKey uint64) error) error {
	lo := jobActivatablePrefix(jobType)
	hi := prefixEnd(lo)
	if before != 0 {
		// UpperBound is exclusive, so this starts at the highest key strictly below
		// `before` — the natural cursor for "the next older page".
		hi = keyJobActivatable(jobType, before)
	}
	return s.scanRangeDesc(lo, hi, func(k, _ []byte) error {
		return fn(trailingKey(k))
	})
}

// JobOfElement returns the key of the job held by the given element instance, or
// ok=false if it holds none. It is the read-side counterpart of Tx.JobOfElement,
// used to resolve one instance's open jobs through the element→job reverse index
// rather than scanning the global activatable index.
func (s *Store) JobOfElement(elKey uint64) (uint64, bool, error) {
	raw, ok, err := getCopy(s.db, keyJobByElement(elKey))
	if err != nil || !ok {
		return 0, false, err
	}
	return binary.BigEndian.Uint64(raw), true, nil
}

// AllActivatableJobs calls fn with the key of every open job of ANY type, via the
// same jobActivatable index — the whole column family rather than one job type's
// slice. It backs the read side of the operator complete/fail affordance (listing
// the jobs an instance is parked on); worker polling still uses the type-scoped
// ActivatableJobs.
func (s *Store) AllActivatableJobs(fn func(jobKey uint64) error) error {
	return s.scanPrefix([]byte{byte(cfJobActivatable)}, func(k, _ []byte) error {
		return fn(trailingKey(k))
	})
}

// DueTimers calls fn for every timer whose due date is at or before now, in due
// order. Because the due date is the index prefix, this is a range scan from the
// start of the timer family up to now — no scheduler structure, no full scan.
func (s *Store) DueTimers(now int64, fn func(timerKey uint64, v *model.TimerValue) error) error {
	lo := []byte{byte(cfTimer)}
	hi := prefixEnd(appendOrderedInt64([]byte{byte(cfTimer)}, now))
	return s.scanRange(lo, hi, func(k, raw []byte) error {
		v, err := model.DecodeValue(model.VTTimer, raw)
		if err != nil {
			return err
		}
		return fn(trailingKey(k), v.(*model.TimerValue))
	})
}

// StartTimers calls fn for every armed start timer — one whose owning process
// instance key is zero, so it instantiates a definition on fire rather than
// continuing a waiting element (ADR-0051). It is a full scan of the timer family,
// used only when a definition is (re)deployed (off the hot path), so arming can be
// idempotent and can supersede a prior version's schedule.
func (s *Store) StartTimers(fn func(timerKey uint64, v *model.TimerValue) error) error {
	return s.scanPrefix([]byte{byte(cfTimer)}, func(k, raw []byte) error {
		v, err := model.DecodeValue(model.VTTimer, raw)
		if err != nil {
			return err
		}
		tv := v.(*model.TimerValue)
		if tv.ProcessInstanceKey != 0 {
			return nil // an instance-owned timer (catch/boundary), not a start timer
		}
		return fn(trailingKey(k), tv)
	})
}

// GetJob returns the committed job for key, reporting whether it was present.
// Unlike Tx.GetJob it reads outside a transaction, for queries such as a worker
// runner pulling activatable jobs.
func (s *Store) GetJob(key uint64) (*model.JobValue, bool, error) {
	raw, ok, err := getCopy(s.db, keyJob(key))
	if err != nil || !ok {
		return nil, ok, err
	}
	v, err := model.DecodeValue(model.VTJob, raw)
	if err != nil {
		return nil, false, err
	}
	return v.(*model.JobValue), true, nil
}

// GetIncident returns the committed incident on an element instance, or nil if
// there is none. It reads outside a transaction, for the resolve endpoint's
// existence check (ADR-0061).
func (s *Store) GetIncident(elKey uint64) (*model.IncidentValue, error) {
	raw, ok, err := getCopy(s.db, keyIncident(elKey))
	if err != nil || !ok {
		return nil, err
	}
	v, err := model.DecodeValue(model.VTIncident, raw)
	if err != nil {
		return nil, err
	}
	return v.(*model.IncidentValue), nil
}

// GetElementInstance returns the committed element instance for key, reporting
// whether it was present. Like GetJob it reads outside a transaction, for
// consumers such as the in-process DMN worker resolving the decision an
// activatable business-rule job belongs to.
func (s *Store) GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error) {
	return decodeElementInstance(getCopy(s.db, keyElementInstance(key)))
}

// decodeElementInstance turns a raw get into an element instance. It is shared by
// the live store and a [ReadView], so the two cannot decode the same bytes
// differently.
func decodeElementInstance(raw []byte, ok bool, err error) (*model.ElementInstanceValue, bool, error) {
	if err != nil || !ok {
		return nil, ok, err
	}
	v, err := model.DecodeValue(model.VTElementInstance, raw)
	if err != nil {
		return nil, false, err
	}
	return v.(*model.ElementInstanceValue), true, nil
}

// ActiveProcessInstanceCount returns how many process instances are live.
func (s *Store) ActiveProcessInstanceCount() (int, error) {
	return s.countPrefix([]byte{byte(cfProcessInstance)})
}

// IncidentCount returns how many unresolved incidents exist — how many tokens are
// parked waiting for an operator (ADR-0061). Counted from state by walking the
// incident family's keys, not from a maintained counter: an incident leaves state
// two ways, resolved by an operator *and* dropped with the element instance it sits
// on (an instance cancel or an interrupting boundary event, which announce no
// resolution), so a maintained number would drift while a scan cannot. The family
// holds one key per stuck token, which is the population an operator is expected to
// keep near zero.
func (s *Store) IncidentCount() (int, error) {
	return s.countPrefix([]byte{byte(cfIncident)})
}

// DefInstanceCount returns how many instances of one definition are live, read
// from the maintained per-definition counter in O(1) rather than scanning every
// instance (ADR-0080).
func (s *Store) DefInstanceCount(procDefKey uint64) (int, error) {
	raw, ok, err := getCopy(s.db, keyDefInstanceCount(procDefKey))
	if err != nil || !ok {
		return 0, err
	}
	return int(decodeCounter(raw)), nil
}

// TotalActiveInstances is how many process instances are live across every definition,
// summed from the maintained per-definition counters (ADR-0080) rather than by walking
// the instance records.
//
// The distinction is the whole point: the authoritative
// [Store.ActiveProcessInstanceCount] scans the runtime set, so it costs more the busier
// the engine is — fine for a request, wrong for something a Prometheus scrape takes every
// fifteen seconds. This sum reads one key per **deployed definition**, a number that
// changes only when someone deploys (ADR-0142).
//
// One honest qualification, measured rather than assumed (BenchmarkTotalActiveInstances):
// these are *merge* counters, so a read also folds in whatever operands Pebble has not
// compacted yet — right after a burst of starts the sum costs O(recent writes), not
// O(definitions). A flush collapses them, after which the sum is flat regardless of how
// many instances are running: 2,000 instances read as fast as 100. Flushes happen on
// their own, and the ADR-0131 checkpoint cadence forces one every few minutes, so the
// backlog is bounded in a running engine. Even un-compacted it stays cheaper than the
// scan it replaces.
func (s *Store) TotalActiveInstances() (int64, error) {
	return s.sumCounters([]byte{byte(cfDefInstanceCount)})
}

// TotalLiveTokens is how many element instances hold live tokens across every definition,
// summed from the maintained per-definition-element counters (ADR-0080). Bounded by the
// number of deployed *elements* — design-time size, not runtime population — for the same
// reason as [Store.TotalActiveInstances].
func (s *Store) TotalLiveTokens() (int64, error) {
	return s.sumCounters([]byte{byte(cfElementTokenCount)})
}

// sumCounters adds up every merge counter in a family. A counter whose value is too short
// to decode reads as zero rather than failing the whole sum: one unreadable key should
// cost a slightly low gauge, not a broken scrape.
func (s *Store) sumCounters(prefix []byte) (int64, error) {
	var total int64
	err := s.scanPrefix(prefix, func(_, raw []byte) error {
		total += decodeCounter(raw)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

// DefCompletedCount returns how many instances of one definition have finished
// (completed or terminated), from the maintained counter in O(1) rather than scanning
// the history (ADR-0083).
func (s *Store) DefCompletedCount(procDefKey uint64) (int, error) {
	raw, ok, err := getCopy(s.db, keyDefCompletedCount(procDefKey))
	if err != nil || !ok {
		return 0, err
	}
	return int(decodeCounter(raw)), nil
}

// DefLastActivity returns the unix-nano timestamp of a definition's most recent
// instance lifecycle event (0 if it has had none), read in O(1) (ADR-0083).
func (s *Store) DefLastActivity(procDefKey uint64) (int64, error) {
	raw, ok, err := getCopy(s.db, keyDefLastActivity(procDefKey))
	if err != nil || !ok {
		return 0, err
	}
	return int64(binary.BigEndian.Uint64(raw)), nil
}

// ElementLiveTokens calls fn with each of a definition's elements that currently
// holds live tokens and how many — one prefix scan over the per-element token
// counters, so it is O(elements), not O(instances) (ADR-0080).
func (s *Store) ElementLiveTokens(procDefKey uint64, fn func(elementId int32, count int64) error) error {
	return s.scanPrefix(runtimeCountPrefix(cfElementTokenCount, procDefKey), func(k, raw []byte) error {
		return fn(elementIdFromCountKey(k), decodeCounter(raw))
	})
}

// ElementVisitTotals calls fn with each of a definition's elements and its
// cumulative visit count — the aggregate heatmap, read in O(elements) from the
// maintained per-element visit counter instead of summing every instance's visit
// history (ADR-0080, aggregating ADR-0022).
func (s *Store) ElementVisitTotals(procDefKey uint64, fn func(elementId int32, count int64) error) error {
	return s.scanPrefix(runtimeCountPrefix(cfElementVisitAgg, procDefKey), func(k, raw []byte) error {
		return fn(elementIdFromCountKey(k), decodeCounter(raw))
	})
}

// metaRuntimeCountersV1 marks that the ADR-0080 runtime counters have been seeded
// from pre-existing state. Its presence makes the backfill a one-time migration.
const metaRuntimeCountersV1 = "runtime_counters_v1"

// backfillRuntimeCountersIfNeeded seeds the runtime aggregate counters from the
// current state the first time a store gains them, then records that it has done
// so. It scans the process-instance, element-instance, and element-visit families
// once, accumulating per-definition and per-(definition, element) totals in memory,
// and writes them plus the marker in a single atomic, synced batch — so a crash
// mid-migration leaves nothing and the next open re-runs cleanly (no double count).
func (s *Store) backfillRuntimeCountersIfNeeded() error {
	if _, ok, err := getCopy(s.db, keyMeta(metaRuntimeCountersV1)); err != nil || ok {
		return err
	}

	type defElem struct {
		def uint64
		el  int32
	}
	defCounts := map[uint64]int64{}
	tokenCounts := map[defElem]int64{}
	visitCounts := map[defElem]int64{}

	if err := s.scanPrefix([]byte{byte(cfProcessInstance)}, func(_, raw []byte) error {
		v, err := model.DecodeValue(model.VTProcessInstance, raw)
		if err != nil {
			return err
		}
		defCounts[v.(*model.ProcessInstanceValue).ProcessDefKey]++
		return nil
	}); err != nil {
		return err
	}
	if err := s.scanPrefix([]byte{byte(cfElementInstance)}, func(_, raw []byte) error {
		v, err := model.DecodeValue(model.VTElementInstance, raw)
		if err != nil {
			return err
		}
		ei := v.(*model.ElementInstanceValue)
		tokenCounts[defElem{ei.ProcessDefKey, ei.ElementId}]++
		return nil
	}); err != nil {
		return err
	}
	if err := s.scanPrefix([]byte{byte(cfElementVisit)}, func(k, raw []byte) error {
		// elVisit key: [cf][procDefKey:8][piKey:8][elementId:4]; value is the count.
		def := binary.BigEndian.Uint64(k[1:9])
		visitCounts[defElem{def, elementIdFromVisitKey(k)}] += decodeCounter(raw)
		return nil
	}); err != nil {
		return err
	}

	// Collect every counter merge into one list so the writes and their single error
	// check are shared across the three families.
	type counterMerge struct {
		key   []byte
		delta int64
	}
	merges := make([]counterMerge, 0, len(defCounts)+len(tokenCounts)+len(visitCounts))
	for def, n := range defCounts {
		merges = append(merges, counterMerge{keyDefInstanceCount(def), n})
	}
	for de, n := range tokenCounts {
		merges = append(merges, counterMerge{keyElementTokenCount(de.def, de.el), n})
	}
	for de, n := range visitCounts {
		merges = append(merges, counterMerge{keyElementVisitAgg(de.def, de.el), n})
	}

	b := s.db.NewBatch()
	defer b.Close()
	for _, m := range merges {
		if err := b.Merge(m.key, encodeCounter(m.delta), nil); err != nil {
			return err
		}
	}
	if err := b.Set(keyMeta(metaRuntimeCountersV1), []byte{1}, nil); err != nil {
		return err
	}
	return b.Commit(pebble.Sync)
}

// OpenJobs is how many jobs are currently waiting for a worker, engine-wide, read from
// the maintained counter rather than by scanning the job family (ADR-0142).
func (s *Store) OpenJobs() (int64, error) { return s.runtimeTotal(rtOpenJobs) }

// PendingTimers is how many timers are currently waiting to fire, engine-wide.
func (s *Store) PendingTimers() (int64, error) { return s.runtimeTotal(rtPendingTimers) }

// MessageSubscriptions is how many message subscriptions are currently waiting to
// correlate, engine-wide.
func (s *Store) MessageSubscriptions() (int64, error) { return s.runtimeTotal(rtMessageSubscriptions) }

// runtimeTotal reads one engine-wide live-entity counter. An unset counter is zero, not
// an error: a store that has never opened one of these has none open.
func (s *Store) runtimeTotal(kind runtimeTotalKind) (int64, error) {
	raw, ok, err := getCopy(s.db, keyRuntimeTotal(kind))
	if err != nil || !ok {
		return 0, err
	}
	return decodeCounter(raw), nil
}

// metaRuntimeTotalsV1 marks that the ADR-0142 engine-wide live-entity counters have
// been seeded from pre-existing state — a one-time migration, separate from the
// ADR-0080 and ADR-0083 markers so a store that already ran those still seeds these.
const metaRuntimeTotalsV1 = "runtime_totals_v1"

// backfillRuntimeTotalsIfNeeded seeds the engine-wide open-job, pending-timer and
// message-subscription counters from current state the first time a store gains them.
// Without it a store that already holds work would report zero for everything already
// open and then drift negative as that work finished — a gauge that is worse than
// absent, because it looks plausible.
//
// It scans each family once and writes the counts plus the marker in a single atomic,
// synced batch, so a crash mid-migration leaves nothing behind and the next open re-runs
// cleanly rather than double-counting (the ADR-0080 backfill's shape).
func (s *Store) backfillRuntimeTotalsIfNeeded() error {
	if _, ok, err := getCopy(s.db, keyMeta(metaRuntimeTotalsV1)); err != nil || ok {
		return err
	}

	counts := map[runtimeTotalKind]int64{}
	for _, fam := range []struct {
		cf   columnFamily
		kind runtimeTotalKind
	}{
		{cfJob, rtOpenJobs},
		{cfTimer, rtPendingTimers},
		{cfMessageSubscription, rtMessageSubscriptions},
	} {
		n, err := s.countPrefix([]byte{byte(fam.cf)})
		if err != nil {
			return err
		}
		counts[fam.kind] = int64(n)
	}

	b := s.db.NewBatch()
	defer b.Close()
	for kind, n := range counts {
		if n == 0 {
			continue // nothing open: leave the counter unset, which already reads as zero
		}
		if err := b.Merge(keyRuntimeTotal(kind), encodeCounter(n), nil); err != nil {
			return err
		}
	}
	if err := b.Set(keyMeta(metaRuntimeTotalsV1), []byte{1}, nil); err != nil {
		return err
	}
	return b.Commit(pebble.Sync)
}

// metaSummaryCountersV1 marks that the ADR-0083 finished-count and last-activity
// counters have been seeded from pre-existing state — a one-time migration, separate
// from metaRuntimeCountersV1 so a store that already ran the ADR-0080 backfill still
// seeds these.
const metaSummaryCountersV1 = "summary_counters_v1"

// backfillSummaryCountersIfNeeded seeds the per-definition finished-instance count and
// last-activity timestamp from current state the first time a store gains them. It
// scans the active process instances (for each definition's newest CreatedAt) and the
// history (finished counts and newest CompletedAt), then writes the counts as merges
// and the last-activity as a set, plus the marker, in one atomic synced batch — so a
// crash mid-migration leaves nothing and the next open re-runs cleanly.
func (s *Store) backfillSummaryCountersIfNeeded() error {
	if _, ok, err := getCopy(s.db, keyMeta(metaSummaryCountersV1)); err != nil || ok {
		return err
	}

	completed := map[uint64]int64{}
	lastActivity := map[uint64]int64{}
	bump := func(def uint64, ts int64) {
		if ts > lastActivity[def] {
			lastActivity[def] = ts
		}
	}
	if err := s.scanPrefix([]byte{byte(cfProcessInstance)}, func(_, raw []byte) error {
		v, err := model.DecodeValue(model.VTProcessInstance, raw)
		if err != nil {
			return err
		}
		pi := v.(*model.ProcessInstanceValue)
		bump(pi.ProcessDefKey, pi.CreatedAt)
		return nil
	}); err != nil {
		return err
	}
	if err := s.scanPrefix([]byte{byte(cfProcessInstanceHistory)}, func(_, raw []byte) error {
		v, err := model.DecodeValue(model.VTProcessInstance, raw)
		if err != nil {
			return err
		}
		pi := v.(*model.ProcessInstanceValue)
		completed[pi.ProcessDefKey]++
		bump(pi.ProcessDefKey, pi.CompletedAt)
		if pi.CreatedAt > lastActivity[pi.ProcessDefKey] {
			lastActivity[pi.ProcessDefKey] = pi.CreatedAt
		}
		return nil
	}); err != nil {
		return err
	}

	b := s.db.NewBatch()
	defer b.Close()
	for def, n := range completed {
		if err := b.Merge(keyDefCompletedCount(def), encodeCounter(n), nil); err != nil {
			return err
		}
	}
	for def, ts := range lastActivity {
		if err := b.Set(keyDefLastActivity(def), appendBE64(nil, uint64(ts)), nil); err != nil {
			return err
		}
	}
	if err := b.Set(keyMeta(metaSummaryCountersV1), []byte{1}, nil); err != nil {
		return err
	}
	return b.Commit(pebble.Sync)
}

// InjectCorruptProcessInstance writes an undecodable record under a process
// instance's key. It is a test/tooling affordance only — it lets a caller in another
// package exercise the decode-error path of the active-instance scan
// (ActiveProcessInstances) that operator read/admin handlers depend on. Production
// code writes process instances through Tx.PutProcessInstance, never this.
func (s *Store) InjectCorruptProcessInstance(key uint64) error {
	return s.db.Set(keyProcessInstance(key), []byte{0x01}, pebble.NoSync)
}

// InjectCorruptElementInstance writes an undecodable record under an element
// instance's key. Like InjectCorruptProcessInstance it is a test/tooling affordance
// only — it lets a caller in another package exercise the decode-error path of a
// GetElementInstance read (e.g. the set-variables handler's scope validation).
// Production code writes element instances through Tx.PutElementInstance, never this.
func (s *Store) InjectCorruptElementInstance(key uint64) error {
	return s.db.Set(keyElementInstance(key), []byte{0x01}, pebble.NoSync)
}

// InjectCorruptIncident writes an undecodable record under an incident's key — the
// incident-list counterpart of InjectCorruptProcessInstance. It lets a caller exercise
// the decode-error branch of the incident scan (Incidents), which the operator "what's
// stuck" list depends on to surface a 500 rather than silently drop rows. Test/tooling
// only; production writes incidents through Tx.PutIncident.
func (s *Store) InjectCorruptIncident(elKey uint64) error {
	return s.db.Set(keyIncident(elKey), []byte{0x01}, pebble.NoSync)
}

// ActiveProcessInstances calls fn with the key and value of every live process
// instance, via the process-instance column family — the operator "list running
// instances" access pattern.
func (s *Store) ActiveProcessInstances(fn func(key uint64, v *model.ProcessInstanceValue) error) error {
	return s.scanPrefix([]byte{byte(cfProcessInstance)}, func(k, raw []byte) error {
		v, err := model.DecodeValue(model.VTProcessInstance, raw)
		if err != nil {
			return err
		}
		return fn(trailingKey(k), v.(*model.ProcessInstanceValue))
	})
}

// CompletedProcessInstances calls fn with the key and value of every process
// instance that has reached a terminal state, via the history column family —
// the operator "list finished instances" access pattern (ADR-0017). Each value
// carries its terminal State and CompletedAt.
func (s *Store) CompletedProcessInstances(fn func(key uint64, v *model.ProcessInstanceValue) error) error {
	return s.scanPrefix([]byte{byte(cfProcessInstanceHistory)}, func(k, raw []byte) error {
		v, err := model.DecodeValue(model.VTProcessInstance, raw)
		if err != nil {
			return err
		}
		return fn(trailingKey(k), v.(*model.ProcessInstanceValue))
	})
}

// errScanWindowFull halts a bounded scan once its per-tick cap is reached; it never
// escapes CompletedProcessInstancesFrom.
var errScanWindowFull = errors.New("state: scan window full")

// CompletedProcessInstancesFrom scans finished instances in key order starting at
// startKey, invoking fn for up to limit of them, and returns the key to resume from
// next and whether the window filled (more may remain). It gives the retention sweep
// a bounded, resumable window so one tick never scans the whole history family on the
// run loop (ADR-0115, honoring the ADR-0085 no-full-scan rule). When the scan reaches
// the end, more is false and the caller restarts from genesis.
func (s *Store) CompletedProcessInstancesFrom(startKey uint64, limit int, fn func(key uint64, v *model.ProcessInstanceValue) error) (next uint64, more bool, err error) {
	lo := keyProcessInstanceHistory(startKey)
	hi := prefixEnd([]byte{byte(cfProcessInstanceHistory)})
	n := 0
	scanErr := s.scanRange(lo, hi, func(k, raw []byte) error {
		if n >= limit {
			more = true
			return errScanWindowFull
		}
		v, derr := model.DecodeValue(model.VTProcessInstance, raw)
		if derr != nil {
			return derr
		}
		key := trailingKey(k)
		if ferr := fn(key, v.(*model.ProcessInstanceValue)); ferr != nil {
			return ferr
		}
		next = key + 1
		n++
		return nil
	})
	if errors.Is(scanErr, errScanWindowFull) {
		scanErr = nil
	}
	if scanErr != nil {
		return 0, false, scanErr
	}
	return next, more, nil
}

// DueHistoryExpiries calls fn with the purge due date and instance key of every finished
// instance whose history TTL has elapsed by now, in due-date order, for up to limit of
// them; it reports whether the window filled (more may be due). This is the retention
// sweep's candidate set (ADR-0146): a range scan bounded by now, so a tick costs what is
// due rather than what the history holds — the property the key-order scan lacked, and
// the one ADR-0085 built the due-timer index for. An idle server pays one empty scan.
func (s *Store) DueHistoryExpiries(now int64, limit int, fn func(dueDate int64, piKey uint64) error) (more bool, err error) {
	lo := []byte{byte(cfHistoryExpiry)}
	hi := prefixEnd(appendOrderedInt64([]byte{byte(cfHistoryExpiry)}, now))
	n := 0
	scanErr := s.scanRange(lo, hi, func(k, _ []byte) error {
		if n >= limit {
			more = true
			return errScanWindowFull
		}
		if ferr := fn(orderedInt64At(k, 1), trailingKey(k)); ferr != nil {
			return ferr
		}
		n++
		return nil
	})
	if errors.Is(scanErr, errScanWindowFull) {
		scanErr = nil
	}
	if scanErr != nil {
		return false, scanErr
	}
	return more, nil
}

// Incidents calls fn with the element-instance key and value of every unresolved
// incident — the operator "list incidents" access pattern (ADR-0061).
func (s *Store) Incidents(fn func(elementKey uint64, v *model.IncidentValue) error) error {
	return s.scanPrefix([]byte{byte(cfIncident)}, func(k, raw []byte) error {
		v, err := model.DecodeValue(model.VTIncident, raw)
		if err != nil {
			return err
		}
		return fn(trailingKey(k), v.(*model.IncidentValue))
	})
}

// ActiveElementInstanceCount returns how many element instances are live.
func (s *Store) ActiveElementInstanceCount() (int, error) {
	return s.countPrefix([]byte{byte(cfElementInstance)})
}

// ActiveElementInstances calls fn with the key and value of every live element
// instance. Each carries the BPMN element (as a compiled-graph index) it sits on,
// which the live diagram overlay maps back to a diagram element.
func (s *Store) ActiveElementInstances(fn func(key uint64, v *model.ElementInstanceValue) error) error {
	return s.scanPrefix([]byte{byte(cfElementInstance)}, func(k, raw []byte) error {
		v, err := model.DecodeValue(model.VTElementInstance, raw)
		if err != nil {
			return err
		}
		return fn(trailingKey(k), v.(*model.ElementInstanceValue))
	})
}

// ElementVisitHistory folds the token-visit counters for a process definition,
// calling fn with each visited element index and how many tokens have passed
// through it. With instanceFilter == 0 it aggregates every instance of the
// definition (the heatmap the live overlay draws in gray); with a non-zero
// instanceFilter it reports only that one instance's visits. Because the key
// ends with the element index and the same element sits under a distinct
// instance-key prefix per instance, a definition-wide scan can report the same
// element index once per instance — the caller sums the counts. Pebble folds
// the merge deltas for each key, so raw carries the current total for that key.
func (s *Store) ElementVisitHistory(procDefKey, instanceFilter uint64, fn func(elementId int32, count int64) error) error {
	prefix := elementVisitDefPrefix(procDefKey)
	if instanceFilter != 0 {
		prefix = elementVisitInstancePrefix(procDefKey, instanceFilter)
	}
	return s.scanPrefix(prefix, func(k, raw []byte) error {
		return fn(elementIdFromVisitKey(k), decodeCounter(raw))
	})
}

// MessageFlowHistory folds the retained message flows a definition received,
// calling fn with each flow's event timestamp, log position, and payload in the
// order they occurred (the replay timeline). Because the key sorts by timestamp
// then position, a definition-wide scan yields a monotonic sequence. The caller
// resolves the receiver element index to a diagram id via the compiled process.
func (s *Store) MessageFlowHistory(receiverDefKey uint64, fn func(ts int64, pos uint64, v *model.MessageFlowValue) error) error {
	return s.scanPrefix(messageFlowDefPrefix(receiverDefKey), func(k, raw []byte) error {
		v, err := model.DecodeValue(model.VTMessageFlow, raw)
		if err != nil {
			return err
		}
		return fn(timestampFromFlowKey(k), positionFromFlowKey(k), v.(*model.MessageFlowValue))
	})
}

// ElementStepHistory folds the retained element-activation steps of one process
// instance, calling fn with each step's event timestamp, log position, and the
// activated element's compiled-graph index in the order they occurred (the
// step-by-step replay timeline, ADR-0046). Because the key sorts by timestamp
// then position, an instance-wide scan yields a monotonic sequence. The caller
// resolves the element index to a diagram id via the instance's compiled process.
func (s *Store) ElementStepHistory(piKey uint64, fn func(ts int64, pos uint64, elementId int32) error) error {
	return s.scanPrefix(elementStepInstancePrefix(piKey), func(k, raw []byte) error {
		return fn(timestampFromStepKey(k), positionFromStepKey(k), int32(binary.BigEndian.Uint32(raw)))
	})
}

// The action codes of an ElementReplayValue — what the token did at this element.
// Completion and termination are deliberately distinct: a completed element hands
// its token on to a successor, a terminated one (interrupted by a boundary event,
// torn down with its scope, cancelled) does not, so a replay must not wait for a
// successor that will never activate (ADR-0136). Codes are persisted, so their
// numeric values are part of the on-disk history format and must not be reused.
const (
	ReplayActivated  byte = 1 // the token entered the element
	ReplayCompleted  byte = 2 // the element finished; its successor takes the token
	ReplayTerminated byte = 3 // the element was torn down; the token dies with it
)

// ElementReplayValue is one durable causal token-lifecycle fact.
type ElementReplayValue struct {
	ElementID, SourceFlowID                    int32
	ElementInstanceKey, TokenID, ParentTokenID uint64
	Action                                     byte
}

// ElementReplayHistory scans causal token lifecycle facts in deterministic order.
func (s *Store) ElementReplayHistory(piKey uint64, fn func(ts int64, pos uint64, v ElementReplayValue) error) error {
	return s.scanPrefix(elementReplayInstancePrefix(piKey), func(k, raw []byte) error {
		if len(raw) != 33 {
			return fmt.Errorf("state: corrupt element replay value (%d bytes)", len(raw))
		}
		return fn(timestampFromStepKey(k), positionFromStepKey(k), ElementReplayValue{
			ElementID: int32(binary.BigEndian.Uint32(raw)), ElementInstanceKey: binary.BigEndian.Uint64(raw[4:]),
			TokenID: binary.BigEndian.Uint64(raw[12:]), ParentTokenID: binary.BigEndian.Uint64(raw[20:]),
			SourceFlowID: int32(binary.BigEndian.Uint32(raw[28:])), Action: raw[32],
		})
	})
}

// VariableSnapshotHistory folds the retained variable changes of one scope (a
// process instance), calling fn with each change's event timestamp, log position,
// and the variable's new state in the order they occurred (ADR-0048). Because the
// key sorts by timestamp then position, a scope-wide scan yields a monotonic
// sequence a caller folds by position to reconstruct the variables as of any step.
func (s *Store) VariableSnapshotHistory(scopeKey uint64, fn func(ts int64, pos uint64, v *model.VariableValue) error) error {
	return s.scanPrefix(variableSnapshotScopePrefix(scopeKey), func(k, raw []byte) error {
		v, err := model.DecodeValue(model.VTVariable, raw)
		if err != nil {
			return err
		}
		return fn(timestampFromVarSnapKey(k), positionFromVarSnapKey(k), v.(*model.VariableValue))
	})
}

// DecisionEvaluationHistory folds the retained DMN decision evaluations of one
// scope (a process instance), calling fn with each evaluation's event timestamp,
// log position, and its frozen record (decision id, inputs, outputs, trace) in the
// order they occurred (ADR-0066). Because the key sorts by timestamp then position,
// a scope-wide scan yields a monotonic sequence — the same ordering as the variable
// and element-step timelines, so a business rule task's decision reasoning lines up
// with the step at which it ran. Used to surface how a decision was made to
// operators, both live and after the instance has finished.
func (s *Store) DecisionEvaluationHistory(scopeKey uint64, fn func(ts int64, pos uint64, v *model.DecisionEvaluationValue) error) error {
	return s.scanPrefix(decisionEvaluationScopePrefix(scopeKey), func(k, raw []byte) error {
		v, err := model.DecodeValue(model.VTDecisionEvaluation, raw)
		if err != nil {
			return err
		}
		return fn(timestampFromDecisionEvalKey(k), positionFromDecisionEvalKey(k), v.(*model.DecisionEvaluationValue))
	})
}

// VariableAuditHistory folds the retained external variable overrides of one process
// instance, calling fn with each override's event timestamp, log position, and its
// frozen record (who set which variable, on which scope, to what value) in the order
// they occurred (ADR-0098). Because the key sorts by timestamp then position, a
// scope-wide scan yields a monotonic sequence — the same ordering as the variable and
// decision timelines — so the "who changed it" trail lines up with the step at which
// each override happened. It surfaces to operators both live and after the instance
// has finished, since the records are append-only history.
func (s *Store) VariableAuditHistory(piKey uint64, fn func(ts int64, pos uint64, v *model.VariableAuditValue) error) error {
	return s.scanPrefix(variableAuditScopePrefix(piKey), func(k, raw []byte) error {
		v, err := model.DecodeValue(model.VTVariableAudit, raw)
		if err != nil {
			return err
		}
		return fn(timestampFromVariableAuditKey(k), positionFromVariableAuditKey(k), v.(*model.VariableAuditValue))
	})
}

// OperatorActionHistory folds the retained operator interventions of one process
// instance, calling fn with each action's event timestamp, log position, and its frozen
// record (who acted, on which element, and why) in the order they occurred (ADR-0159).
// Like VariableAuditHistory the key sorts by timestamp then position, so the trail lines
// up with the step at which each intervention happened, and it surfaces both live and
// after the instance has finished since the records are append-only history.
func (s *Store) OperatorActionHistory(piKey uint64, fn func(ts int64, pos uint64, v *model.OperatorActionValue) error) error {
	return s.scanPrefix(operatorActionScopePrefix(piKey), func(k, raw []byte) error {
		v, err := model.DecodeValue(model.VTOperatorAction, raw)
		if err != nil {
			return err
		}
		return fn(timestampFromOperatorActionKey(k), positionFromOperatorActionKey(k), v.(*model.OperatorActionValue))
	})
}

// EachDecisionEvaluation folds every retained DMN decision evaluation across all
// process instances, calling fn with the owning scope (process instance) key, the
// evaluation's event timestamp, and its frozen record (ADR-0066). It scans the
// whole decision-evaluation column family — the global "which decisions ran, and
// how often" access pattern the Operations decisions overview uses, as opposed to
// DecisionEvaluationHistory, which folds a single instance's evaluations. The scan
// order is scope then timestamp then position; a caller that only aggregates by
// decision id does not depend on it.
func (s *Store) EachDecisionEvaluation(fn func(scopeKey uint64, ts int64, v *model.DecisionEvaluationValue) error) error {
	return s.scanPrefix([]byte{byte(cfDecisionEvaluation)}, func(k, raw []byte) error {
		v, err := model.DecodeValue(model.VTDecisionEvaluation, raw)
		if err != nil {
			return err
		}
		return fn(scopeFromDecisionEvalKey(k), timestampFromDecisionEvalKey(k), v.(*model.DecisionEvaluationValue))
	})
}

// ProcessInstance returns the process instance for key and whether it was found,
// looking first in the active family and then in the terminal-history family
// (ADR-0017). It lets a query resolve an instance's definition whether it is
// still running or already finished — the lookup the single-process replay uses.
func (s *Store) ProcessInstance(key uint64) (*model.ProcessInstanceValue, bool, error) {
	for _, k := range [][]byte{keyProcessInstance(key), keyProcessInstanceHistory(key)} {
		raw, ok, err := getCopy(s.db, k)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			continue
		}
		v, err := model.DecodeValue(model.VTProcessInstance, raw)
		if err != nil {
			return nil, false, err
		}
		return v.(*model.ProcessInstanceValue), true, nil
	}
	return nil, false, nil
}

// VariablesOfScope calls fn with every variable owned by the given scope, via
// the variable column family. Used to build a FEEL evaluation scope and to
// surface an instance's variables to operators.
func (s *Store) VariablesOfScope(scope uint64, fn func(v *model.VariableValue) error) error {
	return s.scanPrefix(variablePrefix(scope), func(_, raw []byte) error {
		return decodeVariable(raw, fn)
	})
}

// decodeVariable turns raw variable bytes into a value for fn. Shared with a
// [ReadView] for the same reason as decodeElementInstance.
func decodeVariable(raw []byte, fn func(v *model.VariableValue) error) error {
	v, err := model.DecodeValue(model.VTVariable, raw)
	if err != nil {
		return err
	}
	return fn(v.(*model.VariableValue))
}

// VisibleVariablesOfScope calls fn with every variable *visible* at the given
// scope: the scope's own variables plus those inherited from each enclosing scope,
// walking up the FlowScopeKey chain to the process root, with the nearest scope
// winning when a name is shadowed. This mirrors the engine's variable visibility
// (ResolveVariable / bindInputsChain, ADR-0068): a token at an activity-local
// scope sees its locals over anything inherited. Passing the process-instance root
// key yields exactly the root's variables (it has no enclosing scope), so callers
// reading a root instance are unaffected.
func (s *Store) VisibleVariablesOfScope(scope uint64, fn func(v *model.VariableValue) error) error {
	seen := map[string]bool{}
	visited := map[uint64]bool{}
	for scope != 0 && !visited[scope] {
		visited[scope] = true
		if err := s.VariablesOfScope(scope, func(v *model.VariableValue) error {
			if seen[v.Name] {
				return nil // a nearer scope already provided this name (shadowing)
			}
			seen[v.Name] = true
			return fn(v)
		}); err != nil {
			return err
		}
		ei, ok, err := s.GetElementInstance(scope)
		if err != nil {
			return err
		}
		if !ok {
			break // reached the process root (no element instance record)
		}
		scope = ei.FlowScopeKey
	}
	return nil
}

// CompensablesOfScope calls fn with every completed compensable activity retained
// under the given scope, in completion order (ADR-0103). Used to surface a scope's
// pending compensations to operators and by tests to assert the index is cleaned when
// a scope tears down. Mirrors VariablesOfScope (committed reads only).
func (s *Store) CompensablesOfScope(scope uint64, fn func(v *model.CompensableValue) error) error {
	return s.scanPrefix(compensableScopePrefix(scope), func(_, raw []byte) error {
		v, err := model.DecodeValue(model.VTCompensable, raw)
		if err != nil {
			return err
		}
		return fn(v.(*model.CompensableValue))
	})
}

// DataObjectsOfScope calls fn with every data object owned by the given scope,
// via the data-object column family — the current value of each. Used to surface
// an instance's data to operators and, later, to build a FEEL scope for data
// associations (ADR-0053). Mirrors VariablesOfScope.
func (s *Store) DataObjectsOfScope(scope uint64, fn func(v *model.DataObjectValue) error) error {
	return s.scanPrefix(dataObjectPrefix(scope), func(_, raw []byte) error {
		v, err := model.DecodeValue(model.VTDataObject, raw)
		if err != nil {
			return err
		}
		return fn(v.(*model.DataObjectValue))
	})
}

// DataObjectSnapshotHistory folds the retained data-object state changes of one
// scope, calling fn with each change's event timestamp, log position, and the
// object's new state in the order they occurred (ADR-0053). Because the key sorts
// by timestamp then position, a scope-wide scan yields a monotonic sequence a
// caller folds by position — the event-sourced data-state timeline and the basis
// for lineage. Mirrors VariableSnapshotHistory (ADR-0048).
func (s *Store) DataObjectSnapshotHistory(scopeKey uint64, fn func(ts int64, pos uint64, v *model.DataObjectValue) error) error {
	return s.scanPrefix(dataObjectSnapshotScopePrefix(scopeKey), func(k, raw []byte) error {
		v, err := model.DecodeValue(model.VTDataObject, raw)
		if err != nil {
			return err
		}
		return fn(timestampFromDataObjSnapKey(k), positionFromDataObjSnapKey(k), v.(*model.DataObjectValue))
	})
}

func (s *Store) countPrefix(prefix []byte) (int, error) {
	count := 0
	err := s.scanPrefix(prefix, func(_, _ []byte) error {
		count++
		return nil
	})
	return count, err
}

func (s *Store) scanPrefix(prefix []byte, fn func(k, v []byte) error) error {
	return s.scanRange(prefix, prefixEnd(prefix), fn)
}

// scanPrefixWith scans one prefix against any pebble reader — the live database or
// a read view — so both share one iteration path.
func scanPrefixWith(r iterReader, prefix []byte, fn func(k, v []byte) error) error {
	return scanRangeWith(r, prefix, prefixEnd(prefix), fn)
}

// iterReader is the slice of pebble both the database and a snapshot provide.
type iterReader interface {
	reader
	NewIter(o *pebble.IterOptions) (*pebble.Iterator, error)
}

func (s *Store) scanRange(lo, hi []byte, fn func(k, v []byte) error) error {
	return scanRangeWith(s.db, lo, hi, fn)
}

func scanRangeWith(r iterReader, lo, hi []byte, fn func(k, v []byte) error) error {
	iter, err := r.NewIter(&pebble.IterOptions{LowerBound: lo, UpperBound: hi})
	if err != nil {
		return err
	}
	defer iter.Close()
	for iter.First(); iter.Valid(); iter.Next() {
		if err := fn(iter.Key(), iter.Value()); err != nil {
			return err
		}
	}
	return iter.Error()
}

// scanRangeDesc is scanRange in reverse — it walks [lo, hi) from the highest key
// down to the lowest, so a caller can page a key-ordered index newest-first.
func (s *Store) scanRangeDesc(lo, hi []byte, fn func(k, v []byte) error) error {
	iter, err := s.db.NewIter(&pebble.IterOptions{LowerBound: lo, UpperBound: hi})
	if err != nil {
		return err
	}
	defer iter.Close()
	for iter.Last(); iter.Valid(); iter.Prev() {
		if err := fn(iter.Key(), iter.Value()); err != nil {
			return err
		}
	}
	return iter.Error()
}

// reader is the read surface shared by *pebble.DB and an indexed *pebble.Batch.
type reader interface {
	Get(key []byte) ([]byte, io.Closer, error)
}

// getCopy fetches key and returns an owned copy of the value, reporting whether
// it was present. Pebble's returned slice is only valid until its closer runs,
// so the value is copied out.
func getCopy(r reader, key []byte) ([]byte, bool, error) {
	v, closer, err := r.Get(key)
	if errors.Is(err, pebble.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	out := append([]byte(nil), v...)
	return out, true, closer.Close()
}

// Snapshot writes a consistent, durable snapshot of the store into destDir, which
// must not already exist (Pebble creates it). It is the state half of an engine
// recovery checkpoint (ADR-0131).
//
// The memtable is flushed first, on purpose: ordinary transactions commit
// pebble.NoSync (ADR-0005) because the WAL's fsync is the durability point, so
// without the flush a snapshot could inherit that same trailing property and
// silently omit recently applied state. Flushing means the snapshot's files
// provably contain every write committed up to the caller's applied position.
//
// Like every other Store method it is called on the owning partition goroutine
// (invariant I3) — for a checkpoint, at a batch boundary.
func (s *Store) Snapshot(destDir string) error {
	if err := s.db.Flush(); err != nil {
		return fmt.Errorf("state: flush before snapshot: %w", err)
	}
	if err := s.db.Checkpoint(destDir, pebble.WithFlushedWAL()); err != nil {
		return fmt.Errorf("state: snapshot: %w", err)
	}
	return nil
}
