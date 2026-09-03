package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
)

// boundaryTimerProcess builds Start → host service task (job "post") → done,
// with a timer boundary event (durNanos, interrupting or not) routed to a
// separate "escalated" end. It returns the process, the host job type, and the
// two end element ids so a test can tell which path ran via the visit history.
func boundaryTimerProcess(t testing.TB, durNanos int64, interrupting bool) (cp *compiler.CompiledProcess, jobType, doneEnd, escEnd int32) {
	t.Helper()
	b := compiler.NewBuilder(defKey, "publish", 1)
	start := b.AddStartEvent()
	host := b.AddServiceTask("post", 3)
	timeout := b.AddBoundaryTimerEvent(host, interrupting, durNanos)
	done := b.AddEndEvent()
	escalated := b.AddEndEvent()
	b.Connect(start, host)
	b.Connect(host, done)
	b.Connect(timeout, escalated)
	built, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jt := built.ServiceTask(built.Node(host).Detail).JobType
	return built, jt, done, escalated
}

// jobGone reports whether no job of jobType is activatable (the host's job was
// canceled or completed).
func jobGone(t *testing.T, s *state.Store, jobType int32) bool {
	t.Helper()
	return len(activatableJobs(t, s, jobType)) == 0
}

// TestBoundaryTimerInterrupts fires an interrupting boundary timer on a waiting
// service task: the host is terminated, its job canceled (gone from the store),
// the escalation flow is taken, and the instance completes down that branch.
func TestBoundaryTimerInterrupts(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const dur = int64(30e9)
	cp, jobType, doneEnd, escEnd := boundaryTimerProcess(t, dur, true)
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
	// Parked: the host task waits (its job) and the boundary event is armed —
	// two live element instances in one process.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 2 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 2 (host + armed boundary)", pi, ei)
	}
	if jobGone(t, h.store, jobType) {
		t.Fatal("host job missing while parked")
	}

	// Fire the boundary timer.
	clk.t = 1_000 + dur + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after interrupt: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if !jobGone(t, h.store, jobType) {
		t.Error("host job survived an interrupting boundary (should be canceled)")
	}
	visits := elementVisits(t, h.store, cp.Key)
	if visits[escEnd] != 1 {
		t.Errorf("escalation end visits = %d, want 1", visits[escEnd])
	}
	if visits[doneEnd] != 0 {
		t.Errorf("normal end visits = %d, want 0 (host was interrupted)", visits[doneEnd])
	}
}

// TestBoundaryTimerNonInterrupting fires a non-interrupting boundary timer: the
// escalation flow runs while the host keeps waiting; completing the host job then
// finishes the instance down the normal path. Both ends are reached.
func TestBoundaryTimerNonInterrupting(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const dur = int64(5e9)
	cp, jobType, doneEnd, escEnd := boundaryTimerProcess(t, dur, false)
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

	// Fire the boundary timer: the escalation branch runs, the host keeps waiting.
	clk.t = 1_000 + dur + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 1 {
		t.Fatalf("after non-interrupting fire: procs=%d, want 1 (host still waiting)", pi)
	}
	if jobGone(t, h.store, jobType) {
		t.Fatal("host job vanished on a non-interrupting boundary")
	}
	if v := elementVisits(t, h.store, cp.Key); v[escEnd] != 1 {
		t.Errorf("escalation end visits = %d, want 1", v[escEnd])
	}

	// Complete the host job: the normal path finishes the instance.
	p.CompleteJob(hostJob)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after complete): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after host completes: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key); v[doneEnd] != 1 {
		t.Errorf("normal end visits = %d, want 1", v[doneEnd])
	}
}

// TestBoundaryDisarmedOnNormalCompletion completes the host before its boundary
// timer is due: the boundary is disarmed, so when the timer later comes due it
// fires as a no-op and the escalation branch never runs.
func TestBoundaryDisarmedOnNormalCompletion(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const dur = int64(30e9)
	cp, jobType, doneEnd, escEnd := boundaryTimerProcess(t, dur, true)
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

	// Complete the host normally, before the timer is due.
	p.CompleteJob(hostJob)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (complete): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after normal completion: process=%d element=%d, want 0 and 0 (boundary disarmed)", pi, ei)
	}

	// The timer comes due later; it must fire as a no-op (its element is gone).
	clk.t = 1_000 + dur + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[doneEnd] != 1 {
		t.Errorf("normal end visits = %d, want 1", v[doneEnd])
	}
	if v[escEnd] != 0 {
		t.Errorf("escalation end visits = %d, want 0 (boundary was disarmed)", v[escEnd])
	}
}

// TestBoundaryMessageInterrupts fires an interrupting message boundary on a
// waiting user task: publishing the correlating message terminates the host,
// cancels its job, and routes to the cancellation branch.
func TestBoundaryMessageInterrupts(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(defKey, "review", 1)
	start := b.AddStartEvent()
	host := b.AddUserTask("Review", "editor", "reviewers", "", 50, 0, 3)
	cancel := b.AddBoundaryMessageEvent(host, true, "cancel", nil) // empty correlation key
	done := b.AddEndEvent()
	canceled := b.AddEndEvent()
	b.Connect(start, host)
	b.Connect(host, done)
	b.Connect(cancel, canceled)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.UserTask(cp.Node(host).Detail).JobType

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if jobGone(t, h.store, jobType) {
		t.Fatal("host job missing while parked")
	}

	// Publish the cancellation message (empty correlation key matches the nil key).
	p.PublishMessage("cancel", "")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (publish): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after message interrupt: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if !jobGone(t, h.store, jobType) {
		t.Error("host job survived an interrupting message boundary")
	}
	if v := elementVisits(t, h.store, cp.Key); v[canceled] != 1 || v[done] != 0 {
		t.Errorf("visits canceled=%d done=%d, want 1 and 0", v[canceled], v[done])
	}
}

// TestTwoInterruptingBoundariesRaceGuard arms two interrupting boundary timers
// on one host, both due at once. The first to fire interrupts the host and
// terminates the second; the second's queued completion must be a no-op (the
// liveness guard), so the escalation branch runs exactly once.
func TestTwoInterruptingBoundariesRaceGuard(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const dur = int64(10e9)

	b := compiler.NewBuilder(defKey, "twoBoundaries", 1)
	start := b.AddStartEvent()
	host := b.AddServiceTask("post", 3)
	b1 := b.AddBoundaryTimerEvent(host, true, dur)
	b2 := b.AddBoundaryTimerEvent(host, true, dur)
	done := b.AddEndEvent()
	escalated := b.AddEndEvent()
	b.Connect(start, host)
	b.Connect(host, done)
	b.Connect(b1, escalated)
	b.Connect(b2, escalated)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ServiceTask(cp.Node(host).Detail).JobType
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
	// Host + two armed boundaries.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 3 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 3", pi, ei)
	}

	clk.t = 1_000 + dur + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after fire: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if !jobGone(t, h.store, jobType) {
		t.Error("host job survived")
	}
	// Exactly one escalation despite two boundaries firing — the guard held.
	if v := elementVisits(t, h.store, cp.Key); v[escalated] != 1 {
		t.Errorf("escalation end visits = %d, want 1 (the second boundary must no-op)", v[escalated])
	}
}

// TestBoundaryInterruptsHostWithoutJob interrupts a host that holds no job (a
// waiting timer catch event), covering the branch where there is no host job to
// cancel. The host's own timer self-retires.
func TestBoundaryInterruptsHostWithoutJob(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const hostDur = int64(1e12) // far in the future — the boundary fires first
	const boundaryDur = int64(10e9)

	b := compiler.NewBuilder(defKey, "eventHost", 1)
	start := b.AddStartEvent()
	host := b.AddTimerCatchEvent(hostDur)
	timeout := b.AddBoundaryTimerEvent(host, true, boundaryDur)
	done := b.AddEndEvent()
	escalated := b.AddEndEvent()
	b.Connect(start, host)
	b.Connect(host, done)
	b.Connect(timeout, escalated)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
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

	clk.t = 1_000 + boundaryDur + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after interrupt: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key); v[escalated] != 1 || v[done] != 0 {
		t.Errorf("visits escalated=%d done=%d, want 1 and 0", v[escalated], v[done])
	}
}

// TestBoundaryTimerRecovers proves an armed boundary timer survives a crash:
// replaying into a fresh store restores the boundary's timer, and it still
// interrupts the host afterward.
func TestBoundaryTimerRecovers(t *testing.T) {
	dir := t.TempDir()
	const dur = int64(10e9)
	cp, jobType, _, escEnd := boundaryTimerProcess(t, dur, true)
	clk := &fixedClock{t: 5_000}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clk)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	h1.close(t)

	// Reopen and replay into a fresh store.
	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clk)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	// The armed boundary survived recovery: host + boundary still live.
	if pi, ei := counts(t, h2.store); pi != 1 || ei != 2 {
		t.Fatalf("after recovery: process=%d element=%d, want 1 and 2", pi, ei)
	}

	// Fire the recovered boundary timer; it interrupts the host as before.
	clk.t = 5_000 + dur + 1
	if err := p2.TickTimers(); err != nil {
		t.Fatalf("TickTimers after recovery: %v", err)
	}
	if pi := activeProcs(t, h2.store); pi != 0 {
		t.Fatalf("after recovered interrupt: procs=%d, want 0", pi)
	}
	if !jobGone(t, h2.store, jobType) {
		t.Error("host job survived a recovered interrupting boundary")
	}
	if v := elementVisits(t, h2.store, cp.Key); v[escEnd] != 1 {
		t.Errorf("escalation end visits = %d, want 1", v[escEnd])
	}
}

// TestBoundaryMessageNonInterruptingRepeats is the boundary-event half of the
// repeat-forever contract an event subprocess already keeps: a non-interrupting message
// boundary stays armed after it fires, so every later message routes its reminder branch
// again while the host keeps working — what BPMN says and what Camunda and Zeebe do
// (ADR-0040 with ADR-0054).
//
// It used to fire exactly once. OnCompleting completed the boundary element instance and
// let its subscription retire with it, so the second message correlated to nothing and
// vanished without a trace — no incident, no log line, the host simply never heard it. A
// recurring *timer* boundary already did the right thing (fireRecurringBoundary): fire the
// outgoing flow without completing the element instance. This is that, for a message.
//
// The test also crashes and recovers between the second and third message: the re-opened
// subscription is an event like any other, so it must rebuild from the log.
func TestBoundaryMessageNonInterruptingRepeats(t *testing.T) {
	dir := t.TempDir()

	b := compiler.NewBuilder(defKey, "reminders", 1)
	start := b.AddStartEvent()
	host := b.AddServiceTask("post", 3)
	ping := b.AddBoundaryMessageEvent(host, false, "ping", mustCompile(t, `"k"`))
	done := b.AddEndEvent()
	reminded := b.AddEndEvent()
	b.Connect(start, host)
	b.Connect(host, done)
	b.Connect(ping, reminded)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ServiceTask(cp.Node(host).Detail).JobType

	h := openHarness(t, dir)
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
		if v := elementVisits(t, h.store, cp.Key)[reminded]; v != int64(i) {
			t.Fatalf("after message %d: reminder end visits = %d, want %d (the boundary must stay armed)", i, v, i)
		}
		if jobGone(t, h.store, jobType) {
			t.Fatalf("non-interrupting boundary cancelled the host job (message %d)", i)
		}
		// Host plus the still-armed boundary: the boundary must not have completed.
		if pi, ei := counts(t, h.store); pi != 1 || ei != 2 {
			t.Fatalf("after message %d: process=%d element=%d, want 1 and 2 (host + armed boundary)", i, pi, ei)
		}
	}
	h.close(t)

	// Restart: the re-opened subscription rebuilds from the log and fires once more.
	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, &manualClock{})
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover after restart: %v", err)
	}
	p2.PublishMessage("ping", "k")
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after publish 3: %v", err)
	}
	if v := elementVisits(t, h2.store, cp.Key)[reminded]; v != 3 {
		t.Errorf("after recovery: reminder end visits = %d, want 3 (re-armed subscription survives a restart)", v)
	}

	// Completing the host disarms the boundary and drains the instance; a later message
	// then correlates to nothing.
	p2.CompleteJob(activatableJobs(t, h2.store, jobType)[0])
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after complete: %v", err)
	}
	if pi, ei := counts(t, h2.store); pi != 0 || ei != 0 {
		t.Fatalf("after host completes: process=%d element=%d, want 0 and 0 (boundary disarmed)", pi, ei)
	}
	p2.PublishMessage("ping", "k")
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after the late publish: %v", err)
	}
	v := elementVisits(t, h2.store, cp.Key)
	if v[reminded] != 3 {
		t.Errorf("after the late publish: reminder end visits = %d, want 3 (boundary was disarmed)", v[reminded])
	}
	if v[done] != 1 {
		t.Errorf("normal end visits = %d, want 1", v[done])
	}
}

// TestBoundarySignalNonInterruptingRepeats is the signal twin of the message case: a
// non-interrupting signal boundary stays armed across broadcasts (ADR-0088 with ADR-0040).
func TestBoundarySignalNonInterruptingRepeats(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(defKey, "signalled", 1)
	start := b.AddStartEvent()
	host := b.AddServiceTask("post", 3)
	sig := b.AddBoundarySignalEvent(host, false, "alarm")
	done := b.AddEndEvent()
	noticed := b.AddEndEvent()
	b.Connect(start, host)
	b.Connect(host, done)
	b.Connect(sig, noticed)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ServiceTask(cp.Node(host).Detail).JobType

	// A signal is broadcast by a throwing instance, not published from outside, so the
	// alarm gets its own one-shot definition to run.
	tb := compiler.NewBuilder(defKey+1, "alarm-thrower", 1)
	tStart := tb.AddStartEvent()
	tThrow := tb.AddSignalThrowEvent("alarm")
	tEnd := tb.AddEndEvent()
	tb.Connect(tStart, tThrow)
	tb.Connect(tThrow, tEnd)
	thrower, err := tb.Build()
	if err != nil {
		t.Fatalf("Build thrower: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	p.Deploy(thrower)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	for i := 1; i <= 3; i++ {
		p.CreateInstance(thrower.Key)
		if err := p.RunUntilIdle(); err != nil {
			t.Fatalf("RunUntilIdle after broadcast %d: %v", i, err)
		}
		if v := elementVisits(t, h.store, cp.Key)[noticed]; v != int64(i) {
			t.Fatalf("after broadcast %d: visits = %d, want %d (the boundary must stay armed)", i, v, i)
		}
	}
	if jobGone(t, h.store, jobType) {
		t.Fatal("non-interrupting signal boundary cancelled the host job")
	}
	p.CompleteJob(activatableJobs(t, h.store, jobType)[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after complete: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after host completes: process=%d element=%d, want 0 and 0", pi, ei)
	}
}
