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
// in three separate views, so a search across portal.js would find it whatever the
// cascade did.

// TestEveryColumnOfTheCascadeOpensWhatItHolds.
//
// A column whose rows cannot be opened is a column whose products carry facts
// nobody can read — the price among them, which exists to be read and nothing
// else.
func TestEveryColumnOfTheCascadeOpensWhatItHolds(t *testing.T) {
	src := readWeb(t, "portal.js")
	for _, col := range []struct{ name, from, to string }{
		{"bundle", "const bundleCol = ", "const offeringCol = "},
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
	body := webRegion(t, readWeb(t, "portal.js"), "function infoPanel(", "\n}")
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
