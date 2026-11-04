package api

import (
	"regexp"
	"slices"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
)

// The server refuses an icon the shop does not draw, and the shop draws only what
// the server allows (ADR-0525). The two lists are written twice,
// once in Go and once in the shop's module, so this keeps them the same list.
func TestTheShopDrawsExactlyTheIconsTheCatalogueAllows(t *testing.T) {
	src := readWeb(t, "shop-icons.js")
	var drawn []string
	for _, m := range regexp.MustCompile(`(?m)^  "([a-z0-9-]+)": \[`).FindAllStringSubmatch(src, -1) {
		drawn = append(drawn, m[1])
	}
	if !slices.Equal(drawn, catalog.Icons) {
		t.Fatalf("shop-icons.js draws %v, the catalogue allows %v", drawn, catalog.Icons)
	}
}
