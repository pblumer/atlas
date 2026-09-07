package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
)

// miJoinProcess builds a multi-instance embedded subprocess run `iterations`
// times, each iteration forking at `gwKind` into two service tasks (A and B) and
// re-joining at a gateway of the same kind before a third task ("after"). Every
// iteration is an independent execution scope, so the two joins must synchronize
// their own iteration's tokens and nobody else's.
//
//	start → [ sub ×N: istart → split ⇉ A,B ⇉ join → after → iend ] → end
func miJoinProcess(t *testing.T, name string, iterations string, inclusive bool) (
	cp *compiler.CompiledProcess, aType, bType, afterType int32,
) {
	t.Helper()
	b := compiler.NewBuilder(1, name, 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess()
	b.SetMultiInstance(sub, false, "", "", nil, mustCompile(t, iterations), nil, nil)
	b.PushScope(sub)
	istart := b.AddStartEvent()
	var split, join int32
	if inclusive {
		split, join = b.AddInclusiveGateway(), b.AddInclusiveGateway()
	} else {
		split, join = b.AddParallelGateway(), b.AddParallelGateway()
	}
	a := b.AddServiceTask(name+".a", 3)
	bb := b.AddServiceTask(name+".b", 3)
	after := b.AddServiceTask(name+".after", 3)
	iend := b.AddEndEvent()
	b.Connect(istart, split)
	fa := b.Connect(split, a)
	fb := b.Connect(split, bb)
	if inclusive {
		b.SetFlowCondition(fa, mustCompile(t, "true"))
		b.SetFlowCondition(fb, mustCompile(t, "true"))
	}
	b.Connect(a, join)
	b.Connect(bb, join)
	b.Connect(join, after)
	b.Connect(after, iend)
	b.PopScope()
	end := b.AddEndEvent()
	b.Connect(start, sub)
	b.Connect(sub, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp,
		cp.ServiceTask(cp.Node(a).Detail).JobType,
		cp.ServiceTask(cp.Node(bb).Detail).JobType,
		cp.ServiceTask(cp.Node(after).Detail).JobType
}

// TestParallelJoinSeparatesMultiInstanceScopes is the audit's F06 reproduction.
// Two iterations of a multi-instance subprocess each fork into A and B and rejoin
// at an AND gateway. Completing only the two A jobs must leave both joins waiting:
// each iteration still owes its own B. Counting arrivals by (process instance,
// node) alone, the join saw two tokens on "its" node — one from each iteration —
// and fired, consuming a token belonging to the other scope.
func TestParallelJoinSeparatesMultiInstanceScopes(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp, aType, bType, afterType := miJoinProcess(t, "and-scope", "2", false)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	as := activatableJobs(t, h.store, aType)
	if len(as) != 2 {
		t.Fatalf("A jobs = %d, want 2 (one per iteration)", len(as))
	}
	for _, k := range as {
		p.CompleteJob(k)
	}
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if n := len(activatableJobs(t, h.store, afterType)); n != 0 {
		t.Fatalf("downstream jobs = %d, want 0: the join fired while neither iteration's B branch had arrived", n)
	}

	// Both B branches arriving releases both joins — once each, not once in total.
	bs := activatableJobs(t, h.store, bType)
	if len(bs) != 2 {
		t.Fatalf("B jobs = %d, want 2 (one per iteration)", len(bs))
	}
	for _, k := range bs {
		p.CompleteJob(k)
	}
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if n := len(activatableJobs(t, h.store, afterType)); n != 2 {
		t.Fatalf("downstream jobs = %d, want 2 (each iteration continues past its own join)", n)
	}
}

// TestInclusiveJoinSeparatesMultiInstanceScopes is the same scope confusion on an
// OR join. Its wait condition ("could a token still arrive?") is scope-blind too,
// but the damage shows on the other side: when the last branch of *some* iteration
// arrives, the join consumes every token parked on its node across all iterations
// and fires once, so the other iteration's token is swallowed and that iteration
// never reaches its downstream task.
func TestInclusiveJoinSeparatesMultiInstanceScopes(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp, aType, bType, afterType := miJoinProcess(t, "or-scope", "2", true)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	for _, k := range activatableJobs(t, h.store, aType) {
		p.CompleteJob(k)
	}
	for _, k := range activatableJobs(t, h.store, bType) {
		p.CompleteJob(k)
	}
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if n := len(activatableJobs(t, h.store, afterType)); n != 2 {
		t.Fatalf("downstream jobs = %d, want 2 (one per iteration)", n)
	}
}

// TestInclusiveJoinWaitsForANestedSubprocess guards the fix from over-correcting.
// Scoping the join's wait to its own FlowScopeKey must not blind it to a token
// running *inside* a subprocess on one of its branches: that subprocess's own
// element instance sits in the join's scope on a node that reaches the join, which
// is what keeps the join waiting. Branch two arrives first and must park until the
// task inside branch one's subprocess completes.
func TestInclusiveJoinWaitsForANestedSubprocess(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(1, "or-nested", 1)
	start := b.AddStartEvent()
	split := b.AddInclusiveGateway()
	sub := b.AddSubProcess()
	b.PushScope(sub)
	sstart := b.AddStartEvent()
	inner := b.AddServiceTask("or-nested.inner", 3)
	send := b.AddEndEvent()
	b.Connect(sstart, inner)
	b.Connect(inner, send)
	b.PopScope()
	fast := b.AddScriptTask(mustCompile(t, "1"), "fast")
	join := b.AddInclusiveGateway()
	after := b.AddServiceTask("or-nested.after", 3)
	end := b.AddEndEvent()
	b.Connect(start, split)
	f1 := b.Connect(split, sub)
	f2 := b.Connect(split, fast)
	b.SetFlowCondition(f1, mustCompile(t, "true"))
	b.SetFlowCondition(f2, mustCompile(t, "true"))
	b.Connect(sub, join)
	b.Connect(fast, join)
	b.Connect(join, after)
	b.Connect(after, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	innerType := cp.ServiceTask(cp.Node(inner).Detail).JobType
	afterType := cp.ServiceTask(cp.Node(after).Detail).JobType

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if n := len(activatableJobs(t, h.store, afterType)); n != 0 {
		t.Fatalf("downstream jobs = %d, want 0: the join fired while a token was still inside the subprocess", n)
	}
	jobs := activatableJobs(t, h.store, innerType)
	if len(jobs) != 1 {
		t.Fatalf("inner jobs = %d, want 1", len(jobs))
	}
	p.CompleteJob(jobs[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if n := len(activatableJobs(t, h.store, afterType)); n != 1 {
		t.Fatalf("downstream jobs = %d, want 1 once both branches arrived", n)
	}
}
