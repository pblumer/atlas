package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/connector/clio"
	"github.com/pblumer/atlas/logging"
)

// Worker job history, kept in clio rather than in Atlas.
//
// The ring beside this (workerjobs.go) is the last fifty jobs per worker, in memory,
// emptied by a restart. That is the right shape for "what is happening now" and the
// wrong one for "what failed on Tuesday". The obvious next step would be a durable
// store in Atlas — and it is the wrong step three times over:
//
//   - The sidecar store is one fsync'd file per record and a directory scan per read.
//     On the lease path, at job rates, that is not a store, it is a brake.
//   - A second Pebble instance is a second thing to back up, compact and restore, for
//     a debugging window.
//   - The event log cannot carry it without putting the resolved input variables into
//     the WAL, where they would be replayed forever and read twice on recovery.
//
// And all three share a problem that has nothing to do with performance: **retention**.
// Atlas deletes finished instances after their age (ADR-0115). Job rows holding those
// instances' variables, kept independently for a day or two, would outlive the data
// they describe — a hole in exactly the promise retention makes.
//
// clio is an event store, this is a stream of events, and Atlas already speaks it
// (ADR-0036, and ADR-draft-worker-job-history-in-clio for the whole argument). So the history goes there: no new storage in Atlas, no new invariant
// surface, no second backup path, and retention becomes the operator's own policy in
// their own store rather than another flag here. It is opt-in by naming a clio
// connector on the command line — an operator who names none keeps the ring and
// nothing else changes.
//
// **It must never slow a job down.** That is the lesson mail taught (ADR-0168): the
// export is a non-blocking send to a bounded buffer from the run loop, drained by a
// goroutine that does its network writes outside it. A clio that is slow or gone
// fills the buffer, and a full buffer *drops* and counts, because an engine that
// stalls to record what it did is worse than one with a gap in its telemetry.

// Event types and the subject a worker's history is written under. One subject per
// worker is what the console reads back; the job type is in the event data, so a
// clio query can group by it without a second subject tree.
const (
	historyEventCompleted = "atlas.job.completed"
	historyEventFailed    = "atlas.job.failed"
	historySubjectRoot    = "/atlas/workers/"
)

// Scopes an operator can choose for what reaches clio.
const (
	// HistoryScopeAll writes every settled job. It is what "how long does a mail
	// send take" needs, and it is the larger bill.
	HistoryScopeAll = "all"
	// HistoryScopeFailed writes only the failures — much less volume, and still the
	// question most often asked of a history.
	HistoryScopeFailed = "failed"
)

// historyBuffer is how many settled jobs may wait to be written. Large enough to ride
// out a slow clio or a burst, small enough that the memory is bounded and a
// persistently unreachable clio is noticed rather than swallowed.
const historyBuffer = 1024

// historyWriteTimeout bounds one append. It is not the connector call budget
// (ADR-0149): nothing waits on this, so the only thing the timeout protects is the
// drain loop's ability to keep moving.
const historyWriteTimeout = 10 * time.Second

// historyExporter appends settled job runs to a clio connector.
type historyExporter struct {
	// connector is the clio connector's name, read off this server's command line and
	// from nowhere else — the same rule the supervisor follows.
	connector string
	scope     string
	events    chan jobRun
	// dropped counts what the buffer could not take. It is reported rather than
	// hidden: a history with a silent gap is worse than one that says where it is.
	dropped atomic.Uint64
	// client resolves the connector on the run loop, because clientreg.Registry has
	// no lock of its own and a rebuild swaps its map wholesale. Once per batch, not
	// once per event.
	client func() (clio.Client, bool)
}

// newHistoryExporter builds an exporter for a named connector. A blank name means an
// operator asked for no history, and nil is what every call site then checks for.
func newHistoryExporter(connector, scope string, client func() (clio.Client, bool)) *historyExporter {
	if strings.TrimSpace(connector) == "" {
		return nil
	}
	if scope != HistoryScopeFailed {
		scope = HistoryScopeAll
	}
	return &historyExporter{
		connector: strings.TrimSpace(connector),
		scope:     scope,
		events:    make(chan jobRun, historyBuffer),
		client:    client,
	}
}

// offer hands a settled job run to the exporter without ever blocking. It runs on the
// run loop, which is the whole reason it may not block: a clio that stopped answering
// must cost this engine nothing.
func (e *historyExporter) offer(run jobRun) {
	if e == nil || run.Outcome == jobRunInFlight {
		return // a lease that has not settled is not yet a history entry
	}
	if e.scope == HistoryScopeFailed && run.Outcome != jobRunFailed {
		return
	}
	select {
	case e.events <- run:
	default:
		e.dropped.Add(1)
	}
}

// run drains the buffer until the server quits, appending each job to clio. It is
// deliberately sequential: the point is to stay out of the engine's way, not to
// saturate the event store.
func (e *historyExporter) run(quit <-chan struct{}) {
	if e == nil {
		return
	}
	var lastDropReport uint64
	for {
		select {
		case <-quit:
			return
		case run := <-e.events:
			client, ok := e.client()
			if !ok {
				// Named but not configured, or configured and unusable. Saying it once
				// per drop-report interval rather than once per job keeps a
				// misconfiguration visible without drowning the log.
				e.dropped.Add(1)
			} else if err := e.write(client, run); err != nil {
				logging.Warn(logging.WorkerHistoryFailed, "could not append a job run to the history connector",
					slog.String("connector", e.connector), slog.String("error", err.Error()))
			}
			// A gap is worth one line each time it grows by a buffer's worth: enough to
			// notice, never enough to flood.
			if dropped := e.dropped.Load(); dropped >= lastDropReport+historyBuffer {
				lastDropReport = dropped
				logging.Warn(logging.WorkerHistoryFailed, "job history is dropping entries; the engine is not waiting for it",
					slog.String("connector", e.connector), slog.Uint64("dropped", dropped))
			}
		}
	}
}

// write appends one job run. The idempotency key is the job key and its outcome, so
// an append retried after a timeout is de-duplicated by clio rather than doubled.
func (e *historyExporter) write(client clio.Client, run jobRun) error {
	ctx, cancel := context.WithTimeout(context.Background(), historyWriteTimeout)
	defer cancel()
	eventType := historyEventCompleted
	if run.Outcome == jobRunFailed {
		eventType = historyEventFailed
	}
	return client.WriteEvent(ctx, clio.Event{
		Subject:        historySubjectRoot + historyWorkerSegment(run.Worker),
		Type:           eventType,
		Data:           historyData(run),
		IdempotencyKey: fmt.Sprintf("%d:%s", run.JobKey, run.Outcome),
	})
}

// historyData is one job run as clio event data. It is the console's own row plus the
// worker, flattened so a clio query can filter on any of it without unwrapping.
func historyData(run jobRun) map[string]any {
	d := map[string]any{
		"worker":    run.Worker,
		"jobKey":    run.JobKey,
		"type":      run.Type,
		"outcome":   run.Outcome,
		"leasedAt":  run.LeasedAt,
		"settledAt": run.SettledAt,
		"retries":   run.Retries,
	}
	if run.SettledAt > run.LeasedAt {
		// Precomputed because "how long does this take" is the question a history is
		// most often asked, and a CEL predicate should not have to subtract.
		d["tookMillis"] = (run.SettledAt - run.LeasedAt) / 1e6
	}
	// Empty fields are left out rather than written as blanks: a clio query asking
	// "which runs have an error" should not have to exclude the empty string.
	for k, v := range map[string]string{
		"elementId": run.ElementID, "error": run.Error, "in": run.In, "out": run.Out,
	} {
		if v != "" {
			d[k] = v
		}
	}
	if run.ProcessInstanceKey != 0 {
		d["processInstanceKey"] = run.ProcessInstanceKey
	}
	if run.ProcessDefKey != 0 {
		d["processDefKey"] = run.ProcessDefKey
	}
	return d
}

// historyWorkerSegment renders a worker id as one subject segment. A worker that gave
// no name is real — it still did the work — and lands under a name that says so
// rather than producing a subject with an empty segment clio would refuse.
func historyWorkerSegment(worker string) string {
	w := strings.TrimSpace(worker)
	if w == "" {
		return "unnamed"
	}
	return strings.ReplaceAll(w, "/", "_")
}

// Bounds on a history read. clio holds whatever the operator's retention keeps, which
// may be far more than a console page: the read covers the newest maxHistoryRead
// events and says so when it hit that cap, rather than pretending the window is the
// whole story.
const (
	maxHistoryRead  = 500
	historyPageSize = 100
)

// historyOf reads one worker's job history back out of clio, newest first.
//
// clio reads oldest-first from a cursor, so this takes a bounded read and returns its
// tail. That is honest about what it is — a window on the newest entries — and it is
// why the response carries `truncated`: past that, the answer is a clio query, which
// is a better tool for "every mail failure this month" than a dialog ever will be.
func (s *Server) historyOf(ctx context.Context, worker string) ([]map[string]any, bool, error) {
	if s.history == nil {
		return nil, false, nil
	}
	client, ok := s.history.client()
	if !ok {
		return nil, false, fmt.Errorf("the history connector %q is not configured on this server", s.history.connector)
	}
	events, err := client.ReadEvents(ctx, clio.ReadEventsRequest{
		Subject: historySubjectRoot + historyWorkerSegment(worker),
		Types:   []string{historyEventCompleted, historyEventFailed},
		Limit:   maxHistoryRead,
	})
	if err != nil {
		return nil, false, err
	}
	rows := make([]map[string]any, 0, len(events))
	for _, ev := range events {
		if ev.Data != nil {
			rows = append(rows, ev.Data)
		}
	}
	// Newest first, matching the ring above it in the same dialog.
	sort.SliceStable(rows, func(i, j int) bool { return historyAt(rows[i]) > historyAt(rows[j]) })
	truncated := len(rows) > historyPageSize
	if truncated {
		rows = rows[:historyPageSize]
	}
	return rows, truncated || len(events) >= maxHistoryRead, nil
}

// historyAt is a row's settle time for ordering. clio returns numbers as
// json.Number, so this reads whatever shape the round trip produced rather than
// asserting one.
func historyAt(row map[string]any) int64 {
	switch v := row["settledAt"].(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	case interface{ Int64() (int64, error) }: // json.Number
		if n, err := v.Int64(); err == nil {
			return n
		}
	}
	return 0
}

// handleWorkerHistory reads one worker's job history back out of clio.
//
// **Admin only, like the ring it sits under**, and for the same reason: the rows
// carry the process's own variables. That the data now lives in the operator's event
// store rather than in this process changes where it is kept, not who may read it
// through here.
func (s *Server) handleWorkerHistory(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return // requireAdmin wrote 403
	}
	if s.history == nil {
		// Not an error: this server was simply not asked to keep one. Saying which
		// flag turns it on is more use than a 404 that leaves an operator guessing.
		httpapi.JSON(w, http.StatusOK, map[string]any{
			"worker": r.PathValue("id"), "jobs": []map[string]any{}, "configured": false,
			"note": "no job history is configured; name a clio connector with --worker-history to keep one",
		})
		return
	}
	worker := strings.TrimSpace(r.PathValue("id"))
	rows, truncated, err := s.historyOf(r.Context(), worker)
	if err != nil {
		httpapi.Error(w, http.StatusBadGateway, "read the job history: "+err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{
		"worker": worker, "jobs": rows, "configured": true,
		"connector": s.history.connector, "scope": s.history.scope,
		"truncated": truncated, "dropped": s.history.dropped.Load(),
	})
}
