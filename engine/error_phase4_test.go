package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// TestErrorBoundaryCodeMatching puts two error boundaries with different codes on one
// subprocess: an error end throwing one code routes to that boundary, not the other
// (ADR-0089). The nearest *matching* code wins.
func TestErrorBoundaryCodeMatching(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(70, "twocode", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess()
	b.PushScope(sub)
	iStart := b.AddStartEvent()
	errEnd := b.AddErrorEndEvent("B")
	b.Connect(iStart, errEnd)
	b.PopScope()
	boundaryA := b.AddBoundaryErrorEvent(sub, "A")
	boundaryB := b.AddBoundaryErrorEvent(sub, "B")
	done := b.AddEndEvent()
	endA := b.AddEndEvent()
	endB := b.AddEndEvent()
	b.Connect(start, sub)
	b.Connect(sub, done)
	b.Connect(boundaryA, endA)
	b.Connect(boundaryB, endB)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after error: process=%d element=%d, want 0 and 0", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[endB] != 1 {
		t.Errorf("endB visits = %d, want 1 (the matching-code boundary fired)", v[endB])
	}
	if v[endA] != 0 || v[done] != 0 {
		t.Errorf("visits endA=%d done=%d, want 0 and 0", v[endA], v[done])
	}
}

// TestErrorCatchAllBoundary proves a code-less error boundary catches any thrown code
// (ADR-0089).
func TestErrorCatchAllBoundary(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	// The subprocess throws "ANYTHING"; the boundary has an empty code (catch-all).
	cp, doneEnd, recEnd := errorSubprocess(t, 71, "ANYTHING", "")

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after error: process=%d element=%d, want 0 and 0", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[recEnd] != 1 || v[doneEnd] != 0 {
		t.Errorf("visits recovered=%d done=%d, want 1 and 0 (catch-all caught the coded error)", v[recEnd], v[doneEnd])
	}
}

// errorChildProcess builds a child process Start → [work service task] → errorEnd(code),
// with no error handler of its own, so its error propagates to the caller (ADR-0089). When
// withJob is set the service task parks so the child can be crashed mid-flight.
func errorChildProcess(t testing.TB, key uint64, procId, code string, withJob bool) (cp *compiler.CompiledProcess, jobType int32) {
	t.Helper()
	b := compiler.NewBuilder(key, procId, 1)
	start := b.AddStartEvent()
	errEnd := b.AddErrorEndEvent(code)
	if withJob {
		work := b.AddServiceTask("childwork", 3)
		b.Connect(start, work)
		b.Connect(work, errEnd)
		built, err := b.Build()
		if err != nil {
			t.Fatalf("child Build: %v", err)
		}
		return built, built.ServiceTask(built.Node(work).Detail).JobType
	}
	b.Connect(start, errEnd)
	built, err := b.Build()
	if err != nil {
		t.Fatalf("child Build: %v", err)
	}
	return built, 0
}

// TestChildErrorCaughtByCallerBoundary proves an error unhandled in a call-activity child
// propagates to the caller: an error boundary on the call activity catches it, terminating
// the child and routing the caller to its recovery flow (ADR-0089/ADR-0076).
func TestChildErrorCaughtByCallerBoundary(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	childCp, _ := errorChildProcess(t, 72, "child-err", "BOOM", false)

	b := compiler.NewBuilder(73, "caller-err", 1)
	start := b.AddStartEvent()
	call := b.AddCallActivity("child-err", compiler.BindingLatest, false, false)
	boundary := b.AddBoundaryErrorEvent(call, "BOOM")
	done := b.AddEndEvent()
	recovered := b.AddEndEvent()
	b.Connect(start, call)
	b.Connect(call, done)
	b.Connect(boundary, recovered)
	callerCp, err := b.Build()
	if err != nil {
		t.Fatalf("caller Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(childCp)
	p.Deploy(callerCp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(callerCp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Both instances are gone: the child aborted, the caller took its recovery flow.
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after child error: process=%d element=%d, want 0 and 0 (child aborted, caller recovered)", pi, ei)
	}
	v := elementVisits(t, h.store, callerCp.Key)
	if v[recovered] != 1 || v[done] != 0 {
		t.Errorf("caller visits recovered=%d done=%d, want 1 and 0", v[recovered], v[done])
	}
}

// TestChildErrorUncaughtRaisesIncident proves a child error that no caller boundary catches
// propagates to the caller and raises an incident there, with the child torn down (ADR-0089).
func TestChildErrorUncaughtRaisesIncident(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	childCp, _ := errorChildProcess(t, 74, "child-err2", "BOOM", false)

	b := compiler.NewBuilder(75, "caller-nobound", 1)
	start := b.AddStartEvent()
	call := b.AddCallActivity("child-err2", compiler.BindingLatest, false, false)
	done := b.AddEndEvent()
	b.Connect(start, call)
	b.Connect(call, done)
	callerCp, err := b.Build()
	if err != nil {
		t.Fatalf("caller Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(childCp)
	p.Deploy(callerCp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(callerCp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// The child is torn down; the caller parks under an incident on its call activity.
	if incs := incidents(t, h.store); len(incs) != 1 {
		t.Fatalf("incidents = %d, want 1 (uncaught child error surfaces at the caller)", len(incs))
	}
	// Exactly the caller instance remains (the child was aborted).
	if pi := activeProcs(t, h.store); pi != 1 {
		t.Fatalf("active procs = %d, want 1 (only the caller, parked under the incident)", pi)
	}
}

// TestChildErrorCallerRecovers proves the child→caller error propagation survives a crash:
// the child parks on a job with the caller's error boundary armed; after replaying the log
// into a fresh store, completing the child's job throws an error that still propagates to
// the recovered caller boundary (ADR-0089/ADR-0076, invariant I4).
func TestChildErrorCallerRecovers(t *testing.T) {
	dir := t.TempDir()
	childCp, jobType := errorChildProcess(t, 76, "child-err3", "BOOM", true)

	b := compiler.NewBuilder(77, "caller-rec", 1)
	start := b.AddStartEvent()
	call := b.AddCallActivity("child-err3", compiler.BindingLatest, false, false)
	boundary := b.AddBoundaryErrorEvent(call, "BOOM")
	done := b.AddEndEvent()
	recovered := b.AddEndEvent()
	b.Connect(start, call)
	b.Connect(call, done)
	b.Connect(boundary, recovered)
	callerCp, err := b.Build()
	if err != nil {
		t.Fatalf("caller Build: %v", err)
	}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, &manualClock{})
	p1.Deploy(childCp)
	p1.Deploy(callerCp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(callerCp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Parked: caller + child instances, the child on its job.
	if pi := activeProcs(t, h1.store); pi != 2 {
		t.Fatalf("parked: procs=%d, want 2 (caller + child)", pi)
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
	p2 := engine.New(1, log2, store2, &manualClock{})
	p2.Deploy(childCp)
	p2.Deploy(callerCp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	if pi := activeProcs(t, store2); pi != 2 {
		t.Fatalf("after replay: procs=%d, want 2 (caller + child rebuilt)", pi)
	}
	// Completing the child job drives it to the error end; the error propagates to the
	// recovered caller boundary.
	p2.CompleteJob(singleActivatableJob(t, store2, jobType))
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after recovery): %v", err)
	}
	if pi, ei := counts(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after child error post-recovery: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if v := elementVisits(t, store2, callerCp.Key); v[recovered] != 1 {
		t.Errorf("caller recovery end visits = %d, want 1 (recovered boundary caught the child error)", v[recovered])
	}
}
