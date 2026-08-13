package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/opensearch"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// These tests exercise the history-retention sweep (ADR-0115) end to end over the
// HTTP surface, but *deterministically*: a single fake clock is shared by the engine
// (so a finished instance's CompletedAt is exact) and the sweep (so its eligibility
// cutoff is exact), and the sweep is driven through an explicit trigger channel rather
// than a real ticker. Eligibility is decided by advancing the clock, not by tuning a
// max age against real setup time, and a sweep's completion is awaited on a channel
// rather than polled — so there is no wall-clock window to race and no time.Sleep.
// This is the deterministic replacement for the earlier timing-based retention tests,
// which had to widen a max age to 500ms (PR #313) to keep a sweep tick from racing the
// pre-purge assertion (AGENTS.md: tests must not depend on wall-clock time or
// scheduling; invariant I4).

// retentionBPMN is a process that parks at a service task (job type "payment", no
// worker), so a created instance stays active until the test terminates it — putting a
// finished instance into history with a CompletedAt the shared clock controls.
const retentionBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="order" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="task">
      <extensionElements><zeebe:taskDefinition type="payment" retries="5"/></extensionElements>
    </serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="task"/>
    <sequenceFlow id="f2" sourceRef="task" targetRef="end"/>
  </process>
</definitions>`

// fakeClock is a settable clock shared by the engine and the retention sweep. Now() is
// stable across reads (unlike the engine test's auto-incrementing manualClock) so a
// test can freeze time, act, then advance it by an exact amount. It is read from the
// engine loop, the sweeper goroutine, and set from the test goroutine, so it is atomic.
type fakeClock struct{ ns atomic.Int64 }

func (c *fakeClock) Now() int64   { return c.ns.Load() }
func (c *fakeClock) set(ns int64) { c.ns.Store(ns) }

// retentionHarness is a server whose retention sweep is under deterministic control.
type retentionHarness struct {
	t     *testing.T
	srv   *Server
	x     deployTestHarness
	clk   *fakeClock
	ticks chan<- time.Time
	swept <-chan struct{}
}

// newRetentionHarness boots a server over a temp dir with the shared fake clock wired
// into both the engine and the retention sweep, and the sweep's trigger replaced by an
// explicit channel. retentionOpts adds the retention configuration (and, for the export
// gate, an exporter). The clock starts at t0.
func newRetentionHarness(t *testing.T, t0 int64, retentionOpts ...Option) retentionHarness {
	t.Helper()
	clk := &fakeClock{}
	clk.set(t0)

	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, log, store, clk) // engine stamps CompletedAt from the same clock
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	ticks := make(chan time.Time)
	swept := make(chan struct{})
	opts := append([]Option{withClock(clk.Now), withRetentionTrigger(ticks, swept)}, retentionOpts...)
	srv, err := New(proc, store, dir, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { srv.Close(); _ = store.Close(); _ = log.Close() })
	return retentionHarness{t, srv, deployTestHarness{t, srv.Handler()}, clk, ticks, swept}
}

// sweep triggers exactly one retention sweep and blocks until it has completed. The
// tick value is ignored by the sweeper (it reads the clock, not the tick), so the
// handshake — not any duration — is what makes this deterministic. The timeouts are
// failsafes that only fire if the sweeper deadlocks; they are not a timing dependency
// of the passing path.
func (h retentionHarness) sweep() {
	h.t.Helper()
	select {
	case h.ticks <- time.Time{}:
	case <-time.After(2 * time.Second):
		h.t.Fatal("retention sweeper did not accept a tick")
	}
	select {
	case <-h.swept:
	case <-time.After(2 * time.Second):
		h.t.Fatal("retention sweep did not complete")
	}
}

// instanceRow is the subset of the instances listing a retention test inspects.
type instanceRow struct {
	Key   uint64 `json:"key"`
	State string `json:"state"`
}

// list returns the current instances listing, which includes finished instances from
// history until retention purges them.
func (h retentionHarness) list() []instanceRow {
	h.t.Helper()
	code, body := h.x.do(http.MethodGet, "/api/v1/instances", "")
	if code != http.StatusOK {
		h.t.Fatalf("list instances status=%d body=%s", code, body)
	}
	var rows []instanceRow
	if err := json.Unmarshal(body, &rows); err != nil {
		h.t.Fatalf("decode instances: %v (%s)", err, body)
	}
	return rows
}

func (h retentionHarness) deploy() {
	h.t.Helper()
	if code, body := h.x.do(http.MethodPost, "/api/v1/deployments", retentionBPMN); code != http.StatusOK {
		h.t.Fatalf("deploy status=%d body=%s", code, body)
	}
}

// createParked starts one instance of retentionBPMN, which parks at its service task
// (job type "payment", no worker), and returns its key. Active instances list in key
// order, so the newest — the one just created — is the last row.
func (h retentionHarness) createParked() uint64 {
	h.t.Helper()
	if code, body := h.x.do(http.MethodPost, "/api/v1/processes/1/instances", "{}"); code != http.StatusOK {
		h.t.Fatalf("create status=%d body=%s", code, body)
	}
	rows := h.list()
	return rows[len(rows)-1].Key
}

func (h retentionHarness) terminate(key uint64) {
	h.t.Helper()
	if code, body := h.x.do(http.MethodDelete, "/api/v1/instances/"+strconv.FormatUint(key, 10), ""); code != http.StatusOK {
		h.t.Fatalf("terminate status=%d body=%s", code, body)
	}
}

// TestRetentionPurgesWhenAged: a finished instance is left untouched while it is
// younger than the max age and hard-deleted once it ages past it — both transitions
// asserted exactly, by advancing the shared clock, with no wall-clock race.
func TestRetentionPurgesWhenAged(t *testing.T) {
	const t0 = int64(10 * time.Hour)
	const maxAge = time.Hour
	h := newRetentionHarness(t, t0, WithRetention(maxAge))
	h.deploy()
	key := h.createParked()
	h.terminate(key) // CompletedAt == t0 (engine reads the frozen clock)

	if rows := h.list(); len(rows) != 1 || rows[0].State != "terminated" {
		t.Fatalf("want one terminated instance before purge, got %v", rows)
	}

	// One nanosecond short of the max age: still ineligible (cutoff = now-maxAge < t0),
	// so a sweep must not purge it. This is the assertion the old test could only make
	// probabilistically by widening the age; here it is exact.
	h.clk.set(t0 + int64(maxAge) - 1)
	h.sweep()
	if rows := h.list(); len(rows) != 1 {
		t.Fatalf("instance purged before it aged past the max age: %v", rows)
	}

	// Exactly at the max age: eligible (cutoff == t0 == CompletedAt), so the sweep
	// purges it.
	h.clk.set(t0 + int64(maxAge))
	h.sweep()
	if rows := h.list(); len(rows) != 0 {
		t.Fatalf("aged instance was not purged: %v", rows)
	}
}

// TestRetentionDrainsBacklogOnePerTick: with a per-tick batch of 1, three finished
// instances drain exactly one per sweep — the bounded, resumable cursor (ADR-0115) —
// asserted as an exact 3→2→1→0 sequence rather than "eventually empty".
func TestRetentionDrainsBacklogOnePerTick(t *testing.T) {
	const t0 = int64(10 * time.Hour)
	const maxAge = time.Hour
	h := newRetentionHarness(t, t0, WithRetention(maxAge), WithRetentionBatch(1))
	h.deploy()
	keys := []uint64{h.createParked(), h.createParked(), h.createParked()}
	for _, k := range keys {
		h.terminate(k) // all complete at t0
	}
	if n := len(h.list()); n != 3 {
		t.Fatalf("want 3 terminated instances before purge, got %d", n)
	}

	// Age them all past the cutoff, then drain one per tick.
	h.clk.set(t0 + int64(maxAge))
	for want := 2; want >= 0; want-- {
		h.sweep()
		if n := len(h.list()); n != want {
			t.Fatalf("after a sweep with batch 1: %d instances remain, want %d", n, want)
		}
	}
}

// gateStub is an OpenSearch _bulk endpoint that fails while paused, so the exporter's
// high-water mark cannot advance — letting a test hold the retention export gate shut.
type gateStub struct {
	srv    *httptest.Server
	paused atomic.Bool
}

func newGateStub(t *testing.T) *gateStub {
	t.Helper()
	g := &gateStub{}
	g.paused.Store(true)
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.paused.Load() {
			http.Error(w, "paused", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"errors":false,"items":[]}`)
	}))
	t.Cleanup(g.srv.Close)
	return g
}

// TestRetentionWaitsForExport: with the exporter enabled, an aged instance is NOT
// purged while its events are unexported (the exporter is stalled) and IS purged once
// export catches up — the export-before-delete gate (ADR-0115). The export cadence is
// set to an hour so the background exporter loop stays dormant; the test advances the
// high-water mark by driving Tick itself, so both the gate-shut and gate-open outcomes
// are observed deterministically without a time.Sleep.
func TestRetentionWaitsForExport(t *testing.T) {
	stub := newGateStub(t) // starts paused: bulk fails, so the exporter never advances
	const t0 = int64(10 * time.Hour)
	const maxAge = time.Hour
	h := newRetentionHarness(t, t0,
		WithRetention(maxAge),
		WithOpenSearchExporter(opensearch.Config{URL: stub.srv.URL, Index: "atlas-retention-test"}),
		WithOpenSearchExportInterval(time.Hour), // keep the background exporter loop dormant
	)
	h.deploy()
	key := h.createParked()
	h.terminate(key)

	// Age the instance past the max age so eligibility is decided solely by the export
	// gate, not by time.
	h.clk.set(t0 + int64(maxAge))

	// The exporter is stalled (paused stub), so its high-water mark is 0 and the
	// instance's events are not provably exported: retention must leave it alone.
	h.sweep()
	if rows := h.list(); len(rows) != 1 || rows[0].State != "terminated" {
		t.Fatalf("instance was purged before its events were exported (gate breached): %v", rows)
	}

	// Open the gate and drive the exporter to full catch-up, advancing the high-water
	// mark past the instance's terminal position.
	stub.paused.Store(false)
	h.exportUntilCaughtUp()

	// Now the instance's events are provably exported, so the sweep purges it.
	h.sweep()
	if rows := h.list(); len(rows) != 0 {
		t.Fatalf("exported instance was not purged once the gate opened: %v", rows)
	}
}

// TestRetentionSweeperRealTickerShutsDown covers the production wiring the
// deterministic tests deliberately bypass: with no injected trigger the sweeper builds
// a real ticker (ADR-0115), and it must exit promptly when the server closes rather
// than leak its goroutine. A hung Close (the failure this guards) would block the
// server's WaitGroup forever; the timeout turns that into a test failure instead.
func TestRetentionSweeperRealTickerShutsDown(t *testing.T) {
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer func() { _ = store.Close(); _ = log.Close() }()
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	// Retention on, a long interval, and no injected trigger → the real-ticker path.
	srv, err := New(proc, store, dir, WithRetention(time.Hour), WithRetentionInterval(time.Hour))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	done := make(chan struct{})
	go func() { srv.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("retention sweeper on the real ticker did not shut down on Close")
	}
}

// exportUntilCaughtUp drives the exporter's Tick directly until its high-water mark
// stops advancing, i.e. it has indexed every durable record. Driving Tick here (rather
// than waiting for the dormant background loop) keeps the catch-up deterministic.
func (h retentionHarness) exportUntilCaughtUp() {
	h.t.Helper()
	for i := 0; i < 1000; i++ {
		before := h.srv.exporter.HighWaterMark()
		if _, err := h.srv.exporter.Tick(context.Background()); err != nil {
			h.t.Fatalf("exporter Tick: %v", err)
		}
		if h.srv.exporter.HighWaterMark() == before {
			return // caught up: nothing durable beyond the mark
		}
	}
	h.t.Fatal("exporter did not catch up within the iteration bound")
}
