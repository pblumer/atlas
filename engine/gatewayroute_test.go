package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// oneIncident returns the single unresolved incident, failing if there is not
// exactly one. A gateway that cannot route must raise one incident — not none
// (the token vanishes) and not one per attempt.
func oneIncident(t *testing.T, h *harness) (uint64, model.IncidentValue) {
	t.Helper()
	all := incidents(t, h.store)
	if len(all) != 1 {
		t.Fatalf("incidents = %d, want exactly 1: %v", len(all), all)
	}
	for k, v := range all {
		return k, v
	}
	return 0, model.IncidentValue{}
}

// TestExclusiveGatewayNoRouteParksAndResolvesOnce is the audit's F08 case, end to
// end: a token reaches an XOR gateway whose only condition is false and which has
// no default flow.
//
// The gateway must not complete. It parks — element instance still Activated,
// one incident naming it — so the token is where an operator can see it. Setting
// the variable the condition reads and resolving the incident re-runs the decision
// exactly once: the branch is taken, and the service task the token had *already*
// completed on the way in is not run a second time.
func TestExclusiveGatewayNoRouteParksAndResolvesOnce(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(1, "xor-route", 1)
	start := b.AddStartEvent()
	before := b.AddServiceTask("xor-route.before", 3)
	gw := b.AddExclusiveGateway()
	routed := b.AddServiceTask("xor-route.routed", 3)
	end := b.AddEndEvent()
	b.Connect(start, before)
	b.Connect(before, gw)
	f := b.Connect(gw, routed)
	b.SetFlowCondition(f, mustCompile(t, "go"))
	b.Connect(routed, end)
	// deliberately no default flow
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	beforeType := cp.ServiceTask(cp.Node(before).Detail).JobType
	routedType := cp.ServiceTask(cp.Node(routed).Detail).JobType

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	jobs := activatableJobs(t, h.store, beforeType)
	if len(jobs) != 1 {
		t.Fatalf("before jobs = %d, want 1", len(jobs))
	}
	p.CompleteJob(jobs[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	elKey, inc := oneIncident(t, h)
	if inc.ElementId != gw {
		t.Errorf("incident on element %d, want the gateway (%d)", inc.ElementId, gw)
	}
	if _, ok, err := h.store.GetElementInstance(elKey); err != nil || !ok {
		t.Fatalf("the gateway completed instead of parking: GetElementInstance(%d) ok=%v err=%v", elKey, ok, err)
	}
	if n := len(activatableJobs(t, h.store, routedType)); n != 0 {
		t.Fatalf("routed jobs = %d, want 0 while the gateway cannot route", n)
	}

	// The operator supplies what the decision was missing and resolves.
	instance := inc.ProcessInstanceKey
	p.SetVariables(instance, instance, "operator",
		model.VariableValue{ScopeKey: instance, Name: "go", Kind: model.VarBool, Bool: true})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	p.ResolveIncident(elKey, 0)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	if all := incidents(t, h.store); len(all) != 0 {
		t.Errorf("incidents after resolve = %d, want 0", len(all))
	}
	if n := len(activatableJobs(t, h.store, routedType)); n != 1 {
		t.Fatalf("routed jobs after resolve = %d, want exactly 1", n)
	}
	if n := len(activatableJobs(t, h.store, beforeType)); n != 0 {
		t.Fatalf("the completed task ran again after the resolve: %d jobs", n)
	}
}

// TestExclusiveGatewayResolveWithoutRouteRaisesAgain: resolving is a genuine retry
// of the decision, not a way to clear it. With the condition still false the
// gateway parks again on a fresh incident rather than reporting success or losing
// the token — the same discipline a re-armed timer follows (ADR-0064).
func TestExclusiveGatewayResolveWithoutRouteRaisesAgain(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp := deadEndGatewayProcess(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, model.VariableValue{Name: "amount", Kind: model.VarNumber, Text: "50"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	elKey, _ := oneIncident(t, h)
	p.ResolveIncident(elKey, 0)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	again, _ := oneIncident(t, h)
	if again != elKey {
		t.Fatalf("the retry parked a different element (%d, was %d); the token must stay where it is", again, elKey)
	}
}

// TestGatewayConditionErrorDoesNotTakeTheDefault separates an evaluation *failure*
// from a valid false. Atlas's standing rule is that a failed FEEL evaluation writes
// null and the token moves on (TestFeelEvaluationFailureWritesNull) — that is a
// rule about producing a *value*, where null is a defensible answer. A routing
// decision has no such answer: taking the default because the real condition could
// not be evaluated silently sends the token down a branch nobody chose. So a
// failing condition parks with an incident even though a default flow exists.
func TestGatewayConditionErrorDoesNotTakeTheDefault(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(1, "xor-error", 1)
	start := b.AddStartEvent()
	gw := b.AddExclusiveGateway()
	primary := b.AddServiceTask("xor-error.primary", 3)
	fallback := b.AddServiceTask("xor-error.fallback", 3)
	end := b.AddEndEvent()
	b.Connect(start, gw)
	f := b.Connect(gw, primary)
	b.SetFlowCondition(f, mustCompile(t, runawayFEEL)) // fails at evaluation time
	d := b.Connect(gw, fallback)
	b.SetFlowDefault(d)
	b.Connect(primary, end)
	b.Connect(fallback, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	fallbackType := cp.ServiceTask(cp.Node(fallback).Detail).JobType

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	if n := len(activatableJobs(t, h.store, fallbackType)); n != 0 {
		t.Fatalf("default jobs = %d, want 0: an unevaluable condition is not a false one", n)
	}
	_, inc := oneIncident(t, h)
	if inc.ElementId != gw {
		t.Errorf("incident on element %d, want the gateway (%d)", inc.ElementId, gw)
	}
}

// TestInclusiveSplitNoRouteParksWithIncident is the same contract on the OR split:
// no condition true and no default is a modeling error that parks a visible token,
// not one that disappears.
func TestInclusiveSplitNoRouteParksWithIncident(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(1, "or-route", 1)
	start := b.AddStartEvent()
	gw := b.AddInclusiveGateway()
	one := b.AddServiceTask("or-route.one", 3)
	two := b.AddServiceTask("or-route.two", 3)
	end := b.AddEndEvent()
	b.Connect(start, gw)
	f1 := b.Connect(gw, one)
	f2 := b.Connect(gw, two)
	b.SetFlowCondition(f1, mustCompile(t, "false"))
	b.SetFlowCondition(f2, mustCompile(t, "false"))
	b.Connect(one, end)
	b.Connect(two, end)
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
	_, inc := oneIncident(t, h)
	if inc.ElementId != gw {
		t.Errorf("incident on element %d, want the gateway (%d)", inc.ElementId, gw)
	}
	if _, ei := counts(t, h.store); ei != 1 {
		t.Fatalf("element instances = %d, want 1 (the parked gateway)", ei)
	}
}
