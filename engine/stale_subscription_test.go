package engine_test

import (
	"testing"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
)

// A subscription whose element no longer waits — the losing branch of an event-based
// gateway, a boundary whose host finished — is left behind and cleared lazily by the
// next send that matches it. That send reaches nobody there: it must not count as a
// delivery, and it must not write its payload into the instance it never reached.

// gatewayRace: Start → event-based gateway → catch "approve" → a user task that waits;
// or → catch "reject" → End. Both catches are keyed on orderId.
func gatewayRace(t testing.TB, key uint64) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(key, "race", 1)
	ck := mustCompile(t, "orderId")
	start := b.AddStartEvent()
	gw := b.AddEventBasedGateway()
	approve := b.AddMessageCatchEvent("approve", ck)
	reject := b.AddMessageCatchEvent("reject", ck)
	work := b.AddUserTask("Work", compiler.Assignment{Literal: ""}, compiler.Assignment{Literal: ""}, "", 0, 0, 3)
	b.Connect(start, gw)
	b.Connect(gw, approve)
	b.Connect(gw, reject)
	b.Connect(approve, work)
	b.Connect(work, b.AddEndEvent())
	b.Connect(reject, b.AddEndEvent())
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// approvedRace starts the race and lets "approve" win: the instance waits at the user
// task, and the "reject" catch's subscription is left behind.
func approvedRace(t *testing.T, h *harness, clk engine.Clock, extra ...*compiler.CompiledProcess) (*engine.Processor, uint64) {
	t.Helper()
	race := gatewayRace(t, 7)
	p := bufferProcessor(t, h, clk, append([]*compiler.CompiledProcess{race}, extra...)...)
	p.CreateInstance(race.Key, numVar("orderId", "42"))
	runIdle(t, p, "race")
	key := activeInstanceOf(t, h.store, race.Key)
	p.PublishMessage("approve", "42", strVar("decision", "yes"))
	runIdle(t, p, "approve")
	if got := readVar(t, h.store, key, "decision"); got == nil || got.Text != "yes" {
		t.Fatalf("decision after approve = %+v, want yes", got)
	}
	return p, key
}

// TestAStaleMessageSubscriptionTakesNoPayload: the reject that arrives after approve
// won reaches nobody, so the running instance keeps what approve gave it.
func TestAStaleMessageSubscriptionTakesNoPayload(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p, key := approvedRace(t, h, &fixedClock{t: 1_000})

	p.PublishMessage("reject", "42", strVar("decision", "no"))
	runIdle(t, p, "late reject")
	if got := readVar(t, h.store, key, "decision"); got == nil || got.Text != "yes" {
		t.Fatalf("decision after a reject nobody waited for = %+v, want yes still", got)
	}
	if pi, _ := counts(t, h.store); pi != 1 {
		t.Fatalf("instances = %d, want the approved one still waiting at its task", pi)
	}
}

// TestAMessageOnlyAStaleSubscriptionMatchesIsKept: a message with a time to live that
// matches nothing but a left-behind subscription is a message nothing waits for, so the
// buffer keeps it (ADR-0370), and the subscription that opens later receives it.
func TestAMessageOnlyAStaleSubscriptionMatchesIsKept(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	catcher := catcherOn(t, 8, "reject")
	p, _ := approvedRace(t, h, &fixedClock{t: 1_000}, catcher)

	p.PublishMessageWithTTL("reject", "42", "", time.Minute, strVar("decision", "no"))
	runIdle(t, p, "late reject with a time to live")
	if got := bufferedMessages(t, h.store); len(got) != 1 || got[0].MessageName != "reject" {
		t.Fatalf("buffer = %+v, want the reject kept: nothing waited for it", got)
	}
	// The event log says the same: a send that reached nobody, kept, and no reception.
	rows := eventLog(t, h.store)
	last := rows[len(rows)-2:]
	if last[0].Kind != state.EventLogDispatched || last[0].Dispatch.Name != "reject" || last[0].Dispatch.Reached != 0 ||
		last[1].Kind != state.EventLogMessageKept {
		t.Fatalf("the event log ends with %v, want the reject sent to nobody and kept", kinds(last))
	}

	p.CreateInstance(catcher.Key, numVar("orderId", "42"))
	runIdle(t, p, "a catcher that waits")
	catcherKey := activeInstanceOf(t, h.store, catcher.Key)
	if got := readVar(t, h.store, catcherKey, "decision"); got == nil || got.Text != "no" {
		t.Fatalf("the catcher that waited later = %+v, want the kept reject", got)
	}
}

// catcherOn: Start → catch name (orderId) → a user task that waits, so the instance is
// still there to read once the message arrived.
func catcherOn(t testing.TB, key uint64, name string) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(key, "catcher-"+name, 1)
	start := b.AddStartEvent()
	catch := b.AddMessageCatchEvent(name, mustCompile(t, "orderId"))
	hold := b.AddUserTask("Hold", compiler.Assignment{Literal: ""}, compiler.Assignment{Literal: ""}, "", 0, 0, 3)
	b.Connect(start, catch)
	b.Connect(catch, hold)
	b.Connect(hold, b.AddEndEvent())
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// TestAStaleSignalSubscriptionTakesNoPayload: a non-interrupting signal boundary whose
// host finished leaves its subscription behind; the broadcast that clears it writes
// nothing into the instance, which has moved on to a task of its own.
func TestAStaleSignalSubscriptionTakesNoPayload(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	b := compiler.NewBuilder(11, "guarded", 1)
	start := b.AddStartEvent()
	task := b.AddServiceTask("work", 3)
	bound := b.AddBoundarySignalEvent(task, false, "cancelled")
	next := b.AddUserTask("Next", compiler.Assignment{Literal: ""}, compiler.Assignment{Literal: ""}, "", 0, 0, 3)
	b.Connect(start, task)
	b.Connect(task, next)
	b.Connect(next, b.AddEndEvent())
	b.Connect(bound, b.AddEndEvent())
	guarded, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := guarded.ServiceTask(guarded.Node(task).Detail).JobType
	thrower := signalThrower(t, 9)
	p := bufferProcessor(t, h, &manualClock{}, guarded, thrower)

	p.CreateInstance(guarded.Key, strVar("reason", "own"))
	runIdle(t, p, "guarded")
	key := activeInstanceOf(t, h.store, guarded.Key)
	p.CompleteJob(singleActivatableJob(t, h.store, jobType))
	runIdle(t, p, "host done")

	p.CreateInstance(thrower.Key, strVar("reason", "thrown"))
	runIdle(t, p, "throw")
	if got := readVar(t, h.store, key, "reason"); got == nil || got.Text != "own" {
		t.Fatalf("reason after a broadcast the finished boundary no longer waited for = %+v, want own", got)
	}
}
