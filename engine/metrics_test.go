package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// ADR-0142 slice 2: the engine reports what each batch did. A counter over work done
// cannot be recovered from state — nothing on disk says how many batches ran or how long
// their fsyncs took — so unlike the durability gauges these have to be pushed from the
// batch loop. That puts them under two rules the tests below pin: they are reported only
// *after* the work they describe is durable (invariant I2), and reporting them must not
// allocate (invariant I1, asserted in alloc_test.go).

// recorder is a Metrics that captures what the engine reported, and — when it holds the
// store — the durable applied position at the moment it was told. That second reading is
// how the durability ordering is asserted rather than assumed: the store's position only
// advances in phase 3, after the fsync.
type recorder struct {
	batches         []engine.BatchStats
	syncFailures    int
	commitFailures  int
	appliedWhenTold []uint64
	store           *state.Store
}

func (r *recorder) BatchCommitted(s engine.BatchStats) {
	r.batches = append(r.batches, s)
	if r.store != nil {
		pos, err := r.store.LastAppliedPosition()
		if err != nil {
			pos = 0
		}
		r.appliedWhenTold = append(r.appliedWhenTold, pos)
	}
}
func (r *recorder) SyncFailed()   { r.syncFailures++ }
func (r *recorder) CommitFailed() { r.commitFailures++ }

func (r *recorder) totals() (commands, events int) {
	for _, b := range r.batches {
		commands += b.Commands
		events += b.Events
	}
	return
}

// instrumented boots a processor over a temp dir with a recorder attached.
func instrumented(t *testing.T) (*engine.Processor, *recorder, *state.Store, func()) {
	t.Helper()
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	p := engine.New(1, log, store, &manualClock{})
	rec := &recorder{}
	p.SetMetrics(rec)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	return p, rec, store, func() {
		if err := store.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
		if err := log.Close(); err != nil {
			t.Errorf("log.Close: %v", err)
		}
	}
}

// TestBatchMetricsCountCommandsAndEvents: a run through a real instance reports batches
// whose command and event counts add up to work that actually happened, and whose
// durations are non-negative real measurements rather than placeholders.
func TestBatchMetricsCountCommandsAndEvents(t *testing.T) {
	p, rec, store, done := instrumented(t)
	defer done()

	cp, jobType := linearProcess(t)
	p.Deploy(cp)
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	if len(rec.batches) == 0 {
		t.Fatal("a run that created an instance reported no batches")
	}
	commands, events := rec.totals()
	if commands == 0 || events == 0 {
		t.Fatalf("reported %d commands and %d events, want both non-zero", commands, events)
	}
	// Events are what reached the log, so they cannot exceed the durable applied position.
	applied, err := store.LastAppliedPosition()
	if err != nil {
		t.Fatalf("LastAppliedPosition: %v", err)
	}
	if uint64(events) > applied {
		t.Errorf("reported %d events but the store only applied through position %d", events, applied)
	}
	for i, b := range rec.batches {
		if b.SyncSeconds < 0 || b.CommitSeconds < 0 {
			t.Errorf("batch %d reported negative durations: %+v", i, b)
		}
		if b.Events > 0 && b.SyncSeconds == 0 {
			t.Errorf("batch %d wrote %d events but reported no fsync time", i, b.Events)
		}
	}

	// A second batch through the same processor keeps counting.
	before := len(rec.batches)
	var jobKey uint64
	if err := store.ActivatableJobs(jobType, func(k uint64) error { jobKey = k; return nil }); err != nil {
		t.Fatalf("ActivatableJobs: %v", err)
	}
	p.CompleteJob(jobKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if len(rec.batches) <= before {
		t.Errorf("completing a job reported no further batches (%d, was %d)", len(rec.batches), before)
	}
}

// TestBatchMetricsAreReportedOnlyOnceDurable is the I2 assertion: a batch's counts are
// reported after its fsync, never before. A metric that ran ahead of the log would claim
// work a crash then loses — the one direction that matters.
func TestBatchMetricsAreReportedOnlyOnceDurable(t *testing.T) {
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
		if err := log.Close(); err != nil {
			t.Errorf("log.Close: %v", err)
		}
	}()

	p := engine.New(1, log, store, &manualClock{})
	rec := &recorder{store: store}
	p.SetMetrics(rec)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	cp, _ := linearProcess(t)
	p.Deploy(cp)
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	if len(rec.appliedWhenTold) == 0 {
		t.Fatal("no batch was reported")
	}
	// Positions are 1-based and monotonic, so the durable applied position after k
	// batches is at least the number of events those batches wrote. Reporting a batch
	// before its commit would show the first one claiming events at position 0.
	cumulative := uint64(0)
	for i, b := range rec.batches {
		cumulative += uint64(b.Events)
		if rec.appliedWhenTold[i] < cumulative {
			t.Fatalf("batch %d reported %d events (%d cumulative) while the store had only "+
				"durably applied through position %d — the count ran ahead of the log",
				i, b.Events, cumulative, rec.appliedWhenTold[i])
		}
	}
}

// TestBatchMetricsReportADurabilityFailure: a batch whose fsync fails reports the
// failure and is *not* counted as committed — an alert on the failure counter is the
// only warning an operator gets, and a batch counted as done would hide it.
func TestBatchMetricsReportADurabilityFailure(t *testing.T) {
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	}()

	p := engine.New(1, log, store, &manualClock{})
	rec := &recorder{}
	p.SetMetrics(rec)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	cp, _ := linearProcess(t)
	p.Deploy(cp)

	// Closing the log leaves Append (which only buffers) working and Sync failing, which
	// is exactly the shape of a disk that stops accepting writes.
	if err := log.Close(); err != nil {
		t.Fatalf("log.Close: %v", err)
	}
	committedBefore := len(rec.batches)
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err == nil {
		t.Fatal("a batch whose fsync failed reported success")
	}
	if rec.syncFailures == 0 {
		t.Error("a failed fsync was not reported")
	}
	if len(rec.batches) != committedBefore {
		t.Errorf("a batch that never became durable was counted as committed (%d -> %d)",
			committedBefore, len(rec.batches))
	}
}

// TestBatchMetricsAreOptional: metrics are an add-on, so a processor without them runs
// exactly as before. Every other engine test asserts this implicitly; this says it.
func TestBatchMetricsAreOptional(t *testing.T) {
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
		if err := log.Close(); err != nil {
			t.Errorf("log.Close: %v", err)
		}
	}()
	p := engine.New(1, log, store, &manualClock{})
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	cp, _ := linearProcess(t)
	p.Deploy(cp)
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle without metrics: %v", err)
	}
}
