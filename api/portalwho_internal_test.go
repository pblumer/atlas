package api

import (
	"strings"
	"testing"
)

// The corner of the portal: who is reading, and where the help sits.
//
// Two small placements that are easy to undo by accident and hard to notice
// undone, because both fail into something that still looks deliberate — a "?"
// among the destinations reads as a fourth destination, and a corner saying
// "myself" to everybody reads as a label rather than as a name nobody filled in.
//
// Each guard reads the region it guards rather than the file.

// TestTheHelpSitsPastThePerson.
//
// It was drawn beside the first destination, where it is the same height and
// shape as its neighbours in a row where everything else navigates the catalogue
// — so it read as one more place to go. At the far end of the row it belongs to
// the corner that is about the reader.
func TestTheHelpSitsPastThePerson(t *testing.T) {
	nav := webRegion(t, readWeb(t, "shop.js"), "function renderNav(", "\n}")
	who := strings.Index(nav, "class: 'who'")
	help := strings.Index(nav, "class: 'help'")
	if who < 0 || help < 0 {
		t.Fatalf("the nav no longer draws both the person (%d) and the help (%d)", who, help)
	}
	if help < who {
		t.Error("the help affordance is drawn before the person, so it is no longer " +
			"the last thing in the row")
	}
	// And both are past the spacer, or "at the far right" is a claim about a row
	// whose right-hand end is wherever the destinations happen to stop.
	spacer := strings.Index(nav, "class: 'spacer'")
	if spacer < 0 || spacer > who {
		t.Error("the corner is not pushed to the right-hand end of the row")
	}
}

// TestTheCornerNamesWhoeverTheOrderIsFor.
//
// Three answers in one order, and the order is the decision. A chosen recipient
// comes first because it is the fact that makes the next click place an order
// somebody else will hold; the reader's own name second; and the word that names
// neither only where there is nobody to name.
func TestTheCornerNamesWhoeverTheOrderIsFor(t *testing.T) {
	body := webRegion(t, readWeb(t, "shop.js"), "function whoLabel(", "\n}")
	at := func(needle string) int {
		i := strings.Index(body, needle)
		if i < 0 {
			t.Fatalf("whoLabel no longer mentions %s, so the corner answers one question fewer", needle)
		}
		return i
	}
	recipient, reader, neither := at("forWhomLabel"), at("state.meName"), at("t('for.self')")
	if !(recipient < reader && reader < neither) {
		t.Errorf("the corner answers in the order recipient=%d reader=%d neither=%d; a "+
			"recipient must come before the reader, and the fallback last",
			recipient, reader, neither)
	}
}

// TestTheReadersNameIsLearnedBeforeTheGateIsAsked.
//
// The one ordering in loadWhoIAm that matters. Everything after the
// may-order-for-others check runs for operators and administrators only, and the
// name in the corner is for everybody — learned there, every ordinary user would
// go on seeing the word that names nobody, and the page would look exactly as it
// does when it works.
func TestTheReadersNameIsLearnedBeforeTheGateIsAsked(t *testing.T) {
	body := webRegion(t, readWeb(t, "shop.js"), "function loadWhoIAm(", "\n}")
	learned := strings.Index(body, "state.meName = String(")
	gate := strings.Index(body, "if (!state.mayOrderForOthers) return;")
	if learned < 0 || gate < 0 {
		t.Fatalf("loadWhoIAm no longer both learns the name (%d) and asks the gate (%d)", learned, gate)
	}
	if learned > gate {
		t.Error("the reader's name is learned after the ordering gate, so everybody who " +
			"may not order for somebody else sees no name at all")
	}
	// And it is cleared on the way in, or a failed reload would leave the previous
	// reader's name in the corner of a page that no longer knows who is reading.
	if !strings.Contains(body, "state.meName = '';") {
		t.Error("the name is not cleared before it is asked for again")
	}
}
