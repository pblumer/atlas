package infomodel

import (
	"testing"
)

// The lifecycle a class declares and the life one datum actually had are two
// different documents, and the whole value of §4 of ADR-0259 is reading them on top
// of each other. These tests are about that overlay and nothing else: the machine is
// given, the trail is given, and what is asserted is which parts of the machine the
// trail lit up — and, just as importantly, which parts of the trail the machine does
// not account for.

// orderLifecycle is the machine the traces below are read against: a start, a middle,
// two ways to end, and a self-loop, which is the shape most business objects have.
func orderLifecycle() *Lifecycle {
	return &Lifecycle{
		States: []LifecycleState{
			{Name: "received", Initial: true},
			{Name: "approved"},
			{Name: "shipped", Final: true},
			{Name: "cancelled", Final: true},
		},
		Transitions: []LifecycleTransition{
			{ID: "t1", Name: "approve", From: "received", To: "approved"},
			{ID: "t2", Name: "ship", From: "approved", To: "shipped"},
			{ID: "t3", Name: "cancel", From: "received", To: "cancelled"},
			{ID: "t4", Name: "revise", From: "received", To: "received"},
		},
	}
}

// stateNamed and transitionNamed read one row out of a trace, so a test asserts about
// the row it means rather than about a position in a slice.
func stateNamed(tr Trace, name string) (TracedState, bool) {
	for _, s := range tr.States {
		if s.Name == name {
			return s, true
		}
	}
	return TracedState{}, false
}

func transitionNamed(tr Trace, id string) (TracedTransition, bool) {
	for _, t := range tr.Transitions {
		if t.ID == id {
			return t, true
		}
	}
	return TracedTransition{}, false
}

func TestATraceMarksWhereTheDatumHasBeenAndWhereItIs(t *testing.T) {
	tr := TraceLifecycle(orderLifecycle(), []TrailEntry{
		{State: "received", At: 10, By: "StartEvent_1"},
		{State: "approved", At: 20, By: "Task_approve"},
	})

	if tr.Current != "approved" {
		t.Errorf("current = %q, want approved — the last state the trail records is where the datum is", tr.Current)
	}
	for _, want := range []string{"received", "approved"} {
		s, ok := stateNamed(tr, want)
		if !ok || !s.Visited {
			t.Errorf("%s: visited = %v (found %v), want true", want, s.Visited, ok)
		}
	}
	// A state nothing wrote is still part of the machine and still drawn — the overlay
	// is the declared life with the lived one on top, not the lived one alone.
	for _, want := range []string{"shipped", "cancelled"} {
		s, ok := stateNamed(tr, want)
		if !ok {
			t.Fatalf("%s is missing from the trace; the whole machine is drawn", want)
		}
		if s.Visited {
			t.Errorf("%s: visited = true, want false — nothing wrote it", want)
		}
	}
	if s, _ := stateNamed(tr, "approved"); !s.Current {
		t.Error("approved: current = false, want true")
	}
	if s, _ := stateNamed(tr, "received"); s.Current {
		t.Error("received: current = true, want false — the datum has moved on")
	}
}

func TestATraceMarksTheMovesActuallyMadeAndWhoMadeThem(t *testing.T) {
	tr := TraceLifecycle(orderLifecycle(), []TrailEntry{
		{State: "received", At: 10},
		{State: "approved", At: 20, By: "Task_approve"},
	})

	took, _ := transitionNamed(tr, "t1")
	if took.Taken != 1 {
		t.Errorf("t1: taken = %d, want 1", took.Taken)
	}
	// The element that made the move is the answer no class diagram can give, and the
	// reason the overlay is worth drawing at all.
	if took.LastBy != "Task_approve" || took.LastAt != 20 {
		t.Errorf("t1: lastBy/lastAt = %q/%d, want Task_approve/20", took.LastBy, took.LastAt)
	}
	for _, id := range []string{"t2", "t3", "t4"} {
		if got, _ := transitionNamed(tr, id); got.Taken != 0 {
			t.Errorf("%s: taken = %d, want 0 — this instance never made that move", id, got.Taken)
		}
	}
}

func TestAWriteThatDoesNotMoveTheDatumIsNotATransition(t *testing.T) {
	// Two writes in the same state is a value being corrected, not the object moving.
	// Counting it as the self-loop would say the process ran `revise` when it did not.
	tr := TraceLifecycle(orderLifecycle(), []TrailEntry{
		{State: "received", At: 10},
		{State: "received", At: 20, By: "Task_fix"},
	})
	if got, _ := transitionNamed(tr, "t4"); got.Taken != 0 {
		t.Errorf("t4 (received→received): taken = %d, want 0 — the state did not change", got.Taken)
	}
	if len(tr.Undeclared) != 0 {
		t.Errorf("undeclared = %v, want none — nothing moved", tr.Undeclared)
	}
}

func TestATraceCountsEveryPassAndKeepsTheLatest(t *testing.T) {
	tr := TraceLifecycle(orderLifecycle(), []TrailEntry{
		{State: "received", At: 10},
		{State: "cancelled", At: 20, By: "Task_cancel"},
		{State: "received", At: 30, By: "Task_reopen"},
		{State: "cancelled", At: 40, By: "Task_cancel_again"},
	})
	got, _ := transitionNamed(tr, "t3")
	if got.Taken != 2 {
		t.Errorf("t3: taken = %d, want 2 — the datum went round twice", got.Taken)
	}
	if got.LastBy != "Task_cancel_again" || got.LastAt != 40 {
		t.Errorf("t3: lastBy/lastAt = %q/%d, want Task_cancel_again/40 — the most recent pass", got.LastBy, got.LastAt)
	}
	// A state entered twice says when it was first reached and when it was last: the
	// two answer different questions and neither substitutes for the other.
	s, _ := stateNamed(tr, "cancelled")
	if s.Entered != 2 || s.FirstAt != 20 || s.LastAt != 40 {
		t.Errorf("cancelled: entered/first/last = %d/%d/%d, want 2/20/40", s.Entered, s.FirstAt, s.LastAt)
	}
}

func TestAMoveTheMachineDoesNotJoinIsReportedRatherThanDrawnAsOne(t *testing.T) {
	// The run-time twin of `data.illegal-transition`: the deploy check reads the model,
	// this reads what actually happened — which catches a process deployed before the
	// lifecycle existed, and an object written by a process the check never saw.
	tr := TraceLifecycle(orderLifecycle(), []TrailEntry{
		{State: "received", At: 10},
		{State: "shipped", At: 20, By: "Task_rush"},
	})
	if len(tr.Undeclared) != 1 {
		t.Fatalf("undeclared = %d, want 1", len(tr.Undeclared))
	}
	u := tr.Undeclared[0]
	if u.From != "received" || u.To != "shipped" || u.By != "Task_rush" || u.At != 20 {
		t.Errorf("undeclared[0] = %+v, want received→shipped by Task_rush at 20", u)
	}
	// And nothing in the machine is marked taken for it: an undeclared move must not
	// borrow a declared edge to be drawn on.
	for _, tt := range tr.Transitions {
		if tt.Taken != 0 {
			t.Errorf("%s: taken = %d, want 0 — the move it stands for was never declared", tt.ID, tt.Taken)
		}
	}
	// The state it landed in was still reached, whatever route it took to get there.
	if s, _ := stateNamed(tr, "shipped"); !s.Visited || !s.Current {
		t.Error("shipped: want visited and current — the datum is there however it arrived")
	}
}

func TestAStateTheMachineNeverDeclaredIsNamedRatherThanSwallowed(t *testing.T) {
	tr := TraceLifecycle(orderLifecycle(), []TrailEntry{
		{State: "received", At: 10},
		{State: "aproved", At: 20, By: "Task_typo"},
	})
	if len(tr.Unknown) != 1 || tr.Unknown[0] != "aproved" {
		t.Errorf("unknown = %v, want [aproved] — the typo that this whole record exists to catch", tr.Unknown)
	}
	if tr.Current != "aproved" {
		t.Errorf("current = %q, want aproved — where the datum is, not where it should be", tr.Current)
	}
	// It is reported once however often it is written, because it is one fact about
	// the model and not one per write.
	tr = TraceLifecycle(orderLifecycle(), []TrailEntry{
		{State: "aproved", At: 10}, {State: "received", At: 20}, {State: "aproved", At: 30},
	})
	if len(tr.Unknown) != 1 {
		t.Errorf("unknown = %v, want one entry however often it is written", tr.Unknown)
	}
}

func TestAWriteCarryingNoStateEndsTheRunRatherThanMovingFromNothing(t *testing.T) {
	// A data object can be written with no data state at all. That is not a state of
	// the machine, so it is not a move out of one and the next state is an entry, not
	// a transition — reading "" as a state would invent an edge from nowhere.
	tr := TraceLifecycle(orderLifecycle(), []TrailEntry{
		{State: "received", At: 10},
		{State: "", At: 20, By: "Task_clear"},
		{State: "approved", At: 30, By: "Task_approve"},
	})
	if got, _ := transitionNamed(tr, "t1"); got.Taken != 0 {
		t.Errorf("t1: taken = %d, want 0 — the datum left the machine in between", got.Taken)
	}
	if len(tr.Undeclared) != 0 {
		t.Errorf("undeclared = %v, want none — an entry is not an illegal move", tr.Undeclared)
	}
	if len(tr.Unknown) != 0 {
		t.Errorf("unknown = %v, want none — carrying no state is not carrying an unknown one", tr.Unknown)
	}
	if tr.Current != "approved" {
		t.Errorf("current = %q, want approved", tr.Current)
	}
}

func TestTwoTransitionsJoiningTheSamePairAreBothMarkedAndSaidToBeAmbiguous(t *testing.T) {
	// The trail records states, not transition ids. Where a model declares two ways to
	// get from one state to the same other, nothing in the record says which was
	// taken — so both are marked and both say so, rather than one being picked.
	lc := &Lifecycle{
		States: []LifecycleState{{Name: "open", Initial: true}, {Name: "closed", Final: true}},
		Transitions: []LifecycleTransition{
			{ID: "a", Name: "resolve", From: "open", To: "closed"},
			{ID: "b", Name: "withdraw", From: "open", To: "closed"},
		},
	}
	tr := TraceLifecycle(lc, []TrailEntry{{State: "open", At: 1}, {State: "closed", At: 2, By: "Task_x"}})
	for _, id := range []string{"a", "b"} {
		got, _ := transitionNamed(tr, id)
		if got.Taken != 1 {
			t.Errorf("%s: taken = %d, want 1 — the trail cannot tell the two apart", id, got.Taken)
		}
		if !got.Ambiguous {
			t.Errorf("%s: ambiguous = false, want true", id)
		}
	}
	// A pair only one transition joins says nothing of the kind.
	tr = TraceLifecycle(orderLifecycle(), []TrailEntry{{State: "received", At: 1}, {State: "approved", At: 2}})
	if got, _ := transitionNamed(tr, "t1"); got.Ambiguous {
		t.Error("t1: ambiguous = true, want false — it is the only way from received to approved")
	}
}

func TestATraceOfNothingIsStillTheMachine(t *testing.T) {
	// An instance that has written nothing yet: the declared life is worth drawing on
	// its own, and an empty picture would read as "this class has no lifecycle".
	tr := TraceLifecycle(orderLifecycle(), nil)
	if len(tr.States) != 4 || len(tr.Transitions) != 4 {
		t.Fatalf("states/transitions = %d/%d, want 4/4", len(tr.States), len(tr.Transitions))
	}
	if tr.Current != "" {
		t.Errorf("current = %q, want empty", tr.Current)
	}
	for _, s := range tr.States {
		if s.Visited {
			t.Errorf("%s: visited = true, want false", s.Name)
		}
	}
}

func TestNoLifecycleIsNoTrace(t *testing.T) {
	// nil is the normal case for a class, and it has to stay silent here exactly as it
	// is everywhere else — an empty machine is not a machine with no states.
	tr := TraceLifecycle(nil, []TrailEntry{{State: "received", At: 10}})
	if len(tr.States) != 0 || len(tr.Transitions) != 0 || tr.Current != "" {
		t.Errorf("trace of a class with no lifecycle = %+v, want empty", tr)
	}
	if len(tr.Unknown) != 0 {
		t.Errorf("unknown = %v, want none — a state is not unknown to a machine that does not exist", tr.Unknown)
	}
}
