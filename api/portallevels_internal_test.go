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
	src := readWeb(t, "shop.js")
	// The two views that *derive* a level. The cascade is no longer one of them, and
	// that is the point rather than a gap: its columns are the levels — the third
	// holds the products, the fourth everything behind the one chosen — so a row's
	// position says what it is and nothing has to be worked out per row. Asking it
	// to call levelOf anyway would be asking it to re-derive what it just laid out.
	//
	// The other two get a set of positions with no layout to read the level off, so
	// they ask, and they ask the same function.
	for _, view := range []struct{ name, from, to string }{
		{"the basket", "function renderBasket(", "\n// --- What a product needs"},
		// The view that made "both" the wrong word: what somebody holds was laid out
		// across the same columns and was reading depth for itself.
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

// TestTheLevelIsReadOffDepthAndNothingElse.
//
// This replaces TestAProductWithNoPartsIsNotABundle, and the replacement is the
// point rather than a retreat. That guard pinned `hasParts ? 'bundle' : 'offering'`
// — the rule that decided whether a root was a bundle. There is no Bundle level any
// more: a bundle is offered as a Marktleistung, so every root is one and what
// stands behind it are services. The case that guard protected (a single product
// announced as something made of other things) cannot occur, because the word it
// was announced with is gone. TestNothingIsCalledABundleAnyMore pins that.
//
// What still needs pinning is that the level is depth and nothing else — not the
// kind of edge, not whether the product has parts, not how it was reached.
func TestTheLevelIsReadOffDepthAndNothingElse(t *testing.T) {
	body := webRegion(t, readWeb(t, "shop.js"), "function levelOf(", "\n}")
	if !strings.Contains(body, "depthOf(") {
		t.Error("the level is worked out without asking how deep the position sits, " +
			"which is the one thing it is")
	}
	for _, field := range []string{"includes", "options", "integral"} {
		if strings.Contains(body, field) {
			t.Errorf("the level rule reads %q. Whether a part comes with the whole or "+
				"is offered beside it is a different question from where it sits, and "+
				"answering the second with the first is what gave one position two "+
				"names", field)
		}
	}
}
