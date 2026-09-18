package api

import (
	"regexp"
	"strings"
	"testing"
)

// The product editor opens beside the list, not under it.
//
// It used to render under the product table, which is fine with three products and
// unusable with forty: opening a row near the bottom put the form below everything
// offered, so it was read after a long scroll and with no sight of the row it
// belonged to. The list and the editor are therefore two columns, and the panel is
// pushed down to the row it was opened from.
//
// The geometry itself is proven in the browser (e2e/catalog-editor-layout.spec.mjs),
// which is the only place a bounding box exists. What is held here is what that
// geometry rests on — a structure and an offset that a tidy-up could take out
// without anything failing in the Go suite.

// TestTheProductEditorIsAColumnBesideTheList.
//
// The markup is the layout: two columns only exist if the editor is a sibling of the
// list rather than a block after it. An editor moved back below the table would still
// render, still save, and silently be the page this change was made to replace.
func TestTheProductEditorIsAColumnBesideTheList(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	page := webRegion(t, src, `<div class="product-cols cat-cols">`, "How the products relate")

	for _, want := range []struct{ frag, what string }{
		{`<div class="product-list cat-main">`, "the list column"},
		{`<aside class="product-side cat-side">`, "the panel column beside it"},
		{`<div class="product-editor"></div>`, "the product's form in that column"},
		{`<div class="assemble-editor"></div>`, "the kit in the same column, beside the same row"},
		{`<div class="product-table">`, "the table's own scroll box, so it cannot run under the panel"},
		{`data-act="new-product"`, "the button that opens an empty form"},
	} {
		if !strings.Contains(page, want.frag) {
			t.Errorf("the products section no longer carries %s (%q), so the editor is not a "+
				"column beside the list", want.what, want.frag)
		}
	}
	// One row, one answer open. The two panels share a column, so whichever is opened
	// has to empty the other — otherwise the second stacks under the first, and the one
	// the reader is looking at is no longer level with anything.
	open := webRegion(t, src, "const openPanel = (into, html, row) => {", "\n  };")
	if !strings.Contains(open, `editor.innerHTML = ""`) || !strings.Contains(open, `assembler.innerHTML = ""`) {
		t.Error("opening a panel no longer empties the other, so one row can have both the " +
			"form and the kit open in one column")
	}

	// The editor's index in the section says which side it is on: a panel emitted
	// before the list is a column, and the wrong one.
	if strings.Index(page, `class="product-list"`) > strings.Index(page, `class="product-side"`) {
		t.Error("the editor column is emitted before the list, so it opens to its left")
	}
}

// TestEveryListOnTheCataloguePageCarriesItsFormBesideIt.
//
// The page is four of one shape: a list, and the form that acts on it. The
// catalogues and the one being created, the products and the panel that edits them,
// the relations and the pair being related, the maintainers and the one being added.
// Stated once in .cat-cols and marked per section, so a fifth of them cannot invent
// a fifth layout — and so a form that is quietly dropped back under its list fails
// here rather than in somebody's afternoon.
func TestEveryListOnTheCataloguePageCarriesItsFormBesideIt(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	if n := strings.Count(src, `class="cat-cols"`) + strings.Count(src, `class="product-cols cat-cols"`); n < 4 {
		t.Errorf("only %d of the page's lists are paired with their form; the catalogues, the "+
			"products, the relations and the maintainers are all that shape", n)
	}
	if a, b := strings.Count(src, `class="cat-main"`), strings.Count(src, `class="cat-side"`); a < 3 || b < 3 {
		t.Errorf("%d halves are marked as the list and %d as the form beside it; a pair needs "+
			"both, and a column with only one half is a column of nothing", a, b)
	}
	// The forms that moved into a column state no width of their own any more, for the
	// reason the product editor's card does not: only the stylesheet knows which of the
	// two layouts is in force, and an inline style wins over both.
	for _, form := range []string{`class="edge-new card"`, `class="share-new card"`} {
		i := strings.Index(src, form)
		if i < 0 {
			t.Fatalf("the page no longer carries %s; this guard has lost its subject", form)
		}
		if tag := src[i : i+strings.Index(src[i:], ">")]; strings.Contains(tag, "style=") {
			t.Errorf("%s spells a style of its own, which overrides both layouts it is read in", form)
		}
	}
}

// TestTheCataloguePageDropsTheCentredColumn.
//
// Two columns need a page to put them on. The console's default content column is
// 1120px wide, which divides into a table of products and a form of about 520px each
// — both narrower than what they hold. The catalogue's own page therefore drops the
// centred column the way the Tasks inbox does, and both halves of that are held here:
// the class app.js puts on the body for this route, and the rule app.css hangs off it.
// Either one alone is a page that silently goes back to 1120px.
func TestTheCataloguePageDropsTheCentredColumn(t *testing.T) {
	js := readWeb(t, "app.js")
	if !strings.Contains(js, `classList.toggle("catalog-mode", route.startsWith("#/catalog"))`) {
		t.Error("the catalogue page no longer asks for the wide layout, so its two columns " +
			"share the default content column")
	}
	if !strings.Contains(readWeb(t, "app.css"), ".catalog-mode main { max-width: none; }") {
		t.Error("nothing acts on catalog-mode any more, so the class is set and ignored")
	}
}

// TestTheCataloguePageTakesTheSharedActionColumn.
//
// A full-width table puts a lot of page between the last column of data and the right
// edge, and a left-aligned action cell leaves its buttons stranded in the middle of
// it. The console already answers this — td.row-actions, right-aligned and on one
// line — and the catalogue's three tables take that class rather than aligning by
// hand, so they keep following it when it changes.
func TestTheCataloguePageTakesTheSharedActionColumn(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	if n := strings.Count(src, `<td class="row-actions"`); n < 4 {
		t.Errorf("only %d action cells take the shared class; the products table, the two "+
			"relation tables and the member list all end in one", n)
	}
	// The class is the whole point: an alignment spelled here is a fourth copy of a
	// decision the console already made once.
	form := webRegion(t, src, "function productRow(", "\n}")
	if strings.Contains(form, "text-align") {
		t.Error("a product row aligns its own cells instead of taking td.row-actions")
	}
}

// TestTheEditorOpensLevelWithItsRow.
//
// The offset is the feature, and it cannot be a stylesheet's: the shared table
// enhancer sorts and filters the tbody, so only the DOM knows where a row ended up.
// The script measures it and hands it over; app.css reads it. Both halves are held
// here because either one alone is a panel that opens at the top of the list.
func TestTheEditorOpensLevelWithItsRow(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	if !strings.Contains(src, `side.style.setProperty("--editor-top"`) {
		t.Fatal("nothing measures where the row is any more, so the panel opens at the top of " +
			"the list whichever product was clicked")
	}
	if !strings.Contains(src, "new ResizeObserver(realign)") {
		t.Error("the alignment is never re-measured, so a resized window leaves the panel " +
			"pointing at a row that has moved")
	}
	if !strings.Contains(src, "window.scrollBy(0, moved)") {
		t.Error("nothing holds the row still while the list reflows, so opening the panel " +
			"pushes the row that was clicked off the reader's screen")
	}

	css := readWeb(t, "app.css")
	if !strings.Contains(css, "margin-top: var(--editor-top, 0px);") {
		t.Error("the stylesheet no longer reads --editor-top, so the measured offset is taken " +
			"and thrown away")
	}
	if !strings.Contains(css, "overflow-anchor: none") {
		t.Error("the browser's own scroll anchoring is back on beside the script's correction; " +
			"two mechanisms undoing one shift each undo part of it")
	}
	// The stacked layout is the default and the two columns are the enhancement, so a
	// viewport that never matches reads exactly the page it read before. A max-width
	// rule here would invert that and make the narrow screen the special case.
	if !regexp.MustCompile(`@media \(min-width: \d+px\) \{[^@]*\.product-cols \{`).MatchString(css) {
		t.Error("the two-column layout is no longer introduced by a min-width rule, so the " +
			"narrow screen is no longer the fallback")
	}
}

// TestTheEditorCardStatesNoWidthOfItsOwn.
//
// The card is read in two layouts — beside the list, and stacked under it on a narrow
// screen — and only the stylesheet knows which is in force. An inline width or margin
// wins over both, which is how the panel ends up 14px out of line with its row for
// reasons nobody can find in the CSS.
func TestTheEditorCardStatesNoWidthOfItsOwn(t *testing.T) {
	form := webRegion(t, readWeb(t, "catalog-admin.js"), "function productForm(", "\n}")
	open := strings.Index(form, `return `+"`"+`<div class="card`)
	if open < 0 {
		t.Fatal("the product form no longer opens with a card; this guard has lost its subject")
	}
	// The opening tag alone: what follows it is the card's contents, and a heading with
	// a margin of its own is neither this guard's business nor a layout decision.
	head := form[open:]
	if end := strings.Index(head, ">"); end >= 0 {
		head = head[:end]
	}
	for _, bad := range []string{"max-width", "margin"} {
		if strings.Contains(head, bad) {
			t.Errorf("the editor card spells its own %s inline, which overrides both layouts "+
				"it is rendered in", bad)
		}
	}
}
