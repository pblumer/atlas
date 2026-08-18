package state

import (
	"encoding/binary"
	"errors"

	"github.com/cockroachdb/pebble"

	"github.com/pblumer/atlas/model"
)

// readInto decodes the value at key into dst without allocating: it decodes
// directly from Pebble's returned bytes before releasing them. Reports whether
// the key was present.
func (t *Tx) readInto(key []byte, dst model.Value) (bool, error) {
	raw, closer, err := t.b.Get(key)
	if errors.Is(err, pebble.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	derr := model.DecodeValueInto(dst, raw)
	closer.Close()
	if derr != nil {
		return false, derr
	}
	return true, nil
}

// Tx is a state transaction: a set of mutations that commit atomically. It is
// an indexed Pebble batch, so reads through it observe its own pending writes.
type Tx struct {
	b       *pebble.Batch
	store   *Store
	scratch []byte // reused value-encode buffer; Pebble copies on Set
}

// Commit applies the transaction. It does not fsync: durability is the WAL's
// responsibility (ADR-0005), and the store is rebuildable by replay, so a state
// commit lost to a crash is simply re-derived on recovery.
func (t *Tx) Commit() error { return t.b.Commit(pebble.NoSync) }

// Close releases the transaction, returning its batch to the store for reuse.
// Safe to call after Commit. The Tx must not be used afterward.
func (t *Tx) Close() error {
	b := t.b
	t.b = nil
	return t.store.recycle(b)
}

// SetLastAppliedPosition records, within this transaction, the highest log
// position folded into state. Committed atomically with the mutations so state
// and position never diverge.
func (t *Tx) SetLastAppliedPosition(pos uint64) error {
	t.scratch = appendBE64(t.scratch[:0], pos)
	return t.b.Set(keyMeta(metaLastApplied), t.scratch, nil)
}

func (t *Tx) encodeValue(v model.Value) []byte {
	t.scratch = model.AppendValue(t.scratch[:0], v)
	return t.scratch
}

// --- ElementInstance ---

// PutElementInstance writes the element instance and its elByProc index entry.
func (t *Tx) PutElementInstance(key uint64, v *model.ElementInstanceValue) error {
	if err := t.b.Set(keyElementInstance(key), t.encodeValue(v), nil); err != nil {
		return err
	}
	return t.b.Set(keyElByProc(v.ProcessInstanceKey, key), nil, nil)
}

// GetElementInstanceInto decodes the element instance into dst without
// allocating, reporting whether it was present.
func (t *Tx) GetElementInstanceInto(key uint64, dst *model.ElementInstanceValue) (bool, error) {
	return t.readInto(keyElementInstance(key), dst)
}

// GetElementInstance returns the element instance, or nil if absent.
func (t *Tx) GetElementInstance(key uint64) (*model.ElementInstanceValue, error) {
	var v model.ElementInstanceValue
	ok, err := t.GetElementInstanceInto(key, &v)
	if err != nil || !ok {
		return nil, err
	}
	return &v, nil
}

// DeleteElementInstance removes the element instance and its index entry. The
// value is required to locate the elByProc entry; on recovery it comes from the
// event payload.
func (t *Tx) DeleteElementInstance(key uint64, v *model.ElementInstanceValue) error {
	if err := t.b.Delete(keyElementInstance(key), nil); err != nil {
		return err
	}
	return t.b.Delete(keyElByProc(v.ProcessInstanceKey, key), nil)
}

// ElementInstancesOfProcess calls fn for every element instance of a process
// instance visible in this transaction — committed rows plus this batch's own
// writes — via the elByProc index. A parallel join uses it to count how many
// tokens have arrived on its incoming flows, including one activated earlier in
// the same batch (which a committed-only store scan would miss).
func (t *Tx) ElementInstancesOfProcess(procKey uint64, fn func(elKey uint64, v *model.ElementInstanceValue) error) error {
	prefix := elByProcPrefix(procKey)
	iter, err := t.b.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: prefixEnd(prefix)})
	if err != nil {
		return err
	}
	defer iter.Close()
	for iter.First(); iter.Valid(); iter.Next() {
		elKey := trailingKey(iter.Key())
		var v model.ElementInstanceValue
		ok, err := t.GetElementInstanceInto(elKey, &v)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := fn(elKey, &v); err != nil {
			return err
		}
	}
	return iter.Error()
}

// --- Job ---

// PutJob writes the job, its activatable index entry, and the reverse
// element→job entry (so an interrupting boundary event can find the host's job).
// The writes go to one in-memory batch; their errors are accumulated (first
// non-nil wins) rather than checked one at a time, which keeps every write on the
// same covered path.
//
// A job is on the activatable index iff it has retries left (Retries > 0): a job
// whose retries are exhausted stays stored — an incident points at it — but is
// never handed to a worker until an operator resolves the incident and restores a
// positive retry count (ADR-0061).
func (t *Tx) PutJob(key uint64, v *model.JobValue) error {
	err := t.b.Set(keyJob(key), t.encodeValue(v), nil)
	// A job is pullable iff it has retries left AND is not currently backing off from a
	// failure (RetryDueDate == 0): a backing-off job stays stored, off the worker-visible
	// index, until its retry timer clears RetryDueDate and re-emits it (ADR-0111).
	if v.Retries > 0 && v.RetryDueDate == 0 {
		if e := t.b.Set(keyJobActivatable(v.JobType, key), nil, nil); err == nil {
			err = e
		}
	} else {
		if e := t.b.Delete(keyJobActivatable(v.JobType, key), nil); err == nil {
			err = e
		}
	}
	if e := t.b.Set(keyJobByElement(v.ElementInstanceKey), appendBE64(nil, key), nil); err == nil {
		err = e
	}
	return err
}

// --- Incident ---

// PutIncident writes an incident, keyed by the element instance it is attached to.
func (t *Tx) PutIncident(v *model.IncidentValue) error {
	return t.b.Set(keyIncident(v.ElementInstanceKey), t.encodeValue(v), nil)
}

// DeleteIncident removes the incident attached to an element instance. Deleting
// one that is absent is a harmless no-op — how terminating an element clears any
// incident it carried without first reading it (ADR-0061).
func (t *Tx) DeleteIncident(elKey uint64) error {
	return t.b.Delete(keyIncident(elKey), nil)
}

// GetIncident returns the incident attached to an element instance, or nil.
func (t *Tx) GetIncident(elKey uint64) (*model.IncidentValue, error) {
	var v model.IncidentValue
	ok, err := t.readInto(keyIncident(elKey), &v)
	if err != nil || !ok {
		return nil, err
	}
	return &v, nil
}

// JobOfElement returns the key of the job held by the given element instance, or
// ok=false if it holds none. Used to cancel a host activity's job when an
// interrupting boundary event terminates it.
func (t *Tx) JobOfElement(elKey uint64) (uint64, bool, error) {
	raw, closer, err := t.b.Get(keyJobByElement(elKey))
	if errors.Is(err, pebble.ErrNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	jobKey := binary.BigEndian.Uint64(raw)
	closer.Close()
	return jobKey, true, nil
}

// GetJobInto decodes the job into dst without allocating, reporting whether it
// was present.
func (t *Tx) GetJobInto(key uint64, dst *model.JobValue) (bool, error) {
	return t.readInto(keyJob(key), dst)
}

// GetJob returns the job, or nil if absent.
func (t *Tx) GetJob(key uint64) (*model.JobValue, error) {
	var v model.JobValue
	ok, err := t.GetJobInto(key, &v)
	if err != nil || !ok {
		return nil, err
	}
	return &v, nil
}

// DeleteJob removes the job, its activatable index entry, and the reverse
// element→job entry. Errors are accumulated across the three batch deletes (first
// non-nil wins), keeping every delete on the same covered path.
func (t *Tx) DeleteJob(key uint64, v *model.JobValue) error {
	err := t.b.Delete(keyJob(key), nil)
	if e := t.b.Delete(keyJobActivatable(v.JobType, key), nil); err == nil {
		err = e
	}
	if e := t.b.Delete(keyJobByElement(v.ElementInstanceKey), nil); err == nil {
		err = e
	}
	return err
}

// --- Timer ---

// PutTimer writes the timer into the due-date index, which is its primary store.
func (t *Tx) PutTimer(key uint64, v *model.TimerValue) error {
	return t.b.Set(keyTimer(v.DueDate, key), t.encodeValue(v), nil)
}

// DeleteTimer removes the timer. The value supplies the due date that locates
// its index key; on recovery it comes from the event payload.
func (t *Tx) DeleteTimer(key uint64, v *model.TimerValue) error {
	return t.b.Delete(keyTimer(v.DueDate, key), nil)
}

// --- ProcessInstance ---

// PutProcessInstance writes the process instance.
func (t *Tx) PutProcessInstance(key uint64, v *model.ProcessInstanceValue) error {
	return t.b.Set(keyProcessInstance(key), t.encodeValue(v), nil)
}

// GetProcessInstanceInto decodes the process instance into dst without
// allocating, reporting whether it was present.
func (t *Tx) GetProcessInstanceInto(key uint64, dst *model.ProcessInstanceValue) (bool, error) {
	return t.readInto(keyProcessInstance(key), dst)
}

// GetProcessInstance returns the process instance, or nil if absent.
func (t *Tx) GetProcessInstance(key uint64) (*model.ProcessInstanceValue, error) {
	var v model.ProcessInstanceValue
	ok, err := t.GetProcessInstanceInto(key, &v)
	if err != nil || !ok {
		return nil, err
	}
	return &v, nil
}

// DeleteProcessInstance removes the process instance.
func (t *Tx) DeleteProcessInstance(key uint64) error {
	return t.b.Delete(keyProcessInstance(key), nil)
}

// PutProcessInstanceHistory records a terminal (completed/terminated) process
// instance in the history index. Written from applyToState when an instance
// ends, from the event alone, so it replays identically on recovery (ADR-0017).
func (t *Tx) PutProcessInstanceHistory(key uint64, v *model.ProcessInstanceValue) error {
	return t.b.Set(keyProcessInstanceHistory(key), t.encodeValue(v), nil)
}

// PurgeInstanceHistory hard-deletes a finished instance from the state store: the
// terminal history record and every per-instance history/live family addressable
// from the instance key (and its definition key), so no orphaned rows outlive it
// (ADR-0115). It touches no per-definition counter — the active count was already
// decremented at termination and the finished count is monotonic (ADR-0083). Every
// delete is idempotent (an absent key is a no-op), so a replayed or re-enqueued
// purge is safe. Called only from applyToState(IntentPurged), so it replays
// identically on recovery (I4/I6).
//
// Not swept (by design, see ADR-0115): message-flow history (keyed by receiver
// definition, not instance) and incidents (an element key; a finished instance holds
// no live incident). Sub-scope variables/data objects that outlived their activity are
// not reached — a finished instance's live state is root-scoped (== the instance key)
// in practice.
func (t *Tx) PurgeInstanceHistory(piKey, procDefKey uint64) error {
	for _, prefix := range [][]byte{
		// The terminal history record is a full key, a strict prefix of no other, so a
		// prefix delete over it removes exactly that record — uniform with the families.
		keyProcessInstanceHistory(piKey),
		elementStepInstancePrefix(piKey),
		elementReplayInstancePrefix(piKey),
		elementVisitInstancePrefix(procDefKey, piKey),
		variableSnapshotScopePrefix(piKey),
		variableAuditScopePrefix(piKey),
		decisionEvaluationScopePrefix(piKey),
		variablePrefix(piKey),
		dataObjectPrefix(piKey),
		dataObjectSnapshotScopePrefix(piKey),
		compensableScopePrefix(piKey),
	} {
		if err := t.deletePrefix(prefix); err != nil {
			return err
		}
	}
	return nil
}

// --- MessageSubscription ---

// PutMessageSubscription writes an open message subscription, keyed by its
// (name, correlationKey) match pair plus its element-instance key.
func (t *Tx) PutMessageSubscription(v *model.MessageSubscriptionValue) error {
	return t.b.Set(keyMessageSubscription(v.MessageName, v.CorrelationKey, v.ElementInstanceKey), t.encodeValue(v), nil)
}

// DeleteMessageSubscription removes a subscription. The value supplies the name,
// correlation key, and element-instance key that locate its index entry; on
// recovery they come from the event payload.
func (t *Tx) DeleteMessageSubscription(v *model.MessageSubscriptionValue) error {
	return t.b.Delete(keyMessageSubscription(v.MessageName, v.CorrelationKey, v.ElementInstanceKey), nil)
}

// CorrelatableSubscriptions calls fn for every open subscription matching the
// given (message name, correlation key), via a prefix scan — the publish access
// pattern. It reads through the in-flight batch, so it observes subscriptions
// created earlier in the same batch (ADR-0020).
func (t *Tx) CorrelatableSubscriptions(name, correlationKey string, fn func(elKey uint64, v *model.MessageSubscriptionValue) error) error {
	prefix := messageSubscriptionPrefix(name, correlationKey)
	iter, err := t.b.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: prefixEnd(prefix)})
	if err != nil {
		return err
	}
	defer iter.Close()
	for iter.First(); iter.Valid(); iter.Next() {
		var v model.MessageSubscriptionValue
		if err := model.DecodeValueInto(&v, iter.Value()); err != nil {
			return err
		}
		if err := fn(trailingKey(iter.Key()), &v); err != nil {
			return err
		}
	}
	return iter.Error()
}

// --- SignalSubscription ---

// PutSignalSubscription writes an open signal subscription, keyed by its signal
// name plus its element-instance key. A signal has no correlation key — it
// matches by name alone (ADR-0088).
func (t *Tx) PutSignalSubscription(v *model.SignalSubscriptionValue) error {
	return t.b.Set(keySignalSubscription(v.SignalName, v.ElementInstanceKey), t.encodeValue(v), nil)
}

// DeleteSignalSubscription removes a signal subscription. The value supplies the
// name and element-instance key that locate its index entry; on recovery they
// come from the event payload.
func (t *Tx) DeleteSignalSubscription(v *model.SignalSubscriptionValue) error {
	return t.b.Delete(keySignalSubscription(v.SignalName, v.ElementInstanceKey), nil)
}

// SubscribedSignals calls fn for every open subscription waiting on the given
// signal name, via a name-only prefix scan — the broadcast access pattern. Like
// CorrelatableSubscriptions it reads through the in-flight batch, so it observes
// subscriptions created earlier in the same batch (ADR-0088).
func (t *Tx) SubscribedSignals(name string, fn func(elKey uint64, v *model.SignalSubscriptionValue) error) error {
	prefix := signalSubscriptionPrefix(name)
	iter, err := t.b.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: prefixEnd(prefix)})
	if err != nil {
		return err
	}
	defer iter.Close()
	for iter.First(); iter.Valid(); iter.Next() {
		var v model.SignalSubscriptionValue
		if err := model.DecodeValueInto(&v, iter.Value()); err != nil {
			return err
		}
		if err := fn(trailingKey(iter.Key()), &v); err != nil {
			return err
		}
	}
	return iter.Error()
}

// --- Compensable (ADR-0103) ---

// RecordCompensable retains one completed compensable activity under its scope,
// keyed by the completion event's log position so a scope scan yields completion
// order. pos comes from the event header; the value carries the scope, the
// compensated activity, and its compensation handler.
func (t *Tx) RecordCompensable(pos uint64, v *model.CompensableValue) error {
	return t.b.Set(keyCompensable(v.ScopeKey, pos), t.encodeValue(v), nil)
}

// DeleteCompensable removes one compensable record (its activity has been
// compensated), located by its scope and sequence — both carried on the consume
// event, so recovery deletes the identical entry. Idempotent.
func (t *Tx) DeleteCompensable(scopeKey, seq uint64) error {
	return t.b.Delete(keyCompensable(scopeKey, seq), nil)
}

// DeleteCompensablesOfScope drops every compensable record held under a scope, called
// when the scope tears down (its subprocess container or process instance completes or is
// terminated) so uncompensated records never leak past the scope (ADR-0103). Keys are
// collected before deleting so the scan is not disturbed. Idempotent — a scope with none
// is a no-op.
func (t *Tx) DeleteCompensablesOfScope(scopeKey uint64) error {
	return t.deletePrefix(compensableScopePrefix(scopeKey))
}

// deletePrefix drops every key under prefix. Keys are collected before deleting so
// the scan is not disturbed by the deletes. Idempotent — an empty prefix is a no-op.
func (t *Tx) deletePrefix(prefix []byte) error {
	iter, err := t.b.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: prefixEnd(prefix)})
	if err != nil {
		return err
	}
	var keys [][]byte
	for iter.First(); iter.Valid(); iter.Next() {
		keys = append(keys, append([]byte(nil), iter.Key()...))
	}
	err = iter.Error()
	if cerr := iter.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	for _, k := range keys {
		if e := t.b.Delete(k, nil); e != nil {
			return e
		}
	}
	return nil
}

// SetCanceling marks the transaction scope txKey as cancelling: a cancel end event fired in
// it, so when its scope drains the transaction routes out its cancel boundary rather than
// completing normally (ADR-0108). It is derived in applyToState from the cancel end event's
// committed Completed event, so it rebuilds identically on replay (I4/I6). The value is a
// single non-empty byte; only presence is meaningful.
func (t *Tx) SetCanceling(txKey uint64) error {
	return t.b.Set(keyCanceling(txKey), []byte{1}, nil)
}

// IsCanceling reports whether the transaction scope txKey was marked cancelling by a cancel
// end event (ADR-0108). It reads through the in-flight batch, so it observes a mark written
// earlier in the same batch.
func (t *Tx) IsCanceling(txKey uint64) (bool, error) {
	_, ok, err := getCopy(t.b, keyCanceling(txKey))
	return ok, err
}

// DeleteCanceling drops the cancelling marker for txKey when the transaction tears down
// (ADR-0108). Idempotent — a transaction that was never cancelled, or a plain subprocess that
// never carried a marker, is a no-op.
func (t *Tx) DeleteCanceling(txKey uint64) error {
	return t.b.Delete(keyCanceling(txKey), nil)
}

// CompensablesOfScopeDesc calls fn for every completed compensable activity recorded
// under scopeKey, newest first (reverse completion order) — the order a compensation
// throw runs handlers in (ADR-0103). It reads through the in-flight batch, so it
// observes records written earlier in the same batch. seq is the record's key
// sequence (log position), which the caller carries on the consume event to delete it.
func (t *Tx) CompensablesOfScopeDesc(scopeKey uint64, fn func(seq uint64, v *model.CompensableValue) error) error {
	prefix := compensableScopePrefix(scopeKey)
	iter, err := t.b.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: prefixEnd(prefix)})
	if err != nil {
		return err
	}
	defer iter.Close()
	for iter.Last(); iter.Valid(); iter.Prev() {
		var v model.CompensableValue
		if err := model.DecodeValueInto(&v, iter.Value()); err != nil {
			return err
		}
		if err := fn(trailingKey(iter.Key()), &v); err != nil {
			return err
		}
	}
	return iter.Error()
}

// --- Variable ---

// PutVariable writes (upserts) a process variable under its scope and name.
func (t *Tx) PutVariable(v *model.VariableValue) error {
	return t.b.Set(keyVariable(v.ScopeKey, v.Name), t.encodeValue(v), nil)
}

// DeleteVariable removes a variable from its scope by name. It is idempotent —
// deleting an absent variable is a no-op — and is used to drop an activity-local
// variable scope when the activity completes (ADR-0068).
func (t *Tx) DeleteVariable(scope uint64, name string) error {
	return t.b.Delete(keyVariable(scope, name), nil)
}

// GetVariable returns a scope's variable by name, or nil if absent.
func (t *Tx) GetVariable(scope uint64, name string) (*model.VariableValue, error) {
	var v model.VariableValue
	ok, err := t.readInto(keyVariable(scope, name), &v)
	if err != nil || !ok {
		return nil, err
	}
	return &v, nil
}

// VariablesOfScope calls fn with every variable owned by scope, via a prefix scan
// over the variable column family. It reads through the in-flight batch, so it
// observes variables written earlier in the same batch. A message throw event
// uses it to gather the payload it publishes (ADR-0035).
func (t *Tx) VariablesOfScope(scope uint64, fn func(v *model.VariableValue) error) error {
	prefix := variablePrefix(scope)
	iter, err := t.b.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: prefixEnd(prefix)})
	if err != nil {
		return err
	}
	defer iter.Close()
	for iter.First(); iter.Valid(); iter.Next() {
		var v model.VariableValue
		if err := model.DecodeValueInto(&v, iter.Value()); err != nil {
			return err
		}
		if err := fn(&v); err != nil {
			return err
		}
	}
	return iter.Error()
}

// --- DataObject ---

// PutDataObject writes (upserts) a data object under its scope and name — the
// current value, mirroring PutVariable. The live store keeps only the latest;
// the whole state history lives in the snapshot family (ADR-0053).
func (t *Tx) PutDataObject(v *model.DataObjectValue) error {
	return t.b.Set(keyDataObject(v.ScopeKey, v.Name), t.encodeValue(v), nil)
}

// GetDataObject returns a scope's data object by name, or nil if absent.
func (t *Tx) GetDataObject(scope uint64, name string) (*model.DataObjectValue, error) {
	var v model.DataObjectValue
	ok, err := t.readInto(keyDataObject(scope, name), &v)
	if err != nil || !ok {
		return nil, err
	}
	return &v, nil
}

// RecordDataObjectSnapshot retains one data-object state change under its scope,
// keyed in change order. ts and pos come from the event header; the value is the
// object's new state (name, data state, value). Written only from applyToState,
// from the event alone, so it rebuilds identically on replay (invariant I4); a
// plain Set on a unique (position-bearing) key, never overwritten (ADR-0053).
func (t *Tx) RecordDataObjectSnapshot(ts int64, pos uint64, v *model.DataObjectValue) error {
	return t.b.Set(keyDataObjectSnapshot(v.ScopeKey, ts, pos), t.encodeValue(v), nil)
}

// --- Active-children counter ---
//
// Each scope (a process instance or a subprocess instance) tracks how many
// child element instances are active. A scope completes when its counter hits
// zero. The counter is pure state — mutated only from applyToState — so it is
// rebuilt identically on recovery.

// --- Inbound delivery high-water (ADR-0075) ---

// PutInboundHighWater upserts an external event source's last-applied sequence.
// It writes the absolute sequence (not a delta), so a replayed IntentInbound-
// DeliveryApplied rebuilds the identical mark (invariant I4). Reads through the
// batch see it immediately, so the guard in the same batch observes it.
func (t *Tx) PutInboundHighWater(sourceID string, seq uint64) error {
	t.scratch = binary.LittleEndian.AppendUint64(t.scratch[:0], seq)
	return t.b.Set(keyInboundHighWater(sourceID), t.scratch, nil)
}

// InboundHighWater returns the last-applied sequence for a source, or 0 if the
// source has no mark yet. It reads through the in-flight batch, so a guard sees a
// mark written earlier in the same batch.
func (t *Tx) InboundHighWater(sourceID string) (uint64, error) {
	raw, ok, err := getCopy(t.b, keyInboundHighWater(sourceID))
	if err != nil || !ok {
		return 0, err
	}
	if len(raw) < 8 {
		return 0, errors.New("state: inbound high-water value too short")
	}
	return binary.LittleEndian.Uint64(raw), nil
}

// IncrementActiveChildren adds one active child to scope. It is a write-only
// merge (no read), so it does not allocate on the hot path (invariant I1).
func (t *Tx) IncrementActiveChildren(scope uint64) error {
	return t.mergeActiveChildren(scope, 1)
}

// DecrementActiveChildren removes one active child from scope. A scope that
// returns to zero leaves a zero-valued counter entry rather than being deleted;
// completion checks treat absent and zero alike.
func (t *Tx) DecrementActiveChildren(scope uint64) error {
	return t.mergeActiveChildren(scope, -1)
}

func (t *Tx) mergeActiveChildren(scope uint64, delta int64) error {
	return t.mergeCounter(keyActiveChildren(scope), delta)
}

// mergeCounter applies a signed delta to a counter key as a write-only Pebble
// merge — no read, no allocation beyond the reused scratch buffer (invariant I1).
func (t *Tx) mergeCounter(key []byte, delta int64) error {
	t.scratch = appendCounter(t.scratch[:0], delta)
	return t.b.Merge(key, t.scratch, nil)
}

// --- Runtime aggregate counters (ADR-0080) ---
//
// Definition-scoped active-instance and per-element live-token/visit counters,
// maintained as signed merges from applyToState so the Operations runtime view
// reads a definition's live state in O(elements) rather than scanning every
// instance. Write-only merges (no read on the hot path, invariant I1); they
// compose across a crash and rebuild on replay (invariant I4), generalizing the
// active-children (ADR) and element-visit (ADR-0022) counters.

// IncDefInstanceCount and DecDefInstanceCount move a definition's active-instance
// count by one, on process-instance creation and termination.
func (t *Tx) IncDefInstanceCount(procDefKey uint64) error {
	return t.mergeCounter(keyDefInstanceCount(procDefKey), 1)
}
func (t *Tx) DecDefInstanceCount(procDefKey uint64) error {
	return t.mergeCounter(keyDefInstanceCount(procDefKey), -1)
}

// IncDefCompletedCount bumps a definition's finished-instance count by one, on each
// process-instance completion or termination. Monotonic (never decremented) — the
// count of finished instances only grows — so the summary's "finished" column reads
// in O(1) instead of scanning the history, which draining active instances only makes
// larger (ADR-0083).
func (t *Tx) IncDefCompletedCount(procDefKey uint64) error {
	return t.mergeCounter(keyDefCompletedCount(procDefKey), 1)
}

// SetDefLastActivity records a definition's most recent instance-event timestamp by
// overwrite (ADR-0083). The processor's event timestamps are non-decreasing in log
// order, so the last write is the latest and replay rebuilds the identical value
// (invariant I4). Write-only, no read.
func (t *Tx) SetDefLastActivity(procDefKey uint64, unixNano int64) error {
	t.scratch = appendBE64(t.scratch[:0], uint64(unixNano))
	return t.b.Set(keyDefLastActivity(procDefKey), t.scratch, nil)
}

// IncElementToken and DecElementToken move a definition-element live-token count
// by one, on element-instance activation and completion/termination.
func (t *Tx) IncElementToken(procDefKey uint64, elementId int32) error {
	return t.mergeCounter(keyElementTokenCount(procDefKey, elementId), 1)
}
func (t *Tx) DecElementToken(procDefKey uint64, elementId int32) error {
	return t.mergeCounter(keyElementTokenCount(procDefKey, elementId), -1)
}

// IncElementVisitAgg bumps a definition-element cumulative-visit count on
// activation. Never decremented — it is the retained historical heatmap.
func (t *Tx) IncElementVisitAgg(procDefKey uint64, elementId int32) error {
	return t.mergeCounter(keyElementVisitAgg(procDefKey, elementId), 1)
}

// --- Element-visit history ---
//
// Every time a token activates an element, its per-(definition, instance,
// element) counter is bumped. Unlike the active element-instance record — which
// applyToState deletes on completion — the visit counter is retained, so the
// Operations overlay can show where tokens have flowed even after the instances
// finished (the gray "history" heatmap; live tokens stay green). Like the
// active-children counter it is a write-only Merge, so it does not read or
// allocate on the hot path (invariant I1) and rebuilds identically on replay
// (invariant I4). Retention is unbounded for now, as with the process-instance
// history index (ADR-0017, ADR-0022).

// RecordElementVisit adds one to the visit counter for an element instance's
// element. Called from applyToState when an element instance is activated.
func (t *Tx) RecordElementVisit(procDefKey, piKey uint64, elementId int32) error {
	t.scratch = appendCounter(t.scratch[:0], 1)
	return t.b.Merge(keyElementVisit(procDefKey, piKey, elementId), t.scratch, nil)
}

// --- Message-flow history ---
//
// Every delivered message flow (a correlation to a catch event, or a message
// that instantiated a message-start process) is retained here so the Operations
// collaboration view can replay which message crossed to which receiving element
// and when — the message-flow analogue of the element-visit heatmap. Written
// only from applyToState, from the event alone (the header's timestamp and
// position plus the payload), so it rebuilds identically on replay (invariant I4,
// ADR-0038). Each record has a unique key (position is monotonic), so this is a
// plain Set, never overwritten and never deleted. Retention is unbounded for now,
// as with the process-instance and element-visit history (ADR-0017, ADR-0022).

// RecordMessageFlow retains one delivered message flow under its receiver
// definition, keyed in time order. ts and pos come from the event header.
func (t *Tx) RecordMessageFlow(ts int64, pos uint64, v *model.MessageFlowValue) error {
	return t.b.Set(keyMessageFlow(v.ReceiverProcessDefKey, ts, pos), t.encodeValue(v), nil)
}

// --- Element-step history ---
//
// Every element activation of a single process instance is retained here, keyed
// in the order it happened, so the Operations view can replay one instance step
// by step — the single-process analogue of the message-flow timeline (ADR-0038).
// It complements the element-visit counter (ADR-0022): the counter answers "how
// often" as an aggregate heatmap, this answers "in what order" per instance.
// Written only from applyToState, from the event alone (the header's timestamp
// and position plus the activated element), so it rebuilds identically on replay
// (invariant I4, ADR-0046). Each record has a unique key (position is monotonic),
// so this is a plain Set, never overwritten and never deleted. Retention is
// unbounded for now, as with the other history families (ADR-0017, ADR-0022).

// RecordElementStep retains one element activation of a process instance under
// its instance key, keyed in time order. ts and pos come from the event header;
// the value is the activated element's compiled-graph index.
func (t *Tx) RecordElementStep(piKey uint64, ts int64, pos uint64, elementId int32) error {
	t.scratch = appendBE32(t.scratch[:0], uint32(elementId))
	return t.b.Set(keyElementStep(piKey, ts, pos), t.scratch, nil)
}

// RecordElementReplay retains an activation or consumption with its durable
// token lineage. It is derived only from the lifecycle event by applyToState.
// action is one of ReplayActivated / ReplayCompleted / ReplayTerminated.
func (t *Tx) RecordElementReplay(piKey uint64, ts int64, pos uint64, elementID int32, elementKey, tokenID, parentTokenID uint64, sourceFlowID int32, action byte) error {
	t.scratch = appendBE32(t.scratch[:0], uint32(elementID))
	t.scratch = appendBE64(t.scratch, elementKey)
	t.scratch = appendBE64(t.scratch, tokenID)
	t.scratch = appendBE64(t.scratch, parentTokenID)
	t.scratch = appendBE32(t.scratch, uint32(sourceFlowID))
	t.scratch = append(t.scratch, action)
	return t.b.Set(keyElementReplay(piKey, ts, pos), t.scratch, nil)
}

// --- Variable-snapshot history ---
//
// Every variable change of a process instance is retained here, keyed in change
// order under the variable's scope, so the single-process replay can fold the
// variable values as they stood at each step — the variable analogue of the
// element-step timeline (ADR-0048). It complements the live variable store (which
// keeps only the current value): this keeps the whole history so scrubbing back
// shows earlier values. Written only from applyToState, from the event alone (the
// header's timestamp and position plus the changed variable), so it rebuilds
// identically on replay (invariant I4). Each record has a unique key (position is
// monotonic), so this is a plain Set, never overwritten and never deleted.
// Retention is unbounded for now, as with the other history families.

// RecordVariableSnapshot retains one variable change under its scope, keyed in
// change order. ts and pos come from the event header; the value is the variable's
// new state (name, kind, value).
func (t *Tx) RecordVariableSnapshot(ts int64, pos uint64, v *model.VariableValue) error {
	return t.b.Set(keyVariableSnapshot(v.ScopeKey, ts, pos), t.encodeValue(v), nil)
}

// --- Decision-evaluation history ---
//
// Every DMN decision a business rule task evaluates is retained here, keyed in
// evaluation order under the owning process instance, so an operator can inspect
// after the fact what inputs a decision saw, what it returned, and which rules
// fired (ADR-0066). The worker evaluates off the processor goroutine and freezes
// the inputs/outputs/trace onto the completion command; this writer runs only from
// applyToState, from the event alone (the header's timestamp and position plus the
// frozen evaluation), so it rebuilds identically on replay without re-evaluating
// the decision (invariant I4/I6). Each record has a unique key (position is
// monotonic), so this is a plain Set, never overwritten and never deleted.
// Retention is unbounded for now, as with the other history families.

// RecordDecisionEvaluation retains one decision evaluation under its owning
// process instance, keyed in evaluation order. ts and pos come from the event
// header; the value carries the decision id, input context, outputs, and trace.
func (t *Tx) RecordDecisionEvaluation(ts int64, pos uint64, v *model.DecisionEvaluationValue) error {
	return t.b.Set(keyDecisionEvaluation(v.ProcessInstanceKey, ts, pos), t.encodeValue(v), nil)
}

// RecordVariableAudit retains one external variable override under its owning
// process instance, keyed in change order (ADR-0098). ts and pos come from the event
// header; the value carries who set the variable, on which scope, and to what value.
// Written only from applyToState, from the event alone, so it rebuilds identically on
// replay (invariant I4); a plain Set on a unique (position-bearing) key, never
// overwritten.
func (t *Tx) RecordVariableAudit(ts int64, pos uint64, v *model.VariableAuditValue) error {
	return t.b.Set(keyVariableAudit(v.ProcessInstanceKey, ts, pos), t.encodeValue(v), nil)
}

// ActiveChildren returns the active-child count for scope (0 if none). This read
// folds the merged deltas, so it is used only where the current count is needed
// (e.g. detecting a finished scope), not on every increment.
func (t *Tx) ActiveChildren(scope uint64) (int32, error) {
	raw, ok, err := getCopy(t.b, keyActiveChildren(scope))
	if err != nil || !ok {
		return 0, err
	}
	return int32(decodeCounter(raw)), nil
}

// IncrementActiveStartKey / DecrementActiveStartKey maintain the count of live
// message-start instances of a definition that began with a correlation key
// (ADR-0094). Like the active-children counter they are write-only composing merges,
// so they neither read nor allocate beyond the reused scratch buffer, and rebuild
// identically on replay (I4/I6).
func (t *Tx) IncrementActiveStartKey(defKey uint64, correlationKey string) error {
	return t.mergeActiveStartKey(defKey, correlationKey, 1)
}

func (t *Tx) DecrementActiveStartKey(defKey uint64, correlationKey string) error {
	return t.mergeActiveStartKey(defKey, correlationKey, -1)
}

func (t *Tx) mergeActiveStartKey(defKey uint64, correlationKey string, delta int64) error {
	t.scratch = appendCounter(t.scratch[:0], delta)
	return t.b.Merge(keyActiveStartKey(defKey, correlationKey), t.scratch, nil)
}

// ActiveStartKeyCount returns how many live instances of defKey began with
// correlationKey (0 if none). It folds the merged deltas, so it is read only where the
// current count is needed — the singleton-start gate (ADR-0094), not on every merge.
func (t *Tx) ActiveStartKeyCount(defKey uint64, correlationKey string) (int32, error) {
	raw, ok, err := getCopy(t.b, keyActiveStartKey(defKey, correlationKey))
	if err != nil || !ok {
		return 0, err
	}
	return int32(decodeCounter(raw)), nil
}
