package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// Deleting a version's finished instances on an administrator's request
// (ADR-0486).
//
// History retention deletes on a policy: a definition's atlas:historyTtl, frozen onto an
// instance when it finishes, or the server-wide --retention-max-age (ADR-0115, ADR-0144,
// ADR-0146). An instance that finished before its process declared a TTL has no entry in
// the due-date index, and only the server-wide age reaches it, through a key-order scan
// that takes days over a few million records and then applies to every process alike.
// A development server running bulk checks is left with millions of finished instances
// nobody will open again and no way to say "these, now".
//
// This is that way. It adds no deletion of its own: it selects instances and enqueues the
// same IntentPurging command the retention sweep does, behind the same export gate, so
// the delete is the durable IntentPurged event recovery already replays (I4, I6). What
// it adds is the selection — one deployed version, finished before a cut-off — and the
// bounds that keep a selection of millions from holding the writer.
//
// # The shape of a call
//
// The selection walks the piDoneByDef index on a read view, off the run loop; only the
// purge commands go onto it (I3, ADR-0382). One call deletes at most `limit`, newest
// first, and answers a cursor to continue from, so a purge of millions is many short
// turns with the loop free between them — the bulk-cancel shape (ADR-0090). A walk also
// stops after passing over ten times its limit, so a long run of instances the export
// gate holds cannot turn one call into a scan of the history.
//
// # What it does not do
//
// It does not give disk back by itself. A purge is an event, so the WAL grows by one
// record per instance until compaction removes the prefix (ADR-0131), and the state
// store shrinks only as Pebble compacts the deletions. The preview says so, and the
// Console offers compaction beside the disk it frees.

// WithoutInstancePurge refuses an administrator's delete of a version's finished
// instances (--instance-purge=false). The preview is still answered and says the purge
// is switched off. A production installation, whose history is evidence, is started
// with it.
func WithoutInstancePurge() Option { return func(s *Server) { s.instancePurgeOff = true } }

const (
	// historyPurgeBatchDefault and historyPurgeBatchMax bound how many instances one
	// call deletes. Every purge is a fold that deletes a dozen families, on the single
	// writer, so the batch is a bound on how long one call holds it. The default is the
	// retention sweep's per-tick batch, which has run on large stores since ADR-0115.
	historyPurgeBatchDefault = 1000
	historyPurgeBatchMax     = 5000
	// historyPurgeScanFactor bounds how many index entries one call passes over, as a
	// multiple of its limit: the instances the export gate holds are read and skipped,
	// and without a bound a long run of them would be a scan of the history.
	historyPurgeScanFactor = 10
)

// The gate a purge waits on, as the preview names it.
const (
	// historyPurgeGateExporter: the exporter runs, and an instance goes only once all of
	// its events are in OpenSearch (ADR-0115).
	historyPurgeGateExporter = "exporter"
	// historyPurgeGateDurable: no exporter runs, and the gate is the durable position —
	// which everything in the state store has already passed (I2).
	historyPurgeGateDurable = "durable"
)

// historyPurgePlan is what GET …/history-purge answers: what a purge up to the cut-off
// would consider, and what it would leave.
type historyPurgePlan struct {
	ProcessDefKey uint64 `json:"processDefKey"`
	ProcessID     string `json:"processId"`
	Version       int32  `json:"version"`
	// Before is the cut-off in unix nanoseconds: the moment asked for, or the server's
	// now when none was. The delete repeats it, so preview and delete agree.
	Before int64 `json:"before"`
	// Finished counts the version's instances that finished strictly before Before. The
	// export gate may hold some of them back; the delete says how many.
	Finished int `json:"finished"`
	// Running counts the version's running instances, which a purge never touches.
	Running int `json:"running"`
	// Gate is what an instance waits for before it may go: historyPurgeGateExporter or
	// historyPurgeGateDurable.
	Gate string `json:"gate"`
	// HistoryTtlSeconds is what the version declares as atlas:historyTtl, 0 for none, and
	// RetentionMaxAgeSeconds the server-wide age: the policies that delete without
	// anybody asking, which is the better answer for a process run again and again.
	HistoryTtlSeconds      float64 `json:"historyTtlSeconds"`
	RetentionMaxAgeSeconds float64 `json:"retentionMaxAgeSeconds"`
	// Enabled says whether this server allows the delete at all. The preview is
	// answered either way.
	Enabled bool `json:"enabled"`
	// Confirm is what the delete has to repeat as ?confirm=: the process id.
	Confirm string `json:"confirm"`
}

// historyPurgeBatch is what one POST …/history-purge did.
type historyPurgeBatch struct {
	ProcessDefKey uint64 `json:"processDefKey"`
	ProcessID     string `json:"processId"`
	Version       int32  `json:"version"`
	Before        int64  `json:"before"`
	// Purged counts the instances deleted in this call, Held the ones passed over
	// because their events are not provably exported yet. A held instance stays, and a
	// later call — or the retention sweep — takes it once the gate opens.
	Purged int `json:"purged"`
	Held   int `json:"held"`
	// Remaining is true when the walk stopped before the end of the range; NextCursor
	// is then where the next call continues, passed as ?after=.
	Remaining  bool   `json:"remaining"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// errHistoryPurgeBatchFull stops the index walk once a call has what it may take.
var errHistoryPurgeBatchFull = errors.New("history purge batch full")

// historyPurgeable reports whether a finished instance may be deleted now. With the
// exporter on it is ADR-0115's rule: the terminal position is known and at or below the
// high-water mark, so OpenSearch holds every event of it. With no exporter the gate is
// durability, and a record without a position goes too: it finished before ADR-0115
// recorded one, and what the state store holds is already in the log (I2). The
// retention sweep skips such a record because it cannot prove it *exported*; with no
// exporter there is nothing to prove.
func historyPurgeable(pi *model.ProcessInstanceValue, safePos uint64, exporter bool) bool {
	if pi.CompletedPosition == 0 {
		return !exporter
	}
	return pi.CompletedPosition <= safePos
}

// handleHistoryPurgePreview answers what deleting a version's finished instances up to
// a cut-off would consider. It changes nothing, and it counts index keys without
// decoding a record, so it costs a key walk over the range and nothing more.
func (s *Server) handleHistoryPurgePreview(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	before, err := s.purgeCutOff(r.URL.Query().Get("before"))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	plan := historyPurgePlan{ProcessDefKey: key, Before: before, Gate: s.historyPurgeGate(),
		RetentionMaxAgeSeconds: s.retentionMaxAge.Seconds(), Enabled: !s.instancePurgeOff}
	found := false
	err = s.readOffLoop(func(rv *state.ReadView, defs defIndex) error {
		d, ok := defs[key]
		if !ok {
			return nil
		}
		found = true
		plan.ProcessID, plan.Version, plan.Confirm = d.ProcessID, d.Version, d.ProcessID
		if d.cp != nil {
			plan.HistoryTtlSeconds = time.Duration(d.cp.HistoryTtlNanos()).Seconds()
		}
		var cerr error
		if plan.Finished, cerr = rv.CountFinishedOfDefBefore(key, before); cerr != nil {
			return cerr
		}
		plan.Running, cerr = rv.DefInstanceCount(key)
		return cerr
	})
	switch {
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read the history: "+err.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no deployment with that key")
	default:
		httpapi.JSON(w, http.StatusOK, plan)
	}
}

// handleHistoryPurge deletes one bounded batch of a version's finished instances that
// finished before the cut-off, newest first, and answers where the next call continues.
func (s *Server) handleHistoryPurge(w http.ResponseWriter, r *http.Request) {
	if s.instancePurgeOff {
		// Refused before anything is read: on a server that switched it off, the answer
		// must not depend on what exists.
		httpapi.Error(w, http.StatusForbidden, "deleting finished instances for good is switched off on "+
			"this server: it was started with --instance-purge=false (ATLAS_INSTANCE_PURGE=false)")
		return
	}
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	q := r.URL.Query()
	before, err := s.purgeCutOff(q.Get("before"))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	limit, err := purgeLimit(q.Get("limit"))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	// The walk is strictly below its cursor. It starts at the cut-off and continues from
	// the last instance the previous call passed; a cursor at or above the cut-off would
	// reach instances nobody asked to delete, so it is ignored in favour of the cut-off.
	curAt, curKey := before, uint64(0)
	if raw := q.Get("after"); raw != "" {
		at, k, err := parsePurgeCursor(raw)
		if err != nil {
			httpapi.Error(w, http.StatusBadRequest, err.Error())
			return
		}
		if at < before {
			curAt, curKey = at, k
		}
	}

	// The exporter's mark is read before the view is taken: it only ever rises, so a
	// mark older than the view is the conservative one.
	exporter := s.exporter != nil
	var safePos uint64
	if exporter {
		safePos = s.exporter.HighWaterMark()
	}
	var (
		found   bool
		d       defMeta
		targets []purgeTarget
		res     historyPurgeBatch
	)
	err = s.readOffLoop(func(rv *state.ReadView, defs defIndex) error {
		var ok bool
		if d, ok = defs[key]; !ok {
			return nil
		}
		found = true
		if q.Get("confirm") != d.ProcessID {
			return nil // refused below, before anything is selected
		}
		if !exporter {
			var err error
			if safePos, err = rv.LastAppliedPosition(); err != nil {
				return err
			}
		}
		scanned := 0
		err := rv.FinishedInstancesOfDefDesc(key, curAt, curKey, func(k uint64, v *model.ProcessInstanceValue) error {
			if len(targets) >= limit || scanned >= limit*historyPurgeScanFactor {
				res.Remaining = true
				return errHistoryPurgeBatchFull
			}
			scanned++
			curAt, curKey = v.CompletedAt, k
			if historyPurgeable(v, safePos, exporter) {
				targets = append(targets, purgeTarget{k, *v})
			} else {
				res.Held++
			}
			return nil
		})
		if errors.Is(err, errHistoryPurgeBatchFull) {
			return nil
		}
		return err
	})
	switch {
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read the history: "+err.Error())
		return
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no deployment with that key")
		return
	case q.Get("confirm") != d.ProcessID:
		httpapi.Error(w, http.StatusBadRequest, "nothing was deleted: repeat the process id as ?confirm="+
			d.ProcessID+" to delete its finished instances for good. GET …/history-purge answers how many would go")
		return
	}

	if len(targets) > 0 {
		ran := false
		var runErr error
		s.do(func() {
			ran = true
			for i := range targets {
				s.proc.PurgeInstance(targets[i].key, &targets[i].pi)
			}
			// Applying the purge commands is the whole of the work: a purge unblocks no
			// in-process handler, so there is nothing for drive() to claim.
			runErr = s.proc.RunUntilIdle()
		})
		switch {
		case !ran:
			httpapi.Error(w, http.StatusServiceUnavailable, "server is shutting down; nothing was deleted")
			return
		case runErr != nil:
			httpapi.Error(w, http.StatusInternalServerError, "delete the instances: "+runErr.Error())
			return
		}
	}

	res.ProcessDefKey, res.ProcessID, res.Version, res.Before = key, d.ProcessID, d.Version, before
	res.Purged = len(targets)
	if res.Remaining {
		res.NextCursor = formatPurgeCursor(curAt, curKey)
	}
	if res.Purged > 0 {
		logging.Info(logging.HistoryPurged, "deleted finished instances for good on an administrator's request",
			slog.String("by", principalName(r)), slog.String("process", d.ProcessID),
			slog.Int("version", int(d.Version)), slog.Uint64("definition", key),
			slog.Int("instances", res.Purged), slog.Int("held", res.Held))
	}
	httpapi.JSON(w, http.StatusOK, res)
}

// historyPurgeGate names the gate a purge on this server waits on.
func (s *Server) historyPurgeGate() string {
	if s.exporter != nil {
		return historyPurgeGateExporter
	}
	return historyPurgeGateDurable
}

// purgeCutOff reads ?before=, RFC 3339 or unix nanoseconds, defaulting to the server's
// now. Zero and negative moments are refused rather than read as "no cut-off": the walk
// treats a zero cursor as "from the newest", and a purge of everything must be asked
// for as a moment, not reached by a typo.
func (s *Server) purgeCutOff(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return s.now(), nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil && t.UnixNano() > 0 {
		return t.UnixNano(), nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("before=%q is neither an RFC 3339 moment nor positive unix nanoseconds", raw)
	}
	return n, nil
}

// purgeLimit reads ?limit=, capped at historyPurgeBatchMax.
func purgeLimit(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return historyPurgeBatchDefault, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid limit %q (want a positive integer)", raw)
	}
	return min(n, historyPurgeBatchMax), nil
}

// formatPurgeCursor and parsePurgeCursor carry the walk's position between calls: the
// completion time and the key of the last instance passed, which is the pair the
// by-definition index is ordered by.
func formatPurgeCursor(at int64, key uint64) string {
	return strconv.FormatInt(at, 10) + ":" + strconv.FormatUint(key, 10)
}

func parsePurgeCursor(raw string) (int64, uint64, error) {
	atRaw, keyRaw, ok := strings.Cut(raw, ":")
	at, aerr := strconv.ParseInt(atRaw, 10, 64)
	key, kerr := strconv.ParseUint(keyRaw, 10, 64)
	if !ok || aerr != nil || kerr != nil || at <= 0 {
		return 0, 0, fmt.Errorf("invalid after=%q (want the nextCursor of the previous call)", raw)
	}
	return at, key, nil
}

// principalName is who a request was made by, for a log line that records an
// administrator's action.
func principalName(r *http.Request) string {
	if p := httpapi.PrincipalFrom(r.Context()); p != nil && p.UserID != "" {
		return p.UserID
	}
	return "single-user"
}
