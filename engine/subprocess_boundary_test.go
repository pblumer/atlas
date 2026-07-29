package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
)

// subProcessBoundaryProcess builds Start → subProcess{ iStart → serviceTask(parks)
// → iEnd } → done, with a timer boundary (interrupting or not) on the subprocess
// routed to a separate "escalated" end. The inner service task parks on a job, so
// an instance stops with a token inside the subprocess and the boundary armed.
func subProcessBoundaryProcess(t testing.TB, durNanos int64, interrupting bool) (cp *compiler.CompiledProcess, jobType, doneEnd, escEnd int32) {
	t.Helper()
	b := compiler.NewBuilder(defKey, "subbound", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess()
	b.PushScope(sub)
	iStart := b.AddStartEvent()
	iTask := b.AddServiceTask("inner", 3)
	iEnd := b.AddEndEvent()
	b.Connect(iStart, iTask)
	b.Connect(iTask, iEnd)
	b.PopScope()
	boundary := b.AddBoundaryTimerEvent(sub, interrupting, durNanos)
	done := b.AddEndEvent()
	escalated := b.AddEndEvent()
	b.Connect(start, sub)
	b.Connect(sub, done)
	b.Connect(boundary, escalated)
	built, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return built, built.ServiceTask(built.Node(iTask).Detail).JobType, done, escalated
}

// TestSubProcessBoundaryInterrupts fires an interrupting boundary timer on a
// subprocess whose inner service task is still parked: the whole subprocess scope
// is torn down — the inner task terminated and its job canceled — and the
// escalation flow runs to completion. Nothing inside the subprocess outlives it.
func TestSubProcessBoundaryInterrupts(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const dur = int64(30e9)
	cp, jobType, doneEnd, escEnd := subProcessBoundaryProcess(t, dur, true)
	clk := &fixedClock{t: 1_000}

	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Parked: subprocess container + inner task + armed boundary = three instances.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 3 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 3 (subprocess + inner task + boundary)", pi, ei)
	}
	if jobGone(t, h.store, jobType) {
		t.Fatal("inner job missing while parked")
	}

	// Fire the boundary timer.
	clk.t = 1_000 + dur + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after interrupt: process=%d element=%d, want 0 and 0 (subprocess torn down)", pi, ei)
	}
	if !jobGone(t, h.store, jobType) {
		t.Error("inner job survived an interrupting boundary on the subprocess (should be canceled)")
	}
	visits := elementVisits(t, h.store, cp.Key)
	if visits[escEnd] != 1 {
		t.Errorf("escalation end visits = %d, want 1", visits[escEnd])
	}
	if visits[doneEnd] != 0 {
		t.Errorf("normal end visits = %d, want 0 (subprocess was interrupted)", visits[doneEnd])
	}
}

// TestSubProcessBoundaryNonInterrupting fires a non-interrupting boundary on a
// subprocess: the escalation branch runs while the subprocess keeps executing;
// completing the inner job then drains the scope and finishes down the normal
// path. Both ends are reached.
func TestSubProcessBoundaryNonInterrupting(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const dur = int64(5e9)
	cp, jobType, doneEnd, escEnd := subProcessBoundaryProcess(t, dur, false)
	clk := &fixedClock{t: 1_000}

	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	hostJob := singleActivatableJob(t, h.store, jobType)

	// Fire: the escalation branch runs, the subprocess keeps waiting on its job.
	clk.t = 1_000 + dur + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 1 {
		t.Fatalf("after non-interrupting fire: procs=%d, want 1 (subprocess still running)", pi)
	}
	if jobGone(t, h.store, jobType) {
		t.Fatal("inner job vanished on a non-interrupting boundary")
	}
	if v := elementVisits(t, h.store, cp.Key); v[escEnd] != 1 {
		t.Errorf("escalation end visits = %d, want 1", v[escEnd])
	}

	// Complete the inner job: the subprocess drains and the instance finishes.
	p.CompleteJob(hostJob)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after complete): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after inner completes: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key); v[doneEnd] != 1 {
		t.Errorf("normal end visits = %d, want 1", v[doneEnd])
	}
}

// TestSubProcessBoundaryInterruptsAfterRecovery parks inside a subprocess with an
// armed boundary, crashes, recovers, and only then fires the interrupt: the
// recovered subprocess scope and armed boundary must still tear down cleanly.
func TestSubProcessBoundaryInterruptsAfterRecovery(t *testing.T) {
	dir := t.TempDir()
	const dur = int64(30e9)
	cp, jobType, _, escEnd := subProcessBoundaryProcess(t, dur, true)
	clk := &fixedClock{t: 1_000}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clk)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	if pi, ei := counts(t, h1.store); pi != 1 || ei != 3 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 3", pi, ei)
	}
	h1.close(t)

	// Restart and recover.
	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clk)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	if pi, ei := counts(t, h2.store); pi != 1 || ei != 3 {
		t.Fatalf("after recovery: process=%d element=%d, want 1 and 3", pi, ei)
	}

	// Fire the interrupt on the recovered instance.
	clk.t = 1_000 + dur + 1
	if err := p2.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if pi, ei := counts(t, h2.store); pi != 0 || ei != 0 {
		t.Fatalf("after interrupt: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if !jobGone(t, h2.store, jobType) {
		t.Error("inner job survived interrupt after recovery")
	}
	if v := elementVisits(t, h2.store, cp.Key); v[escEnd] != 1 {
		t.Errorf("escalation end visits = %d, want 1", v[escEnd])
	}
}
