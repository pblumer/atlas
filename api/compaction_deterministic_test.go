package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pblumer/atlas/checkpoint"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/opensearch"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// ADR-0131 slice 7: the server finally *deletes* the WAL segments a checkpoint and every
// consumer watermark make redundant. This is the one irreversible operation in the whole
// feature, so most of what these tests assert is what compaction **refuses** to do —
// and the one that matters most is that after a compaction the engine still restarts to
// exactly the state it had.
//
// Compaction rides the checkpoint tick: a fresh checkpoint is what licenses new
// deletion, so there is nothing to schedule separately. The tick is the same explicit
// trigger the checkpoint tests use, so no wall-clock cadence is involved.

// compactionHarness is a server whose checkpoint-and-compaction pass is under explicit
// control, over a WAL that rolls a segment per batch so there is something to delete.
type compactionHarness struct {
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

func newCompactionHarness(t *testing.T, dir string, opts ...Option) *compactionHarness {
	t.Helper()
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
	h := &compactionHarness{t: t, dir: dir, srv: srv, x: deployTestHarness{t, srv.Handler()},
		store: store, log: lg, ticks: ticks, done: done}
	t.Cleanup(h.close)
	return h
}

func (h *compactionHarness) close() {
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

// pass triggers one checkpoint-and-compaction pass and blocks until it completes.
func (h *compactionHarness) pass() {
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

func (h *compactionHarness) segments() int {
	h.t.Helper()
	segs, err := filepath.Glob(filepath.Join(h.dir, "wal", "*.wal"))
	if err != nil {
		h.t.Fatalf("glob: %v", err)
	}
	return len(segs)
}

func (h *compactionHarness) deploy() {
	h.t.Helper()
	if code, body := h.x.do(http.MethodPost, "/api/v1/deployments", checkpointBPMN); code != http.StatusOK {
		h.t.Fatalf("deploy status=%d body=%s", code, body)
	}
}

func (h *compactionHarness) create(n int) {
	h.t.Helper()
	for i := 0; i < n; i++ {
		if code, body := h.x.do(http.MethodPost, "/api/v1/processes/1/instances", "{}"); code != http.StatusOK {
			h.t.Fatalf("create instance %d: status=%d body=%s", i, code, body)
		}
	}
}

// instanceCount reads the number of instances the server lists.
func (h *compactionHarness) instanceCount() int {
	h.t.Helper()
	code, body := h.x.do(http.MethodGet, "/api/v1/instances", "")
	if code != http.StatusOK {
		h.t.Fatalf("list instances status=%d body=%s", code, body)
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(body, &rows); err != nil {
		h.t.Fatalf("decode instances: %v (%s)", err, body)
	}
	return len(rows)
}

// TestCompactionDeletesRedundantSegmentsAndSurvivesRestart is the payoff and the proof
// in one: a pass deletes segments, and a restart over what is left comes back to exactly
// the same instances — which is the only thing that makes the deletion legitimate.
func TestCompactionDeletesRedundantSegmentsAndSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir, WithWALCompaction())
	h.deploy()
	h.create(3)
	before := h.segments()
	want := h.instanceCount()
	if want != 3 {
		t.Fatalf("fixture created %d instances, want 3", want)
	}

	h.pass()

	after := h.segments()
	if after >= before {
		t.Fatalf("segments %d -> %d: compaction deleted nothing", before, after)
	}
	if h.instanceCount() != want {
		t.Fatalf("live instances changed across a compaction: %d, want %d", h.instanceCount(), want)
	}
	h.close()

	restarted := newCompactionHarness(t, dir, WithWALCompaction())
	if got := restarted.instanceCount(); got != want {
		t.Fatalf("after a restart over the compacted log: %d instances, want %d", got, want)
	}
}

// TestCompactionOffUnlessConfigured: deletion is opt-in. A server that checkpoints but
// was never told to compact keeps every segment — the log stays a complete archive.
func TestCompactionOffUnlessConfigured(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir) // checkpoints on, compaction not configured
	h.deploy()
	h.create(3)
	before := h.segments()

	h.pass()

	if got := h.segments(); got != before {
		t.Fatalf("segments %d -> %d: a server that was not asked to compact deleted some", before, got)
	}
	if positions, err := checkpoint.List(checkpoint.Dir(dir)); err != nil || len(positions) == 0 {
		t.Fatalf("the pass published no checkpoint (%v, %v); the test proves nothing", positions, err)
	}
}

// TestCompactionHonorsTheExportWatermark: with export enabled but nothing exported yet,
// every record is still owed to OpenSearch, so nothing may be deleted however durable
// the checkpoint is. This is the fail-closed direction that matters — an exporter that
// has not caught up must hold the log, not be overridden by it (ADR-0114).
func TestCompactionHonorsTheExportWatermark(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir, WithWALCompaction(),
		WithOpenSearchExporter(opensearch.Config{URL: "http://127.0.0.1:1", Index: "atlas-test"}))
	h.deploy()
	h.create(3)
	before := h.segments()

	h.pass()

	if h.srv.exporter == nil {
		t.Fatal("the exporter was not built; the watermark under test does not exist")
	}
	if hwm := h.srv.exporter.HighWaterMark(); hwm != 0 {
		t.Fatalf("exporter high-water mark = %d, want 0 for a fixture that never exported", hwm)
	}
	if got := h.segments(); got != before {
		t.Fatalf("segments %d -> %d: compaction deleted records the exporter had not read", before, got)
	}
}

// TestCompactionWaitsForAnInFlightBackup: a whole-instance snapshot streams the WAL
// files off the run loop, so deleting segments underneath it would ship an archive with
// a hole. A pass that finds a backup in flight simply skips and retries on the next tick.
func TestCompactionWaitsForAnInFlightBackup(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir, WithWALCompaction())
	h.deploy()
	h.create(3)
	before := h.segments()

	h.srv.backupsInFlight.Add(1)
	h.pass()
	if got := h.segments(); got != before {
		t.Fatalf("segments %d -> %d: compaction ran while a snapshot was streaming the WAL", before, got)
	}

	// Once the backup finishes, the next pass proceeds.
	h.srv.backupsInFlight.Add(-1)
	h.pass()
	if got := h.segments(); got >= before {
		t.Fatalf("segments %d -> %d: compaction stayed blocked after the backup finished", before, got)
	}
}

// TestBackupMarksItselfInFlight: the guard above is only worth anything if the backup
// endpoint actually raises it, and raises it before it picks the checkpoint it will
// carry — that ordering is what makes a pass which observes zero provably harmless.
func TestBackupMarksItselfInFlight(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir)
	h.deploy()
	h.create(1)

	probe := &inFlightProbe{srv: h.srv}
	h.srv.handleBackupFull(probe, httptest.NewRequest(http.MethodGet, "/api/v1/backup/full", nil))

	if probe.writes == 0 {
		t.Fatal("the backup wrote nothing, so the in-flight count was never observed")
	}
	if probe.duringBackup < 1 {
		t.Fatalf("in-flight count during a backup = %d, want at least 1", probe.duringBackup)
	}
	if n := h.srv.backupsInFlight.Load(); n != 0 {
		t.Fatalf("in-flight count after the backup returned = %d, want 0", n)
	}
}

// inFlightProbe is a ResponseWriter that samples the server's in-flight backup counter
// the first time the backup writes a byte — i.e. while the archive is being produced.
type inFlightProbe struct {
	srv          *Server
	writes       int
	duringBackup int64
	hdr          http.Header
}

func (p *inFlightProbe) Header() http.Header {
	if p.hdr == nil {
		p.hdr = http.Header{}
	}
	return p.hdr
}

func (p *inFlightProbe) Write(b []byte) (int, error) {
	if p.writes == 0 {
		p.duringBackup = p.srv.backupsInFlight.Load()
	}
	p.writes++
	return len(b), nil
}

func (p *inFlightProbe) WriteHeader(int) {}

// TestCompactionWithRetentionEnabledStillProceeds: history retention (ADR-0115) reads the
// state store, not the log, so its safe position is the store's own applied position when
// no exporter is configured — at or above any checkpoint, and therefore never a reason to
// hold the log. It is passed as a watermark anyway so the gate matches ADR-0131's rule
// literally; this pins that including it does not quietly disable compaction.
func TestCompactionWithRetentionEnabledStillProceeds(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir, WithWALCompaction(), WithRetention(time.Hour))
	h.deploy()
	h.create(3)
	before := h.segments()

	h.pass()

	if got := h.segments(); got >= before {
		t.Fatalf("segments %d -> %d: retention's watermark blocked a compaction it does not constrain", before, got)
	}
	if h.instanceCount() != 3 {
		t.Fatalf("instances = %d after compaction, want 3", h.instanceCount())
	}
}

// TestCompactionFailureIsLoggedAndRetried: compaction is an optimization over disk, so a
// failing pass leaves the log alone, logs, and comes back next tick rather than taking
// the server down. A file where the checkpoint root belongs fails every pass.
func TestCompactionFailureIsLoggedAndRetried(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(checkpoint.Dir(dir), []byte("in the way"), 0o644); err != nil {
		t.Fatalf("seed blocking file: %v", err)
	}
	h := newCompactionHarness(t, dir, WithWALCompaction())
	h.deploy()
	h.create(3)
	before := h.segments()

	h.pass()
	h.pass() // the loop still accepts and completes a second pass

	if got := h.segments(); got != before {
		t.Fatalf("segments %d -> %d: a failing compaction deleted something anyway", before, got)
	}
	if h.instanceCount() != 3 {
		t.Fatalf("instances = %d after two failed passes, want 3", h.instanceCount())
	}
}
