package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// stdLoopScriptProcess builds start → setup(n = 0) → work → end, where work is a
// script task marked with a BPMN standard loop (ADR-0133): it recomputes n from
// script on every iteration and repeats while cond holds. The work node's element
// index is returned so a test can count its activations.
func stdLoopScriptProcess(t *testing.T, script, cond string, testBefore bool, max int32) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(1, "stdloop", 1)
	start := b.AddStartEvent()
	setup := b.AddScriptTask(mustCompile(t, "0"), "n")
	work := b.AddScriptTask(mustCompile(t, script), "n")
	b.SetStandardLoop(work, testBefore, max, mustCompile(t, cond))
	end := b.AddEndEvent()
	b.Connect(start, setup)
	b.Connect(setup, work)
	b.Connect(work, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, work
}

// runStdLoop starts one instance of cp, runs to idle, and returns the harness so a
// test can inspect what the loop left behind.
func runStdLoop(t *testing.T, cp *compiler.CompiledProcess) *harness {
	t.Helper()
	h := openHarness(t, t.TempDir())
	t.Cleanup(func() { h.close(t) })
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
		t.Fatalf("after run: process=%d element=%d, want 0 and 0 (the loop ended and the instance finished)", pi, ei)
	}
	return h
}

// iterations reports how many times the loop activity actually ran: its element-visit
// count minus the one visit of the loop body itself, which wraps the iterations.
func iterations(t *testing.T, h *harness, cp *compiler.CompiledProcess, work int32) int {
	t.Helper()
	return int(elementVisits(t, h.store, cp.Key)[work]) - 1
}

// instanceKey returns the single completed process instance's key.
func instanceKey(t *testing.T, h *harness) uint64 {
	t.Helper()
	done := completedInstances(t, h.store)
	if len(done) != 1 {
		t.Fatalf("completed instances = %d, want 1", len(done))
	}
	for k := range done {
		return k
	}
	return 0
}

// TestStandardLoopRepeatsWhileConditionHolds runs a standard loop with the BPMN
// default (testBefore absent): the activity runs, then the condition decides whether
// to run it again. n counts up 1, 2, 3 and the loop stops when n < 3 no longer holds
// — three iterations, and the value the last one produced escapes the loop into the
// enclosing scope, so the rest of the process sees it (ADR-0133).
func TestStandardLoopRepeatsWhileConditionHolds(t *testing.T) {
	cp, work := stdLoopScriptProcess(t, "n + 1", "n < 3", false, 0)
	h := runStdLoop(t, cp)

	if got := iterations(t, h, cp, work); got != 3 {
		t.Errorf("iterations = %d, want 3 (repeat while n < 3)", got)
	}
	// The promoted result is the last value written at the instance scope.
	snaps := variableSnapshots(t, h.store, instanceKey(t, h))
	if len(snaps) == 0 || snaps[len(snaps)-1] != "n=3" {
		t.Errorf("instance-scope variable history = %v, want it to end at n=3 (the loop's result escapes)", snaps)
	}
}

// TestStandardLoopTestBeforeSkipsWhenConditionFails checks the while-loop form:
// with testBefore the condition is checked before the first iteration, so a loop
// whose condition is already false runs zero times and the activity just completes,
// taking its outgoing flow (ADR-0133).
func TestStandardLoopTestBeforeSkipsWhenConditionFails(t *testing.T) {
	cp, work := stdLoopScriptProcess(t, "n + 1", "n < 0", true, 0)
	h := runStdLoop(t, cp)

	if got := iterations(t, h, cp, work); got != 0 {
		t.Errorf("iterations = %d, want 0 (testBefore with a false condition skips the activity)", got)
	}
}

// TestStandardLoopRepeatUntilRunsAtLeastOnce is the counterpart: without testBefore
// the condition is only checked after an iteration, so a loop whose condition never
// holds still runs its activity exactly once (BPMN's repeat-until form).
func TestStandardLoopRepeatUntilRunsAtLeastOnce(t *testing.T) {
	cp, work := stdLoopScriptProcess(t, "n + 1", "n < 0", false, 0)
	h := runStdLoop(t, cp)

	if got := iterations(t, h, cp, work); got != 1 {
		t.Errorf("iterations = %d, want 1 (repeat-until always runs once)", got)
	}
}

// TestStandardLoopMaximumCaps checks that loopMaximum bounds a condition that would
// otherwise never stop: the loop runs exactly loopMaximum times and then completes
// like any activity, rather than spinning forever (ADR-0133).
func TestStandardLoopMaximumCaps(t *testing.T) {
	cp, work := stdLoopScriptProcess(t, "n + 1", "true", false, 3)
	h := runStdLoop(t, cp)

	if got := iterations(t, h, cp, work); got != 3 {
		t.Errorf("iterations = %d, want 3 (capped by loopMaximum)", got)
	}
}

// TestStandardLoopMaximumOnly checks the condition-less form: a loop bounded only by
// loopMaximum runs exactly that many iterations.
func TestStandardLoopMaximumOnly(t *testing.T) {
	b := compiler.NewBuilder(1, "stdloop-max", 1)
	start := b.AddStartEvent()
	work := b.AddScriptTask(mustCompile(t, `"ran"`), "last")
	b.SetStandardLoop(work, false, 2, nil)
	end := b.AddEndEvent()
	b.Connect(start, work)
	b.Connect(work, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	h := runStdLoop(t, cp)
	if got := iterations(t, h, cp, work); got != 2 {
		t.Errorf("iterations = %d, want 2 (loopMaximum alone bounds the loop)", got)
	}
}

// stdLoopServiceProcess builds start → work → end with work a service task marked
// with a standard loop: each iteration parks on its own job, and the worker's output
// decides whether the loop runs again.
func stdLoopServiceProcess(t *testing.T, cond string) (*compiler.CompiledProcess, int32, int32) {
	t.Helper()
	b := compiler.NewBuilder(1, "stdloop-svc", 1)
	start := b.AddStartEvent()
	work := b.AddServiceTask("loopwork", 3)
	b.SetStandardLoop(work, false, 0, mustCompile(t, cond))
	end := b.AddEndEvent()
	b.Connect(start, work)
	b.Connect(work, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, work, cp.ServiceTask(cp.Node(work).Detail).JobType
}

// TestStandardLoopRunsOneIterationAtATime drives a service-task standard loop: exactly
// one job is live at a time, each iteration carries its 1-based loopCounter, and the
// worker's output feeds the condition that decides on the next round (ADR-0133).
func TestStandardLoopRunsOneIterationAtATime(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, _, jobType := stdLoopServiceProcess(t, "not(approved)")

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// Two rounds: the first worker rejects (the loop repeats), the second approves.
	for _, approved := range []bool{false, true} {
		jobs := activatableJobs(t, h.store, jobType)
		if len(jobs) != 1 {
			t.Fatalf("active jobs = %d, want 1 (a standard loop runs one iteration at a time)", len(jobs))
		}
		p.CompleteJob(jobs[0], model.VariableValue{Name: "approved", Kind: model.VarBool, Bool: approved})
		if err := p.RunUntilIdle(); err != nil {
			t.Fatalf("RunUntilIdle: %v", err)
		}
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after approval: process=%d element=%d, want 0 and 0 (the loop ended)", pi, ei)
	}
	if left := activatableJobs(t, h.store, jobType); len(left) != 0 {
		t.Errorf("leftover jobs = %d, want 0", len(left))
	}
}

// TestStandardLoopRecovers parks a standard loop mid-iteration, crashes, and recovers:
// the body, the live iteration, and the scope counter rebuild from the log, so
// completing the recovered job still decides the next round and finishes the loop
// (ADR-0018 recovery contract, ADR-0133).
func TestStandardLoopRecovers(t *testing.T) {
	dir := t.TempDir()
	cp, _, jobType := stdLoopServiceProcess(t, "not(approved)")
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
	// First round: the worker rejects, so the loop seeds a second iteration; crash there.
	jobs := activatableJobs(t, h1.store, jobType)
	if len(jobs) != 1 {
		t.Fatalf("before crash: jobs = %d, want 1", len(jobs))
	}
	p1.CompleteJob(jobs[0], model.VariableValue{Name: "approved", Kind: model.VarBool, Bool: false})
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	h1.close(t)

	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clk)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	jobs = activatableJobs(t, h2.store, jobType)
	if len(jobs) != 1 {
		t.Fatalf("after recovery: jobs = %d, want 1 (the live iteration rebuilt from the log)", len(jobs))
	}
	if pi, ei := counts(t, h2.store); pi != 1 || ei != 2 {
		t.Fatalf("after recovery: process=%d element=%d, want 1 and 2 (body + live iteration)", pi, ei)
	}
	job, ok, err := h2.store.GetJob(jobs[0])
	if err != nil || !ok {
		t.Fatalf("GetJob: ok=%v err=%v", ok, err)
	}
	if lc := readVar(t, h2.store, job.ElementInstanceKey, "loopCounter"); lc == nil || atoi(t, lc.Text) != 2 {
		t.Fatalf("recovered loopCounter = %v, want 2 (the second iteration)", lc)
	}
	p2.CompleteJob(jobs[0], model.VariableValue{Name: "approved", Kind: model.VarBool, Bool: true})
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 2: %v", err)
	}
	if pi, ei := counts(t, h2.store); pi != 0 || ei != 0 {
		t.Fatalf("after completion: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestStandardLoopCounterVisibleToCondition checks that the condition is evaluated
// over the finished iteration's own scope chain, so it can read the 1-based
// loopCounter — the usual way to write "run this at most three times".
func TestStandardLoopCounterVisibleToCondition(t *testing.T) {
	cp, work := stdLoopScriptProcess(t, "n + 1", "loopCounter < 3", false, 0)
	h := runStdLoop(t, cp)

	if got := iterations(t, h, cp, work); got != 3 {
		t.Errorf("iterations = %d, want 3 (loopCounter 1, 2, 3 — the third stops the loop)", got)
	}
}

// TestStandardLoopInterruptingBoundary tears the loop down when an interrupting
// boundary timer on the looping activity fires: the body is a scope, so terminateScope
// reaches the live iteration and the boundary's flow is taken instead of the loop's
// (ADR-0133 — inherited from ADR-0077, asserted here because a loop that cannot be
// interrupted would run until the instance is cancelled).
func TestStandardLoopInterruptingBoundary(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const dur = int64(30e9)
	b := compiler.NewBuilder(1, "stdloop-boundary", 1)
	start := b.AddStartEvent()
	work := b.AddServiceTask("loopwork", 3)
	b.SetStandardLoop(work, false, 0, mustCompile(t, "not(approved)"))
	timeout := b.AddBoundaryTimerEvent(work, true, dur)
	done := b.AddEndEvent()
	esc := b.AddEndEvent()
	b.Connect(start, work)
	b.Connect(work, done)
	b.Connect(timeout, esc)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ServiceTask(cp.Node(work).Detail).JobType
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
	// One round rejects, so a second iteration is live when the timer fires.
	jobs := activatableJobs(t, h.store, jobType)
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	p.CompleteJob(jobs[0], model.VariableValue{Name: "approved", Kind: model.VarBool, Bool: false})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	clk.t = 1_000 + dur + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after interrupt: process=%d element=%d, want 0 and 0 (the loop was torn down)", pi, ei)
	}
	if left := activatableJobs(t, h.store, jobType); len(left) != 0 {
		t.Errorf("iteration jobs = %d, want 0 (cancelled by the interrupting boundary)", len(left))
	}
	if v := elementVisits(t, h.store, cp.Key); v[esc] != 1 || v[done] != 0 {
		t.Errorf("end visits = {esc:%d done:%d}, want {1 0} (boundary flow taken)", v[esc], v[done])
	}
}
