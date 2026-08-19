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

// TestConditionalCatchAlreadyTrue: a conditional catch whose condition already holds when the
// token arrives fires at once and passes straight through (ADR-0137). The instance is seeded
// with ready=true, so the catch's arm-time self-evaluation completes it immediately.
func TestConditionalCatchAlreadyTrue(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(100, "cond-catch-true", 1)
	start := b.AddStartEvent()
	wait := b.AddConditionalCatchEvent(mustCompile(t, "ready"))
	end := b.AddEndEvent()
	b.Connect(start, wait)
	b.Connect(wait, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, boolVar("ready", true))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after conditional catch: process=%d element=%d, want 0 and 0 (condition true at arm, passed through)", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key)[end]; v != 1 {
		t.Errorf("end visits = %d, want 1 (the catch fired at once)", v)
	}
}

// TestConditionalCatchWaitsThenFires: a conditional catch whose condition is false at arrival
// parks, then fires when a later SetVariables makes it true — proving the variable-change
// re-check drives an armed conditional (ADR-0137), including via an external SetVariables with
// no activity completing.
func TestConditionalCatchWaitsThenFires(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(101, "cond-catch-wait", 1)
	start := b.AddStartEvent()
	wait := b.AddConditionalCatchEvent(mustCompile(t, "ready"))
	end := b.AddEndEvent()
	b.Connect(start, wait)
	b.Connect(wait, end)
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
	// Parked at the conditional catch — its condition is false (ready unset).
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 1 (waiting on the condition)", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key)[end]; v != 0 {
		t.Fatalf("end visits = %d, want 0 (condition not yet true)", v)
	}
	// An external variable write makes the condition true; the re-check fires the catch.
	p.SetVariables(model.NewKey(1, 1), 0, "operator", boolVar("ready", true))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after set): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after set: process=%d element=%d, want 0 and 0 (condition became true, passed through)", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key)[end]; v != 1 {
		t.Errorf("end visits = %d, want 1 (the re-check fired the catch)", v)
	}
}

// TestConditionalBoundaryInterruptingFires: an interrupting conditional boundary on a waiting
// service task fires when a variable makes its condition true — cancelling the host and routing
// the recovery flow (ADR-0137).
func TestConditionalBoundaryInterruptingFires(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(102, "cond-boundary", 1)
	start := b.AddStartEvent()
	work := b.AddServiceTask("work", 3)
	boundary := b.AddBoundaryConditionalEvent(work, mustCompile(t, "cancelled"), true)
	done := b.AddEndEvent()
	recovered := b.AddEndEvent()
	b.Connect(start, work)
	b.Connect(work, done)
	b.Connect(boundary, recovered)
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
	// Parked on the service task's job, boundary armed (condition false).
	if pi, ei := counts(t, h.store); pi != 1 || ei != 2 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 2 (task + armed boundary)", pi, ei)
	}
	// A variable makes the condition true; the interrupting boundary fires.
	p.SetVariables(model.NewKey(1, 1), 0, "operator", boolVar("cancelled", true))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after set): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after set: process=%d element=%d, want 0 and 0 (boundary fired, host cancelled)", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[recovered] != 1 {
		t.Errorf("recovery end visits = %d, want 1 (conditional boundary fired)", v[recovered])
	}
	if v[done] != 0 {
		t.Errorf("normal end visits = %d, want 0 (host cancelled)", v[done])
	}
	if !jobGone(t, h.store, jobType) {
		t.Error("host job survived the interrupting conditional boundary")
	}
}

// TestConditionalBoundaryFiresImmediatelyIfTrue: a conditional boundary whose condition already
// holds when its host activates fires at once (ADR-0137) — the host never really runs.
func TestConditionalBoundaryFiresImmediatelyIfTrue(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(103, "cond-boundary-now", 1)
	start := b.AddStartEvent()
	work := b.AddServiceTask("work", 3)
	boundary := b.AddBoundaryConditionalEvent(work, mustCompile(t, "cancelled"), true)
	done := b.AddEndEvent()
	recovered := b.AddEndEvent()
	b.Connect(start, work)
	b.Connect(work, done)
	b.Connect(boundary, recovered)
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
	// Seed cancelled=true, so the condition holds the moment the boundary arms.
	p.CreateInstance(cp.Key, boolVar("cancelled", true))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after create: process=%d element=%d, want 0 and 0 (boundary fired at arm)", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[recovered] != 1 || v[done] != 0 {
		t.Errorf("visits recovered=%d done=%d, want 1 and 0 (fired immediately)", v[recovered], v[done])
	}
	if !jobGone(t, h.store, jobType) {
		t.Error("host job survived an immediately-firing conditional boundary")
	}
}

// TestConditionalBoundaryNonInterruptingFiresOnce: a non-interrupting conditional boundary fires
// when its condition becomes true and takes its handler flow while the host job keeps running
// (ADR-0137). It fires exactly once — a later unrelated write while the condition stays true does
// not re-fire it — and the host completes normally when its job is done.
func TestConditionalBoundaryNonInterruptingFiresOnce(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(107, "cond-boundary-nonint", 1)
	start := b.AddStartEvent()
	work := b.AddServiceTask("work", 3)
	boundary := b.AddBoundaryConditionalEvent(work, mustCompile(t, "flag"), false)
	done := b.AddEndEvent()
	notified := b.AddEndEvent()
	b.Connect(start, work)
	b.Connect(work, done)
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
	// The condition becomes true: the non-interrupting boundary fires alongside the job.
	p.SetVariables(model.NewKey(1, 1), 0, "operator", boolVar("flag", true))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after set): %v", err)
	}
	if v := elementVisits(t, h.store, cp.Key)[notified]; v != 1 {
		t.Fatalf("notified visits = %d, want 1 (non-interrupting boundary fired)", v)
	}
	if jobGone(t, h.store, jobType) {
		t.Fatal("the non-interrupting conditional boundary cancelled the host job")
	}
	// A later write while the condition stays true must not re-fire the (consumed) boundary.
	p.SetVariables(model.NewKey(1, 1), 0, "operator", boolVar("other", true))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after 2nd set): %v", err)
	}
	if v := elementVisits(t, h.store, cp.Key)[notified]; v != 1 {
		t.Errorf("notified visits = %d, want 1 (fired once, no re-fire while condition stays true)", v)
	}
	// Completing the job finishes the host and drains the instance.
	p.CompleteJob(singleActivatableJob(t, h.store, jobType))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after job): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after job: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key)[done]; v != 1 {
		t.Errorf("done visits = %d, want 1 (host completed normally)", v)
	}
}

// TestConditionalEventSubInterrupting: an interrupting conditional event subprocess fires when a
// variable makes its condition true — it tears down the main flow (the waiting job) and runs the
// handler (ADR-0137), reusing the event-subprocess arm/fire machinery (ADR-0082).
func TestConditionalEventSubInterrupting(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(105, "cond-eventsub-int", 1)
	start := b.AddStartEvent()
	work := b.AddServiceTask("work", 3)
	mainEnd := b.AddEndEvent()
	b.Connect(start, work)
	b.Connect(work, mainEnd)
	handler := b.AddSubProcess()
	b.PushScope(handler)
	hStart := b.AddStartEvent()
	hEnd := b.AddEndEvent()
	b.Connect(hStart, hEnd)
	b.PopScope()
	b.SetEventSubProcess(handler, compiler.EventSubProcessDetail{
		StartNode: hStart, Interrupting: true, Kind: compiler.BoundaryConditional, Condition: mustCompile(t, "alert"),
	})
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !cp.HasConditionalEvents() {
		t.Fatal("HasConditionalEvents = false, want true (conditional event subprocess)")
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
	// A variable makes the condition true; the interrupting event subprocess fires.
	p.SetVariables(model.NewKey(1, 1), 0, "operator", boolVar("alert", true))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after set): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after set: process=%d element=%d, want 0 and 0 (handler ran, main flow torn down)", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[hEnd] != 1 {
		t.Errorf("handler end visits = %d, want 1 (conditional event subprocess fired)", v[hEnd])
	}
	if v[mainEnd] != 0 {
		t.Errorf("main end visits = %d, want 0 (interrupting handler tore down the main flow)", v[mainEnd])
	}
	if !jobGone(t, h.store, jobType) {
		t.Error("main-flow job survived the interrupting conditional event subprocess")
	}
}

// TestConditionalEventSubNonInterrupting: a non-interrupting conditional event subprocess fires
// when its condition becomes true and runs the handler alongside the still-running main flow —
// the waiting job is untouched (ADR-0137). It fires once; the main flow completes on its own.
func TestConditionalEventSubNonInterrupting(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(106, "cond-eventsub-nonint", 1)
	start := b.AddStartEvent()
	work := b.AddServiceTask("work", 3)
	mainEnd := b.AddEndEvent()
	b.Connect(start, work)
	b.Connect(work, mainEnd)
	handler := b.AddSubProcess()
	b.PushScope(handler)
	hStart := b.AddStartEvent()
	hEnd := b.AddEndEvent()
	b.Connect(hStart, hEnd)
	b.PopScope()
	b.SetEventSubProcess(handler, compiler.EventSubProcessDetail{
		StartNode: hStart, Interrupting: false, Kind: compiler.BoundaryConditional, Condition: mustCompile(t, "alert"),
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
	// A variable makes the condition true; the non-interrupting handler runs alongside.
	p.SetVariables(model.NewKey(1, 1), 0, "operator", boolVar("alert", true))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after set): %v", err)
	}
	if v := elementVisits(t, h.store, cp.Key)[hEnd]; v != 1 {
		t.Errorf("handler end visits = %d, want 1 (non-interrupting conditional event subprocess ran)", v)
	}
	if jobGone(t, h.store, jobType) {
		t.Fatal("the non-interrupting conditional handler cancelled the main-flow job")
	}
	// Completing the main-flow job drains the instance; the fired-once trigger is gone.
	p.CompleteJob(singleActivatableJob(t, h.store, jobType))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after job): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after main flow completes: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key)[mainEnd]; v != 1 {
		t.Errorf("main end visits = %d, want 1 (main flow completed on its own)", v)
	}
}

// TestConditionalCatchRecovers proves an armed conditional catch survives a crash: it parks with
// its condition false; after replaying the log into a fresh store the catch is rebuilt, so a
// post-recovery variable change re-checks it and fires it (ADR-0137, invariant I6).
func TestConditionalCatchRecovers(t *testing.T) {
	dir := t.TempDir()

	b := compiler.NewBuilder(104, "cond-catch-rec", 1)
	start := b.AddStartEvent()
	wait := b.AddConditionalCatchEvent(mustCompile(t, "ready"))
	end := b.AddEndEvent()
	b.Connect(start, wait)
	b.Connect(wait, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

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
	if pi, ei := counts(t, h1.store); pi != 1 || ei != 1 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 1 (armed conditional catch)", pi, ei)
	}
	h1.close(t)

	// Replay into a fresh, empty store: the armed conditional catch must rebuild.
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
	if pi, ei := counts(t, store2); pi != 1 || ei != 1 {
		t.Fatalf("after replay: process=%d element=%d, want 1 and 1 (armed catch rebuilt)", pi, ei)
	}
	// A post-recovery variable change re-checks the recovered catch and fires it.
	p2.SetVariables(model.NewKey(1, 1), 0, "operator", boolVar("ready", true))
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after recovery): %v", err)
	}
	if pi, ei := counts(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after set post-recovery: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if v := elementVisits(t, store2, cp.Key)[end]; v != 1 {
		t.Errorf("end visits = %d, want 1 (recovered catch fired on the variable change)", v)
	}
}

// TestConditionalCatchInSubprocess: a conditional catch nested in an embedded subprocess reads the
// enclosing scope's variables (ADR-0137/0086). Parked inside the subprocess with its condition
// false, it fires — and the subprocess and instance complete — when a later SetVariables makes it
// true, proving the re-check reaches a conditional whose flow scope is the subprocess, not the root.
func TestConditionalCatchInSubprocess(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(109, "cond-catch-sub", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess()
	outerEnd := b.AddEndEvent()
	b.Connect(start, sub)
	b.Connect(sub, outerEnd)
	b.PushScope(sub)
	iStart := b.AddStartEvent()
	wait := b.AddConditionalCatchEvent(mustCompile(t, "ready"))
	iEnd := b.AddEndEvent()
	b.Connect(iStart, wait)
	b.Connect(wait, iEnd)
	b.PopScope()
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
	// Parked at the conditional catch inside the subprocess (condition false).
	if pi, ei := counts(t, h.store); pi != 1 || ei != 2 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 2 (subprocess + inner catch)", pi, ei)
	}
	// A variable write at the root becomes true up the scope chain; the re-check fires the catch.
	p.SetVariables(model.NewKey(1, 1), 0, "operator", boolVar("ready", true))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after set): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after set: process=%d element=%d, want 0 and 0 (catch fired, subprocess drained)", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key)[outerEnd]; v != 1 {
		t.Errorf("outer end visits = %d, want 1 (subprocess completed after the catch fired)", v)
	}
}

// TestConditionalUnrelatedWriteFiresNothing: a parked conditional catch is re-checked on every
// variable write in its instance, but a write of an unrelated variable — the condition still false —
// fires nothing (ADR-0137, follow-up: an unrelated write must not trip a conditional). Only the
// write that makes the predicate true fires it.
func TestConditionalUnrelatedWriteFiresNothing(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(110, "cond-unrelated", 1)
	start := b.AddStartEvent()
	wait := b.AddConditionalCatchEvent(mustCompile(t, "ready"))
	end := b.AddEndEvent()
	b.Connect(start, wait)
	b.Connect(wait, end)
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
	// An unrelated write triggers a re-check, but the condition (ready) is still false: nothing fires.
	p.SetVariables(model.NewKey(1, 1), 0, "operator", boolVar("other", true))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after unrelated set): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("after unrelated set: process=%d element=%d, want 1 and 1 (still parked)", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key)[end]; v != 0 {
		t.Fatalf("end visits = %d, want 0 (unrelated write fired nothing)", v)
	}
	// The write that makes the predicate true fires it. Two variables are written in the one batch,
	// so the instance is marked dirty twice — the second write hits the dedup guard, scheduling a
	// single re-check rather than two (ADR-0137).
	p.SetVariables(model.NewKey(1, 1), 0, "operator", boolVar("ready", true), boolVar("also", true))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after set): %v", err)
	}
	if v := elementVisits(t, h.store, cp.Key)[end]; v != 1 {
		t.Errorf("end visits = %d, want 1 (the relevant write fired the catch)", v)
	}
}

// TestConditionalEventSubNonInterruptingFiresAtArm: a non-interrupting conditional event subprocess
// nested in an embedded subprocess whose condition already holds when that subprocess is entered
// fires at once (ADR-0137) — its arm-time self-evaluation runs the handler alongside the still-
// running inner task, which then completes normally. Being non-interrupting, firing at arm tears
// nothing down, so the inner flow is untouched.
func TestConditionalEventSubNonInterruptingFiresAtArm(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(111, "cond-eventsub-atarm-ni", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess()
	outerEnd := b.AddEndEvent()
	b.Connect(start, sub)
	b.Connect(sub, outerEnd)
	b.PushScope(sub)
	iStart := b.AddStartEvent()
	work := b.AddServiceTask("work", 3)
	iEnd := b.AddEndEvent()
	b.Connect(iStart, work)
	b.Connect(work, iEnd)
	handler := b.AddSubProcess()
	b.PushScope(handler)
	hStart := b.AddStartEvent()
	hEnd := b.AddEndEvent()
	b.Connect(hStart, hEnd)
	b.PopScope()
	b.SetEventSubProcess(handler, compiler.EventSubProcessDetail{
		StartNode: hStart, Interrupting: false, Kind: compiler.BoundaryConditional, Condition: mustCompile(t, "alert"),
	})
	b.PopScope()
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
	// Seed alert=true: when the subprocess is entered the event-sub arms and, on its self-evaluation,
	// finds the condition already true and runs the handler alongside the inner task.
	p.CreateInstance(cp.Key, boolVar("alert", true))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if v := elementVisits(t, h.store, cp.Key)[hEnd]; v != 1 {
		t.Fatalf("handler end visits = %d, want 1 (non-interrupting event-sub fired at arm)", v)
	}
	if jobGone(t, h.store, jobType) {
		t.Fatal("the non-interrupting handler cancelled the inner job")
	}
	// The inner task completes on its own; the subprocess and instance then drain.
	p.CompleteJob(singleActivatableJob(t, h.store, jobType))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after job): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after job: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key)[outerEnd]; v != 1 {
		t.Errorf("outer end visits = %d, want 1", v)
	}
}

// TestConditionalDirtyTrackingSkipsPlainProcess: a process with no conditional event pays nothing
// on a variable write — markConditionDirty resolves the instance, sees HasConditionalEvents is
// false, and records nothing, so no re-check is scheduled (ADR-0137). This pins the early-out that
// keeps conditional-free processes on the same write path they had before conditional events.
func TestConditionalDirtyTrackingSkipsPlainProcess(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(112, "plain-no-cond", 1)
	start := b.AddStartEvent()
	work := b.AddServiceTask("work", 3)
	end := b.AddEndEvent()
	b.Connect(start, work)
	b.Connect(work, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if cp.HasConditionalEvents() {
		t.Fatal("HasConditionalEvents = true, want false (no conditional events)")
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
	// A variable write on a conditional-free instance goes through markConditionDirty, which sees
	// no conditional events and records nothing; the instance stays parked on its job.
	p.SetVariables(model.NewKey(1, 1), 0, "operator", boolVar("anything", true))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after set): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("after set: process=%d element=%d, want 1 and 1 (still parked, no re-check)", pi, ei)
	}
	// The instance still completes normally on job completion.
	p.CompleteJob(singleActivatableJob(t, h.store, jobType))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after job): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after job: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestConditionalEventSubInterruptingFiresAtArm is a regression test for an orphaned-element bug:
// an interrupting conditional event subprocess whose condition already holds when its scope is
// entered must fire cleanly, leaving no live element behind (ADR-0137). Firing synchronously at
// arm interrupted the scope while its inner flow was still activating in the same wave, so a
// not-yet-activated inner task (here `work`) was orphaned — the instance finished with a stray
// element. The at-arm fire is now deferred to a re-check that runs once the inner flow has
// settled, so terminateScope tears the whole scope down.
func TestConditionalEventSubInterruptingFiresAtArm(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(113, "cond-eventsub-int-atarm", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess()
	outerEnd := b.AddEndEvent()
	b.Connect(start, sub)
	b.Connect(sub, outerEnd)
	b.PushScope(sub)
	iStart := b.AddStartEvent()
	work := b.AddServiceTask("work", 3)
	iEnd := b.AddEndEvent()
	b.Connect(iStart, work)
	b.Connect(work, iEnd)
	handler := b.AddSubProcess()
	b.PushScope(handler)
	hStart := b.AddStartEvent()
	hEnd := b.AddEndEvent()
	b.Connect(hStart, hEnd)
	b.PopScope()
	b.SetEventSubProcess(handler, compiler.EventSubProcessDetail{
		StartNode: hStart, Interrupting: true, Kind: compiler.BoundaryConditional, Condition: mustCompile(t, "alert"),
	})
	b.PopScope()
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
	p.CreateInstance(cp.Key, boolVar("alert", true))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after create: process=%d element=%d, want 0 and 0 (fired at arm, scope fully torn down — no orphan)", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[hEnd] != 1 || v[outerEnd] != 1 || v[iEnd] != 0 {
		t.Errorf("visits handler=%d outer=%d innerEnd=%d, want 1, 1, 0", v[hEnd], v[outerEnd], v[iEnd])
	}
	if !jobGone(t, h.store, jobType) {
		t.Error("inner job survived the interrupting conditional event subprocess (orphaned element)")
	}
}

// TestConditionalBoundaryInterruptingOnSubprocessFiresAtArm is the boundary counterpart of the
// orphaned-element regression: an interrupting conditional boundary on a subprocess host whose
// condition holds at arm must tear the whole host scope down, not leave its still-activating inner
// task behind (ADR-0137).
func TestConditionalBoundaryInterruptingOnSubprocessFiresAtArm(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(114, "cond-bnd-sub-atarm", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess()
	done := b.AddEndEvent()
	recovered := b.AddEndEvent()
	boundary := b.AddBoundaryConditionalEvent(sub, mustCompile(t, "alert"), true)
	b.Connect(start, sub)
	b.Connect(sub, done)
	b.Connect(boundary, recovered)
	b.PushScope(sub)
	iStart := b.AddStartEvent()
	work := b.AddServiceTask("work", 3)
	iEnd := b.AddEndEvent()
	b.Connect(iStart, work)
	b.Connect(work, iEnd)
	b.PopScope()
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
	p.CreateInstance(cp.Key, boolVar("alert", true))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after create: process=%d element=%d, want 0 and 0 (boundary tore the host scope down — no orphan)", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[recovered] != 1 || v[done] != 0 {
		t.Errorf("visits recovered=%d done=%d, want 1 and 0", v[recovered], v[done])
	}
	if !jobGone(t, h.store, jobType) {
		t.Error("inner job survived the interrupting conditional boundary (orphaned element)")
	}
}
