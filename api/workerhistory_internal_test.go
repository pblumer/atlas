package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/clio"
)

// A worker's history in clio — and, above everything else here, the rule that it
// must never cost a job anything.
//
// The ring beside it is memory and forgets. This is the durable half, and the whole
// reason it is an event store rather than a table in Atlas is that keeping it must
// not become the engine's problem: not its retention, not its disk, and above all
// not its latency.

// fakeClio is a clio client that records what it was asked to write and can be made
// slow or broken on demand.
type fakeClio struct {
	mu      sync.Mutex
	written []clio.Event
	read    []clio.InboundEvent
	block   chan struct{} // when non-nil, WriteEvent waits on it
	err     error
}

func (c *fakeClio) WriteEvent(ctx context.Context, e clio.Event) error {
	c.mu.Lock()
	block, err := c.block, c.err
	c.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.written = append(c.written, e)
	return nil
}

func (c *fakeClio) GetState(context.Context, string, string) (map[string]any, error) {
	return nil, nil
}
func (c *fakeClio) Query(context.Context, string, string) (any, error) { return nil, nil }
func (c *fakeClio) ReadEvents(context.Context, clio.ReadEventsRequest) ([]clio.InboundEvent, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return nil, c.err
	}
	return append([]clio.InboundEvent(nil), c.read...), nil
}

func (c *fakeClio) wrote() []clio.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]clio.Event(nil), c.written...)
}

// exporterOver builds an exporter over a fake clio and drains it until the test ends.
func exporterOver(t *testing.T, scope string, c *fakeClio) *historyExporter {
	t.Helper()
	e := newHistoryExporter("telemetry", scope, func() (clio.Client, bool) { return c, true })
	quit := make(chan struct{})
	go e.run(quit)
	t.Cleanup(func() { close(quit) })
	return e
}

// waitForWrites blocks until the exporter has written n events, or the test fails.
func waitForWrites(t *testing.T, c *fakeClio, n int) []clio.Event {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := c.wrote(); len(got) >= n {
			return got
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("the exporter wrote %d events, want %d", len(c.wrote()), n)
	return nil
}

// The payoff: a settled job becomes an event an operator can still read tomorrow,
// carrying what the ring would have forgotten.
func TestASettledJobIsAppendedToTheHistory(t *testing.T) {
	c := &fakeClio{}
	e := exporterOver(t, HistoryScopeAll, c)

	e.offer(jobRun{
		JobKey: 7, Worker: "mail", Type: "io.atlas.mail.send", Outcome: jobRunFailed,
		ProcessInstanceKey: 42, ElementID: "send", Error: "connection refused",
		LeasedAt: 1_000_000_000, SettledAt: 1_060_000_000, In: `{"to":"a@example.ch"}`,
	})

	ev := waitForWrites(t, c, 1)[0]
	if ev.Subject != "/atlas/workers/mail" {
		t.Errorf("subject = %q, want the worker's own", ev.Subject)
	}
	if ev.Type != historyEventFailed {
		t.Errorf("type = %q, want %q", ev.Type, historyEventFailed)
	}
	// An at-least-once retry must not double the entry.
	if ev.IdempotencyKey != "7:failed" {
		t.Errorf("idempotencyKey = %q, want the job key and its outcome", ev.IdempotencyKey)
	}
	for k, want := range map[string]any{
		"worker": "mail", "type": "io.atlas.mail.send", "outcome": jobRunFailed,
		"error": "connection refused", "in": `{"to":"a@example.ch"}`,
	} {
		if got := ev.Data[k]; got != want {
			t.Errorf("data[%q] = %v, want %v", k, got, want)
		}
	}
	// Precomputed so a clio query asking "how long does this take" need not subtract.
	if got := ev.Data["tookMillis"]; got != int64(60) {
		t.Errorf("tookMillis = %v, want 60", got)
	}
}

// THE rule. A clio that has stopped answering must not hold up the run loop for a
// moment: offer runs on the single writer, and a job's progress is not negotiable
// against telemetry. This is the mail lesson (ADR-0168) applied before it bites.
func TestOfferNeverBlocksEvenWhenClioIsWedged(t *testing.T) {
	c := &fakeClio{block: make(chan struct{})} // every write hangs
	e := exporterOver(t, HistoryScopeAll, c)

	// Far more than the buffer holds, so it is certainly full partway through.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range historyBuffer * 3 {
			e.offer(jobRun{JobKey: uint64(i), Worker: "w", Outcome: jobRunCompleted})
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("offer blocked on a wedged clio — the run loop would have stalled")
	}
	if e.dropped.Load() == 0 {
		t.Error("nothing was dropped, so the buffer cannot have filled; the test proves nothing")
	}
	close(c.block)
}

// A gap is counted rather than hidden. An operator reading a history needs to know it
// is missing entries, or they will read "no failures" as "nothing failed".
func TestDroppedEntriesAreCounted(t *testing.T) {
	c := &fakeClio{block: make(chan struct{})}
	e := exporterOver(t, HistoryScopeAll, c)
	defer close(c.block)

	for i := range historyBuffer * 2 {
		e.offer(jobRun{JobKey: uint64(i), Worker: "w", Outcome: jobRunCompleted})
	}
	if got := e.dropped.Load(); got == 0 {
		t.Fatal("dropped = 0 after overflowing the buffer")
	}
}

// The narrow scope writes failures and nothing else, which is the setting for an
// operator who wants the history without the volume.
func TestTheFailedScopeWritesOnlyFailures(t *testing.T) {
	c := &fakeClio{}
	e := exporterOver(t, HistoryScopeFailed, c)

	e.offer(jobRun{JobKey: 1, Worker: "w", Outcome: jobRunCompleted})
	e.offer(jobRun{JobKey: 2, Worker: "w", Outcome: jobRunFailed})

	got := waitForWrites(t, c, 1)
	// Give a wrongly-written completion time to show up before concluding.
	time.Sleep(50 * time.Millisecond)
	if got = c.wrote(); len(got) != 1 || got[0].IdempotencyKey != "2:failed" {
		t.Errorf("wrote %d events (%+v), want only the failure", len(got), got)
	}
}

// A job still out with its worker is not history yet: it has no outcome, no duration
// and no result, and writing it would put a row in the store that a later one
// contradicts.
func TestAJobStillInFlightIsNotWritten(t *testing.T) {
	c := &fakeClio{}
	e := exporterOver(t, HistoryScopeAll, c)

	e.offer(jobRun{JobKey: 1, Worker: "w", Outcome: jobRunInFlight})
	time.Sleep(50 * time.Millisecond)

	if got := c.wrote(); len(got) != 0 {
		t.Errorf("wrote %+v, want nothing for a job that has not settled", got)
	}
}

// A worker that gave no name still did the work, and its history needs a subject
// clio will accept rather than one with an empty segment.
func TestAnUnnamedWorkerGetsAUsableSubject(t *testing.T) {
	c := &fakeClio{}
	e := exporterOver(t, HistoryScopeAll, c)

	e.offer(jobRun{JobKey: 1, Worker: "", Outcome: jobRunCompleted})

	if got := waitForWrites(t, c, 1)[0].Subject; got != "/atlas/workers/unnamed" {
		t.Errorf("subject = %q, want a named segment", got)
	}
}

// An exporter nobody configured is nil, and every call site tolerates that — which is
// what keeps the ordinary server, the one that names no connector, unchanged.
func TestNoConnectorMeansNoExporter(t *testing.T) {
	if e := newHistoryExporter("  ", HistoryScopeAll, nil); e != nil {
		t.Fatal("a blank connector produced an exporter")
	}
	var nilExporter *historyExporter
	nilExporter.offer(jobRun{Outcome: jobRunCompleted}) // must not panic
	nilExporter.run(make(chan struct{}))                // must return at once
}

// A server told to keep no history says so, rather than answering an empty list an
// operator would read as "nothing ever ran".
func TestTheHistoryEndpointSaysWhenNoneIsConfigured(t *testing.T) {
	srv, _ := newValidateServer(t)

	code, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/workers/w1/history", "", "")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, raw)
	}
	var out struct {
		Configured bool   `json:"configured"`
		Note       string `json:"note"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Configured {
		t.Error("a server with no history connector reported one")
	}
	if !strings.Contains(out.Note, "--worker-history") {
		t.Errorf("note = %q, want it to name the flag that turns this on", out.Note)
	}
}

// Reading it back: the rows come out newest first, matching the ring above them in
// the same dialog.
func TestTheHistoryReadsBackNewestFirst(t *testing.T) {
	c := &fakeClio{read: []clio.InboundEvent{
		{ID: "1", Type: historyEventCompleted, Data: map[string]any{"jobKey": 1, "settledAt": int64(100)}},
		{ID: "2", Type: historyEventFailed, Data: map[string]any{"jobKey": 2, "settledAt": int64(300)}},
		{ID: "3", Type: historyEventCompleted, Data: map[string]any{"jobKey": 3, "settledAt": int64(200)}},
	}}
	srv, _ := newValidateServer(t)
	srv.history = newHistoryExporter("telemetry", HistoryScopeAll, func() (clio.Client, bool) { return c, true })

	rows, truncated, err := srv.historyOf(context.Background(), "w1")
	if err != nil {
		t.Fatalf("historyOf: %v", err)
	}
	if truncated {
		t.Error("three rows were reported as a truncated window")
	}
	var order []int64
	for _, r := range rows {
		order = append(order, historyAt(r))
	}
	if len(order) != 3 || order[0] != 300 || order[1] != 200 || order[2] != 100 {
		t.Errorf("settle times = %v, want newest first", order)
	}
}

// A clio that cannot be reached is reported as such rather than as an empty history:
// "I could not ask" and "there is nothing" are different answers, and only one of
// them means the operator should stop looking.
func TestAnUnreachableHistoryIsReportedNotSwallowed(t *testing.T) {
	c := &fakeClio{err: errors.New("connection refused")}
	srv, _ := newValidateServer(t)
	srv.history = newHistoryExporter("telemetry", HistoryScopeAll, func() (clio.Client, bool) { return c, true })

	if _, _, err := srv.historyOf(context.Background(), "w1"); err == nil {
		t.Fatal("an unreachable clio produced no error")
	}

	code, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/workers/w1/history", "", "")
	if code != http.StatusBadGateway {
		t.Errorf("status = %d body=%s, want 502 — the failure is the far end's", code, raw)
	}
}

// A connector named but not configured is the operator's likeliest mistake, and it
// must not read as an empty history either.
func TestANamedButUnconfiguredConnectorIsReported(t *testing.T) {
	srv, _ := newValidateServer(t)
	srv.history = newHistoryExporter("telemetry", HistoryScopeAll, func() (clio.Client, bool) { return nil, false })

	_, _, err := srv.historyOf(context.Background(), "w1")
	if err == nil || !strings.Contains(err.Error(), "telemetry") {
		t.Errorf("err = %v, want it to name the connector that is missing", err)
	}
}

// The same gate as the ring: these rows carry the process's own variables, and moving
// them into the operator's own store changes where they live, not who may read them
// through this endpoint.
func TestWorkerHistoryIsAdminOnly(t *testing.T) {
	srv, _ := newValidateServer(t, WithAuth())

	if code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/workers/w1/history", "", ""); code == http.StatusOK {
		t.Fatalf("an unauthenticated caller read a worker's history: %s", body)
	}
}

// A window is honest about being one. clio holds whatever the operator's retention
// keeps; the console reads a bounded slice of it, and an operator who is not told so
// would read the last page as the whole story.
func TestAWindowOverALongHistorySaysItIsOne(t *testing.T) {
	c := &fakeClio{}
	for i := range maxHistoryRead {
		c.read = append(c.read, clio.InboundEvent{
			ID: string(rune('a' + i%26)), Type: historyEventCompleted,
			Data: map[string]any{"jobKey": i, "settledAt": int64(i)},
		})
	}
	srv, _ := newValidateServer(t)
	srv.history = newHistoryExporter("telemetry", HistoryScopeAll, func() (clio.Client, bool) { return c, true })

	rows, truncated, err := srv.historyOf(context.Background(), "w1")
	if err != nil {
		t.Fatalf("historyOf: %v", err)
	}
	if !truncated {
		t.Error("a read that filled its bound was not reported as a window")
	}
	if len(rows) != historyPageSize {
		t.Errorf("rows = %d, want the page size %d", len(rows), historyPageSize)
	}
	// Still the newest, not the oldest: a window on the wrong end would be worse than
	// no window at all.
	if got := historyAt(rows[0]); got != int64(maxHistoryRead-1) {
		t.Errorf("newest row settledAt = %d, want %d", got, maxHistoryRead-1)
	}
}

// Empty fields are left out rather than written as blanks. A clio query asking "which
// runs have an error" should not have to also exclude the empty string, and a store
// full of empty keys is a store nobody wants to write predicates against.
func TestEmptyFieldsAreNotWrittenToTheHistory(t *testing.T) {
	d := historyData(jobRun{JobKey: 1, Worker: "w", Type: "t", Outcome: jobRunCompleted})

	for _, absent := range []string{"error", "in", "out", "elementId", "processInstanceKey", "processDefKey"} {
		if _, present := d[absent]; present {
			t.Errorf("data carries an empty %q", absent)
		}
	}
	// A run that never settled has no duration, so the key an aggregate query groups
	// on is absent rather than zero.
	if _, present := d["tookMillis"]; present {
		t.Error("data carries a duration for a run with no settle time")
	}
}

// An unrecognised scope means all, rather than silently writing nothing. An operator
// who mistypes it should get more history than they asked for, never less: the
// failure mode of a typo must not be a silently empty record.
func TestAnUnrecognisedScopeMeansEverything(t *testing.T) {
	e := newHistoryExporter("telemetry", "faliures", nil)
	if e.scope != HistoryScopeAll {
		t.Errorf("scope = %q, want %q for an unrecognised value", e.scope, HistoryScopeAll)
	}
}
