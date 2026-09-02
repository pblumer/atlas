package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
)

// A start event is a *trigger*, and the one that fires is the one that seeds the
// instance. A process may carry several — a message start for "a ticket arrived", a
// none start for "somebody pressed Start" — and BPMN instantiates at the one that
// actually happened, not at all of them.
//
// Atlas seeded a token at *every* root start event whatever created the instance, which
// ADR-0035 recorded as "a message start behaves at runtime exactly like a none start".
// That is true of a process with one start event and false of a process with two, and
// the difference is not cosmetic: a Jira watch published jira.ticket.created, the
// message-started instance also ran the none-start branch, that branch created a Jira
// issue, the watch matched it, and the loop ran until somebody deleted the watch.
//
// mixedStarts is that shape, reduced: the message branch ends immediately, the none
// branch parks at a catch that never correlates. So "did the none branch run?" is
// answerable as "is an instance still active?".
func mixedStarts(t testing.TB, key uint64) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(key, "mixed", 1)
	ms := b.AddMessageStartEvent("ticket.created", nil, false)
	msEnd := b.AddEndEvent()
	b.Connect(ms, msEnd)

	none := b.AddStartEvent()
	park := b.AddMessageCatchEvent("never", nil)
	noneEnd := b.AddEndEvent()
	b.Connect(none, park)
	b.Connect(park, noneEnd)

	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// TestMessageStartSeedsOnlyItsOwnBranch is the loop's root cause, stated as a test.
func TestMessageStartSeedsOnlyItsOwnBranch(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(mixedStarts(t, 7))
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	p.PublishMessage("ticket.created", "")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 0 {
		t.Fatalf("after the message: active=%d, want 0 — the message start's branch ends, "+
			"and the none start's branch must not have been seeded by a message", pi)
	}
}

// The mirror: pressing Start must not fire the message start event either. Same shape
// with the branches swapped, so an instance left active means the message branch ran.
func TestApiCreateSeedsOnlyTheNoneStart(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(7, "mixed-swapped", 1)
	ms := b.AddMessageStartEvent("ticket.created", nil, false)
	park := b.AddMessageCatchEvent("never", nil)
	msEnd := b.AddEndEvent()
	b.Connect(ms, park)
	b.Connect(park, msEnd)
	none := b.AddStartEvent()
	noneEnd := b.AddEndEvent()
	b.Connect(none, noneEnd)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(7)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 0 {
		t.Fatalf("after an API create: active=%d, want 0 — the none start's branch ends, "+
			"and the message start must not have been seeded by pressing Start", pi)
	}
}

// The permissiveness ADR-0035 recorded stays: a process whose *only* entry is a message
// start can still be created through the API, and then just flows on. Narrowing an API
// create to the none starts would otherwise create an instance with no token at all —
// one that never ends and that nothing can be seen to be waiting for.
func TestApiCreateStillStartsAMessageOnlyProcess(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(parkingResponder(t, 7))
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(7)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 1 {
		t.Fatalf("after an API create: active=%d, want 1 — a message-start-only process "+
			"is still startable by hand and parks at its catch", pi)
	}
}
