package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// The catalogue tools driven the way a product manager would: create a
// catalogue, file a product, discover that publishing refuses it, fix what the
// refusal named, publish, and finally withdraw the product — which is what
// "delete" means here, because an order placed years ago still resolves through
// the thing it ordered.

// catalogID pulls the id out of whatever a catalogue tool answered with.
func catalogID(t *testing.T, text string) string {
	t.Helper()
	var got struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode catalogue %q: %v", text, err)
	}
	if got.ID == "" {
		t.Fatalf("catalogue answer carries no id: %q", text)
	}
	return got.ID
}

// productRevision reads the revision a save answered with — what the next write
// states as its precondition.
//
// It decodes into a float64 on purpose: that is what a JSON client hands a tool
// argument back as, so a revision that did not survive the round trip would fail
// here rather than in production. The same test written against updatedAt cannot
// pass, because Unix nanoseconds are past the 2^53 a float64 represents exactly.
func productRevision(t *testing.T, text string) float64 {
	t.Helper()
	var got struct {
		Revision float64 `json:"revision"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode product %q: %v", text, err)
	}
	if got.Revision == 0 {
		t.Fatalf("product answer carries no revision: %q", text)
	}
	return got.Revision
}

func TestCatalogueToolsRoundTrip(t *testing.T) {
	ts := newAtlas(t)

	text, isErr := callText(t, ts, "atlas_create_catalog", map[string]any{
		"texts": map[string]any{"de": "Standardarbeitsplatz"}, "rank": 1,
		"languages": []any{"de"}, "groups": []any{"grp_staff"},
	})
	if isErr {
		t.Fatalf("create catalogue = %q", text)
	}
	cat := catalogID(t, text)

	if text, isErr = callText(t, ts, "atlas_list_catalogs", map[string]any{}); isErr ||
		!strings.Contains(text, cat) {
		t.Fatalf("list = (%q, isErr=%v), want the new catalogue", text, isErr)
	}
	if text, isErr = callText(t, ts, "atlas_get_catalog", map[string]any{"id": cat}); isErr ||
		!strings.Contains(text, "Standardarbeitsplatz") {
		t.Fatalf("get = (%q, isErr=%v)", text, isErr)
	}

	// A product, deliberately incomplete: no bindings yet, so publishing must
	// refuse it and say so.
	text, isErr = callText(t, ts, "atlas_save_catalog_product", map[string]any{
		"id": "vpn", "homeCatalog": cat, "state": "active",
		"texts": map[string]any{"de": "VPN-Zugang"}, "keywords": []any{"fernzugriff"},
		"approval": map[string]any{"kind": "superior"},
	})
	if isErr {
		t.Fatalf("save product = %q", text)
	}
	revision := productRevision(t, text)

	// Storing a product does not offer it. The catalogue has to carry the id.
	if text, isErr = callText(t, ts, "atlas_update_catalog", map[string]any{
		"id": cat, "items": []any{"vpn"},
	}); isErr || !strings.Contains(text, "vpn") {
		t.Fatalf("update catalogue = (%q, isErr=%v)", text, isErr)
	}

	// The refusal names every problem at once, so an agent fixes them together.
	text, isErr = callText(t, ts, "atlas_publish_catalog", map[string]any{"id": cat})
	if !isErr {
		t.Fatalf("publishing an unbound product succeeded: %q", text)
	}
	if !strings.Contains(text, "vpn") {
		t.Errorf("the refusal does not name the product it is about: %q", text)
	}

	// Fix what it named, stating the revision read so a concurrent edit would be
	// caught rather than overwritten.
	text, isErr = callText(t, ts, "atlas_save_catalog_product", map[string]any{
		"id": "vpn", "homeCatalog": cat, "state": "active",
		"texts": map[string]any{"de": "VPN-Zugang"}, "keywords": []any{"fernzugriff"},
		"approval":         map[string]any{"kind": "superior"},
		"provisionProcess": "grant-vpn", "deprovisionProcess": "revoke-vpn",
		"revision": revision,
	})
	if isErr {
		t.Fatalf("second save = %q", text)
	}
	fixed := productRevision(t, text)

	// The same revision again is stale now, and is refused rather than silently
	// overwriting the write that moved it.
	if text, isErr = callText(t, ts, "atlas_save_catalog_product", map[string]any{
		"id": "vpn", "homeCatalog": cat, "texts": map[string]any{"de": "Etwas anderes"},
		"revision": revision,
	}); !isErr {
		t.Fatalf("a stale save was accepted: %q", text)
	}

	if text, isErr = callText(t, ts, "atlas_publish_catalog", map[string]any{"id": cat}); isErr {
		t.Fatalf("publish = %q", text)
	}
	if text, isErr = callText(t, ts, "atlas_catalog_releases", map[string]any{"id": cat}); isErr ||
		!strings.Contains(text, "vpn") {
		t.Fatalf("releases = (%q, isErr=%v), want the frozen product", text, isErr)
	}

	// "Delete" is a state, and the tool surface offers no other spelling of it.
	if text, isErr = callText(t, ts, "atlas_save_catalog_product", map[string]any{
		"id": "vpn", "homeCatalog": cat, "state": "withdrawn",
		"texts": map[string]any{"de": "VPN-Zugang"}, "keywords": []any{"fernzugriff"},
		"approval":         map[string]any{"kind": "superior"},
		"provisionProcess": "grant-vpn", "deprovisionProcess": "revoke-vpn",
		"revision": fixed,
	}); isErr || !strings.Contains(text, "withdrawn") {
		t.Fatalf("withdraw = (%q, isErr=%v)", text, isErr)
	}

	// The published release is frozen: withdrawing the product afterwards does not
	// reach back into what somebody already ordered against.
	if text, isErr = callText(t, ts, "atlas_catalog_releases", map[string]any{"id": cat}); isErr ||
		!strings.Contains(text, "vpn") {
		t.Fatalf("releases after withdrawal = (%q, isErr=%v), want the release unchanged", text, isErr)
	}
}

// TestCatalogueToolsRefuseAnUnknownCatalogue: every id-taking tool forwards the
// server's own refusal rather than inventing one, so an agent reads the same
// message a person would.
func TestCatalogueToolsRefuseAnUnknownCatalogue(t *testing.T) {
	ts := newAtlas(t)
	for _, tool := range []string{"atlas_get_catalog", "atlas_publish_catalog", "atlas_catalog_releases"} {
		t.Run(tool, func(t *testing.T) {
			text, isErr := callText(t, ts, tool, map[string]any{"id": "cat_nothing"})
			if !isErr {
				t.Fatalf("%s on an unknown catalogue succeeded: %q", tool, text)
			}
		})
	}
}

// TestSavingAProductNeedsAnIDAndAHome: the two arguments without which the write
// cannot be attributed to anybody, refused by the adapter before a request is made.
func TestSavingAProductNeedsAnIDAndAHome(t *testing.T) {
	ts := newAtlas(t)
	for name, args := range map[string]map[string]any{
		"no id":   {"homeCatalog": "cat_x"},
		"no home": {"id": "vpn"},
	} {
		t.Run(name, func(t *testing.T) {
			if text, isErr := callText(t, ts, "atlas_save_catalog_product", args); !isErr {
				t.Fatalf("save without %s succeeded: %q", name, text)
			}
		})
	}
}

// The ArchiMate import, driven end to end.
//
// The contract test calls every advertised tool with empty arguments, which
// reaches each handler's first refusal and stops there — so without this the
// import tool's only exercised line is the one that rejects a missing id, and the
// request it exists to make is never made. A tool whose only proof is that it
// refuses to run is not covered.
const archiMateModel = `<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/"
       xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
       identifier="id-model">
  <name xml:lang="de">Leistungskatalog</name>
  <elements>
    <element identifier="id-wp" xsi:type="Product">
      <name xml:lang="de">Arbeitsplatz</name>
    </element>
    <element identifier="id-acc" xsi:type="BusinessService">
      <name xml:lang="de">Benutzerkonto</name>
    </element>
  </elements>
  <relationships>
    <relationship identifier="r1" source="id-wp" target="id-acc" xsi:type="Composition"/>
  </relationships>
</model>`

func TestImportingAnArchiMateModelFilesDrafts(t *testing.T) {
	ts := newAtlas(t)

	text, isErr := callText(t, ts, "atlas_create_catalog", map[string]any{
		"texts": map[string]any{"de": "Aus dem Modell"}, "rank": 2, "languages": []any{"de"},
	})
	if isErr {
		t.Fatalf("create catalogue = %q", text)
	}
	cat := catalogID(t, text)

	if text, isErr = callText(t, ts, "atlas_import_catalog_archimate", map[string]any{
		"id": cat, "xml": archiMateModel,
	}); isErr {
		t.Fatalf("import = %q", text)
	}
	if !strings.Contains(text, "arbeitsplatz") || !strings.Contains(text, "benutzerkonto") {
		t.Fatalf("the import reports neither product: %q", text)
	}

	// Nothing the import files is orderable: everything arrives as a draft, and
	// the bindings it cannot know are still empty.
	products, isErr := callText(t, ts, "atlas_list_catalog_products", map[string]any{})
	if isErr || !strings.Contains(products, "arbeitsplatz") {
		t.Fatalf("list products = (%q, isErr=%v)", products, isErr)
	}
	if !strings.Contains(products, `"state":"draft"`) {
		t.Errorf("an imported product is not a draft: %q", products)
	}

	// So publishing refuses it, which is the state the import is meant to leave.
	if text, isErr = callText(t, ts, "atlas_publish_catalog", map[string]any{"id": cat}); !isErr {
		t.Fatalf("publishing a freshly imported catalogue succeeded: %q", text)
	}
}

// TestImportingNeedsTheModel: the second argument, refused by the adapter before
// a request is made.
func TestImportingNeedsTheModel(t *testing.T) {
	ts := newAtlas(t)
	if text, isErr := callText(t, ts, "atlas_import_catalog_archimate",
		map[string]any{"id": "cat_x"}); !isErr {
		t.Fatalf("import without a model succeeded: %q", text)
	}
}

// TestUpdatingACatalogueCanStateItsRevision: the catalogue's lists are replaced
// whole and an agent's list is always one it read and changed, so the tool has to
// be able to say which read it changed. A precondition the adapter advertises and
// never forwards would be worse than none — it would read as protection.
func TestUpdatingACatalogueCanStateItsRevision(t *testing.T) {
	ts := newAtlas(t)

	text, isErr := callText(t, ts, "atlas_create_catalog", map[string]any{
		"texts": map[string]any{"de": "Konflikt"}, "rank": 3, "languages": []any{"de"},
	})
	if isErr {
		t.Fatalf("create catalogue = %q", text)
	}
	cat := catalogID(t, text)
	revision := catalogRevision(t, text)

	// The revision the create answered with is the one a first change states.
	if text, isErr = callText(t, ts, "atlas_update_catalog", map[string]any{
		"id": cat, "items": []any{"vpn"}, "revision": revision,
	}); isErr {
		t.Fatalf("update stating the revision read = %q", text)
	}
	if next := catalogRevision(t, text); next <= revision {
		t.Fatalf("revision = %v after a change, want it past %v", next, revision)
	}

	// The same revision again is stale, and is refused rather than erasing the
	// change that moved it.
	if text, isErr = callText(t, ts, "atlas_update_catalog", map[string]any{
		"id": cat, "items": []any{"laptop"}, "revision": revision,
	}); !isErr {
		t.Fatalf("a stale update was accepted: %q", text)
	}

	// And the refused write changed nothing.
	if text, isErr = callText(t, ts, "atlas_get_catalog", map[string]any{"id": cat}); isErr ||
		!strings.Contains(text, "vpn") || strings.Contains(text, "laptop") {
		t.Fatalf("get = (%q, isErr=%v), want only the first change to have landed", text, isErr)
	}
}

// catalogRevision reads the revision a catalogue tool answered with, as a float64
// for the reason productRevision does: that is what a JSON client hands back.
func catalogRevision(t *testing.T, text string) float64 {
	t.Helper()
	var got struct {
		Revision float64 `json:"revision"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode catalogue %q: %v", text, err)
	}
	if got.Revision == 0 {
		t.Fatalf("catalogue answer carries no revision: %q", text)
	}
	return got.Revision
}
