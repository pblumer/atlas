package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// workerErrorProcess builds Start → serviceTask("work") [error boundary code] → done, with
// the error boundary routed to a "recovered" end. A worker that throws the code from the
// task's job is caught by the boundary (ADR-0089).
func workerErrorProcess(t testing.TB, key uint64, boundaryCode string) (cp *compiler.CompiledProcess, jobType, doneEnd, recEnd int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "workererr", 1)
	start := b.AddStartEvent()
	task := b.AddServiceTask("work", 3)
	boundary := b.AddBoundaryErrorEvent(task, boundaryCode)
	done := b.AddEndEvent()
	recovered := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, done)
	b.Connect(boundary, recovered)
	built, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return built, built.ServiceTask(built.Node(task).Detail).JobType, done, recovered
}

// TestWorkerErrorCaughtByTaskBoundary has a worker throw a BPMN error from a service task's
// job; an error boundary on the task catches it, cancelling the job and routing to the
// recovery flow (ADR-0089).
func TestWorkerErrorCaughtByTaskBoundary(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType, doneEnd, recEnd := workerErrorProcess(t, 60, "OOPS")

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Parked: the service task (its job) and the armed error boundary.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 2 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 2 (task + armed boundary)", pi, ei)
	}
	job := singleActivatableJob(t, h.store, jobType)

	// The worker throws the error code the boundary catches.
	p.ThrowJobError(job, "OOPS")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (throw): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after worker error: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if !jobGone(t, h.store, jobType) {
		t.Error("job survived a worker error caught by the boundary (should be canceled)")
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[recEnd] != 1 || v[doneEnd] != 0 {
		t.Errorf("visits recovered=%d done=%d, want 1 and 0", v[recEnd], v[doneEnd])
	}
}

// TestWorkerErrorUncaughtRaisesIncident has a worker throw an error with no enclosing
// boundary: propagation reaches the root uncaught and raises an incident (ADR-0089).
func TestWorkerErrorUncaughtRaisesIncident(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	b := compiler.NewBuilder(62, "workernobound", 1)
	start := b.AddStartEvent()
	task := b.AddServiceTask("work", 3)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ServiceTask(cp.Node(task).Detail).JobType

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	job := singleActivatableJob(t, h.store, jobType)
	p.ThrowJobError(job, "OOPS")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (throw): %v", err)
	}
	// The job is consumed and an incident parks the token on the task.
	if !jobGone(t, h.store, jobType) {
		t.Error("job survived an uncaught worker error (should be canceled)")
	}
	if incs := incidents(t, h.store); len(incs) != 1 {
		t.Fatalf("incidents = %d, want 1 (uncaught worker error)", len(incs))
	}
}

// TestErrorEventSubprocessHandlesScopeError throws an error inside a subprocess caught by an
// error event subprocess declared in that same subprocess: the event subprocess interrupts
// the scope and runs its handler, then the subprocess completes and the flow continues
// (ADR-0089, reusing ADR-0082).
func TestErrorEventSubprocessHandlesScopeError(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(61, "erresub", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess()
	b.PushScope(sub)
	iStart := b.AddStartEvent()
	errEnd := b.AddErrorEndEvent("BOOM")
	b.Connect(iStart, errEnd)
	handler := b.AddSubProcess()
	b.PushScope(handler)
	hStart := b.AddStartEvent()
	hEnd := b.AddEndEvent()
	b.Connect(hStart, hEnd)
	b.PopScope()
	b.SetEventSubProcess(handler, compiler.EventSubProcessDetail{
		StartNode: hStart, Interrupting: true, Kind: compiler.BoundaryError, ErrorCode: "BOOM",
	})
	b.PopScope()
	done := b.AddEndEvent()
	b.Connect(start, sub)
	b.Connect(sub, done)
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
	if v[hEnd] != 1 {
		t.Errorf("handler end visits = %d, want 1 (error event subprocess ran)", v[hEnd])
	}
	if v[done] != 1 {
		t.Errorf("outer end visits = %d, want 1 (subprocess completed after the handler drained its scope)", v[done])
	}
}

// TestErrorEventSubprocessAtRoot throws an error end at the process root caught by a
// root-level error event subprocess: propagation reaches the root, fires the event
// subprocess, and its handler runs to completion (ADR-0089).
func TestErrorEventSubprocessAtRoot(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(64, "erresub-root", 1)
	start := b.AddStartEvent()
	errEnd := b.AddErrorEndEvent("BOOM")
	b.Connect(start, errEnd)
	handler := b.AddSubProcess()
	b.PushScope(handler)
	hStart := b.AddStartEvent()
	hEnd := b.AddEndEvent()
	b.Connect(hStart, hEnd)
	b.PopScope()
	b.SetEventSubProcess(handler, compiler.EventSubProcessDetail{
		StartNode: hStart, Interrupting: true, Kind: compiler.BoundaryError, ErrorCode: "BOOM",
	})
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
	if v := elementVisits(t, h.store, cp.Key)[hEnd]; v != 1 {
		t.Errorf("handler end visits = %d, want 1 (root error event subprocess caught the error)", v)
	}
}

// TestWorkerErrorRecovers proves the worker-error path survives a crash: a service task
// parks on its job with an error boundary armed; after replaying the log into a fresh store
// the boundary is rebuilt, so a worker error thrown post-recovery is still caught (ADR-0089,
// invariant I4).
func TestWorkerErrorRecovers(t *testing.T) {
	dir := t.TempDir()
	cp, jobType, _, recEnd := workerErrorProcess(t, 63, "OOPS")

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
	if pi, ei := counts(t, h1.store); pi != 1 || ei != 2 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 2", pi, ei)
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
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	if pi, ei := counts(t, store2); pi != 1 || ei != 2 {
		t.Fatalf("after replay: process=%d element=%d, want 1 and 2 (armed boundary rebuilt)", pi, ei)
	}
	p2.ThrowJobError(singleActivatableJob(t, store2, jobType), "OOPS")
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (throw after recovery): %v", err)
	}
	if pi, ei := counts(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after worker error post-recovery: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if v := elementVisits(t, store2, cp.Key); v[recEnd] != 1 {
		t.Errorf("recovery end visits = %d, want 1 (recovered boundary caught the worker error)", v[recEnd])
	}
	if !jobGone(t, store2, jobType) {
		t.Error("job survived the recovered error boundary")
	}
}
