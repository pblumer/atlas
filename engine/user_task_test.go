package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

func userTaskProcess(t testing.TB) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(defKey, "approval", 1)
	start := b.AddStartEvent()
	task := b.AddUserTask("editor", "reviewers", 3)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.UserTask(cp.Node(task).Detail).JobType
	return cp, jobType
}

func TestUserTaskJobLifecycle(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp, jobType := userTaskProcess(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	if pi := activeProcs(t, h.store); pi != 1 {
		t.Fatalf("after activation: active=%d, want 1 (parked on the user task job)", pi)
	}
	jobKey := singleActivatableJob(t, h.store, jobType)

	p.CompleteJob(jobKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after complete): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after job completion: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

func TestUserTaskRecovers(t *testing.T) {
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
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	jobsBefore := activatableJobs(t, h1.store, jobType)
	if len(jobsBefore) != 1 {
		t.Fatalf("before crash: activatable jobs = %d, want 1", len(jobsBefore))
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

	jobsAfter := activatableJobs(t, store2, jobType)
	if len(jobsAfter) != 1 {
		t.Fatalf("after replay: activatable jobs = %d, want 1", len(jobsAfter))
	}

	p2.CompleteJob(jobsAfter[0])
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 2 (after complete): %v", err)
	}
	if pi, ei := counts(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after replay+complete: process=%d element=%d, want 0 and 0", pi, ei)
	}
}
