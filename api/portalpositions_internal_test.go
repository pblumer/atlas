package api

import (
	"strings"
	"testing"
)

// The basket can ask for one product in two shapes, where the catalogue allows it.
//
// A variant chooser that took one answer per product could not express what the
// catalogue's own rule describes — the same service in two variants, kept — and
// there was nowhere to put the second answer. The chooser now offers the shapes
// and the orderer ticks them, with the catalogue deciding how many may be ticked:
// `multipleAllowed` already says whether a person may hold the product more than
// once, and inventing a second flag for the same question would give the catalogue
// two answers to it.

// TestTheChooserFollowsWhatTheCatalogueAllows.
func TestTheChooserFollowsWhatTheCatalogueAllows(t *testing.T) {
	body := webRegion(t, readWeb(t, "shop.js"), "function renderBasket(", "\n// --- What a product needs")
	// The exact read, not the word: the paragraph above it explains the rule and
	// would satisfy a guard that only looked for the name.
	if !strings.Contains(body, "item.multipleAllowed") {
		t.Error("the basket lets any number of shapes be chosen, or only ever one, " +
			"without asking the catalogue which — so it either offers an order the " +
			"server refuses or hides one it would accept")
	}
}

// TestAShapeIsChosenAndUnchosen.
//
// A tick that could not be untaken would make the only way out of a wrong colour
// emptying the basket.
func TestAShapeIsChosenAndUnchosen(t *testing.T) {
	src := readWeb(t, "shop.js")
	body := webRegion(t, src, "function pickShape(", "\n}")
	// The decision written down, branch by branch. A guard that only looked for
	// "push" and "splice" would be satisfied by a version that pushed on the wrong
	// side of the condition — which is what a JavaScript rule costs when the tests
	// read the source rather than run it.
	for _, half := range []struct{ needle, why string }{
		{"const at = chosen.indexOf(variant);", "nothing asks whether this shape is already taken"},
		{"chosen.splice(at, 1);", "an already chosen shape cannot be taken back"},
		{"} else if (multiple) {", "a second shape is added without asking whether the " +
			"catalogue allows the product to be held twice"},
		{"chosen.length = 0;", "choosing a second shape of a product held once leaves " +
			"the first one chosen too"},
	} {
		if !strings.Contains(body, half.needle) {
			t.Errorf("choosing a shape does not read %q: %s", half.needle, half.why)
		}
	}
}

// TestTheOrderCarriesEveryChosenShape.
//
// The answer collected on screen is a list now, and sending only its first entry
// would order one phone and show two.
func TestTheOrderCarriesEveryChosenShape(t *testing.T) {
	body := webRegion(t, readWeb(t, "shop.js"), "async function order(", "\nfunction el(")
	if strings.Contains(body, "variants[c.id] = state.variants[c.id];") {
		t.Error("the order sends one shape per product, so the second position of a " +
			"product ordered twice is dropped on the way out")
	}
	if !strings.Contains(body, "state.variants[c.id]") {
		t.Error("the order no longer carries the shapes that were chosen")
	}
}
