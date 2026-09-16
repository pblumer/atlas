package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
)

// The column that waited for its data (ADR-0360).
//
// The portal has drawn a Kategorie column since the layout landed, filled with the
// catalogue's own name and a note saying the data had none. The note is gone
// because the data is there, and these hold the three things that would quietly
// bring it back.

// webRegion returns the source between from and to.
//
// Every source guard on this package's pages reads the region it guards rather
// than the file (the collective-approval guards use it too), because a
// search for a bare name over a whole file also finds the declaration of the
// thing it is meant to prove is *used*, and then stays green when the use is
// removed. Where a marker has moved the guard stops the test rather than
// passing: a source check that cannot find its subject proves nothing, and
// silence is the one answer it must not give.
func webRegion(t *testing.T, src, from, to string) string {
	t.Helper()
	start := strings.Index(src, from)
	if start < 0 {
		t.Fatalf("portal.js no longer contains %q; this guard has lost its subject "+
			"and would otherwise pass without checking anything", from)
	}
	end := strings.Index(src[start:], to)
	if end < 0 {
		t.Fatalf("portal.js no longer contains %q after %q; this guard has lost its "+
			"subject and would otherwise pass without checking anything", to, from)
	}
	return src[start : start+end]
}

// TestThePortalGroupsByTheFieldTheReleaseCarries.
//
// Derived from the marshalled item rather than a tag somebody read once: renaming
// the Go field, the JSON tag or the portal's reader leaves the column showing one
// heading for everything, which looks exactly like a catalogue nobody has grouped.
func TestThePortalGroupsByTheFieldTheReleaseCarries(t *testing.T) {
	marker := "atlas-category-marker"
	raw, err := json.Marshal(catalog.Item{Category: marker})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	key := ""
	for k, v := range out {
		if v == marker {
			key = k
		}
	}
	if key == "" {
		t.Fatalf("no field carries the marker; the fixture has gone stale: %s", raw)
	}
	src := readWeb(t, "portal.js")
	// Both sides of the grouping: the one that collects the headings and the one
	// that decides what falls under the heading now open. Either reading a field
	// the release does not carry leaves every product under one heading.
	for _, fn := range []string{"function categoriesOf(", "function inCategory("} {
		body := webRegion(t, src, fn, "\n}")
		// The selection in state carries the same name as the field on the item, so
		// a bare search for it also matches a function that only ever reads the
		// selection. Blind that one out, or the guard passes without a read.
		body = strings.ReplaceAll(body, "state."+key, "state.<the open heading>")
		if !strings.Contains(body, "."+key) {
			t.Errorf("the release spells the heading %q and %s never reads it, so "+
				"every product sits under one heading", key, strings.TrimSuffix(fn, "("))
		}
	}
}

// TestTheColumnNoLongerSaysTheDataIsMissing.
//
// It said "Atlas has no category level above the bundle today". That was true and
// is not. A note that outlives its cause is worse than none: it tells somebody
// looking at their own headings that the feature does not exist.
func TestTheColumnNoLongerSaysTheDataIsMissing(t *testing.T) {
	src := readWeb(t, "portal.js")
	if strings.Contains(src, "note.noCategories") {
		t.Error("the portal still carries the note saying Atlas has no category, which " +
			"now contradicts the column beside it")
	}
	// Read the column's own construction, not the file: the same two labels are
	// spelled again in the services view, so a file-wide search stays green when
	// the column stops using them.
	col := webRegion(t, src, "const headings = categoriesOf(rel);", "const bundleCol")
	if !strings.Contains(col, "t('cat.all')") {
		t.Error("the column has no row that clears the selection, so a heading opened " +
			"by accident cannot be closed again")
	}
	if !strings.Contains(col, "t('cat.none')") {
		t.Error("products carrying no heading sit under an unnamed row nobody can " +
			"click with confidence")
	}
}

// TestAHeadingHasNoOrderingOfItsOwn.
//
// The decision, visible as an absence. A rank on a category is the entity the
// decision refused, arriving through the back door — so the sort is the locale's
// and nothing else.
func TestAHeadingHasNoOrderingOfItsOwn(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "function categoriesOf(", "\n}")
	if !strings.Contains(body, "localeCompare") {
		t.Error("the headings are not sorted by the locale's own rule, so their order " +
			"is whatever the catalogue happened to store")
	}
	for _, rank := range []string{".rank", "categoryRank", "sortOrder"} {
		if strings.Contains(body, rank) {
			t.Errorf("the headings are ordered by %q. A category with a rank is the "+
				"entity this decision refused; if it is wanted, it arrives as one", rank)
		}
	}
}
