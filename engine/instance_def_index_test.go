package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// instancesOfDef reads a definition's live and finished instances out of the
// by-definition indexes, in the order those indexes yield them.
func instancesOfDef(t *testing.T, s *state.Store, defKey uint64) (active, finished []uint64) {
	t.Helper()
	if err := s.ActiveInstancesOfDefDesc(defKey, 0, func(key uint64, _ *model.ProcessInstanceValue) error {
		active = append(active, key)
		return nil
	}); err != nil {
		t.Fatalf("ActiveInstancesOfDefDesc: %v", err)
	}
	if err := s.FinishedInstancesOfDefDesc(defKey, 0, 0, func(key uint64, _ *model.ProcessInstanceValue) error {
		finished = append(finished, key)
		return nil
	}); err != nil {
		t.Fatalf("FinishedInstancesOfDefDesc: %v", err)
	}
	return active, finished
}

// TestInstanceDefIndexSurvivesRecovery is the invariant that matters for a derived
// index (I4/I6): the state a replay rebuilds is the state that was built live. One
// instance is left parked on its user task and one is run to completion, so both
// the live index and the finished index carry an entry; replaying the log into a
// fresh, empty store must produce exactly the same two.
func TestInstanceDefIndexSurvivesRecovery(t *testing.T) {
	dir := t.TempDir()
	cp, jobType := userTaskProcess(t)
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	jobs := activatableJobs(t, h1.store, jobType)
	if len(jobs) != 2 {
		t.Fatalf("parked jobs = %d, want 2", len(jobs))
	}
	// Finish exactly one of the two, so the two indexes hold one instance each.
	p1.CompleteJob(jobs[0])
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after complete: %v", err)
	}

	active, finished := instancesOfDef(t, h1.store, cp.Key)
	if len(active) != 1 || len(finished) != 1 {
		t.Fatalf("live index = %v, finished index = %v; want one instance each", active, finished)
	}
	if active[0] == finished[0] {
		t.Fatalf("instance %d is in both indexes at once", active[0])
	}
	h1.close(t)

	// Replay the log into a fresh, empty store: the indexes rebuild from the events.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() { _ = store2.Close(); _ = log2.Close() }()
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}

	active2, finished2 := instancesOfDef(t, store2, cp.Key)
	if len(active2) != len(active) || (len(active) == 1 && active2[0] != active[0]) {
		t.Errorf("replayed live index = %v, want %v", active2, active)
	}
	if len(finished2) != len(finished) || (len(finished) == 1 && finished2[0] != finished[0]) {
		t.Errorf("replayed finished index = %v, want %v", finished2, finished)
	}
	// Nothing else leaked in under a definition that never ran anything.
	if a, f := instancesOfDef(t, store2, cp.Key+1); a != nil || f != nil {
		t.Errorf("unrelated definition = %v / %v, want nothing", a, f)
	}
}
