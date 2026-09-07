package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// selfLoopingGateway builds Start → XOR gateway, with the gateway's first outgoing
// flow looping straight back to itself under a condition that always holds and a
// default flow to an end event that is therefore never taken. Every element in it is
// automatic, so nothing ever waits: this is the shape the audit used to show that
// RunUntilIdle means what it says.
func selfLoopingGateway(t *testing.T) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(1, "automatic-cycle", 1)
	start := b.AddStartEvent()
	gw := b.AddExclusiveGateway()
	end := b.AddEndEvent()
	b.Connect(start, gw)
	loop := b.Connect(gw, gw)
	b.SetFlowCondition(loop, mustCompile(t, "true"))
	b.SetFlowDefault(b.Connect(gw, end))
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// overBudget returns the single incident the execution budget raised, failing if
// there is not exactly one or if it was raised for some other reason.
func overBudget(t *testing.T, h *harness) (uint64, model.IncidentValue) {
	t.Helper()
	all := incidents(t, h.store)
	if len(all) != 1 {
		t.Fatalf("incidents = %d, want exactly 1: %v", len(all), all)
	}
	for k, v := range all {
		if v.Reason != model.IncidentOverBudget {
			t.Fatalf("incident reason = %d, want IncidentOverBudget (%d): %q", v.Reason, model.IncidentOverBudget, v.Message)
		}
		return k, v
	}
	return 0, model.IncidentValue{}
}

// TestAutomaticCycleStopsAtItsExecutionBudget is the audit's F12 case. A cycle of
// purely automatic elements produces a followup for every command it processes, so
// the queue never drains and RunUntilIdle never returns — with the partition's only
// writer inside it. The budget makes it return: the token is stopped where it stands,
// with an incident saying so.
func TestAutomaticCycleStopsAtItsExecutionBudget(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp := selfLoopingGateway(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.SetExecutionBudget(40)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)

	// This is the assertion: it returns at all.
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	_, inc := overBudget(t, h)
	if inc.ProcessInstanceKey == 0 {
		t.Error("the incident names no instance")
	}
	// The token is parked on the gateway, not lost and not still queued.
	if _, ei := counts(t, h.store); ei != 1 {
		t.Fatalf("element instances = %d, want 1 (the stopped token)", ei)
	}
}

// TestAStoppedCycleDoesNotStopEveryoneElse is the fairness half: with the runaway
// stopped, the work queued behind it runs in the same call. Before the budget it
// could not — the cycle held the writer, so nothing else in the partition advanced.
func TestAStoppedCycleDoesNotStopEveryoneElse(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cycle := selfLoopingGateway(t)
	b := compiler.NewBuilder(2, "bystander", 1)
	s, task, e := b.AddStartEvent(), b.AddScriptTask(mustCompile(t, `"done"`), "path"), b.AddEndEvent()
	b.Connect(s, task)
	b.Connect(task, e)
	bystander, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.SetExecutionBudget(40)
	p.Deploy(cycle)
	p.Deploy(bystander)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cycle.Key)
	p.CreateInstance(bystander.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	overBudget(t, h) // the cycle was stopped
	done := completedInstances(t, h.store)
	if len(done) != 1 {
		t.Fatalf("completed instances = %d, want 1 (the bystander finished)", len(done))
	}
	for _, pi := range done {
		if pi.ProcessDefKey != bystander.Key {
			t.Fatalf("the completed instance is not the bystander: defKey %d", pi.ProcessDefKey)
		}
	}
}

// TestACycleThroughAForkCannotResetItsBudget guards the counting rule. A fork, a
// join and a subprocess exit all mint a fresh token id for what is still one thread
// of control, so a budget counted per token would be reset every lap by a cycle that
// passes through any of them. A minted token inherits its parent's count, which is
// what closes that.
func TestACycleThroughAForkCannotResetItsBudget(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(1, "fork-cycle", 1)
	start := b.AddStartEvent()
	merge := b.AddExclusiveGateway() // two incoming: the start and the back edge
	fork := b.AddParallelGateway()
	one := b.AddScriptTask(mustCompile(t, `1`), "one")
	two := b.AddScriptTask(mustCompile(t, `2`), "two")
	join := b.AddParallelGateway()
	b.Connect(start, merge)
	b.Connect(merge, fork)
	b.Connect(fork, one)
	b.Connect(fork, two)
	b.Connect(one, join)
	b.Connect(two, join)
	b.Connect(join, merge) // back edge
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.SetExecutionBudget(40)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if all := incidents(t, h.store); len(all) == 0 {
		t.Fatal("the cycle ran to idle: a token that passes through a fork must carry its budget with it")
	}
}

// TestManyIterationsAreNotOneRunawayToken is the false-positive guard, and the reason
// the budget is counted per token rather than per instance. A multi-instance activity
// over more items than the budget is ordinary heavy work: each iteration is its own
// token taking one step, not one token going round and round.
func TestManyIterationsAreNotOneRunawayToken(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(1, "many-items", 1)
	start := b.AddStartEvent()
	task := b.AddScriptTask(mustCompile(t, `1`), "item")
	b.SetMultiInstance(task, false, "", "", nil, mustCompile(t, "40"), nil, nil)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.SetExecutionBudget(10) // far below the iteration count
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if all := incidents(t, h.store); len(all) != 0 {
		t.Fatalf("incidents = %d, want 0: forty iterations are forty tokens, not one runaway: %v", len(all), all)
	}
	if len(completedInstances(t, h.store)) != 1 {
		t.Fatal("the instance did not finish")
	}
}

// TestResolvingAnOverBudgetIncidentRunsTheElement: the element the budget stopped
// never ran, so resolving runs it — whatever its type. Here it is a service task, so
// the proof is the job that was never created appearing. A resolve grants a fresh
// budget, exactly as resolving a runaway loop grants another ceiling's worth of runs.
func TestResolvingAnOverBudgetIncidentRunsTheElement(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(1, "budget-then-task", 1)
	start := b.AddStartEvent()
	merge := b.AddExclusiveGateway()
	work := b.AddServiceTask("budget.work", 3)
	end := b.AddEndEvent()
	b.Connect(start, merge)
	loop := b.Connect(merge, merge)
	b.SetFlowCondition(loop, mustCompile(t, "keepGoing"))
	b.SetFlowDefault(b.Connect(merge, work))
	b.Connect(work, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	workType := cp.ServiceTask(cp.Node(work).Detail).JobType

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.SetExecutionBudget(20)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, model.VariableValue{Name: "keepGoing", Kind: model.VarBool, Bool: true})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	elKey, inc := overBudget(t, h)

	// Take the loop out and let the token continue: the default flow now runs the
	// service task, and the resolve is what starts the element that never started.
	p.SetVariables(inc.ProcessInstanceKey, inc.ProcessInstanceKey, "operator",
		model.VariableValue{Name: "keepGoing", Kind: model.VarBool, Bool: false})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	p.ResolveIncident(elKey, 0)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if all := incidents(t, h.store); len(all) != 0 {
		t.Fatalf("incidents after resolve = %d, want 0: %v", len(all), all)
	}
	if n := len(activatableJobs(t, h.store, workType)); n != 1 {
		t.Fatalf("jobs after resolve = %d, want 1 (the element the budget stopped ran)", n)
	}
}
