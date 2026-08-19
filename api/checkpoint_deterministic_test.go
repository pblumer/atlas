package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pblumer/atlas/checkpoint"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// These tests cover ADR-0131 slice 5: the server-side checkpoint cadence, and the
// startup recovery that reads exactly what that cadence publishes. Like the retention
// sweep and the OpenSearch exporter, the cadence is driven through an explicit trigger
// channel rather than a real ticker, so a checkpoint happens precisely when the test
// says it does — no wall-clock window to race, no sleeps (invariant I4).
//
// Nothing here deletes a WAL segment. This slice only *produces* checkpoints and
// recovers from them; the log stays the whole source of truth, so every assertion
// below is about an optimization that can be wrong only by being slow.

// checkpointBPMN parks at a service task with no worker, so a created instance stays
// active and leaves durable state for a checkpoint to capture.
const checkpointBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="ledger" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="task">
      <extensionElements><zeebe:taskDefinition type="settle" retries="5"/></extensionElements>
    </serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="task"/>
    <sequenceFlow id="f2" sourceRef="task" targetRef="end"/>
  </process>
</definitions>`

// checkpointHarness is one boot of the whole stack over a fixed data dir, with the
// checkpoint cadence under explicit control. It can be closed and re-booted over the
// same dir to exercise a restart through a published checkpoint.
type checkpointHarness struct {
	t      *testing.T
	dir    string
	srv    *Server
	x      deployTestHarness
	store  *state.Store
	log    *wal.Log
	ticks  chan<- time.Time
	done   <-chan struct{}
	closed bool
}

// bootCheckpointServer opens the WAL and store under dir, recovers through the
// checkpoint root the server publishes into — the wiring under test — and starts a
// server whose checkpoint loop is trigger-driven. The interval is only what enables
// the loop; the injected trigger is what actually fires it.
func bootCheckpointServer(t *testing.T, dir string, opts ...Option) *checkpointHarness {
	t.Helper()
	// A one-byte segment cap rolls a segment per batch, so the log a restart replays
	// is many segments rather than one. That is what lets a checkpointed recovery
	// actually skip a prefix here instead of trivially replaying a single segment.
	lg, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal"), MaxSegmentSize: 1})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, lg, store, nil)
	if err := proc.RecoverFrom(checkpoint.Dir(dir)); err != nil {
		t.Fatalf("RecoverFrom: %v", err)
	}
	ticks := make(chan time.Time)
	done := make(chan struct{})
	all := append([]Option{WithCheckpoints(time.Hour), withCheckpointTrigger(ticks, done)}, opts...)
	srv, err := New(proc, store, dir, all...)
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	h := &checkpointHarness{t: t, dir: dir, srv: srv, x: deployTestHarness{t, srv.Handler()},
		store: store, log: lg, ticks: ticks, done: done}
	t.Cleanup(h.close)
	return h
}

func (h *checkpointHarness) close() {
	if h.closed {
		return
	}
	h.closed = true
	h.srv.Close()
	if err := h.store.Close(); err != nil {
		h.t.Errorf("store.Close: %v", err)
	}
	if err := h.log.Close(); err != nil {
		h.t.Errorf("log.Close: %v", err)
	}
}

// checkpointNow triggers exactly one checkpoint pass and blocks until it finishes.
// The handshake is what makes this deterministic; the timeouts are failsafes that fire
// only if the loop deadlocks, never on the passing path.
func (h *checkpointHarness) checkpointNow() {
	h.t.Helper()
	select {
	case h.ticks <- time.Time{}:
	case <-time.After(2 * time.Second):
		h.t.Fatal("checkpoint loop did not accept a tick")
	}
	select {
	case <-h.done:
	case <-time.After(2 * time.Second):
		h.t.Fatal("checkpoint pass did not complete")
	}
}

// published lists the checkpoint positions currently on disk under the data dir.
func (h *checkpointHarness) published() []uint64 {
	h.t.Helper()
	positions, err := checkpoint.List(checkpoint.Dir(h.dir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		h.t.Fatalf("checkpoint.List: %v", err)
	}
	return positions
}

func (h *checkpointHarness) applied() uint64 {
	h.t.Helper()
	pos, err := h.store.LastAppliedPosition()
	if err != nil {
		h.t.Fatalf("LastAppliedPosition: %v", err)
	}
	return pos
}

func (h *checkpointHarness) deploy() {
	h.t.Helper()
	if code, body := h.x.do(http.MethodPost, "/api/v1/deployments", checkpointBPMN); code != http.StatusOK {
		h.t.Fatalf("deploy status=%d body=%s", code, body)
	}
}

// createInstance starts one parked instance and returns its key.
func (h *checkpointHarness) createInstance() uint64 {
	h.t.Helper()
	if code, body := h.x.do(http.MethodPost, "/api/v1/processes/1/instances", "{}"); code != http.StatusOK {
		h.t.Fatalf("create status=%d body=%s", code, body)
	}
	keys := h.instanceKeys()
	return keys[len(keys)-1]
}

// instanceKeys is the current instance listing, in the order the API returns it.
func (h *checkpointHarness) instanceKeys() []uint64 {
	h.t.Helper()
	code, body := h.x.do(http.MethodGet, "/api/v1/instances", "")
	if code != http.StatusOK {
		h.t.Fatalf("list instances status=%d body=%s", code, body)
	}
	var rows []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		h.t.Fatalf("decode instances: %v (%s)", err, body)
	}
	keys := make([]uint64, len(rows))
	for i, r := range rows {
		keys[i] = r.Key
	}
	return keys
}

// TestCheckpointLoopPublishesAtAppliedPosition is the core wiring assertion: a tick
// publishes exactly one fully verifiable checkpoint, for this partition, at the store's
// durable applied position, into the root the server's own recovery reads.
func TestCheckpointLoopPublishesAtAppliedPosition(t *testing.T) {
	dir := t.TempDir()
	h := bootCheckpointServer(t, dir)
	h.deploy()
	h.createInstance()

	applied := h.applied()
	if applied == 0 {
		t.Fatal("fixture applied nothing; the test would prove nothing")
	}
	h.checkpointNow()

	positions := h.published()
	if len(positions) != 1 || positions[0] != applied {
		t.Fatalf("published %v, want exactly [%d] (the store's applied position)", positions, applied)
	}
	// Verify, not Load: the state files must be intact too, since they are what a
	// restore reads. This is the same check compaction gates deletion on.
	m, err := checkpoint.Verify(checkpoint.Dir(dir), applied)
	if err != nil {
		t.Fatalf("published checkpoint does not verify: %v", err)
	}
	if m.Partition != 1 {
		t.Errorf("checkpoint partition = %d, want 1", m.Partition)
	}
	if m.AppliedPosition != applied || m.HighestPosition < applied {
		t.Errorf("manifest applied=%d highest=%d, want applied=%d and highest >= it",
			m.AppliedPosition, m.HighestPosition, applied)
	}
	// The manifest's AtlasVersion is stamped by the binary (cmd/atlas sets
	// engine.BuildVersion from api.Version), so it is empty in a library test and
	// deliberately not asserted here — it is diagnostics, nothing branches on it.
}

// TestCheckpointLoopSkipsIdleServer: with nothing applied there is no state worth
// snapshotting, so a tick publishes nothing rather than an empty checkpoint at
// position zero that recovery would have to skip past anyway.
func TestCheckpointLoopSkipsIdleServer(t *testing.T) {
	dir := t.TempDir()
	h := bootCheckpointServer(t, dir)
	if h.applied() != 0 {
		t.Fatal("a freshly booted server already applied something")
	}
	h.checkpointNow()
	if positions := h.published(); len(positions) != 0 {
		t.Fatalf("idle server published %v, want nothing", positions)
	}
}

// TestCheckpointLoopPrunesToRetention: an unbounded checkpoint schedule would pin
// every SSTable it ever hard-linked, so each pass keeps only the newest few.
func TestCheckpointLoopPrunesToRetention(t *testing.T) {
	dir := t.TempDir()
	h := bootCheckpointServer(t, dir, WithCheckpointRetention(2))
	h.deploy()

	var wanted []uint64
	for i := 0; i < 3; i++ {
		h.createInstance() // advance the applied position so each pass is a new checkpoint
		h.checkpointNow()
		wanted = append(wanted, h.applied())
	}
	if wanted[0] == wanted[1] || wanted[1] == wanted[2] {
		t.Fatalf("the three passes did not land on distinct positions (%v); the fixture proves nothing", wanted)
	}

	positions := h.published()
	if len(positions) != 2 {
		t.Fatalf("kept %d checkpoints (%v), want 2", len(positions), positions)
	}
	if positions[0] != wanted[1] || positions[1] != wanted[2] {
		t.Errorf("kept %v, want the newest two %v", positions, wanted[1:])
	}
}

// TestCheckpointedRestartServesRecoveredState: after a checkpoint, a full restart that
// recovers *through the checkpoint root* still serves exactly the instances the first
// boot did and still mints fresh keys — the regression guard on wiring RecoverFrom into
// startup.
//
// It does not prove the prefix was skipped, and cannot: this restart is clean, so the
// store is already caught up and replaying more of the log changes nothing. Skipping,
// and the seeding that makes it safe when the store trails the log after a crash, are
// proven in engine/restore_test.go against exactly that condition
// (TestRecoverFromCheckpointNeedsNoPrefixSegments, TestRecoverFromCheckpointSeedsKeyCounter).
func TestCheckpointedRestartServesRecoveredState(t *testing.T) {
	dir := t.TempDir()
	first := bootCheckpointServer(t, dir)
	first.deploy()
	first.createInstance()
	first.createInstance()
	first.checkpointNow()
	before := first.instanceKeys()
	if len(before) != 2 {
		t.Fatalf("first boot listed %d instances, want 2", len(before))
	}
	first.close()

	second := bootCheckpointServer(t, dir)
	if got := second.instanceKeys(); !equalKeys(got, before) {
		t.Fatalf("after a checkpointed restart instances = %v, want %v", got, before)
	}
	fresh := second.createInstance()
	for _, k := range before {
		if fresh == k {
			t.Fatalf("new instance reused key %d from before the restart", fresh)
		}
	}
}

func equalKeys(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestCheckpointsOffUnlessConfigured: no cadence, no checkpoints — and in particular
// no checkpoint directory appears in the data dir of a server that never asked for one.
func TestCheckpointsOffUnlessConfigured(t *testing.T) {
	dir := t.TempDir()
	lg, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, lg, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv, err := New(proc, store, dir)
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	defer func() {
		srv.Close()
		if err := store.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
		if err := lg.Close(); err != nil {
			t.Errorf("log.Close: %v", err)
		}
	}()
	x := deployTestHarness{t, srv.Handler()}
	if code, body := x.do(http.MethodPost, "/api/v1/deployments", checkpointBPMN); code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	if code, body := x.do(http.MethodPost, "/api/v1/processes/1/instances", "{}"); code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", code, body)
	}
	if _, err := os.Stat(checkpoint.Dir(dir)); !os.IsNotExist(err) {
		t.Fatalf("a server with no checkpoint cadence created %s (stat err = %v)", checkpoint.Dir(dir), err)
	}
}

// TestCheckpointLoopSurvivesPublishFailure: a checkpoint is an optimization, so a
// failing one is logged and retried on the next tick rather than taking the server
// down or wedging the loop. A file where the checkpoint root belongs makes every
// publish fail.
func TestCheckpointLoopSurvivesPublishFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(checkpoint.Dir(dir), []byte("in the way"), 0o644); err != nil {
		t.Fatalf("seed blocking file: %v", err)
	}
	h := bootCheckpointServer(t, dir)
	h.deploy()
	h.createInstance()

	h.checkpointNow()
	h.checkpointNow() // the loop still accepts and completes a second pass

	// Nothing was published, and nothing forced the blocking file out of the way.
	info, err := os.Stat(checkpoint.Dir(dir))
	if err != nil {
		t.Fatalf("stat checkpoint root: %v", err)
	}
	if info.IsDir() {
		t.Fatal("a failing checkpoint replaced the file in its root's place with a directory")
	}
	// The server is still serving: the failed checkpoint touched nothing else.
	if keys := h.instanceKeys(); len(keys) != 1 {
		t.Fatalf("instances = %v, want the one created before the failed checkpoints", keys)
	}
}

// TestCheckpointLoopRealTickerShutsDown covers the production path — no injected
// trigger, a real ticker — and asserts Close stops it. The interval is long enough
// that the passing path never depends on a tick arriving.
func TestCheckpointLoopRealTickerShutsDown(t *testing.T) {
	dir := t.TempDir()
	lg, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, lg, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv, err := New(proc, store, dir, WithCheckpoints(time.Hour))
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	srv.Close() // returns only once every loop goroutine, this one included, has stopped
	if err := store.Close(); err != nil {
		t.Errorf("store.Close: %v", err)
	}
	if err := lg.Close(); err != nil {
		t.Errorf("log.Close: %v", err)
	}
}
