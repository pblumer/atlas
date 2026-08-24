package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// escalationSubprocess builds Start → subProcess{ iStart → escalationEnd(code) } → done, with
// an escalation boundary (boundaryCode, interrupting) on the subprocess routed to a
// "recovered" end (ADR-0125). Reaching the escalation end raises it; a matching interrupting
// boundary aborts the subprocess and routes out.
func escalationSubprocess(t testing.TB, key uint64, code, boundaryCode string, interrupting bool) (cp *compiler.CompiledProcess, doneEnd, recEnd int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "escsub", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess()
	b.PushScope(sub)
	iStart := b.AddStartEvent()
	escEnd := b.AddEscalationEndEvent(code)
	b.Connect(iStart, escEnd)
	b.PopScope()
	boundary := b.AddBoundaryEscalationEvent(sub, boundaryCode, interrupting)
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

// TestEscalationInterruptingBoundaryCatchesSubprocess raises an escalation end inside a
// subprocess caught by an *interrupting* escalation boundary on that subprocess: the
// escalation aborts the subprocess and the recovery flow runs, the normal outgoing flow is not
// taken (ADR-0125 Phase 2 — the ADR-0089 error shape, but via BoundaryEscalation).
func TestEscalationInterruptingBoundaryCatchesSubprocess(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, doneEnd, recEnd := escalationSubprocess(t, 60, "LATE", "LATE", true)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// The escalation aborted the subprocess and the boundary routed to recovery; nothing hangs.
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after escalation: process=%d element=%d, want 0 and 0", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[recEnd] != 1 {
		t.Errorf("recovery end visits = %d, want 1 (escalation boundary fired)", v[recEnd])
	}
	if v[doneEnd] != 0 {
		t.Errorf("normal end visits = %d, want 0 (the subprocess was aborted by the interrupting boundary)", v[doneEnd])
	}
}

// TestEscalationPropagatesToOuterBoundary raises an escalation in an inner subprocess whose
// only escalation boundary catches a *different* code; the escalation propagates past it to the
// outer subprocess's matching (interrupting) boundary — nearest *matching* enclosing handler
// wins (ADR-0125).
func TestEscalationPropagatesToOuterBoundary(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(61, "esc-nested", 1)
	start := b.AddStartEvent()
	outer := b.AddSubProcess()
	b.PushScope(outer)
	oStart := b.AddStartEvent()
	inner := b.AddSubProcess()
	b.PushScope(inner)
	iStart := b.AddStartEvent()
	escEnd := b.AddEscalationEndEvent("LATE")
	b.Connect(iStart, escEnd)
	b.PopScope()
	oEnd := b.AddEndEvent()
	b.Connect(oStart, inner)
	b.Connect(inner, oEnd)
	b.PopScope()
	innerBoundary := b.AddBoundaryEscalationEvent(inner, "OTHER", true) // wrong code: does not catch LATE
	outerBoundary := b.AddBoundaryEscalationEvent(outer, "LATE", true)  // matches: catches it
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
		t.Fatalf("after escalation: process=%d element=%d, want 0 and 0", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[recovered] != 1 {
		t.Errorf("outer recovery end visits = %d, want 1 (escalation propagated to the matching outer boundary)", v[recovered])
	}
	if v[wrong] != 0 {
		t.Errorf("inner boundary end visits = %d, want 0 (its code does not match)", v[wrong])
	}
	if v[done] != 0 {
		t.Errorf("normal end visits = %d, want 0", v[done])
	}
}

// TestUncaughtEscalationIsBenign raises an escalation end at the process root with no enclosing
// escalation handler: propagation reaches the root uncaught and — unlike an error — raises NO
// incident. The escalation end simply ends its path like a none end, so the instance completes
// normally (ADR-0125, the key divergence from ADR-0089).
func TestUncaughtEscalationIsBenign(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(62, "esc-uncaught", 1)
	start := b.AddStartEvent()
	escEnd := b.AddEscalationEndEvent("LATE")
	b.Connect(start, escEnd)
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
	// The instance completes cleanly — an uncaught escalation is benign, not a failure.
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after uncaught escalation: process=%d element=%d, want 0 and 0 (completed normally)", pi, ei)
	}
	if incs := incidents(t, h.store); len(incs) != 0 {
		t.Errorf("incidents = %d, want 0 (an uncaught escalation is benign, unlike an error)", len(incs))
	}
	if v := elementVisits(t, h.store, cp.Key); v[escEnd] != 1 {
		t.Errorf("escalation end visits = %d, want 1 (it ends its path like a none end)", v[escEnd])
	}
}

// TestEscalationNonInterruptingBoundaryAndThrowContinues exercises the two genuinely new
// escalation semantics at once (ADR-0125 Phase 3): a subprocess raises an escalation with an
// *intermediate throw* (not an end) and then continues to a waiting service task; a
// *non-interrupting* escalation boundary on the subprocess catches it and runs a handler
// branch — WITHOUT tearing the subprocess down. So the handler runs AND the subprocess keeps
// running on its job; completing the job then drains the subprocess normally.
func TestEscalationNonInterruptingBoundaryAndThrowContinues(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(64, "esc-nonint", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess()
	b.PushScope(sub)
	iStart := b.AddStartEvent()
	thr := b.AddEscalationThrowEvent("MGR")
	work := b.AddServiceTask("work", 3)
	subEnd := b.AddEndEvent()
	b.Connect(iStart, thr)
	b.Connect(thr, work) // the throw continues on its outgoing flow to the waiting task
	b.Connect(work, subEnd)
	b.PopScope()
	boundary := b.AddBoundaryEscalationEvent(sub, "MGR", false) // non-interrupting
	notified := b.AddEndEvent()
	done := b.AddEndEvent()
	b.Connect(start, sub)
	b.Connect(sub, done)
	b.Connect(boundary, notified)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ServiceTask(cp.Node(work).Detail).JobType

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// The non-interrupting handler ran; the throw continued, so the subprocess is still alive
	// on its job and the outer flow is not yet taken.
	v := elementVisits(t, h.store, cp.Key)
	if v[notified] != 1 {
		t.Errorf("handler end visits = %d, want 1 (non-interrupting boundary fired)", v[notified])
	}
	if v[done] != 0 {
		t.Errorf("outer end visits = %d, want 0 (the subprocess keeps running — it was not interrupted)", v[done])
	}
	if pi := activeProcs(t, h.store); pi != 1 {
		t.Fatalf("procs = %d, want 1 (subprocess still running on its job)", pi)
	}
	// Completing the job drains the subprocess, which then completes normally.
	p.CompleteJob(singleActivatableJob(t, h.store, jobType))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after job): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after job: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key); v[done] != 1 {
		t.Errorf("outer end visits = %d, want 1 (subprocess completed after its job)", v[done])
	}
}

// TestEscalationInterruptingThrowDoesNotContinue is the throw-then-continue GUARD (ADR-0125's
// headline risk): an escalation *intermediate throw* whose escalation is caught by an
// *interrupting* boundary must NOT take its outgoing flow — the interrupt tears the throwing
// token down, so continuing would run a node that should never run (and double-count). The
// subprocess is aborted and only the recovery branch runs.
func TestEscalationInterruptingThrowDoesNotContinue(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(65, "esc-int-throw", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess()
	b.PushScope(sub)
	iStart := b.AddStartEvent()
	thr := b.AddEscalationThrowEvent("MGR")
	shouldNotRun := b.AddEndEvent()
	b.Connect(iStart, thr)
	b.Connect(thr, shouldNotRun) // must NOT be reached: the interrupt tears this token down
	b.PopScope()
	boundary := b.AddBoundaryEscalationEvent(sub, "MGR", true) // interrupting
	recovered := b.AddEndEvent()
	done := b.AddEndEvent()
	b.Connect(start, sub)
	b.Connect(sub, done)
	b.Connect(boundary, recovered)
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
		t.Fatalf("after escalation: process=%d element=%d, want 0 and 0", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[shouldNotRun] != 0 {
		t.Errorf("throw continuation visits = %d, want 0 (an interrupting catch tears the throwing token down; it must not continue)", v[shouldNotRun])
	}
	if v[recovered] != 1 {
		t.Errorf("recovery end visits = %d, want 1 (interrupting boundary fired)", v[recovered])
	}
	if v[done] != 0 {
		t.Errorf("normal end visits = %d, want 0 (subprocess aborted)", v[done])
	}
}

// TestEscalationNonInterruptingEventSubprocessAtRoot raises an escalation with an intermediate
// throw at the process root, caught by a *non-interrupting* root escalation event subprocess.
// The main flow continues past the throw to a waiting service task; the handler runs alongside
// it (the main-flow job is untouched). Completing the job then drains the instance and the
// re-armed trigger disarms (ADR-0125 Phase 3, reusing ADR-0082).
func TestEscalationNonInterruptingEventSubprocessAtRoot(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(66, "esc-eventsub-root", 1)
	start := b.AddStartEvent()
	thr := b.AddEscalationThrowEvent("E1")
	work := b.AddServiceTask("work", 3)
	mainEnd := b.AddEndEvent()
	b.Connect(start, thr)
	b.Connect(thr, work) // the throw continues to a waiting task, so the main flow stays alive
	b.Connect(work, mainEnd)
	handler := b.AddSubProcess()
	b.PushScope(handler)
	hStart := b.AddStartEvent()
	hEnd := b.AddEndEvent()
	b.Connect(hStart, hEnd)
	b.PopScope()
	b.SetEventSubProcess(handler, compiler.EventSubProcessDetail{
		StartNode: hStart, Interrupting: false, Kind: compiler.BoundaryEscalation, EscalationCode: "E1",
	})
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ServiceTask(cp.Node(work).Detail).JobType

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// The non-interrupting handler ran; the main flow continued to its job (not cancelled).
	if v := elementVisits(t, h.store, cp.Key)[hEnd]; v != 1 {
		t.Errorf("handler end visits = %d, want 1 (non-interrupting escalation event subprocess ran)", v)
	}
	if jobGone(t, h.store, jobType) {
		t.Fatal("the non-interrupting escalation handler cancelled the main-flow job")
	}
	// Completing the main-flow job drains the instance; the re-armed trigger disarms.
	p.CompleteJob(singleActivatableJob(t, h.store, jobType))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after job): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after main flow completes: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key)[mainEnd]; v != 1 {
		t.Errorf("main end visits = %d, want 1 (the throw continued past to the task and on)", v)
	}
}

// TestEscalationBoundaryRecovers proves an armed interrupting escalation boundary survives a
// crash: a subprocess parks on an inner job with the boundary armed; after replaying the log
// into a fresh store the boundary is rebuilt, so completing the job — which drives the token to
// the escalation end — raises an escalation the recovered boundary still catches (ADR-0125,
// invariant I4).
func TestEscalationBoundaryRecovers(t *testing.T) {
	dir := t.TempDir()

	b := compiler.NewBuilder(63, "escsub-rec", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess()
	b.PushScope(sub)
	iStart := b.AddStartEvent()
	work := b.AddServiceTask("work", 3)
	escEnd := b.AddEscalationEndEvent("LATE")
	b.Connect(iStart, work)
	b.Connect(work, escEnd)
	b.PopScope()
	boundary := b.AddBoundaryEscalationEvent(sub, "LATE", true)
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
	// Parked: subprocess + inner task + armed escalation boundary.
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
	// Completing the job drives the token to the escalation end; the recovered boundary catches.
	p2.CompleteJob(singleActivatableJob(t, store2, jobType))
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after recovery): %v", err)
	}
	if pi, ei := counts(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after escalation post-recovery: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if v := elementVisits(t, store2, cp.Key); v[recovered] != 1 {
		t.Errorf("recovery end visits = %d, want 1 (recovered boundary caught the escalation)", v[recovered])
	}
	if !jobGone(t, store2, jobType) {
		t.Error("inner job survived the interrupting escalation boundary")
	}
}
