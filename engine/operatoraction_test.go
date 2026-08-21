package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// TestCompleteJobManuallyRecordsOperatorAction covers the operator-driven completion
// path (ADR-0159). CompleteJobManually finishes a parked job exactly as a worker's
// CompleteJob would, but additionally freezes an append-only audit event naming who
// forced it and why, so a hand-completed step is never indistinguishable from one the
// engine drove. Because that record is a persisted fact rather than a live-only
// annotation, replaying the log into an empty store must rebuild the identical trail
// (invariants I4/I6).
func TestCompleteJobManuallyRecordsOperatorAction(t *testing.T) {
	dir := t.TempDir()
	h := openHarness(t, dir)
	cp, jobType := linearProcess(t) // Start → ServiceTask → End

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	jobs := activatableJobs(t, h.store, jobType)
	if len(jobs) != 1 {
		t.Fatalf("activatable jobs = %d, want 1 (parked at the service task)", len(jobs))
	}
	piKey := model.NewKey(1, 1)

	const (
		actor  = "alice"
		reason = "vendor confirmed delivery out-of-band"
	)

	// The operator forces the parked job, with attribution and a reason — the whole
	// difference from CompleteJob is that this leaves a trail.
	p.CompleteJobManually(jobs[0], actor, reason)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after manual complete): %v", err)
	}
	// The completion drove the token onward exactly as a worker's would.
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after manual completion: process=%d element=%d, want 0 and 0", pi, ei)
	}

	assertOneAction := func(s *state.Store, what string) {
		t.Helper()
		var got []model.OperatorActionValue
		if err := s.OperatorActionHistory(piKey, func(_ int64, _ uint64, v *model.OperatorActionValue) error {
			got = append(got, *v)
			return nil
		}); err != nil {
			t.Fatalf("%s: OperatorActionHistory: %v", what, err)
		}
		if len(got) != 1 {
			t.Fatalf("%s: operator actions = %d, want exactly 1", what, len(got))
		}
		a := got[0]
		if a.Actor != actor || a.Reason != reason ||
			a.Kind != model.OperatorActionCompleteJob ||
			a.JobKey != jobs[0] || a.ProcessInstanceKey != piKey {
			t.Errorf("%s: operator action = %+v, want the manual completion by %q", what, a, actor)
		}
	}

	// Live: the intervention is recorded the moment it is applied.
	assertOneAction(h.store, "live")
	h.close(t)

	// Recovery: replay the WAL into a brand-new, empty store. The audit trail must
	// rebuild identically — a forced step is replayed from the log, never regenerated.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("reopen wal: %v", err)
	}
	store2, err := state.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatalf("fresh state.Open: %v", err)
	}
	defer func() {
		if err := store2.Close(); err != nil {
			t.Errorf("store2.Close: %v", err)
		}
		if err := log2.Close(); err != nil {
			t.Errorf("log2.Close: %v", err)
		}
	}()
	rp := engine.New(1, log2, store2, &manualClock{})
	rp.Deploy(cp)
	if err := rp.Recover(); err != nil {
		t.Fatalf("recovery Recover: %v", err)
	}
	assertOneAction(store2, "after replay")
}
