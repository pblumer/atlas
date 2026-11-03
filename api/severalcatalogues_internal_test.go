package api

import (
	"strings"
	"testing"
)

// The shop offers what every catalogue a person reaches offers
// (ADR-draft-several-catalogues-per-person). The server half is tested in
// api/catalog; this holds the page to its half: it asks for every catalogue, lays their
// releases over one another remembering where each product came from, and places a
// basket as one order per release — an order names one release, so an order naming the
// top release for a product of another would be refused, or worse, snapshot the wrong
// catalogue.
func TestTheShopOrdersFromEveryCatalogueItShows(t *testing.T) {
	src := readWeb(t, "shop.js")
	load := webRegion(t, src, "async function loadCatalogue(", "\n}")
	if !strings.Contains(load, "'/api/v1/shop/catalogs'") || !strings.Contains(load, "mergeReleases(") {
		t.Error("loadCatalogue no longer reads every catalogue and merges their releases")
	}
	merge := webRegion(t, src, "function mergeReleases(", "\n}")
	if !strings.Contains(merge, "state.releaseOf = owner") {
		t.Error("mergeReleases no longer records which release each product came from")
	}
	if !strings.Contains(src, "releaseId: rid,") || !strings.Contains(src, "(state.releaseOf || {})[id]") {
		t.Error("placing a basket no longer names each product's own release")
	}
	if strings.Contains(src, "releaseId: state.release.id,") {
		t.Error("an order still names the merged release for every product")
	}
}
