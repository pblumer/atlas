package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// boolVar builds a boolean start variable.
func boolVar(name string, v bool) model.VariableValue {
	return model.VariableValue{Name: name, Kind: model.VarBool, Bool: v}
}

// inclusiveProcess builds Start → inclusive split → (branch A if `a`) & (branch B
// if `b`) → inclusive join → serviceTask "after" → End. Branches are pass-through
// tasks when passthrough is true, else service tasks (so the test can hold them
// mid-branch). Returns the compiled process and the "after" job type.
func inclusiveProcess(t testing.TB, passthrough bool) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(defKey, "inclusive", 1)
	start := b.AddStartEvent()
	split := b.AddInclusiveGateway()
	var ta, tb int32
	if passthrough {
		ta, tb = b.AddTask(), b.AddTask()
	} else {
		ta, tb = b.AddServiceTask("A", 3), b.AddServiceTask("B", 3)
	}
	join := b.AddInclusiveGateway()
	after := b.AddServiceTask("after", 3)
	end := b.AddEndEvent()
	b.Connect(start, split)
	b.SetFlowCondition(b.Connect(split, ta), mustCompile(t, "a"))
	b.SetFlowCondition(b.Connect(split, tb), mustCompile(t, "b"))
	b.Connect(ta, join)
	b.Connect(tb, join)
	b.Connect(join, after)
	b.Connect(after, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ServiceTask(cp.Node(after).Detail).JobType
}

// TestInclusiveForkJoinPassThrough is the double-fire guard: both branches are
// taken and are pass-through, so both join-arrival tokens land in the same batch.
// The join must still fire exactly once — proven by there being exactly one
// "after" job (a double fire would create two).
func TestInclusiveForkJoinPassThrough(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, afterJob := inclusiveProcess(t, true)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, boolVar("a", true), boolVar("b", true))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if jobs := activatableJobs(t, h.store, afterJob); len(jobs) != 1 {
		t.Fatalf("after jobs = %d, want exactly 1 (inclusive join must fire once, not per branch)", len(jobs))
	}
	if pi := activeProcs(t, h.store); pi != 1 {
		t.Fatalf("active instances = %d, want 1 (parked at the 'after' task)", pi)
	}
}

// TestInclusiveForkJoinOneBranch takes only branch A (b is false); the join must
// fire from the single arriving token without waiting for B, which never got one.
func TestInclusiveForkJoinOneBranch(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, afterJob := inclusiveProcess(t, true)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, boolVar("a", true), boolVar("b", false))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if jobs := activatableJobs(t, h.store, afterJob); len(jobs) != 1 {
		t.Fatalf("after jobs = %d, want 1 (join fires without waiting for the untaken branch)", len(jobs))
	}
}

// TestInclusiveSplitDefault covers the default-flow path: with no condition true,
// the inclusive split takes its default branch, which flows through the join to
// the end.
func TestInclusiveSplitDefault(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(defKey, "incdefault", 1)
	start := b.AddStartEvent()
	split := b.AddInclusiveGateway()
	ta := b.AddTask()
	tb := b.AddTask()
	join := b.AddInclusiveGateway()
	end := b.AddEndEvent()
	b.Connect(start, split)
	b.SetFlowCondition(b.Connect(split, ta), mustCompile(t, "a")) // conditional
	b.SetFlowDefault(b.Connect(split, tb))                        // default
	b.Connect(ta, join)
	b.Connect(tb, join)
	b.Connect(join, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	// a is false, so no condition holds → the default branch is taken.
	p.CreateInstance(cp.Key, boolVar("a", false))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after run: process=%d element=%d, want 0 and 0 (default branch ran to end)", pi, ei)
	}
}

// TestInclusiveJoinWaitsForActiveBranch uses service-task branches so both are
// held mid-branch. Completing only one leaves the join waiting (no "after" job);
// completing the second fires it exactly once.
func TestInclusiveJoinWaitsForActiveBranch(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, afterJob := inclusiveProcess(t, false)
	jobA := cp.ServiceTask(cp.Node(branchNode(t, cp, "A")).Detail).JobType
	jobB := cp.ServiceTask(cp.Node(branchNode(t, cp, "B")).Detail).JobType

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, boolVar("a", true), boolVar("b", true))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// Complete branch A only: the join waits, so there is no "after" job yet.
	p.CompleteJob(activatableJobs(t, h.store, jobA)[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (A): %v", err)
	}
	if jobs := activatableJobs(t, h.store, afterJob); len(jobs) != 0 {
		t.Fatalf("after completing only A: after jobs = %d, want 0 (join waits for B)", len(jobs))
	}

	// Complete branch B: the join fires once.
	p.CompleteJob(activatableJobs(t, h.store, jobB)[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (B): %v", err)
	}
	if jobs := activatableJobs(t, h.store, afterJob); len(jobs) != 1 {
		t.Fatalf("after jobs = %d, want exactly 1", len(jobs))
	}
}

// TestInclusiveJoinRecovers proves a half-arrived inclusive join survives a
// crash: with one branch complete and the join waiting, replay restores the wait,
// and completing the second branch fires the join.
func TestInclusiveJoinRecovers(t *testing.T) {
	dir := t.TempDir()
	cp, afterJob := inclusiveProcess(t, false)
	jobA := cp.ServiceTask(cp.Node(branchNode(t, cp, "A")).Detail).JobType
	jobB := cp.ServiceTask(cp.Node(branchNode(t, cp, "B")).Detail).JobType
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key, boolVar("a", true), boolVar("b", true))
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	p1.CompleteJob(activatableJobs(t, h1.store, jobA)[0])
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (A): %v", err)
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
	defer func() { _ = store2.Close(); _ = log2.Close() }()
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	if jobs := activatableJobs(t, store2, afterJob); len(jobs) != 0 {
		t.Fatalf("after replay: after jobs = %d, want 0 (join still waiting)", len(jobs))
	}
	p2.CompleteJob(activatableJobs(t, store2, jobB)[0])
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (B after recovery): %v", err)
	}
	if jobs := activatableJobs(t, store2, afterJob); len(jobs) != 1 {
		t.Fatalf("after jobs = %d, want 1 (recovered join fired on branch B)", len(jobs))
	}
}

// branchNode returns the element id of the service task whose job type is name.
func branchNode(t testing.TB, cp *compiler.CompiledProcess, jobType string) int32 {
	t.Helper()
	for id := int32(0); ; id++ {
		n := cp.Node(id)
		if n.Type == compiler.TypeServiceTask && cp.Intern(cp.ServiceTask(n.Detail).JobType) == jobType {
			return id
		}
		if id > 50 {
			t.Fatalf("no service task with job type %q", jobType)
		}
	}
}
