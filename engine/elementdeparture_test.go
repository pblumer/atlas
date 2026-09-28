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

// departedFrom reads one departure index: which instances of a definition a token
// completed an element in and moved on from, or was cancelled at, newest first.
func departedFrom(t *testing.T, s *state.Store, how state.Departure, defK uint64, elementId int32) []uint64 {
	t.Helper()
	var out []uint64
	if err := s.InstancesDepartedElementDesc(how, defK, elementId, 0, func(key uint64, _ *model.ProcessInstanceValue) error {
		out = append(out, key)
		return nil
	}); err != nil {
		t.Fatalf("InstancesDepartedElementDesc: %v", err)
	}
	return out
}

// departures is everything the two indexes say about the linear process's three
// elements, in one comparable value.
func departures(t *testing.T, s *state.Store, defK uint64) map[string][]uint64 {
	t.Helper()
	out := map[string][]uint64{}
	for el, name := range []string{"start", "task", "end"} {
		out[name+"/completed"] = departedFrom(t, s, state.DepartedCompleted, defK, int32(el))
		out[name+"/cancelled"] = departedFrom(t, s, state.DepartedCancelled, defK, int32(el))
	}
	return out
}

// runDepartures drives the linear process (start → service task → end) through both
// ways of leaving the task: one instance's job completes, the other instance is
// cancelled while its token sits there. It returns the two instance keys.
func runDepartures(t *testing.T, p *engine.Processor, s *state.Store, defK uint64, jobType int32) (completed, cancelled uint64) {
	t.Helper()
	p.CreateInstance(defK)
	p.CreateInstance(defK)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	on := instancesOn(t, s, defK, 1)
	if len(on) != 2 {
		t.Fatalf("instances on the task = %v, want two", on)
	}
	completed, cancelled = on[1], on[0]
	jobs := activatableJobs(t, s, jobType)
	if len(jobs) != 2 {
		t.Fatalf("open jobs = %d, want 2", len(jobs))
	}
	p.CompleteJob(jobs[0]) // the older instance's job
	p.CancelInstance(cancelled)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 2: %v", err)
	}
	return completed, cancelled
}

// TestDeparturesRecordedByTheEngine runs the two departure indexes through the real
// processor: a completion writes the element into the completed index, a termination
// into the cancelled one, and neither is written for a token that is merely sitting
// there. Those are the instances behind the diagram's gray and amber counts, so this is
// the property the Operations click depends on.
func TestDeparturesRecordedByTheEngine(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType := linearProcess(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// Before anything leaves the task, nobody has completed or been cancelled at it —
	// both are sitting there, which is the other index's answer.
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if got := departedFrom(t, h.store, state.DepartedCompleted, cp.Key, 1); got != nil {
		t.Fatalf("completed at the task before its job ran = %v, want nothing", got)
	}
	waiting := instancesOn(t, h.store, cp.Key, 1)[0]
	p.CancelInstance(waiting)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	completed, cancelled := runDepartures(t, p, h.store, cp.Key, jobType)

	got := departures(t, h.store, cp.Key)
	want := map[string][]uint64{
		// Every instance's start event completed and handed its token on.
		"start/completed": {cancelled, completed, waiting},
		"start/cancelled": nil,
		// The task: one got through, two were cancelled while sitting on it.
		"task/completed": {completed},
		"task/cancelled": {cancelled, waiting},
		// Only the instance that got through reached the end.
		"end/completed": {completed},
		"end/cancelled": nil,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("departures = %v\nwant %v", got, want)
	}
}

// TestDeparturesRebuiltOnReplay covers the indexes as derived state. They are written
// only from the element-instance completion and termination events, so replaying the
// log into an empty store must rebuild them identically (I4/I6) — otherwise a recovered
// engine would answer "which instances completed this task" with nothing.
func TestDeparturesRebuiltOnReplay(t *testing.T) {
	dir := t.TempDir()
	cp, jobType := linearProcess(t)
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	runDepartures(t, p1, h1.store, cp.Key, jobType)
	before := departures(t, h1.store, cp.Key)
	if len(before["task/completed"]) != 1 || len(before["task/cancelled"]) != 1 {
		t.Fatalf("departures before replay = %v, want one of each at the task", before)
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
	if got := departures(t, store2, cp.Key); !reflect.DeepEqual(got, before) {
		t.Errorf("after replay the indexes hold %v\nwant %v", got, before)
	}
}
