package api

import (
	"strings"
	"testing"
)

// The two headings, read in the language the portal is read in
// (ADR-0412).
//
// ADR-0360 shipped the category and the product group as one string each and
// recorded "no translation" as a stated cost. A catalogue kept in four languages
// translated every product name and then filed them all under a German word.
//
// The shape this is paid off in is what these guards hold, and it is the half that
// is easy to lose: the string on the product stays the KEY — what the cascade
// groups by, what a search hit sets, what a published release already holds — and
// the map beside it is only how that key is WORDED. Grouping by the wording would
// make one category two the moment two products agreed in German and differed in
// French, which is a split nobody publishing it can see.

// TestAHeadingIsGroupedByItsKeyAndShownByItsWording.
func TestAHeadingIsGroupedByItsKeyAndShownByItsWording(t *testing.T) {
	src := readWeb(t, "shop.js")
	body := webRegion(t, src, "function headingsOf(", "\n}")

	// Collected under the key. The map is keyed by it and the entries carry the
	// wording beside it, rather than the wording being what distinguishes them.
	if !strings.Contains(body, "wording.set(key,") {
		t.Error("the headings are no longer collected under the key, so two products " +
			"whose wording differs in one language are two categories in that " +
			"language and one in every other")
	}
	if !strings.Contains(body, "{ key, text }") {
		t.Error("a collected heading no longer carries the key beside the wording; " +
			"a column that only has the wording can only select by it, and the " +
			"selection is what the release is filtered by")
	}

	// The filters keep reading the raw field, because that field IS the key.
	for _, fn := range []string{"function inCategory(", "function inGroup("} {
		f := webRegion(t, src, fn, "\n}")
		if strings.Contains(f, "Texts") {
			t.Errorf("%s filters on the wording; the wording is what a reader sees "+
				"and the key is what a product is filed under, so a catalogue "+
				"translated into the reader's language would filter to nothing", fn)
		}
	}
}

// TestAnUntranslatedHeadingStillReads.
//
// The installed base is in exactly this state: every product written before this
// field exists carries a key and no wordings, and so does every product in a
// single-language catalogue. A reader with no fallback would meet an empty column
// head for all of them — the whole catalogue filed under a blank.
func TestAnUntranslatedHeadingStillReads(t *testing.T) {
	body := webRegion(t, readWeb(t, "shop.js"), "function headingOf(", "\n}")
	if !strings.Contains(body, "textOf(") {
		t.Error("the heading is not read through textOf, so it reaches a reader by a " +
			"rule of its own rather than the one every other text on this page " +
			"follows — including the fall-through that stops a catalogue kept in " +
			"German and French from showing an English reader nothing at all")
	}
	if !strings.Contains(body, "`${field}Texts`") {
		t.Error("the heading no longer reads the wordings beside the key at all")
	}
	// The key is the fallback and not the empty string. textOf's last resort is
	// whatever it is handed, so handing it the key is what makes an untranslated
	// heading render.
	if !strings.Contains(body, "textOf((item || {})[`${field}Texts`], key)") {
		t.Error("the key is no longer what an untranslated heading falls back to; " +
			"every product written before this field would file under a blank")
	}
}

// TestTheSelectionIsTheKeyEverywhereItIsSet.
//
// The one place outside the columns that writes the selection is a search hit,
// which opens the cascade at the product's own two headings. Writing the wording
// there would open a column whose own selection excludes it: the filters compare
// against the key, so the cascade would show nothing under a heading the reader
// just clicked.
func TestTheSelectionIsTheKeyEverywhereItIsSet(t *testing.T) {
	src := readWeb(t, "shop.js")
	hit := webRegion(t, src, "function renderSearch(", "\n}")
	for _, want := range []string{
		"state.category = (item.category || '').trim();",
		"state.group = (item.productGroup || '').trim();",
	} {
		if !strings.Contains(hit, want) {
			t.Errorf("a search hit no longer sets the selection from the product's own "+
				"key (%q); opened on the wording, the cascade filters to nothing", want)
		}
	}
}
