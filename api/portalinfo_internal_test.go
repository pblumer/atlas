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
		{"what somebody holds", "function renderServices(", "\n// The brand mark"},
	} {
		body := webRegion(t, src, view.from, view.to)
		if !strings.Contains(body, "infoButton(") {
			continue // a view with no button owes no panel
		}
		if !strings.Contains(body, "infoPanel(") && !strings.Contains(body, "infoDialog(") {
			t.Errorf("%s draws the info button and never the panel, so pressing it "+
				"does nothing at all — the control answers and the answer is blank",
				view.name)
		}
	}
}

// TestTheInfoPanelOpensAsADialog: the "i" opens its panel over the page, where
// the person pressing it is looking, and not under the last row of a long
// catalogue. The dialog says what it is to a screen reader, closes on its own
// button and on Escape, and every view that draws the panel draws it this way.
func TestTheInfoPanelOpensAsADialog(t *testing.T) {
	src := readWeb(t, "shop.js")
	dialog := webRegion(t, src, "function infoDialog(", "\n}")
	for _, want := range []string{"role: 'dialog'", "'aria-modal': 'true'", "t('info.close')", "infoPanel(rel, item)",
		"e.target === e.currentTarget"} {
		if !strings.Contains(dialog, want) {
			t.Errorf("infoDialog lacks %s", want)
		}
	}
	if !strings.Contains(src, "e.key === 'Escape' && state.info") {
		t.Error("Escape does not close the info dialog")
	}
	for _, view := range []struct{ name, from, to string }{
		{"the catalogue", "function renderCatalogue(", "\nfunction renderBasket("},
		{"the basket", "function renderBasket(", "\n// --- What a product needs"},
		{"what somebody holds", "function renderServices(", "\n// The brand mark"},
	} {
		body := webRegion(t, src, view.from, view.to)
		if strings.Contains(body, "infoPanel(") {
			t.Errorf("%s still draws the panel inline instead of as a dialog", view.name)
		}
	}
	if n := strings.Count(src, "'info.close':"); n != 4 {
		t.Errorf("info.close is translated %d times, want once per locale (de, en, fr, it)", n)
	}
	if !strings.Contains(readWeb(t, "shop.html"), ".info-backdrop") {
		t.Error("shop.html has no style for the dialog's backdrop")
	}
}

// TestAMarketOfferingsInfoIsItsFactSheet: the "i" of a product with parts — a
// market offering, and only that — opens a fact sheet: the offering's
// description, then the included services' descriptions, then the optional ones',
// in that order, with a way to copy it and to print it alone.
func TestAMarketOfferingsInfoIsItsFactSheet(t *testing.T) {
	src := readWeb(t, "shop.js")
	panel := webRegion(t, src, "function infoPanel(", "\n}")
	if !strings.Contains(panel, "if (carried.length || offered.length) return factsheet(rel, item);") {
		t.Error("the info panel does not hand a market offering to its fact sheet")
	}
	sheet := webRegion(t, src, "function factsheet(", "\n}")
	offering := strings.Index(sheet, "t('factsheet.offering')")
	included := strings.Index(sheet, "t('factsheet.included')")
	optional := strings.Index(sheet, "t('factsheet.optional')")
	if offering < 0 || included < offering || optional < included {
		t.Errorf("the fact sheet's sections are not offering, included, optional in that order (%d, %d, %d)",
			offering, included, optional)
	}
	for _, want := range []string{"copyText(factsheetText(rel, item))", "printFactsheet", "descriptionView(p,",
		"factsheet-logo", "t('factsheet.version')", "class: 'secondary'"} {
		if !strings.Contains(sheet, want) {
			t.Errorf("the fact sheet lacks %s", want)
		}
	}
	text := webRegion(t, src, "function factsheetText(", "\n}")
	if strings.Index(text, "'includes'") > strings.Index(text, "'options'") {
		t.Error("the copied fact sheet does not list the included services before the optional ones")
	}
	for _, key := range []string{"factsheet.offering", "factsheet.included", "factsheet.optional", "factsheet.copy",
		"factsheet.print", "factsheet.copied", "factsheet.copyFailed", "factsheet.noDescription", "factsheet.version"} {
		if n := strings.Count(src, "'"+key+"':"); n != 4 {
			t.Errorf("%s is translated %d times, want once per locale (de, en, fr, it)", key, n)
		}
	}
	if !strings.Contains(readWeb(t, "shop.html"), "body.printing-factsheet .factsheet") {
		t.Error("shop.html has no print style that puts the fact sheet alone on the paper")
	}
}

// TestARichDescriptionIsDrawnAsTextAndNeverAsMarkup: the shop draws the wiki's
// layout from data — every word a text node through el(), never innerHTML — and an
// image only from Atlas's own copy, by an id of the shape the server names images
// with, so neither a wiki author nor a forged release can make the shop load
// anything else.
func TestARichDescriptionIsDrawnAsTextAndNeverAsMarkup(t *testing.T) {
	src := readWeb(t, "shop.js")
	draw := webRegion(t, src, "function richBlocks(", "\n}\n")
	for _, never := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "DOMParser", "href"} {
		if strings.Contains(draw, never) {
			t.Errorf("the rich description is drawn with %s", never)
		}
	}
	if !strings.Contains(draw, "/content-images/") || !strings.Contains(draw, "/^[0-9a-f]{32}$/.test(b.image") {
		t.Error("an image is drawn from somewhere other than Atlas's own copy, or by an id of any shape")
	}
	if !strings.Contains(draw, "RICH_ALIGNS.includes(b.align)") {
		t.Error("an alignment reaches a style attribute without being one of the known ones")
	}
}
