package api

import (
	"strings"
	"testing"
)

// The info panel is reachable from every column that carries a product.
//
// It was drawn on services alone, and the result was not a missing feature but an
// inconsistency inside one page: the panel already worked for a bundle — the
// search opens it, and the services view has carried the button on all four levels
// since it was built — so a maintainer could write a price onto a bundle, see it
// in "my services", find it through the search, and not reach it from the column
// the bundle lives in.
//
// The guards read the region they guard rather than the file: `infoButton` appears
// in three separate views, so a search across shop.js would find it whatever the
// cascade did.

// TestEveryColumnOfTheCascadeOpensWhatItHolds.
//
// A column whose rows cannot be opened is a column whose products carry facts
// nobody can read — the price among them, which exists to be read and nothing
// else.
func TestEveryColumnOfTheCascadeOpensWhatItHolds(t *testing.T) {
	src := readWeb(t, "shop.js")
	// The two columns that hold products. The two to their left hold headings —
	// a category and a product group are strings a product writes on itself, not
	// things with a price or an approval rule, so there is nothing for a panel to
	// open about them.
	//
	// The bundle column is gone with the level: a bundle is offered as a
	// Marktleistung, so the products column is the offering column.
	for _, col := range []struct{ name, from, to string }{
		{"offering", "const offeringCol = ", "const serviceCol = "},
		{"service", "const serviceCol = ", "\n  // Favourites this catalogue does not carry"},
	} {
		body := webRegion(t, src, col.from, col.to)
		if !strings.Contains(body, "infoButton(") {
			t.Errorf("the %s column has no way to open what it holds, so a price written "+
				"on one is stored and never shown", col.name)
		}
	}
}

// TestThePanelItselfAsksNothingAboutTheLevel.
//
// The reason the button could simply be added: the panel reads what any product
// carries — its id, its texts, its price, its approval, whether it repeats — and
// none of that is a service's rather than a bundle's. A panel that branched on the
// level would be a second thing to keep true, and the first place it would go
// wrong is the level nobody clicks.
func TestThePanelItselfAsksNothingAboutTheLevel(t *testing.T) {
	body := webRegion(t, readWeb(t, "shop.js"), "function infoPanel(", "\n}")
	for _, level := range []string{"bundle", "offering", "integral", "depthOf", "levelsOf"} {
		if strings.Contains(body, level) {
			t.Errorf("the panel reads %q, so it says something different depending on "+
				"which column was clicked", level)
		}
	}
	// And it does read the price, which is the fact this measure exists to reach.
	if !strings.Contains(body, "item.price") {
		t.Error("the panel no longer shows what a product costs")
	}
}

// TestEveryViewThatOffersThePanelAlsoDrawsIt.
//
// The "i" sets state.info and redraws. Whether anything appears is a second,
// separate statement — the view has to render the panel — and the basket made the
// first without the second. The button was there, it responded, and nothing
// opened: on the one screen where somebody is deciding whether to actually order
// the thing, the price, the approval rule, the description and the picture were
// unreachable.
//
// Guarded per view rather than per file, because infoPanel appears three times and
// a search across shop.js would find it however many views had forgotten it.
func TestEveryViewThatOffersThePanelAlsoDrawsIt(t *testing.T) {
	src := readWeb(t, "shop.js")
	for _, view := range []struct{ name, from, to string }{
		{"the catalogue", "function renderCatalogue(", "\nfunction renderBasket("},
		{"the basket", "function renderBasket(", "\n// --- What a product needs"},
		{"what somebody holds", "const row = (id) => {", "\n// The brand mark"},
	} {
		body := webRegion(t, src, view.from, view.to)
		if !strings.Contains(body, "infoButton(") {
			continue // a view with no button owes no panel
		}
		if !strings.Contains(body, "infoPanel(") {
			t.Errorf("%s draws the info button and never the panel, so pressing it "+
				"does nothing at all — the control answers and the answer is blank",
				view.name)
		}
	}
}
