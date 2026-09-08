package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// lopsidedJoin builds a process that puts *two* tokens on one of a join's incoming
// flows and, on the other, a token that waits for a worker:
//
//	start → fork ⇉ a, b ─→ merge ─→ join ─→ after
//	             ⇉ slow ───────────↗
//
// a and b both pass through an exclusive merge, so both continue down the *same*
// sequence flow into the join. That is the shape a count of arrivals cannot tell
// apart from one token on each flow.
func lopsidedJoin(t *testing.T, key uint64) (cp *compiler.CompiledProcess, slowType, afterType int32) {
	t.Helper()
	cp, slowType, afterType, _ = lopsidedJoinNodes(t, key)
	return cp, slowType, afterType
}

// lopsidedJoinNodes is lopsidedJoin plus the join's node id, for the assertions
// that have to say *where* a token is rather than how many there are.
func lopsidedJoinNodes(t *testing.T, key uint64) (cp *compiler.CompiledProcess, slowType, afterType, joinNode int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "lopsided", 1)
	start := b.AddStartEvent()
	fork := b.AddParallelGateway()
	a := b.AddScriptTask(mustCompile(t, `1`), "a")
	bb := b.AddScriptTask(mustCompile(t, `2`), "b")
	merge := b.AddExclusiveGateway()
	slow := b.AddServiceTask("lopsided.slow", 3)
	join := b.AddParallelGateway()
	after := b.AddServiceTask("lopsided.after", 3)
	end := b.AddEndEvent()
	b.Connect(start, fork)
	b.Connect(fork, a)
	b.Connect(fork, bb)
	b.Connect(fork, slow)
	b.Connect(a, merge)
	b.Connect(bb, merge)
	b.Connect(merge, join) // one flow, two tokens
	b.Connect(slow, join)  // the other flow, one token, held by a worker
	b.Connect(join, after)
	b.Connect(after, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp,
		cp.ServiceTask(cp.Node(slow).Detail).JobType,
		cp.ServiceTask(cp.Node(after).Detail).JobType,
		join
}

// TestTwoTokensOnOneFlowDoNotSatisfyTheOther is F06's second half. OMG BPMN 2.0.2
// §13.4 activates a parallel join when there is at least one token on *each*
// incoming sequence flow. ADR-0024 approximated that by counting the tokens parked
// on the join node, and recorded the approximation as a known limitation: "no
// token-count-per-flow, so a single incoming flow feeding two tokens (uncommon) is
// not distinguished".
//
// It is not uncommon — a fork whose branches rejoin through a merge produces it —
// and the consequence is that the join fires while one of its branches has not
// arrived at all, which is the whole of what a parallel join is for.
func TestTwoTokensOnOneFlowDoNotSatisfyTheOther(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp, slowType, afterType := lopsidedJoin(t, 1)
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
		t.Fatalf("downstream jobs = %d, want 0: two tokens on one flow are not one token on each", n)
	}

	// The third branch arrives, so now every incoming flow holds a token.
	jobs := activatableJobs(t, h.store, slowType)
	if len(jobs) != 1 {
		t.Fatalf("slow jobs = %d, want 1", len(jobs))
	}
	p.CompleteJob(jobs[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if n := len(activatableJobs(t, h.store, afterType)); n != 1 {
		t.Fatalf("downstream jobs = %d, want exactly 1 firing", n)
	}
}

// liveOnNode counts the live element instances of an instance that sit on one
// compiled node. The totals `counts` returns cannot answer this test: a wrong
// pairing and a right one leave the same *number* of tokens behind and put them in
// different places, so the place is what has to be asserted.
func liveOnNode(t *testing.T, h *harness, piKey uint64, elementId int32) int {
	t.Helper()
	n := 0
	if err := h.store.ElementInstancesOfProcess(piKey, func(k uint64) error {
		ei, ok, err := h.store.GetElementInstance(k)
		if err != nil || !ok {
			return err
		}
		if ei.ElementId == elementId {
			n++
		}
		return nil
	}); err != nil {
		t.Fatalf("ElementInstancesOfProcess: %v", err)
	}
	return n
}

// TestTheSurplusTokenIsKeptNotSwallowed: a firing consumes one token per incoming
// flow — not everything parked on the node. The token that could not be paired
// stays where it is, waiting for the next one to arrive on the flow it needs.
//
// Consuming the surplus is how a fork's second branch silently disappears: the
// instance carries on with one token where the model says two are in play. The
// count of live elements is the same either way, which is why this asserts *where*
// they are.
func TestTheSurplusTokenIsKeptNotSwallowed(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp, slowType, _, join := lopsidedJoinNodes(t, 2)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	instance := model.NewKey(1, 1)
	// Both tokens off the merge are waiting on the join, unpaired: nothing has come
	// down the other flow yet.
	if n := liveOnNode(t, h, instance, join); n != 2 {
		t.Fatalf("tokens parked on the join = %d, want 2 before the other branch arrives", n)
	}

	jobs := activatableJobs(t, h.store, slowType)
	if len(jobs) != 1 {
		t.Fatalf("slow jobs = %d, want 1", len(jobs))
	}
	p.CompleteJob(jobs[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// One set fired. The token that had no partner is still on the join, waiting for
	// one that will never come — a deadlocked join an operator can see and cancel,
	// which is what the model says. The alternative is losing it silently.
	if n := liveOnNode(t, h, instance, join); n != 1 {
		t.Fatalf("tokens parked on the join = %d, want the 1 surplus kept", n)
	}
}

// deferredDoubleJoin puts two tokens on each of a join's two incoming flows, but
// not at the same time: the A side runs straight through, the B side waits for a
// worker. That ordering is the point — four tokens arriving together tell a correct
// join and a counting one apart only by *when* the first one fires.
//
//	start → fork ⇉ a1, a2 ────→ mergeA ─→ join ─→ after
//	             ⇉ slow1, slow2 → mergeB ─↗
func deferredDoubleJoin(t *testing.T, key uint64) (cp *compiler.CompiledProcess, slowType, afterType, joinNode int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "double", 1)
	start := b.AddStartEvent()
	fork := b.AddParallelGateway()
	a1, a2 := b.AddScriptTask(mustCompile(t, `1`), "a1"), b.AddScriptTask(mustCompile(t, `2`), "a2")
	s1, s2 := b.AddServiceTask("double.slow", 3), b.AddServiceTask("double.slow", 3)
	mergeA, mergeB := b.AddExclusiveGateway(), b.AddExclusiveGateway()
	join := b.AddParallelGateway()
	after := b.AddServiceTask("double.after", 3)
	end := b.AddEndEvent()
	b.Connect(start, fork)
	for _, n := range []int32{a1, a2, s1, s2} {
		b.Connect(fork, n)
	}
	b.Connect(a1, mergeA)
	b.Connect(a2, mergeA)
	b.Connect(s1, mergeB)
	b.Connect(s2, mergeB)
	b.Connect(mergeA, join)
	b.Connect(mergeB, join)
	b.Connect(join, after)
	b.Connect(after, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp,
		cp.ServiceTask(cp.Node(s1).Detail).JobType,
		cp.ServiceTask(cp.Node(after).Detail).JobType,
		join
}

// TestAJoinFiresOncePerCompleteSetOfTokens is the repeat case: four tokens, two on
// each incoming flow, are two complete sets and therefore two firings — and neither
// firing may happen before its set is complete.
//
// The number of firings alone does not tell the two implementations apart; counting
// arrivals also produces two. What separates them is that a counting join pairs the
// two A tokens with each other and fires while the B side has not arrived at all.
// So this walks the arrivals: nothing after the A side, one firing per B token.
func TestAJoinFiresOncePerCompleteSetOfTokens(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp, slowType, afterType, join := deferredDoubleJoin(t, 3)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	instance := model.NewKey(1, 1)

	if n := len(activatableJobs(t, h.store, afterType)); n != 0 {
		t.Fatalf("downstream jobs = %d, want 0: two tokens on the A flow are not a set", n)
	}
	if n := liveOnNode(t, h, instance, join); n != 2 {
		t.Fatalf("tokens parked on the join = %d, want both A tokens waiting", n)
	}

	slow := activatableJobs(t, h.store, slowType)
	if len(slow) != 2 {
		t.Fatalf("slow jobs = %d, want 2", len(slow))
	}
	p.CompleteJob(slow[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if n := len(activatableJobs(t, h.store, afterType)); n != 1 {
		t.Fatalf("downstream jobs = %d after one B token, want exactly 1 firing", n)
	}
	if n := liveOnNode(t, h, instance, join); n != 1 {
		t.Fatalf("tokens parked on the join = %d, want the second A token still waiting", n)
	}

	p.CompleteJob(slow[1])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if n := len(activatableJobs(t, h.store, afterType)); n != 2 {
		t.Fatalf("downstream jobs = %d after both B tokens, want 2", n)
	}
	if n := liveOnNode(t, h, instance, join); n != 0 {
		t.Fatalf("tokens parked on the join = %d, want none — both sets were paired off", n)
	}
}

// TestTheOrdinaryJoinIsUnchanged guards the common case the rewrite must not
// disturb: one token per incoming flow, one firing, nothing left behind.
func TestTheOrdinaryJoinIsUnchanged(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(4, "ordinary", 1)
	start := b.AddStartEvent()
	fork := b.AddParallelGateway()
	one := b.AddScriptTask(mustCompile(t, `1`), "one")
	two := b.AddScriptTask(mustCompile(t, `2`), "two")
	join := b.AddParallelGateway()
	after := b.AddServiceTask("ordinary.after", 3)
	end := b.AddEndEvent()
	b.Connect(start, fork)
	b.Connect(fork, one)
	b.Connect(fork, two)
	b.Connect(one, join)
	b.Connect(two, join)
	b.Connect(join, after)
	b.Connect(after, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
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
	if n := len(activatableJobs(t, h.store, afterType)); n != 1 {
		t.Fatalf("downstream jobs = %d, want 1", n)
	}
	if _, ei := counts(t, h.store); ei != 1 {
		t.Fatalf("element instances = %d, want 1 (the task after the join)", ei)
	}
}

// TestAnInclusiveJoinResolvesItsSurplusAtOnce is the same rule on the OR join, and
// the one place it comes out differently. An inclusive join fires when nothing more
// can arrive — so a token left over after one firing is waiting for an arrival that
// will never come. It resolves every set it holds, then and there, rather than
// parking a surplus the way a parallel join rightly does.
//
//	start → split ⇉ a1, a2 ─→ merge ─→ join ─→ after
//	              ⇉ b ────────────────↗
func TestAnInclusiveJoinResolvesItsSurplusAtOnce(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(5, "or-surplus", 1)
	start := b.AddStartEvent()
	split := b.AddInclusiveGateway()
	a1, a2 := b.AddScriptTask(mustCompile(t, `1`), "a1"), b.AddScriptTask(mustCompile(t, `2`), "a2")
	only := b.AddScriptTask(mustCompile(t, `3`), "only")
	merge := b.AddExclusiveGateway()
	join := b.AddInclusiveGateway()
	after := b.AddServiceTask("or-surplus.after", 3)
	end := b.AddEndEvent()
	b.Connect(start, split)
	for _, n := range []int32{a1, a2, only} {
		f := b.Connect(split, n)
		b.SetFlowCondition(f, mustCompile(t, "true"))
	}
	b.Connect(a1, merge)
	b.Connect(a2, merge)
	b.Connect(merge, join) // two tokens down one flow
	b.Connect(only, join)  // one token down the other
	b.Connect(join, after)
	b.Connect(after, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
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

	// Three tokens, two flows: one complete pair and one leftover that is alone on
	// its flow. Both are firings, because nothing more is coming.
	if n := len(activatableJobs(t, h.store, afterType)); n != 2 {
		t.Fatalf("downstream jobs = %d, want 2 — the pair and the leftover both fire", n)
	}
	if n := liveOnNode(t, h, model.NewKey(1, 1), join); n != 0 {
		t.Fatalf("tokens parked on the join = %d, want none left waiting for an arrival that cannot come", n)
	}
}
