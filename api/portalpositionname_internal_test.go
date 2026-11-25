package api

import (
	"strings"
	"testing"
)

// What a held position is called.
//
// Reported 2026-11-24: somebody holding the same Marktleistung twice — two SharePoint
// Team Sites — saw the actions of both under the product's name alone and could not
// tell which site a button acts on. A position somebody answered an order form for is
// named by the first answer given, in the form's own order, with a name field first.
// The behaviour is exercised in e2e/shop-position-names.spec.mjs; these guards keep
// the wiring from coming apart.

// TestTwoPositionsOfOneProductAreToldApartByName.
func TestTwoPositionsOfOneProductAreToldApartByName(t *testing.T) {
	src := readWeb(t, "shop.js")
	actions := webRegion(t, src, "function entryActions(", "\n}")
	if !strings.Contains(actions, "positionLabel(r.line)") {
		t.Error("the row of actions names a position by its product alone, so two " +
			"positions of one product read the same")
	}
	if !strings.Contains(actions, "askPositionNames(") {
		t.Error("nothing reads the forms the names are taken from when an entry opens")
	}
	if item := webRegion(t, src, "function lineItem(", "\n}"); !strings.Contains(item, "positionLabel(l)") {
		t.Error("the position under its order is not named the way its actions are")
	}
	// In the form's order, not the answers': those arrive with sorted keys, which
	// would name a site after its cost centre.
	name := webRegion(t, src, "function positionName(", "\n}")
	for _, frag := range []string{"schema.components", "atlasAvailability", "NAMING_TYPES"} {
		if !strings.Contains(name, frag) {
			t.Errorf("positionName does not read %s, so the name is not the form's "+
				"first answer", frag)
		}
	}
	// Every locale words the named position.
	for _, locale := range []struct{ name, from string }{
		{"de", "  de: {"}, {"en", "  en: {"}, {"fr", "  fr: {"}, {"it", "  it: {"},
	} {
		if !strings.Contains(webRegion(t, src, locale.from, "\n  },"), "'position.named'") {
			t.Errorf("%s carries no 'position.named'", locale.name)
		}
	}
}
