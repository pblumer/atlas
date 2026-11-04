package catalog

import "testing"

// The tile layout draws a product's icon by name (ADR-draft-shop-tile-layout).
// A name the shop does not draw would show nothing while the catalogue claims a
// choice was made, so publishing refuses it; an empty name is the default.

func TestAKnownIconPublishes(t *testing.T) {
	it := item("laptop")
	it.Icon = "device-laptop"
	mustPublish(t, Input{Items: []Item{it}})
}

func TestNoIconIsTheDefault(t *testing.T) {
	mustPublish(t, Input{Items: []Item{item("laptop")}})
}

func TestAnUnknownIconIsRefused(t *testing.T) {
	it := item("laptop")
	it.Icon = "rocket-ship"
	_, problems := Publish(Input{Items: []Item{it}})
	contains(t, problems, `names icon "rocket-ship"`)
}

func TestTheIconSetHasNoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, n := range Icons {
		if seen[n] {
			t.Errorf("icon %q is listed twice", n)
		}
		seen[n] = true
	}
}

func TestALayoutIsCascadeOrTiles(t *testing.T) {
	for _, ok := range []string{"", "cascade", "tiles"} {
		if err := (Theme{Layout: ok}).Valid(); err != nil {
			t.Errorf("layout %q refused: %v", ok, err)
		}
	}
	if err := (Theme{Layout: "carousel"}).Valid(); err == nil {
		t.Error("an unknown layout was accepted")
	}
}
