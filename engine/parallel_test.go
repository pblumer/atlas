package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// forkJoinProcess builds Start → parallel fork → (serviceTask A) & (serviceTask B)
// → parallel join → End, and returns it with the two job types.
func forkJoinProcess(t testing.TB) (*compiler.CompiledProcess, int32, int32) {
	t.Helper()
	b := compiler.NewBuilder(defKey, "forkjoin", 1)
	start := b.AddStartEvent()
	fork := b.AddParallelGateway()
	sa := b.AddServiceTask("A", 3)
	sb := b.AddServiceTask("B", 3)
	join := b.AddParallelGateway()
	end := b.AddEndEvent()
	b.Connect(start, fork)
	b.Connect(fork, sa)
	b.Connect(fork, sb)
	b.Connect(sa, join)
	b.Connect(sb, join)
	b.Connect(join, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ServiceTask(cp.Node(sa).Detail).JobType, cp.ServiceTask(cp.Node(sb).Detail).JobType
}

// TestParallelForkJoin runs a fork/join: the fork activates both branches; the
// join must wait for BOTH before continuing. Completing only one branch must not
// finish the instance — that is the synchronization property.
func TestParallelForkJoin(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobA, jobB := forkJoinProcess(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Both branches forked and parked at their service tasks.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 2 {
		t.Fatalf("after fork: process=%d element=%d, want 1 and 2 (both branches)", pi, ei)
	}
	jobsA := activatableJobs(t, h.store, jobA)
	jobsB := activatableJobs(t, h.store, jobB)
	if len(jobsA) != 1 || len(jobsB) != 1 {
		t.Fatalf("jobs A=%d B=%d, want 1 and 1", len(jobsA), len(jobsB))
	}

	// Complete branch A only: the join waits, so the instance is still running.
	p.CompleteJob(jobsA[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (A): %v", err)
	}
	if pi, _ := counts(t, h.store); pi != 1 {
		t.Fatalf("after completing only branch A: process=%d, want 1 (join must wait for B)", pi)
	}

	// Complete branch B: the join fires once and the instance finishes.
	p.CompleteJob(jobsB[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (B): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after completing both branches: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestParallelJoinRecovers proves a half-arrived join survives a crash: one branch
// has reached the join and it is waiting; after replay the instance is still
// running, and completing the second branch fires the join to completion.
func TestParallelJoinRecovers(t *testing.T) {
	dir := t.TempDir()
	cp, jobA, jobB := forkJoinProcess(t)
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Complete branch A so one token is parked at the join.
	p1.CompleteJob(activatableJobs(t, h1.store, jobA)[0])
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (A): %v", err)
	}
	h1.close(t)

	// Replay into a fresh store.
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
	// The join is still waiting on branch B.
	if pi, _ := counts(t, store2); pi != 1 {
		t.Fatalf("after replay: process=%d, want 1 (join still waiting)", pi)
	}
	// Completing branch B fires the recovered join.
	p2.CompleteJob(activatableJobs(t, store2, jobB)[0])
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (B after recovery): %v", err)
	}
	if pi, ei := counts(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after completing branch B: process=%d element=%d, want 0 and 0", pi, ei)
	}
}
