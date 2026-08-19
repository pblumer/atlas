package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
)

// TestStandardLoopOverPassThroughTask repeats an undefined/manual task — an activity
// with no implementation, run as a pass-through. It has no work of its own, so nothing
// parks: the loop runs its cap on the run loop and the body then takes its outgoing
// flow. This is the marker meaning what it draws even on a task nobody has implemented
// yet (ADR-0133, amended).
func TestStandardLoopOverPassThroughTask(t *testing.T) {
	b := compiler.NewBuilder(1, "loop-plain", 1)
	start := b.AddStartEvent()
	work := b.AddTask()
	b.SetStandardLoop(work, false, 3, nil)
	end := b.AddEndEvent()
	b.Connect(start, work)
	b.Connect(work, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	h := runStdLoop(t, cp)

	if got := iterations(t, h, cp, work); got != 3 {
		t.Errorf("iterations = %d, want 3 (the pass-through task ran once per round)", got)
	}
	if v := elementVisits(t, h.store, cp.Key)[end]; v != 1 {
		t.Errorf("end visits = %d, want 1 (the body takes the outgoing flow once, not once per run)", v)
	}
}

// TestMultiInstanceOverPassThroughTask is the other marker on the same activity: one
// pass-through run per collection element, joining into a single outgoing token.
func TestMultiInstanceOverPassThroughTask(t *testing.T) {
	b := compiler.NewBuilder(1, "mi-plain", 1)
	start := b.AddStartEvent()
	setup := b.AddScriptTask(mustCompile(t, "[1, 2, 3, 4]"), "items")
	work := b.AddTask()
	b.SetMultiInstance(work, false /*parallel*/, "item", "", mustCompile(t, "items"), nil, nil, nil)
	end := b.AddEndEvent()
	b.Connect(start, setup)
	b.Connect(setup, work)
	b.Connect(work, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	h := runStdLoop(t, cp)

	if got := iterations(t, h, cp, work); got != 4 {
		t.Errorf("iterations = %d, want 4 (one per collection element)", got)
	}
	if v := elementVisits(t, h.store, cp.Key)[end]; v != 1 {
		t.Errorf("end visits = %d, want 1 (the iterations join at the body)", v)
	}
}

// brtLoopProcess builds Start → business rule task → End with the task marked by a
// standard loop that repeats while `again` holds, and returns the DMN job type so a
// test can complete each round's decision.
func brtLoopProcess(t *testing.T) (*compiler.CompiledProcess, int32, int32) {
	t.Helper()
	b := compiler.NewBuilder(1, "loop-decide", 1)
	start := b.AddStartEvent()
	task, err := b.AddBusinessRuleTask("Dish", map[string]any{"Season": "Winter"}, 3)
	if err != nil {
		t.Fatalf("AddBusinessRuleTask: %v", err)
	}
	b.SetStandardLoop(task, false, 0, mustCompile(t, "again"))
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, task, cp.BusinessRuleTask(cp.Node(task).Detail).JobType
}

// TestStandardLoopOverBusinessRuleTask repeats a DMN decision until its own result says
// stop: each round parks on its own DMN job, one at a time, and the decision the worker
// writes back feeds the condition that decides on the next round (ADR-0133, amended).
func TestStandardLoopOverBusinessRuleTask(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, task, jobType := brtLoopProcess(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// Two rounds: the first decision asks for another, the second ends the loop.
	for _, again := range []bool{true, false} {
		jobs := activatableJobs(t, h.store, jobType)
		if len(jobs) != 1 {
			t.Fatalf("DMN jobs = %d, want 1 (a standard loop runs one round at a time)", len(jobs))
		}
		p.CompleteJob(jobs[0], boolVar("again", again))
		if err := p.RunUntilIdle(); err != nil {
			t.Fatalf("RunUntilIdle: %v", err)
		}
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after the last round: process=%d element=%d, want 0 and 0 (the loop ended)", pi, ei)
	}
	if got := int(elementVisits(t, h.store, cp.Key)[task]) - 1; got != 2 {
		t.Errorf("decision runs = %d, want 2", got)
	}
	if left := activatableJobs(t, h.store, jobType); len(left) != 0 {
		t.Errorf("leftover DMN jobs = %d, want 0", len(left))
	}
}
