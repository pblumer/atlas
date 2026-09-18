package api

import (
	"strings"
	"testing"
)

// The two upper columns, on both sides of the portal.
//
// ADR-0360 promises one answer to "where do I find this" in the catalogue and in
// what a person already holds. The two screens read the same two fields and read
// them off different products: the cascade collects them from the products nothing
// contains, and the services view read them straight off each held id.
//
// Those are not the same set. Both strings are attributes of the offering
// (ADR-0383) — a service two edges down has never had a heading of its own — so a
// person holding services saw "Ohne Kategorie" for things the catalogue filed
// under a real heading. Nothing looked broken; the column was simply empty for the
// products people actually hold.

// TestWhatSomebodyHoldsIsFiledUnderTheHeadingTheyOrderedItUnder.
//
// The fix is one rule read in one place, so the guard is that the column goes
// through it and resolves the offering first.
func TestWhatSomebodyHoldsIsFiledUnderTheHeadingTheyOrderedItUnder(t *testing.T) {
	src := readWeb(t, "portal.js")

	body := webRegion(t, src, "function headingsHeld(", "\n}")
	if !strings.Contains(body, "rootOf(") {
		t.Error("the column reads the heading off the held item instead of off the " +
			"product it belongs to, so every service a person holds sits under the " +
			"bucket for products that carry none")
	}
	// The bucket last and only when something is in it, as the catalogue's own two
	// columns do it. A heading for nothing is a heading nobody can use, and the two
	// screens disagreeing about where the bucket sits is the smaller version of the
	// disagreement this whole guard is about.
	if !strings.Contains(body, "localeCompare") {
		t.Error("the headings are not sorted by the locale's own rule, so the two " +
			"screens order the same strings differently")
	}

	// And the view takes that answer rather than keeping its own reading beside it.
	// Held by the region and not the file: the catalogue's columns legitimately read
	// both fields, so a file-wide search proves nothing here.
	view := webRegion(t, src, "function renderServices(", "\n}")
	// The column head is looked up by a key that ends in the field's own name, so a
	// bare search for the field also matches the label. Blind it out, or the guard
	// can never go green.
	view = strings.ReplaceAll(view, "t('col.category')", "t(<the column head>)")
	for _, field := range []string{".category", ".productGroup"} {
		if strings.Contains(view, field) {
			t.Errorf("the services view still reads %s itself; the heading of a held "+
				"service is the offering's, and a second reading is how the two screens "+
				"came to disagree", field)
		}
	}
	for _, want := range []string{"headingsHeld(rel, by, ids, 'category')",
		"headingsHeld(rel, by, ids, 'productGroup')"} {
		if !strings.Contains(view, want) {
			t.Errorf("the services view does not build a column with %s, so this guard "+
				"has lost its subject", want)
		}
	}
}
