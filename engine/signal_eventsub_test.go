package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
)

// signalEventSubProcess builds start → work(service task) → end, plus a root-level
// signal-triggered event subprocess {es(signal "cancelled") → he(end)} — interrupting
// or not (ADR-0088 Phase 4, reusing ADR-0082). A broadcast of "cancelled" from any
// instance fires the trigger. It returns the process, the work job type, and the
// handler end element id so a test can count handler runs via the visit history.
func signalEventSubProcess(t *testing.T, key uint64, interrupting bool) (cp *compiler.CompiledProcess, jobType, handlerEnd int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "esp-signal", 1)
	start := b.AddStartEvent()
	work := b.AddServiceTask("work", 3)
	end := b.AddEndEvent()
	b.Connect(start, work)
	b.Connect(work, end)

	// The event subprocess handler, added at the root scope, then filled under its scope.
	handler := b.AddSubProcess()
	b.PushScope(handler)
	es := b.AddSignalStartEvent("cancelled")
	he := b.AddEndEvent()
	b.Connect(es, he)
	b.PopScope()
	b.SetEventSubProcess(handler, compiler.EventSubProcessDetail{
		StartNode: es, Interrupting: interrupting, Kind: compiler.BoundarySignal,
		SignalName: "cancelled",
	})

	built, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return built, built.ServiceTask(built.Node(work).Detail).JobType, he
}

// TestSignalEventSubprocessInterrupts arms a root signal-triggered interrupting event
// subprocess while a service task waits; a separate instance broadcasts "cancelled",
// which tears down the main flow (cancelling its job), runs the handler, and completes
// the instance (ADR-0088 Phase 4).
func TestSignalEventSubprocessInterrupts(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	guarded, jobType, he := signalEventSubProcess(t, 40, true)
	thrower := signalThrower(t, 41)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(guarded)
	p.Deploy(thrower)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(guarded.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (guarded): %v", err)
	}
	// Parked: the service task (its job) and the armed signal event-sub trigger.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 2 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 2 (task + armed trigger)", pi, ei)
	}
	if jobGone(t, h.store, jobType) {
		t.Fatal("work job missing while parked")
	}

	// A separate instance broadcasts "cancelled": the interrupting event sub fires
	// cross-instance, tears down the main flow, and runs the handler.
	p.CreateInstance(thrower.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (throw): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after trigger: process=%d element=%d, want 0 and 0 (main flow interrupted, handler ran)", pi, ei)
	}
	if !jobGone(t, h.store, jobType) {
		t.Error("work job survived the interrupting signal event subprocess (should be canceled)")
	}
	if v := elementVisits(t, h.store, guarded.Key)[he]; v != 1 {
		t.Errorf("handler end visits = %d, want 1", v)
	}
}

// TestSignalEventSubprocessNonInterruptingFiresTwice arms a root non-interrupting signal
// event subprocess while a service task waits; two broadcasts run the handler twice in
// parallel with the untouched main flow, and the instance completes only once the main
// flow finishes (ADR-0088 Phase 4).
func TestSignalEventSubprocessNonInterruptingFiresTwice(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	guarded, jobType, he := signalEventSubProcess(t, 42, false)
	thrower := signalThrower(t, 43)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(guarded)
	p.Deploy(thrower)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(guarded.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (guarded): %v", err)
	}

	// Two broadcasts, each a fresh throwing instance: the trigger re-arms between them.
	for i := 1; i <= 2; i++ {
		p.CreateInstance(thrower.Key)
		if err := p.RunUntilIdle(); err != nil {
			t.Fatalf("RunUntilIdle after throw %d: %v", i, err)
		}
		if jobGone(t, h.store, jobType) {
			t.Fatalf("non-interrupting signal trigger cancelled the main-flow job (throw %d)", i)
		}
	}
	if v := elementVisits(t, h.store, guarded.Key)[he]; v != 2 {
		t.Errorf("handler end visits = %d, want 2 (fired twice, re-armed between)", v)
	}
	// Completing the main flow drains the scope; the re-armed trigger disarms.
	p.CompleteJob(activatableJobs(t, h.store, jobType)[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after complete: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after main flow completes: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestSignalEventSubprocessNonInterruptingRecovers fires a non-interrupting signal
// trigger once (running the handler and re-arming), crashes, and recovers: the re-armed
// subscription rebuilds from the log so a broadcast after restart runs the handler again
// (ADR-0088 Phase 4 — recovery across a re-arm).
func TestSignalEventSubprocessNonInterruptingRecovers(t *testing.T) {
	dir := t.TempDir()
	guarded, _, he := signalEventSubProcess(t, 44, false)
	thrower := signalThrower(t, 45)
	clk := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clk)
	p1.Deploy(guarded)
	p1.Deploy(thrower)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(guarded.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	p1.CreateInstance(thrower.Key) // first fire → handler runs, trigger re-arms
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after throw 1: %v", err)
	}
	if v := elementVisits(t, h1.store, guarded.Key)[he]; v != 1 {
		t.Fatalf("before crash: handler visits = %d, want 1", v)
	}
	h1.close(t)

	// Recover — the re-armed signal subscription must rebuild — then broadcast again.
	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clk)
	p2.Deploy(guarded)
	p2.Deploy(thrower)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	p2.CreateInstance(thrower.Key)
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after throw 2: %v", err)
	}
	if v := elementVisits(t, h2.store, guarded.Key)[he]; v != 2 {
		t.Errorf("after recovery: handler visits = %d, want 2 (re-armed signal trigger fired post-restart)", v)
	}
}
