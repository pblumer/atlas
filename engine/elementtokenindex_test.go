package engine_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// instancesOn reads the piByEl index: which instances of a definition hold a token
// on one element, newest first.
func instancesOn(t *testing.T, s *state.Store, defK uint64, elementId int32) []uint64 {
	t.Helper()
	var out []uint64
	if err := s.InstancesOnElementDesc(defK, elementId, 0, func(key uint64, _ *model.ProcessInstanceValue) error {
		out = append(out, key)
		return nil
	}); err != nil {
		t.Fatalf("InstancesOnElementDesc: %v", err)
	}
	return out
}

// elementTokens reads the maintained live-token counter for one element — the
// number the Operations overlay puts on the shape (ADR-0080).
func elementTokens(t *testing.T, s *state.Store, defK uint64, elementId int32) int64 {
	t.Helper()
	var n int64
	if err := s.ElementLiveTokens(defK, func(id int32, count int64) error {
		if id == elementId {
			n = count
		}
		return nil
	}); err != nil {
		t.Fatalf("ElementLiveTokens: %v", err)
	}
	return n
}

// TestInstancesOnElementTracksLiveTokens is the property the index exists for, run
// through the real engine rather than the store alone: the instances it names for
// an element are exactly the instances whose token is sitting there, and the count
// it yields agrees with the live-token counter the diagram badges the shape with.
//
// Two instances park on the service task; completing one job takes that instance
// out of the index and leaves the other, and the counter moves with it.
func TestInstancesOnElementTracksLiveTokens(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType := linearProcess(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// The service task is the second node the builder added (start, task, end).
	const task = int32(1)
	first, second := model.NewKey(1, 1), model.NewKey(1, 3)
	if got, want := instancesOn(t, h.store, cp.Key, task), []uint64{second, first}; !reflect.DeepEqual(got, want) {
		t.Fatalf("instances on the task = %v, want %v (newest first)", got, want)
	}
	if got := elementTokens(t, h.store, cp.Key, task); got != 2 {
		t.Fatalf("live-token counter = %d, want 2 — the index and the badge must agree", got)
	}

	// Complete the newer instance's job: its token leaves the task, and so must its
	// index entry — an operator filtering by this task would otherwise be handed an
	// instance that has already moved on.
	jobs := activatableJobs(t, h.store, jobType)
	if len(jobs) != 2 {
		t.Fatalf("open jobs = %d, want 2", len(jobs))
	}
	p.CompleteJob(jobs[1])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 2: %v", err)
	}
	if got, want := instancesOn(t, h.store, cp.Key, task), []uint64{first}; !reflect.DeepEqual(got, want) {
		t.Errorf("after one job completed = %v, want %v", got, want)
	}
	if got := elementTokens(t, h.store, cp.Key, task); got != 1 {
		t.Errorf("live-token counter = %d, want 1", got)
	}
}

// TestInstancesOnElementRebuiltOnReplay covers the index as derived state. It is
// written only from the element-instance activation and completion events, so
// replaying that log into an empty store must rebuild it identically (I4/I6) —
// otherwise a recovered engine would answer "which instances are waiting on this
// task" with nothing, for tokens that are demonstrably still there.
func TestInstancesOnElementRebuiltOnReplay(t *testing.T) {
	dir := t.TempDir()
	cp, _ := linearProcess(t)
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
	const task = int32(1)
	before := instancesOn(t, h1.store, cp.Key, task)
	if len(before) != 2 {
		t.Fatalf("instances on the task = %v, want two", before)
	}
	h1.close(t)

	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() {
		if err := store2.Close(); err != nil {
			t.Errorf("store2.Close: %v", err)
		}
		if err := log2.Close(); err != nil {
			t.Errorf("log2.Close: %v", err)
		}
	}()
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	if got := instancesOn(t, store2, cp.Key, task); !reflect.DeepEqual(got, before) {
		t.Errorf("after replay the index holds %v, want %v", got, before)
	}
}
