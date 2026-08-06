package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// errorSubprocess builds Start → subProcess{ iStart → errorEnd(code) } → done, with an
// error boundary (boundaryCode) on the subprocess routed to a "recovered" end (ADR-0089).
// Reaching the error end throws; a matching boundary aborts the subprocess and routes out.
func errorSubprocess(t testing.TB, key uint64, code, boundaryCode string) (cp *compiler.CompiledProcess, doneEnd, recEnd int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "errsub", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess()
	b.PushScope(sub)
	iStart := b.AddStartEvent()
	errEnd := b.AddErrorEndEvent(code)
	b.Connect(iStart, errEnd)
	b.PopScope()
	boundary := b.AddBoundaryErrorEvent(sub, boundaryCode)
	done := b.AddEndEvent()
	recovered := b.AddEndEvent()
	b.Connect(start, sub)
	b.Connect(sub, done)
	b.Connect(boundary, recovered)
	built, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return built, done, recovered
}

// TestErrorBoundaryCatchesSubprocessError throws an error end inside a subprocess caught by
// an error boundary on that subprocess: the error aborts the subprocess and the recovery
// flow runs, the normal outgoing flow is not taken (ADR-0089).
func TestErrorBoundaryCatchesSubprocessError(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, doneEnd, recEnd := errorSubprocess(t, 50, "BOOM", "BOOM")

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// The error aborted the subprocess and the boundary routed to recovery; nothing hangs.
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after error: process=%d element=%d, want 0 and 0", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[recEnd] != 1 {
		t.Errorf("recovery end visits = %d, want 1 (error boundary fired)", v[recEnd])
	}
	if v[doneEnd] != 0 {
		t.Errorf("normal end visits = %d, want 0 (the subprocess was aborted by the error)", v[doneEnd])
	}
}

// TestErrorPropagatesToOuterBoundary throws an error in an inner subprocess whose only
// error boundary catches a *different* code; the error propagates past it to the outer
// subprocess's matching boundary (ADR-0089) — nearest *matching* enclosing handler wins.
func TestErrorPropagatesToOuterBoundary(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(51, "nested", 1)
	start := b.AddStartEvent()
	outer := b.AddSubProcess()
	b.PushScope(outer)
	oStart := b.AddStartEvent()
	inner := b.AddSubProcess()
	b.PushScope(inner)
	iStart := b.AddStartEvent()
	errEnd := b.AddErrorEndEvent("BOOM")
	b.Connect(iStart, errEnd)
	b.PopScope()
	oEnd := b.AddEndEvent()
	b.Connect(oStart, inner)
	b.Connect(inner, oEnd)
	b.PopScope()
	innerBoundary := b.AddBoundaryErrorEvent(inner, "OTHER") // wrong code: does not catch BOOM
	outerBoundary := b.AddBoundaryErrorEvent(outer, "BOOM")  // matches: catches it
	done := b.AddEndEvent()
	wrong := b.AddEndEvent()
	recovered := b.AddEndEvent()
	b.Connect(start, outer)
	b.Connect(outer, done)
	b.Connect(innerBoundary, wrong)
	b.Connect(outerBoundary, recovered)
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
	if v[recovered] != 1 {
		t.Errorf("outer recovery end visits = %d, want 1 (error propagated to the matching outer boundary)", v[recovered])
	}
	if v[wrong] != 0 {
		t.Errorf("inner boundary end visits = %d, want 0 (its code does not match)", v[wrong])
	}
	if v[done] != 0 {
		t.Errorf("normal end visits = %d, want 0", v[done])
	}
}

// TestUnhandledErrorRaisesIncident throws an error end at the process root with no enclosing
// error boundary: propagation reaches the root uncaught and raises an incident on the
// throwing element, and the instance parks rather than completing (ADR-0089/ADR-0061).
func TestUnhandledErrorRaisesIncident(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(52, "uncaught", 1)
	start := b.AddStartEvent()
	errEnd := b.AddErrorEndEvent("BOOM")
	b.Connect(start, errEnd)
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
	// The instance parks under the incident (it neither completes nor hangs silently).
	if pi := activeProcs(t, h.store); pi != 1 {
		t.Fatalf("after uncaught error: procs=%d, want 1 (parked under the incident)", pi)
	}
	incs := incidents(t, h.store)
	if len(incs) != 1 {
		t.Fatalf("incidents = %d, want 1 (an uncaught error raises one)", len(incs))
	}
	for _, inc := range incs {
		if inc.ProcessInstanceKey == 0 || inc.ElementInstanceKey == 0 {
			t.Errorf("incident = %+v, want it anchored to the throwing element instance", inc)
		}
	}
}

// TestErrorBoundaryRecovers proves an armed error boundary survives a crash: a subprocess
// parks on an inner job with the boundary armed; after replaying the log into a fresh store
// the boundary is rebuilt, so completing the job — which drives the token to the error end
// — throws an error that the recovered boundary still catches (ADR-0089, invariant I4).
func TestErrorBoundaryRecovers(t *testing.T) {
	dir := t.TempDir()

	b := compiler.NewBuilder(53, "errsub-rec", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess()
	b.PushScope(sub)
	iStart := b.AddStartEvent()
	work := b.AddServiceTask("work", 3)
	errEnd := b.AddErrorEndEvent("BOOM")
	b.Connect(iStart, work)
	b.Connect(work, errEnd)
	b.PopScope()
	boundary := b.AddBoundaryErrorEvent(sub, "BOOM")
	done := b.AddEndEvent()
	recovered := b.AddEndEvent()
	b.Connect(start, sub)
	b.Connect(sub, done)
	b.Connect(boundary, recovered)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ServiceTask(cp.Node(work).Detail).JobType

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, &manualClock{})
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Parked: subprocess + inner task + armed error boundary.
	if pi, ei := counts(t, h1.store); pi != 1 || ei != 3 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 3 (subprocess + inner task + boundary)", pi, ei)
	}
	h1.close(t)

	// Replay into a fresh, empty store: the armed boundary must rebuild from the log.
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
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	if pi, ei := counts(t, store2); pi != 1 || ei != 3 {
		t.Fatalf("after replay: process=%d element=%d, want 1 and 3 (armed boundary rebuilt)", pi, ei)
	}
	// Completing the job drives the token to the error end; the recovered boundary catches.
	p2.CompleteJob(singleActivatableJob(t, store2, jobType))
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after recovery): %v", err)
	}
	if pi, ei := counts(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after error post-recovery: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if v := elementVisits(t, store2, cp.Key); v[recovered] != 1 {
		t.Errorf("recovery end visits = %d, want 1 (recovered boundary caught the error)", v[recovered])
	}
	if !jobGone(t, store2, jobType) {
		t.Error("inner job survived the interrupting error boundary")
	}
}
