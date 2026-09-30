package api

import (
	"strings"
	"testing"
)

// Where the product editor opens.
//
// It first rendered under the whole list, which is fine with three products and
// unusable with forty; then in a column beside the list, level with its row, which on
// a wide screen was a third of the page and scrolled inside itself. It now opens
// across the list's width: an existing product in a row directly under its own, a new
// product under the list's buttons, where the two panel containers live.
//
// The geometry itself is proven in the browser (e2e/catalog-editor-layout.spec.mjs),
// which is the only place a bounding box exists. What is held here is what that
// geometry rests on.

// TestTheProductPanelsLiveUnderTheList.
//
// The containers sit in the list's own flow, after its buttons: that is where a new
// product opens, and where an edited product's panel goes back to when it closes.
func TestTheProductPanelsLiveUnderTheList(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	page := webRegion(t, src, `<div class="product-list">`, "How the products relate")

	for _, want := range []struct{ frag, what string }{
		{`<div class="product-panels">`, "the place the panels live"},
		{`<div class="product-editor"></div>`, "the product's form"},
		{`<div class="assemble-editor"></div>`, "the kit, in the same place"},
		{`<div class="product-table">`, "the table's own scroll box"},
		{`data-act="new-product"`, "the button that opens an empty form"},
	} {
		if !strings.Contains(page, want.frag) {
			t.Errorf("the products section no longer carries %s (%q)", want.what, want.frag)
		}
	}
	if strings.Index(page, `data-act="new-product"`) > strings.Index(page, `class="product-panels"`) {
		t.Error("the panels are emitted before the list's buttons, so a new product's form " +
			"opens above the button that asked for it")
	}
	if strings.Contains(src, "product-side") || strings.Contains(src, "--editor-top") {
		t.Error("the product list is laid out beside a column again; the form opens across " +
			"its width, under the row or the buttons")
	}
	// One row, one answer open: whichever panel is opened empties the other.
	open := webRegion(t, src, "const openPanel = (into, html, row) => {", "\n  };")
	if !strings.Contains(open, `editor.innerHTML = ""`) || !strings.Contains(open, `assembler.innerHTML = ""`) {
		t.Error("opening a panel no longer empties the other, so one row can have both the " +
			"form and the kit open")
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
	if n := strings.Count(src, `class="cat-cols"`); n < 3 {
		t.Errorf("only %d of the page's lists are paired with their form; the catalogues, the "+
			"relations and the maintainers are all that shape", n)
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

// TestTheEditorCardStatesNoWidthOfItsOwn.
//
// The card is read in two places — in a row under its product, and under the list's
// buttons — and only the stylesheet knows which. An inline width or margin wins over
// both, for reasons nobody can then find in the CSS.
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

// TestAProductOpensUnderItsOwnRow.
//
// An existing product opens in a row of its own directly under it, across the table's
// width. The row is a detail row for the shared table enhancer, which is what keeps it
// under its product when the list is sorted and hides it with its product when a
// filter removes that. The geometry is proven in e2e/catalog-editor-layout.
func TestAProductOpensUnderItsOwnRow(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	open := webRegion(t, src, "const openPanel = (into, html, row) => {", "\n  };")
	for _, want := range []struct{ frag, what string }{
		{`detailRow.setAttribute("data-dt-detail", "")`, "the table enhancer's mark for a row that belongs to the one above it"},
		{`cell.colSpan = anchor.cells.length`, "the table's whole width"},
		{`anchor.after(detailRow)`, "the place directly under the row"},
		{`cell.appendChild(into)`, "the panel moved into that row"},
	} {
		if !strings.Contains(open, want.frag) {
			t.Errorf("opening a product no longer uses %s (%q)", want.what, want.frag)
		}
	}
	// Closing puts the containers back where the page rendered them, or the next new
	// product opens its form into a row that no longer exists.
	if !strings.Contains(src, "home.append(editor, assembler)") {
		t.Error("closing a panel no longer returns the containers to where they live")
	}
	// A sort appends every row again: the panel is put back under its product.
	if !strings.Contains(src, "if (anchor.nextElementSibling !== detailRow) anchor.after(detailRow);") {
		t.Error("nothing keeps the form under its product once the list has been re-ordered")
	}
}

// TestTheProductFormFoldsAndCanPublish.
//
// The form is long, so its sections fold and a folded one stays folded for the next
// product; a required field inside a folded section opens it rather than stopping
// the save with a message pointing at nothing. Beside Save Draft, Save & Publish
// freezes the catalogue straight after the save and carries a refusal across the
// reload to where the Publish button reports its own.
func TestTheProductFormFoldsAndCanPublish(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	form := webRegion(t, src, "function productForm(", "\n}")
	for _, key := range []string{"shows", "filing", "offer", "order", "fulfil"} {
		if !strings.Contains(form, `group("`+key+`", `) {
			t.Errorf("the form no longer draws the %q section as one that folds", key)
		}
	}
	for _, want := range []string{`data-publish="no">Save Draft</button>`, `data-publish="yes"`, `data-act="fold-sections"`} {
		if !strings.Contains(form, want) {
			t.Errorf("the form no longer carries %q", want)
		}
	}
	for _, want := range []string{
		`localStorage.setItem(SECTIONS_KEY`,
		`if (d && !d.open) d.open = true;`,
		`e.submitter && e.submitter.dataset.publish === "yes"`,
		"carriedRefusal = { catalog: id, err: pubErr };",
		"report.innerHTML = refusalCard(carriedRefusal.err);",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("catalog-admin.js no longer carries %q", want)
		}
	}
}

// TestTheProductFormSaysWhatPublishingNeeds.
//
// Save & Publish on a product that lacks what publishing checks saves it and is then
// refused. The form states the product's half of those checks up front and keeps the
// list current, so the rules are learned beside the fields rather than from a refusal.
// The checks it mirrors are catalog.Publish's; if one is added there and not here, the
// list is incomplete but not wrong — which is why it says publishing checks more.
func TestTheProductFormSaysWhatPublishingNeeds(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	if !strings.Contains(webRegion(t, src, "function productForm(", "\n}"), `<div class="publish-needs"`) {
		t.Fatal("the product form no longer carries the list of what publishing needs")
	}
	needs := webRegion(t, src, "function publishNeeds(form) {", "\n}")
	for _, want := range []string{`val("id")`, `[name^="t-"]`, `val("state") === "active"`,
		`val("provisionProcess")`, `val("deprovisionProcess")`, `val("opProvision")`,
		`val("opDeprovision")`, `kind === "fixed" || kind === "role"`} {
		if !strings.Contains(needs, want) {
			t.Errorf("publishNeeds no longer checks %s", want)
		}
	}
	wire := webRegion(t, src, "function wireProductForm() {", "\n  }\n")
	if !strings.Contains(wire, `pform.addEventListener("input", refresh)`) {
		t.Error("the list is drawn once and not kept current while the form is filled in")
	}
}
