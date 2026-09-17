package api

import (
	"strings"
	"testing"
)

// The step, to the person whose position it is
// (ADR-0390).
//
// The portal already offered a way into the process working on a position, and it
// was an operator's: following it lands in the console, which shows the whole
// engine state of that instance. The orderer gets an answer of their own instead —
// which step the position is sitting on — from a route gated on owning the order
// rather than on a role.
//
// These hold the three ways that quietly becomes the operator's answer again.

// TestTheStepIsOfferedWithoutARole.
//
// The button sits between the withdrawal and the operator's link, and the guard
// reads exactly that stretch: a button moved inside the role condition still
// appears in the file, still reads correctly, and is gone for everybody the route
// was written for.
func TestTheStepIsOfferedWithoutARole(t *testing.T) {
	rows := webRegion(t, readWeb(t, "portal.js"), "function orderRowBodies(", "\n}")
	if !strings.Contains(rows, "askProgress(o, l)") {
		t.Fatal("a position row offers no way to ask where it stands, so the reader is " +
			"told their order is running and not where it is")
	}
	ungated := webRegion(t, rows, "withdrawable(l)", "state.mayFollowProcess")
	if !strings.Contains(ungated, "askProgress(o, l)") {
		t.Error("asking where a position stands is offered only to a reader who may " +
			"open an instance, which is the operations surface this route exists to " +
			"avoid needing")
	}
}

// TestTheStepComesFromThePortalsOwnRoute.
//
// The instance search is the operator's answer and it is right there in the same
// file. Asking it here would work for an operator, answer 403 for everybody else,
// and hand back the instance's variables to whoever it did answer.
func TestTheStepComesFromThePortalsOwnRoute(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "async function askProgress(", "\n}")
	if !strings.Contains(body, "/progress") || !strings.Contains(body, "portal/orders/") {
		t.Error("the step is not read from the portal's own progress route")
	}
	for _, operators := range []string{"instances/search", "operations/i/"} {
		if strings.Contains(body, operators) {
			t.Errorf("the step is read through %q, which is an operations surface the "+
				"orderer has no role for", operators)
		}
	}
}

// TestTheAnswerIsKeptPerPosition.
//
// One order, four positions, four different steps. An answer filed under the order
// shows the first position's step against all four — and every one of them reads
// as a fact about that line.
func TestTheAnswerIsKeptPerPosition(t *testing.T) {
	src := readWeb(t, "portal.js")
	// The key itself, and not merely a mention of the position. The ask spells the
	// position into its URL as well, so a guard reading for that alone stays green
	// with the answer filed under the order.
	const key = "`${order.id}|${lineKey(line)}`"
	for _, fn := range []string{"async function askProgress(", "function progressNote("} {
		body := webRegion(t, src, fn, "\n}")
		if !strings.Contains(body, key) {
			t.Errorf("%s files the answer under something other than the position, so "+
				"one line's step is shown against every line of the order",
				strings.TrimSuffix(strings.TrimPrefix(fn, "async "), "("))
		}
	}
}

// TestNothingRunningIsSaidAndNotLeftBlank.
//
// It is the ordinary state of most positions for most of an order's life: before
// the position is reached, and after it is finished. A button that answered it
// with an empty line reads as a button that failed.
func TestNothingRunningIsSaidAndNotLeftBlank(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "function progressNote(", "\n}")
	if !strings.Contains(body, "proc.nothingRunning") {
		t.Error("a position nothing is running for is answered with silence, which " +
			"reads as the page having broken rather than as the ordinary case")
	}
}
