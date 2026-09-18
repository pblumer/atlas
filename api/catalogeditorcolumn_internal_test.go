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
	page := webRegion(t, src, `<div class="product-cols">`, "How the products relate")

	for _, want := range []struct{ frag, what string }{
		{`<div class="product-list">`, "the list column"},
		{`<aside class="product-editor"></aside>`, "the editor column beside it"},
		{`<div class="product-table">`, "the table's own scroll box, so it cannot run under the panel"},
		{`data-act="new-product"`, "the button that opens an empty form"},
	} {
		if !strings.Contains(page, want.frag) {
			t.Errorf("the products section no longer carries %s (%q), so the editor is not a "+
				"column beside the list", want.what, want.frag)
		}
	}
	// The editor's index in the section says which side it is on: a panel emitted
	// before the list is a column, and the wrong one.
	if strings.Index(page, `class="product-list"`) > strings.Index(page, `class="product-editor"`) {
		t.Error("the editor column is emitted before the list, so it opens to its left")
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
	if !strings.Contains(js, `classList.toggle("catalog-mode", route.startsWith("#/catalog/c/"))`) {
		t.Error("the catalogue page no longer asks for the wide layout, so its two columns " +
			"share the default content column")
	}
	if !strings.Contains(readWeb(t, "app.css"), ".catalog-mode main { max-width: none; }") {
		t.Error("nothing acts on catalog-mode any more, so the class is set and ignored")
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
	if !strings.Contains(src, `editor.style.setProperty("--editor-top"`) {
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
