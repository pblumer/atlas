package api

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
)

// The authoring screen against what the server actually does.
//
// Two statements on that page were about server behaviour, and both were wrong in
// the same way: written once from memory, never held against the code, and no test
// between them.

// TestTheAudienceFieldDoesNotPromiseEverybody.
//
// It said "Audience (groups, empty means everybody)" and listed an empty audience
// as "everybody". [catalog.Catalog.ReachedBy] returns false for an empty group
// list: empty reaches **nobody**, deliberately, because the dangerous default is
// the one where a catalogue somebody is still filling is already open to all.
//
// So an operator created a catalogue, was told it was open to everybody, and every
// visitor read "no catalogue is assigned to you" — with the one screen that could
// have explained it saying the opposite. The behaviour is right; the sentence was
// the defect.
func TestTheAudienceFieldDoesNotPromiseEverybody(t *testing.T) {
	// Asked of the code rather than assumed, so that a future decision to make an
	// empty audience mean everybody fails here instead of leaving the label wrong
	// in the other direction.
	if (catalog.Catalog{}).ReachedBy([]string{"grp_any"}) {
		t.Skip("an empty audience now reaches somebody; this test's premise is gone")
	}

	src := readWeb(t, "catalog-admin.js")
	if strings.Contains(src, "empty means everybody") {
		t.Error("the audience field still says an empty group list means everybody. " +
			"ReachedBy answers false for one, so the catalogue reaches nobody and the " +
			"portal tells every visitor that no catalogue is assigned to them")
	}
	// And the list column, which said the same thing in one word.
	if strings.Contains(src, ">everybody</span>") {
		t.Error("the catalogue list still shows an empty audience as \"everybody\"")
	}
	if !strings.Contains(src, "reaches nobody") {
		t.Error("nothing on the page says what an empty audience does, so somebody " +
			"has to find out from the portal's silence")
	}
}

// TestTheAppearanceCardIsOfferedToWhoMayUseIt.
//
// The appearance is the administrator's — stricter than the rest of the page,
// where an editor may change what the catalogue offers. Drawing the form for an
// editor would end every save in 403.
func TestTheAppearanceCardIsOfferedToWhoMayUseIt(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	start := strings.Index(src, "function mayTheme(")
	if start < 0 {
		t.Fatal("catalog-admin.js has no mayTheme(); if the appearance card moved, " +
			"this test now passes vacuously and says so instead")
	}
	end := strings.Index(src[start:], "\n}")
	body := src[start : start+end]
	if !strings.Contains(body, `"admin"`) {
		t.Error("mayTheme does not check the admin role, so the form is drawn for " +
			"editors whose every save ends in 403")
	}
	if !strings.Contains(body, "if (!enforced) return true;") {
		t.Error("mayTheme hides the appearance form when enforcement is off. With " +
			"nobody signed in there is nobody to be, not nobody who may — and a demo " +
			"instance is exactly where somebody brands a catalogue to look at it")
	}
}

// TestTheTypefacesOnScreenAreTheOnesTheServerShips.
//
// The list is a choice and not a URL, so the page has to spell the same four the
// binary does. Offering a fifth would be a select whose every value is refused;
// dropping one would hide a typeface an installation paid for in nothing but a
// forgotten line.
func TestTheTypefacesOnScreenAreTheOnesTheServerShips(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	start := strings.Index(src, "const TYPEFACES = [")
	if start < 0 {
		t.Fatal("catalog-admin.js declares no TYPEFACES; if it moved, this test now " +
			"checks nothing and says so instead")
	}
	end := strings.Index(src[start:], "\n];")
	block := src[start : start+end]

	for name := range catalog.Typefaces {
		if !strings.Contains(block, `id: "`+name+`"`) {
			t.Errorf("the server ships the %q typeface and the page does not offer it", name)
		}
	}
	// And nothing beyond them: an option the server refuses is a control that
	// cannot work, found only by somebody choosing it.
	for _, line := range strings.Split(block, "\n") {
		i := strings.Index(line, `id: "`)
		if i < 0 {
			continue
		}
		rest := line[i+5:]
		name := rest[:strings.Index(rest, `"`)]
		if _, ok := catalog.Typefaces[name]; !ok {
			t.Errorf("the page offers a %q typeface the server does not ship; every "+
				"save choosing it is refused", name)
		}
	}
}
