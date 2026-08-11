package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// terminateAtRootProcess builds Start → parallel split → { ServiceTask "work" → End } and a
// { TerminateEnd }. When the terminate branch runs it ends the whole instance, cancelling the
// other branch's waiting job (ADR-0114). Returns the process, the "work" job type, and the
// terminate node id.
func terminateAtRootProcess(t testing.TB, key uint64) (*compiler.CompiledProcess, int32, int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "term-root", 1)
	start := b.AddStartEvent()
	fork := b.AddParallelGateway()
	work := b.AddServiceTask("work", 3)
	workEnd := b.AddEndEvent()
	term := b.AddTerminateEndEvent()
	b.Connect(start, fork)
	b.Connect(fork, work)
	b.Connect(work, workEnd)
	b.Connect(fork, term)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ServiceTask(cp.Node(work).Detail).JobType, term
}

// TestTerminateEndEndsInstance proves a terminate end on one branch of a parallel split ends the
// whole instance at the root, cancelling the other branch's waiting job — the modeled abort a
// plain end could never do (ADR-0114).
func TestTerminateEndEndsInstance(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, workJobType, term := terminateAtRootProcess(t, 120)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.SetJobNotifier(func(int32) {})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// The instance is gone: both branches (the waiting service task and the terminate) are torn down.
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after terminate: process=%d element=%d, want 0 and 0 (instance ended)", pi, ei)
	}
	if got := activatableJobs(t, h.store, workJobType); len(got) != 0 {
		t.Fatalf("the parallel branch's job is still activatable: %v, want it cancelled by the terminate", got)
	}
	if v := elementVisits(t, h.store, cp.Key)[term]; v != 1 {
		t.Errorf("terminate visits = %d, want 1", v)
	}
}

// terminateInSubProcess builds Start → Sub[ iStart → parallel split → {ServiceTask "inner" → End}
// and {TerminateEnd} ] → ServiceTask "after" → End. The terminate ends only the subprocess; the
// parent must continue on the subprocess's outgoing flow to "after" (ADR-0114). Returns the
// process and the "inner"/"after" job types.
func terminateInSubProcess(t testing.TB, key uint64) (*compiler.CompiledProcess, int32, int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "term-sub", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess()
	b.PushScope(sub)
	iStart := b.AddStartEvent()
	ifork := b.AddParallelGateway()
	inner := b.AddServiceTask("inner", 3)
	iEnd := b.AddEndEvent()
	iterm := b.AddTerminateEndEvent()
	b.Connect(iStart, ifork)
	b.Connect(ifork, inner)
	b.Connect(inner, iEnd)
	b.Connect(ifork, iterm)
	b.PopScope()
	after := b.AddServiceTask("after", 3)
	end := b.AddEndEvent()
	b.Connect(start, sub)
	b.Connect(sub, after)
	b.Connect(after, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ServiceTask(cp.Node(inner).Detail).JobType, cp.ServiceTask(cp.Node(after).Detail).JobType
}

// TestTerminateEndScopesToSubProcess proves a terminate end inside an embedded subprocess ends
// only that subprocess — cancelling its inner job — and the parent continues on the subprocess's
// outgoing flow, parking on the following "after" task (ADR-0114). This is the scoped semantics: a
// terminate is not always a whole-instance abort.
func TestTerminateEndScopesToSubProcess(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, innerJobType, afterJobType := terminateInSubProcess(t, 121)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.SetJobNotifier(func(int32) {})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// The instance lives on, parked on "after": the subprocess was terminated but the parent continued.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("after subprocess terminate: process=%d element=%d, want 1 and 1 (parent parked on \"after\")", pi, ei)
	}
	if got := activatableJobs(t, h.store, innerJobType); len(got) != 0 {
		t.Fatalf("the subprocess's inner job is still activatable: %v, want it cancelled", got)
	}
	afterJob := singleActivatableJob(t, h.store, afterJobType)

	// Completing "after" finishes the instance normally — proof the parent really continued.
	p.CompleteJob(afterJob)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (complete after): %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 0 {
		t.Fatalf("after completing \"after\": active=%d, want 0 (instance finished)", pi)
	}
}

// TestTerminateEndRecovers proves the scoped terminate's effect is durable: after the subprocess
// terminate parks the parent on "after", replaying the log into a fresh store rebuilds exactly
// that state (the inner job gone, "after" activatable), and completing "after" finishes the
// instance (ADR-0114, invariant I4).
func TestTerminateEndRecovers(t *testing.T) {
	dir := t.TempDir()
	cp, innerJobType, afterJobType := terminateInSubProcess(t, 122)

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, &manualClock{})
	p1.SetJobNotifier(func(int32) {})
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	h1.close(t)

	// Replay into a fresh, empty store: the terminated subprocess and the parked parent must rebuild.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() { _ = store2.Close(); _ = log2.Close() }()
	p2 := engine.New(1, log2, store2, &manualClock{})
	p2.SetJobNotifier(func(int32) {})
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	if pi, ei := counts(t, store2); pi != 1 || ei != 1 {
		t.Fatalf("after replay: process=%d element=%d, want 1 and 1 (parent parked on \"after\")", pi, ei)
	}
	if got := activatableJobs(t, store2, innerJobType); len(got) != 0 {
		t.Fatalf("after replay: inner job still activatable %v, want gone", got)
	}
	afterJob := singleActivatableJob(t, store2, afterJobType)
	p2.CompleteJob(afterJob)
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (complete after recovery): %v", err)
	}
	if pi := activeProcs(t, store2); pi != 0 {
		t.Fatalf("after completing \"after\": active=%d, want 0", pi)
	}
}
