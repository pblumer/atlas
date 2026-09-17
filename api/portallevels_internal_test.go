package api

import (
	"strings"
	"testing"
)

// One rule decides what a position is called, and both screens read it.
//
// Atlas has no "Bundle / Marktleistung / Service" typing: an item is an item, and
// the hierarchy is the containment graph. The portal derived the level from
// position in that graph — and derived it twice, differently. The cascade used
// depth, so the direct part of a bundle sat under MARKTLEISTUNG. The basket used
// *kind*, splitting integral from optional, so the same position sat under
// SERVICE. One position, two names, on two halves of one screen.
//
// Depth and kind are two different questions — "where does this sit" and "can it
// be taken out" — and the basket was answering the first with the second.

// TestOneRuleNamesTheLevel.
func TestOneRuleNamesTheLevel(t *testing.T) {
	src := readWeb(t, "portal.js")
	for _, view := range []struct{ name, from, to string }{
		{"the cascade", "function renderCatalogue(", "\n// What a keystroke redraws"},
		{"the basket", "function renderBasket(", "\n// --- What a product needs"},
		// The third view, and the one that made "both" the wrong word: what
		// somebody holds is laid out across the same three columns, and it was
		// reading depth for itself. It agreed with the cascade everywhere except
		// the root with no parts — which is exactly the case this rule changed.
		{"what the person holds", "function renderServices(", "\n// The brand mark"},
	} {
		if !strings.Contains(webRegion(t, src, view.from, view.to), "levelOf(") {
			t.Errorf("%s names a level without asking the one rule that decides it, so "+
				"the same position can be called two things on one screen", view.name)
		}
	}
	// And the basket no longer splits its columns by whether a part can be taken
	// out. That is a different question from where the part sits, and it is
	// answered per row by the control, not by the column.
	basket := webRegion(t, src, "function renderBasket(", "\n// --- What a product needs")
	if strings.Contains(basket, "filter((x) => !x.integral)") ||
		strings.Contains(basket, "filter((x) => x.integral)") {
		t.Error("the basket still groups by whether a position is integral, which is " +
			"what made one position carry two level names")
	}
}

// TestAProductWithNoPartsIsNotABundle.
//
// The second half of the same defect: every item nothing contained landed under
// BUNDLE, so a single product with nothing inside it was announced as a bundle.
// A bundle is a thing made of other things. Something with no parts and no parent
// is an offering — the level the catalogue's own vocabulary gives to a thing that
// is offered on its own.
func TestAProductWithNoPartsIsNotABundle(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "function levelOf(", "\n}")
	if !strings.Contains(body, "hasParts ? 'bundle' : 'offering'") {
		t.Error("a product with no parent and no parts is still called a bundle, so a " +
			"single product is announced as something made of other things")
	}
	// The rule reads both kinds of containment. An aggregation is as much a part as
	// a composition where the question is what something is made of.
	for _, field := range []string{"includes", "options"} {
		if !strings.Contains(body, field) {
			t.Errorf("the level rule never reads %q, so half the graph decides nothing", field)
		}
	}
}
