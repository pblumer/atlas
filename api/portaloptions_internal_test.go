package api

import (
	"strings"
	"testing"
)

// Optional parts are part of the catalogue, and the portal showed them in exactly
// one place.
//
// A release carries two kinds of containment (ADR-0312):
// a **composition** is a part that comes with the whole and cannot be dropped, an
// **aggregation** is an offer standing beside it. The cascade has rendered both
// since the columns were laid out the way the catalogue is shaped. Two other views
// had not caught up:
//
//   - the info panel named neither group, so the one screen that exists to say
//     what a product *is* said nothing about what it carries;
//   - the basket pulled in compositions and stopped there, so the offers attached
//     to a bundle were unreachable from the one screen that decides what is
//     ordered.
//
// The guards read the region they guard rather than the file: `includes` and
// `options` appear in half a dozen functions, and a search across shop.js would
// pass whatever those two views did.

// TestTheInfoPanelNamesWhatAProductCarries.
//
// "What does this cost, and what comes with it" is one question. The panel
// answered the first half and left the second to a column the person had to find
// for themselves.
//
// It then answered the second half for one level only, which is the defect this
// now guards against. A bundle whose hardware carries an operating system named
// the hardware and said nothing about the system — while the column beside the
// card listed both, and the basket ordered both. The panel walks the whole
// containment graph through descendantsOf, which is the function those two
// already use: a second walk here is how two screens come to disagree about what
// a product is.
func TestTheInfoPanelNamesWhatAProductCarries(t *testing.T) {
	body := webRegion(t, readWeb(t, "shop.js"), "function infoPanel(", "\n}")
	if !strings.Contains(body, "descendantsOf(rel, item.id)") {
		t.Error("the info panel does not read the product's descendants, so a part two " +
			"edges down is missing from a card that claims to say what the product carries")
	}
	for _, group := range []struct{ filter, why string }{
		{"p.integral", "what always comes with the product"},
		{"!p.integral", "what is offered beside it"},
	} {
		if !strings.Contains(body, group.filter) {
			t.Errorf("the info panel never filters on %q, so it does not separate %s",
				group.filter, group.why)
		}
	}
}

// TestTheBasketOffersWhatIsOptional.
//
// An aggregation that cannot be ticked in the basket is an aggregation nobody can
// order: the cascade's "+" reaches it only while the bundle is the open column,
// and the basket is where somebody decides what they are actually asking for.
func TestTheBasketOffersWhatIsOptional(t *testing.T) {
	body := webRegion(t, readWeb(t, "shop.js"), "function renderBasket(", "\n// --- What a product needs")
	if !strings.Contains(body, "options") {
		t.Error("the basket never reads the release's options, so the optional parts of " +
			"a chosen bundle cannot be ordered from the screen that places the order")
	}
	// Reached through the cascade's own control rather than a second one: a tick
	// has to mean the same thing on both screens, and two controls would be two
	// places for "what is in the basket" to be decided.
	if !strings.Contains(body, "toggle(rel,") {
		t.Error("the basket offers no way to take an optional part, so the rows it draws " +
			"are a list rather than a choice")
	}
	// And nothing pre-selects them. An offer that arrived ticked would be ordered
	// by everybody who did not look, which is the opposite of an offer — so the
	// only recursion the basket performs is over what is integral.
	walk := webRegion(t, readWeb(t, "shop.js"), "  const add = (id, integral)", "  for (const id of state.basket)")
	if strings.Contains(walk, "options") {
		t.Error("the basket pulls optional parts in by itself, so an aggregation is " +
			"ordered by anybody who did not notice it")
	}
}
