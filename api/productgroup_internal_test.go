package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
)

// The cascade a requester reads is Kategorie > Produktgruppe > Produkt > Services.
//
// Two changes in one, because they are one change. The **Bundle** level goes: a
// bundle is offered as a Marktleistung, it holds the orchestration, and the
// services behind it hold their own provisioning — so every root is a Marktleistung
// whether or not it carries parts, and the question "is this a bundle or an
// offering" stops being asked rather than being answered. And a **Produktgruppe**
// takes the column the Bundle level vacated.
//
// The two upper columns are attributes a product writes on itself; the two lower
// ones are read off the containment graph. That split is why a group can be a
// string at all, and it is the whole of the storage decision: the group has no
// record, so the chain is assembled per product — this product is in category X,
// group Y — and a group whose products sit in two categories appears under both.
// Nothing is contradicted, because nothing claims a group belongs to one category.

// TestAProductCarriesItsGroupTheWayItCarriesItsCategory.
//
// Derived from the marshalled item rather than a tag somebody read once: renaming
// the Go field, the JSON tag or the portal's reader leaves the column showing one
// group for everything, which looks exactly like a catalogue nobody has grouped.
func TestAProductCarriesItsGroupTheWayItCarriesItsCategory(t *testing.T) {
	marker := "atlas-group-marker"
	raw, err := json.Marshal(catalog.Item{ProductGroup: marker})
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

	src := readWeb(t, "shop.js")
	// Both halves of the grouping, as the category guard checks both of its own: the
	// one that collects the groups and the one that decides what falls under the
	// group now open. Either reading a field the release does not carry leaves every
	// product under one group.
	//
	// Two spellings are accepted because the two halves legitimately have them: the
	// column names the field to the function that collects both headings, and the
	// filter reads it off the product. Renaming the tag still fails both.
	for _, fn := range []string{"function groupsOf(", "function inGroup("} {
		body := webRegion(t, src, fn, "\n}")
		body = strings.ReplaceAll(body, "state."+key, "state.<the open group>")
		if !strings.Contains(body, "."+key) && !strings.Contains(body, "'"+key+"'") {
			t.Errorf("the release spells the group %q and %s never reads it, so every "+
				"product sits under one group", key, strings.TrimSuffix(fn, "("))
		}
	}
}

// TestTheGroupColumnIsNarrowedByTheCategory.
//
// The chain is a display chain: the group has no record and therefore no category
// of its own, so the relation is read off the products that carry both. A group
// column built from every product in the catalogue would put groups under headings
// that hold none of their products.
func TestTheGroupColumnIsNarrowedByTheCategory(t *testing.T) {
	body := webRegion(t, readWeb(t, "shop.js"), "function groupsOf(", "\n}")
	if !strings.Contains(body, "inCategory") {
		t.Error("the group column is built from every product rather than from the " +
			"ones under the heading now open, so a group appears under a category none " +
			"of its products is in")
	}
}

// TestNothingIsCalledABundleAnyMore.
//
// A bundle is offered as a Marktleistung, and what stands behind it are services.
// The level said otherwise in three places and the word survived in the column
// heads; a screen that still says Bundle is a screen teaching a vocabulary the
// catalogue has dropped.
func TestNothingIsCalledABundleAnyMore(t *testing.T) {
	src := readWeb(t, "shop.js")
	body := webRegion(t, src, "function levelOf(", "\n}")
	if strings.Contains(body, "'bundle'") {
		t.Error("levelOf still answers 'bundle'. Every root is a Marktleistung, with " +
			"or without parts — the distinction is what announced a single product as " +
			"something made of other things")
	}
	if strings.Contains(src, "'col.bundle'") {
		t.Error("a column still reads its head from col.bundle")
	}
}

// TestTheGroupIsMaintainableBesideTheCategory.
//
// A field the portal reads and no form writes is a column that is empty for every
// product and cannot be filled — which reads as a feature that does not work.
func TestTheGroupIsMaintainableBesideTheCategory(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	// A row of boxes, one per language the catalogue declares, filled from the key
	// and its wordings together. The control names are built inside the helper, so
	// what is searched for is the helper being given the field.
	if !strings.Contains(src, `headingBoxes(v.productGroup, v.productGroupTexts, langs)`) {
		t.Error("the product form has no control for the group, so the column the " +
			"portal draws can never be filled")
	}
	// And the save carries it. The form builds its body from the stored record with
	// the form's own fields laid over it, so a control whose value is never read
	// leaves the field at whatever was stored — which looks like a form that
	// silently refuses to change one field.
	//
	// The assembly is productBody; it was a block inside the submit handler when
	// this guard was written and moved out to be provable in a browser.
	body := webRegion(t, src, "export function productBody(", "\n}")
	if !strings.Contains(body, "productGroup:") {
		t.Error("the save does not read the group control, so editing it changes nothing")
	}
}
