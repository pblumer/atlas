package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
)

// perPositionStrand deploys the smallest per-position lifecycle: a provision start,
// then a wait for the position's return, then the return's own step. The catch is
// keyed on the position, the way a per-position lifecycle process must key it.
func perPositionStrand(t *testing.T, h *harness) *engine.Processor {
	t.Helper()
	b := compiler.NewBuilder(9, "strand", 1)
	s := b.AddMessageStartEvent("strand.provision", nil, false)
	issued := b.AddScriptTask(mustCompile(t, "true"), "ran_provision")
	wait := b.AddMessageCatchEvent("strand.deprovision", mustCompile(t, "positionKey"))
	ret := b.AddScriptTask(mustCompile(t, "true"), "ran_deprovision")
	b.Connect(s, issued)
	b.Connect(issued, wait)
	b.Connect(wait, ret)
	b.Connect(ret, b.AddEndEvent())
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	return p
}

func issue(t *testing.T, p *engine.Processor, position string) uint64 {
	t.Helper()
	var res engine.TriggerResult
	p.TriggerStart(9, "strand.provision", "", "", &res, strVar("positionKey", position))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if res.Outcome != engine.TriggerCreated {
		t.Fatalf("provision = %+v, want created", res)
	}
	return res.InstanceKey
}

func deliver(t *testing.T, p *engine.Processor, key uint64, name, position, id string) engine.DeliveryResult {
	t.Helper()
	var res engine.DeliveryResult
	p.DeliverMessage(key, name, position, "caller:test", id, &res, strVar("reason", "left"))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	return res
}

func active(t *testing.T, h *harness, key uint64) bool {
	t.Helper()
	_, ok, err := h.store.ActiveProcessInstance(key)
	if err != nil {
		t.Fatalf("ActiveProcessInstance: %v", err)
	}
	return ok
}

// TestDeliveryReachesOnlyTheNamedInstance: two positions whose strands wait under the
// same correlation key — the case a name-correlated publish returns both of — and a
// delivery addressed to one returns that one only, carrying its payload.
func TestDeliveryReachesOnlyTheNamedInstance(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p := perPositionStrand(t, h)
	first, second := issue(t, p, "o1/laptop"), issue(t, p, "o1/laptop")

	res := deliver(t, p, first, "strand.deprovision", "o1/laptop", "ret-1")
	if res.Outcome != engine.DeliveryDelivered || res.InstanceKey != first || len(res.Elements) != 1 {
		t.Fatalf("delivery = %+v, want delivered to %d at one element", res, first)
	}
	if active(t, h, first) || readVar(t, h.store, first, "reason") == nil {
		t.Fatalf("the addressed strand did not take the return and its payload")
	}
	if !active(t, h, second) || readVar(t, h.store, second, "ran_deprovision") != nil {
		t.Fatal("the other strand under the same key was returned too")
	}
}

// TestDeliverySaysWhyItDeliveredNothing: a message the instance does not wait for,
// a correlation key that is not the position's, an instance that finished and a key
// that never existed are each answered, and none of them changes anything.
func TestDeliverySaysWhyItDeliveredNothing(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p := perPositionStrand(t, h)
	key := issue(t, p, "o1/laptop")

	for _, c := range []struct{ name, position string }{
		{"strand.change", "o1/laptop"},
		{"strand.deprovision", "o2/laptop"},
	} {
		if res := deliver(t, p, key, c.name, c.position, ""); res.Outcome != engine.DeliveryNotWaiting || res.InstanceKey != key {
			t.Errorf("%s under %s = %+v, want not waiting", c.name, c.position, res)
		}
	}
	if !active(t, h, key) || readVar(t, h.store, key, "ran_deprovision") != nil {
		t.Fatal("a delivery that reached nobody moved the strand")
	}

	if res := deliver(t, p, key, "strand.deprovision", "o1/laptop", ""); res.Outcome != engine.DeliveryDelivered {
		t.Fatalf("return = %+v, want delivered", res)
	}
	if res := deliver(t, p, key, "strand.deprovision", "o1/laptop", ""); res.Outcome != engine.DeliveryGone {
		t.Errorf("after the strand ended = %+v, want gone", res)
	}
	if res := deliver(t, p, 424242, "strand.deprovision", "o1/laptop", ""); res.Outcome != engine.DeliveryGone {
		t.Errorf("unknown instance = %+v, want gone", res)
	}
}

// TestARetriedDeliveryIsAnsweredFromItsReceipt: the same trigger id from the same
// sender answers with the first delivery — even after the strand has ended, which is
// exactly when a retry would otherwise be told "gone" and fall back to a second
// return — and the receipt survives recovery.
func TestARetriedDeliveryIsAnsweredFromItsReceipt(t *testing.T) {
	dir := t.TempDir()
	h := openHarness(t, dir)
	p := perPositionStrand(t, h)
	key := issue(t, p, "o1/laptop")
	if res := deliver(t, p, key, "strand.deprovision", "o1/laptop", "ret-1"); res.Outcome != engine.DeliveryDelivered {
		t.Fatalf("first = %+v, want delivered", res)
	}
	if res := deliver(t, p, key, "strand.deprovision", "o1/laptop", "ret-1"); res.Outcome != engine.DeliveryReplayed || res.InstanceKey != key {
		t.Fatalf("retry = %+v, want replayed naming %d", res, key)
	}
	h.close(t)

	h = openHarness(t, dir)
	defer h.close(t)
	p = perPositionStrand(t, h)
	if res := deliver(t, p, key, "strand.deprovision", "o1/laptop", "ret-1"); res.Outcome != engine.DeliveryReplayed {
		t.Fatalf("retry after recovery = %+v, want replayed", res)
	}
}

// changeAndReturnStrand is the shape of the per-position template: after issue the
// strand waits at an event-based gateway for a change or the return; a change is a
// user task that the return interrupts through a boundary event, and a finished
// change goes back to waiting.
func changeAndReturnStrand(t *testing.T, h *harness) *engine.Processor {
	t.Helper()
	b := compiler.NewBuilder(9, "strand", 1)
	key := mustCompile(t, "positionKey")
	s := b.AddMessageStartEvent("strand.provision", nil, false)
	wait := b.AddExclusiveGateway()
	gw := b.AddEventBasedGateway()
	askChange := b.AddMessageCatchEvent("strand.change", key)
	change := b.AddUserTask("Change", compiler.Assignment{Literal: ""}, compiler.Assignment{Literal: ""}, "", 0, 0, 3)
	cut := b.AddBoundaryMessageEvent(change, true, "strand.deprovision", key)
	askReturn := b.AddMessageCatchEvent("strand.deprovision", key)
	ret := b.AddExclusiveGateway()
	done := b.AddScriptTask(mustCompile(t, "true"), "ran_deprovision")
	b.Connect(s, wait)
	b.Connect(wait, gw)
	b.Connect(gw, askChange)
	b.Connect(gw, askReturn)
	b.Connect(askChange, change)
	b.Connect(change, wait)
	b.Connect(askReturn, ret)
	b.Connect(cut, ret)
	b.Connect(ret, done)
	b.Connect(done, b.AddEndEvent())
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	return p
}

// TestDeliveryFollowsTheStrandThroughGatewayAndBoundary: a change is taken at the
// event-based gateway and disarms the gateway's other branch; while the change runs
// a second change is not waited for, and the return is — through the boundary event,
// which ends the change and returns the right.
func TestDeliveryFollowsTheStrandThroughGatewayAndBoundary(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p := changeAndReturnStrand(t, h)
	key := issue(t, p, "o1/laptop")

	if res := deliver(t, p, key, "strand.change", "o1/laptop", "chg-1"); res.Outcome != engine.DeliveryDelivered {
		t.Fatalf("change = %+v, want delivered at the gateway", res)
	}
	if res := deliver(t, p, key, "strand.change", "o1/laptop", "chg-2"); res.Outcome != engine.DeliveryNotWaiting {
		t.Fatalf("second change during the first = %+v, want not waiting", res)
	}
	if res := deliver(t, p, key, "strand.deprovision", "o1/laptop", "ret-1"); res.Outcome != engine.DeliveryDelivered {
		t.Fatalf("return during the change = %+v, want delivered through the boundary", res)
	}
	if active(t, h, key) || readVar(t, h.store, key, "ran_deprovision") == nil {
		t.Fatal("the return during a change did not end the strand through the return path")
	}
}

// changeOrResetStrand waits at an event-based gateway for a change, which a person
// then works, or a password reset, which runs at once and waits again.
func changeOrResetStrand(t *testing.T, h *harness) *engine.Processor {
	t.Helper()
	b := compiler.NewBuilder(9, "strand", 1)
	key := mustCompile(t, "positionKey")
	s := b.AddMessageStartEvent("strand.provision", nil, false)
	wait := b.AddExclusiveGateway()
	gw := b.AddEventBasedGateway()
	askChange := b.AddMessageCatchEvent("strand.change", key)
	change := b.AddUserTask("Change", compiler.Assignment{Literal: ""}, compiler.Assignment{Literal: ""}, "", 0, 0, 3)
	askReset := b.AddMessageCatchEvent("strand.reset", key)
	reset := b.AddScriptTask(mustCompile(t, "true"), "ran_reset")
	b.Connect(s, wait)
	b.Connect(wait, gw)
	b.Connect(gw, askChange)
	b.Connect(gw, askReset)
	b.Connect(askChange, change)
	b.Connect(change, wait)
	b.Connect(askReset, reset)
	b.Connect(reset, wait)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	return p
}

// TestADeliveryToACatchThatLostItsRaceDeliversNothing: once a change wins the
// event-based gateway, the reset branch is terminated but its subscription stays
// behind until a later correlation clears it. A reset delivered while the change is
// worked must be answered "not waiting" — before, it was "delivered": the payload
// was written into the strand, a receipt recorded it, and nothing ran.
func TestADeliveryToACatchThatLostItsRaceDeliversNothing(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p := changeOrResetStrand(t, h)
	key := issue(t, p, "o1/laptop")

	if res := deliver(t, p, key, "strand.change", "o1/laptop", "chg-1"); res.Outcome != engine.DeliveryDelivered {
		t.Fatalf("change = %+v, want delivered at the gateway", res)
	}
	var res engine.DeliveryResult
	p.DeliverMessage(key, "strand.reset", "o1/laptop", "caller:test", "rst-1", &res, strVar("resetFor", "locked out"))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if res.Outcome != engine.DeliveryNotWaiting {
		t.Fatalf("reset during the change = %+v, want not waiting", res)
	}
	if readVar(t, h.store, key, "resetFor") != nil {
		t.Fatal("a delivery nothing received wrote its payload into the strand")
	}
	// Refused, not received: the same command id is free to arrive once the strand
	// waits for it again, rather than replaying a delivery that never happened.
	if res := deliver(t, p, key, "strand.reset", "o1/laptop", "rst-1"); res.Outcome != engine.DeliveryNotWaiting {
		t.Fatalf("retry = %+v, want not waiting again rather than replayed", res)
	}
}
