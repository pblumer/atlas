package api

import (
	"strings"
	"testing"
)

// Moving a product between catalogues is a decision, and it used to be a side
// effect.
//
// A product is referenced by catalogues and edited through exactly one
// (ADR-0315). The server treats a save naming a different home as a deliberate
// adoption: it checks the caller may edit both sides, and moves it. That gate is
// right, and the Console was walking through it by accident — the product form
// sent the catalogue being *viewed* as the home on every save. So opening a
// product from a catalogue that merely offers it and pressing save took it away
// from whoever was responsible for it, with nothing said, and from then on the
// boxes were drawn from the new home's languages instead.
//
// Nothing in the model can catch that: every field the server saw was one a
// legitimate adoption sends. The screen is where the intent is, so the guard is
// here.

// TestAdoptingAProductIsAskedForRatherThanAssumed.
func TestAdoptingAProductIsAskedForRatherThanAssumed(t *testing.T) {
	body := webRegion(t, readWeb(t, "catalog-admin.js"), "function wireProductForm(", "\n  }")

	if !strings.Contains(body, "window.confirm(") {
		t.Fatal("the product form sends a home catalogue without asking. Saving a " +
			"product from a catalogue that only offers it then moves it, silently, " +
			"away from whoever maintains it")
	}
	// Asked only where there is something to ask. A new product has no home yet
	// and a product already at home here has nothing to move — a prompt on either
	// is a prompt people learn to dismiss, which is how the silent move comes back.
	if !strings.Contains(body, "was.homeCatalog && was.homeCatalog !== id") {
		t.Error("the question is not conditioned on the product actually having " +
			"another home, so it is asked when there is nothing to decide")
	}
	// Cancelling keeps the home and still saves. Abandoning the save would make
	// the answer to "should this move" decide whether the edit happens at all,
	// which are two different questions.
	if !strings.Contains(body, "? id\n          : was.homeCatalog") {
		t.Error("cancelling does not keep the existing home; the two decisions — " +
			"whether to move it and whether to save the edit — have been merged")
	}
	if !strings.Contains(body, "homeCatalog: home") {
		t.Error("the body is still built from the catalogue being viewed rather than " +
			"from what was decided, so the question changes nothing")
	}
}
