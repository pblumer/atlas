package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
)

// eventSubMessageProcess builds start → work(service task) → end, plus a root-level
// message-triggered event subprocess {es(message "cancel") → he(end)} — interrupting or
// not. It returns the process and the work job type.
func eventSubMessageProcess(t *testing.T, interrupting bool) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(1, "esp", 1)
	start := b.AddStartEvent()
	work := b.AddServiceTask("work", 3)
	end := b.AddEndEvent()
	b.Connect(start, work)
	b.Connect(work, end)

	// The event subprocess handler, added at the root scope, then filled under its scope.
	handler := b.AddSubProcess()
	b.PushScope(handler)
	corr := mustCompile(t, `"k"`)
	es := b.AddMessageStartEvent("cancel", corr, false)
	he := b.AddEndEvent()
	b.Connect(es, he)
	b.PopScope()
	b.SetEventSubProcess(handler, compiler.EventSubProcessDetail{
		StartNode: es, Interrupting: interrupting, Kind: compiler.BoundaryMessage,
		MessageName: "cancel", CorrelationKey: corr,
	})

	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ServiceTask(cp.Node(work).Detail).JobType
}

// TestEventSubprocessMessageInterrupts arms a root message-triggered interrupting event
// subprocess while a service task waits; a correlating message terminates the main flow
// (cancelling its job), runs the handler, and the instance completes (ADR-0082 Phase 2).
func TestEventSubprocessMessageInterrupts(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType := eventSubMessageProcess(t, true)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Parked: the service task (its job) and the armed event-sub trigger are the two live
	// element instances; the trigger is uncounted but still a real element instance.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 2 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 2 (task + armed trigger)", pi, ei)
	}
	if jobGone(t, h.store, jobType) {
		t.Fatal("work job missing while parked")
	}

	// A correlating message fires the trigger: interrupt the main flow, run the handler.
	p.PublishMessage("cancel", "k")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after publish: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after trigger: process=%d element=%d, want 0 and 0 (main flow interrupted, handler ran)", pi, ei)
	}
	if !jobGone(t, h.store, jobType) {
		t.Error("work job survived the interrupting event subprocess (should be canceled)")
	}
}

// TestEventSubprocessDisarmedOnCompletion arms a message event subprocess but never
// triggers it: the main flow completes normally, and the armed trigger is disarmed as the
// instance completes rather than lingering (ADR-0082).
func TestEventSubprocessDisarmedOnCompletion(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType := eventSubMessageProcess(t, true)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Complete the main-flow job normally; the instance finishes and the trigger disarms.
	p.CompleteJob(activatableJobs(t, h.store, jobType)[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after complete: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after normal completion: process=%d element=%d, want 0 and 0 (trigger disarmed)", pi, ei)
	}
	// A late message must not resurrect anything — the subscription self-retired.
	p.PublishMessage("cancel", "k")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after late publish: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after late message: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestEventSubprocessRecovers arms the trigger, parks, crashes, and recovers: the armed
// subscription and the parked main flow rebuild from the log so a message after restart
// still interrupts the flow and runs the handler (ADR-0082).
func TestEventSubprocessRecovers(t *testing.T) {
	dir := t.TempDir()
	cp, jobType := eventSubMessageProcess(t, true)
	clk := &manualClock{}

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
	if pi, ei := counts(t, h1.store); pi != 1 || ei != 2 {
		t.Fatalf("before crash: process=%d element=%d, want 1 and 2", pi, ei)
	}
	h1.close(t)

	// Recover, then correlate the message.
	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clk)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	if pi, ei := counts(t, h2.store); pi != 1 || ei != 2 {
		t.Fatalf("after recovery: process=%d element=%d, want 1 and 2 (task + trigger rebuilt)", pi, ei)
	}
	p2.PublishMessage("cancel", "k")
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 2: %v", err)
	}
	if pi, ei := counts(t, h2.store); pi != 0 || ei != 0 {
		t.Fatalf("after recovered trigger: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if !jobGone(t, h2.store, jobType) {
		t.Error("work job survived the recovered interrupt")
	}
}

// TestEventSubprocessNonInterruptingMessageFiresTwice arms a root non-interrupting
// message event subprocess while a service task waits; two correlating messages run the
// handler twice in parallel with the untouched main flow, and the instance completes only
// once the main flow finishes (ADR-0082 Phase 3).
func TestEventSubprocessNonInterruptingMessageFiresTwice(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	b := compiler.NewBuilder(1, "esp-ni", 1)
	start := b.AddStartEvent()
	work := b.AddServiceTask("work", 3)
	end := b.AddEndEvent()
	b.Connect(start, work)
	b.Connect(work, end)
	handler := b.AddSubProcess()
	b.PushScope(handler)
	corr := mustCompile(t, `"k"`)
	es := b.AddMessageStartEvent("ping", corr, false)
	he := b.AddEndEvent()
	b.Connect(es, he)
	b.PopScope()
	b.SetEventSubProcess(handler, compiler.EventSubProcessDetail{
		StartNode: es, Interrupting: false, Kind: compiler.BoundaryMessage,
		MessageName: "ping", CorrelationKey: corr,
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

	for i := 1; i <= 2; i++ {
		p.PublishMessage("ping", "k")
		if err := p.RunUntilIdle(); err != nil {
			t.Fatalf("RunUntilIdle after publish %d: %v", i, err)
		}
		if jobGone(t, h.store, jobType) {
			t.Fatalf("non-interrupting trigger cancelled the main-flow job (publish %d)", i)
		}
	}
	if v := elementVisits(t, h.store, cp.Key)[he]; v != 2 {
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

// eventSubTimerProcess builds start → work(service task) → end plus a root timer-triggered
// event subprocess {es(timer) → he(end)} with the given schedule and interrupting flag.
func eventSubTimerProcess(t *testing.T, interrupting bool, sched compiler.TimerSchedule) (*compiler.CompiledProcess, int32, int32) {
	t.Helper()
	b := compiler.NewBuilder(1, "esp-timer", 1)
	start := b.AddStartEvent()
	work := b.AddServiceTask("work", 3)
	end := b.AddEndEvent()
	b.Connect(start, work)
	b.Connect(work, end)
	handler := b.AddSubProcess()
	b.PushScope(handler)
	es := b.AddTimerStartEvent(sched)
	he := b.AddEndEvent()
	b.Connect(es, he)
	b.PopScope()
	b.SetEventSubProcess(handler, compiler.EventSubProcessDetail{
		StartNode: es, Interrupting: interrupting, Kind: compiler.BoundaryTimer, Schedule: sched,
	})
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ServiceTask(cp.Node(work).Detail).JobType, he
}

// TestEventSubprocessTimerInterrupts fires an interrupting timer-triggered event
// subprocess: the timer arms while the service task waits, and on its due date the main
// flow is torn down (its job canceled) and the handler runs to instance completion
// (ADR-0082 Phase 3).
func TestEventSubprocessTimerInterrupts(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const dur = int64(30e9)
	cp, jobType, _ := eventSubTimerProcess(t, true, compiler.TimerSchedule{Kind: compiler.TimerDuration, BaseNanos: dur})
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
	if pi, ei := counts(t, h.store); pi != 1 || ei != 2 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 2 (task + armed timer trigger)", pi, ei)
	}
	clk.t = 1_000 + dur + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after timer: process=%d element=%d, want 0 and 0 (main flow interrupted, handler ran)", pi, ei)
	}
	if !jobGone(t, h.store, jobType) {
		t.Error("work job survived the interrupting timer event subprocess")
	}
}

// TestEventSubprocessNonInterruptingTimerCycle fires a non-interrupting cycle timer event
// subprocess the scheduled number of times (R2 → three fires), each running the handler in
// parallel with the untouched main flow, then stops re-arming (ADR-0082 Phase 3).
func TestEventSubprocessNonInterruptingTimerCycle(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const iv = int64(100)
	sched := compiler.TimerSchedule{Kind: compiler.TimerCycleInterval, BaseNanos: iv, Repetitions: 2} // R2 → 3 fires
	cp, jobType, he := eventSubTimerProcess(t, false, sched)
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
	// Advance well past each occurrence; the trigger re-arms after each of the first three.
	for i := 0; i < 5; i++ {
		clk.t += iv + 1
		if err := p.TickTimers(); err != nil {
			t.Fatalf("TickTimers %d: %v", i, err)
		}
	}
	if v := elementVisits(t, h.store, cp.Key)[he]; v != 3 {
		t.Errorf("handler end visits = %d, want 3 (R2 = first + 2 repetitions)", v)
	}
	if jobGone(t, h.store, jobType) {
		t.Error("non-interrupting cycle cancelled the main-flow job")
	}
	p.CompleteJob(activatableJobs(t, h.store, jobType)[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after complete: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after main flow completes: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestEventSubprocessNonInterruptingRecovers fires a non-interrupting message trigger once
// (running the handler and re-arming), crashes, and recovers: the re-armed subscription
// rebuilds from the log so a second message after restart runs the handler again
// (ADR-0082 Phase 3).
func TestEventSubprocessNonInterruptingRecovers(t *testing.T) {
	dir := t.TempDir()
	b := compiler.NewBuilder(1, "esp-ni-rec", 1)
	start := b.AddStartEvent()
	work := b.AddServiceTask("work", 3)
	end := b.AddEndEvent()
	b.Connect(start, work)
	b.Connect(work, end)
	handler := b.AddSubProcess()
	b.PushScope(handler)
	corr := mustCompile(t, `"k"`)
	es := b.AddMessageStartEvent("ping", corr, false)
	he := b.AddEndEvent()
	b.Connect(es, he)
	b.PopScope()
	b.SetEventSubProcess(handler, compiler.EventSubProcessDetail{
		StartNode: es, Interrupting: false, Kind: compiler.BoundaryMessage,
		MessageName: "ping", CorrelationKey: corr,
	})
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	clk := &manualClock{}

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
	p1.PublishMessage("ping", "k") // first fire → handler runs, trigger re-arms
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after publish 1: %v", err)
	}
	if v := elementVisits(t, h1.store, cp.Key)[he]; v != 1 {
		t.Fatalf("before crash: handler visits = %d, want 1", v)
	}
	h1.close(t)

	// Recover — the re-armed subscription must rebuild — then fire again.
	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clk)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	p2.PublishMessage("ping", "k")
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after publish 2: %v", err)
	}
	if v := elementVisits(t, h2.store, cp.Key)[he]; v != 2 {
		t.Errorf("after recovery: handler visits = %d, want 2 (re-armed trigger fired post-restart)", v)
	}
}

// subprocessEventSubProcess builds start → sub{istart → inner(svc) → iend} → after(svc)
// → end, where the subprocess `sub` carries a message-triggered interrupting event
// subprocess {es(message "cancel") → he}. It returns the process and the inner/after job
// types. Firing the message interrupts only the subprocess; `after` (outside it) runs on.
func subprocessEventSubProcess(t *testing.T) (*compiler.CompiledProcess, int32, int32) {
	t.Helper()
	b := compiler.NewBuilder(1, "esp-sub", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess()
	after := b.AddServiceTask("after", 3)
	end := b.AddEndEvent()
	b.Connect(start, sub)
	b.Connect(sub, after)
	b.Connect(after, end)

	b.PushScope(sub)
	istart := b.AddStartEvent()
	inner := b.AddServiceTask("inner", 3)
	iend := b.AddEndEvent()
	b.Connect(istart, inner)
	b.Connect(inner, iend)
	handler := b.AddSubProcess()
	b.PushScope(handler)
	corr := mustCompile(t, `"k"`)
	es := b.AddMessageStartEvent("cancel", corr, false)
	he := b.AddEndEvent()
	b.Connect(es, he)
	b.PopScope() // handler
	b.SetEventSubProcess(handler, compiler.EventSubProcessDetail{
		StartNode: es, Interrupting: true, Kind: compiler.BoundaryMessage,
		MessageName: "cancel", CorrelationKey: corr,
	})
	b.PopScope() // sub

	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ServiceTask(cp.Node(inner).Detail).JobType, cp.ServiceTask(cp.Node(after).Detail).JobType
}

// TestEventSubprocessSubprocessLevelInterrupt fires a message-triggered interrupting event
// subprocess scoped to an embedded subprocess: only the subprocess is torn down (its inner
// job canceled, its handler run), the subprocess then completes and takes its outgoing
// flow, and the flow *after* the subprocess continues — the instance is not aborted
// (ADR-0082 Phase 4).
func TestEventSubprocessSubprocessLevelInterrupt(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, innerJT, afterJT := subprocessEventSubProcess(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if jobGone(t, h.store, innerJT) {
		t.Fatal("inner job missing while the subprocess runs")
	}

	p.PublishMessage("cancel", "k")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after publish: %v", err)
	}
	if !jobGone(t, h.store, innerJT) {
		t.Error("inner job survived the subprocess-level interrupt (should be canceled)")
	}
	if jobGone(t, h.store, afterJT) {
		t.Fatal("the flow after the subprocess did not continue (after job missing)")
	}
	if pi, _ := counts(t, h.store); pi != 1 {
		t.Fatalf("instance count = %d, want 1 (only the subprocess was interrupted, not the instance)", pi)
	}

	p.CompleteJob(activatableJobs(t, h.store, afterJT)[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after complete: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after outer flow completes: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestEventSubprocessSubprocessDisarmOnCompletion completes an embedded subprocess normally
// while its event subprocess is armed: the trigger is disarmed as the subprocess drains,
// the outer flow runs on, and a late message is inert (ADR-0082 Phase 4).
func TestEventSubprocessSubprocessDisarmOnCompletion(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, innerJT, afterJT := subprocessEventSubProcess(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Complete the inner task normally: the subprocess drains (disarming its trigger) and
	// the outer `after` task activates.
	p.CompleteJob(activatableJobs(t, h.store, innerJT)[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after inner: %v", err)
	}
	if jobGone(t, h.store, afterJT) {
		t.Fatal("outer `after` task did not activate after the subprocess completed")
	}
	// A message now must not resurrect the disarmed event subprocess.
	p.PublishMessage("cancel", "k")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after late publish: %v", err)
	}
	p.CompleteJob(activatableJobs(t, h.store, afterJT)[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after after: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after normal completion: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestEventSubprocessSubprocessInterruptRecovers arms a subprocess-scoped event subprocess,
// parks, crashes, and recovers: the armed subscription (scoped to the subprocess) rebuilds
// so a message after restart still interrupts only the subprocess and the outer flow
// continues (ADR-0082 Phase 4).
func TestEventSubprocessSubprocessInterruptRecovers(t *testing.T) {
	dir := t.TempDir()
	cp, innerJT, afterJT := subprocessEventSubProcess(t)
	clk := &manualClock{}

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
	if jobGone(t, h1.store, innerJT) {
		t.Fatal("inner job missing before crash")
	}
	h1.close(t)

	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clk)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	p2.PublishMessage("cancel", "k")
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 2: %v", err)
	}
	if !jobGone(t, h2.store, innerJT) {
		t.Error("inner job survived the recovered subprocess-level interrupt")
	}
	if jobGone(t, h2.store, afterJT) {
		t.Fatal("outer flow did not continue after the recovered interrupt")
	}
	p2.CompleteJob(activatableJobs(t, h2.store, afterJT)[0])
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after complete: %v", err)
	}
	if pi, ei := counts(t, h2.store); pi != 0 || ei != 0 {
		t.Fatalf("after recovery completion: process=%d element=%d, want 0 and 0", pi, ei)
	}
}
