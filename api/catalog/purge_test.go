package catalog

import (
	"reflect"
	"testing"
)

func TestStripProductsRemovesTheOfferAndEveryEdgeNamingIt(t *testing.T) {
	c := Catalog{
		ID:    "shop",
		Items: []string{"laptop", "phone", "dock"},
		Edges: []Edge{
			{From: "laptop", To: "dock", Kind: EdgeComposition},
			{From: "phone", To: "laptop", Kind: EdgeRequires},
		},
	}
	got, lost := StripProducts(c, map[string]bool{"dock": true})

	if want := []string{"laptop", "phone"}; !reflect.DeepEqual(got.Items, want) {
		t.Errorf("items = %v, want %v", got.Items, want)
	}
	if want := []Edge{{From: "phone", To: "laptop", Kind: EdgeRequires}}; !reflect.DeepEqual(got.Edges, want) {
		t.Errorf("edges = %v, want %v", got.Edges, want)
	}
	if want := []string{"dock"}; !reflect.DeepEqual(lost, want) {
		t.Errorf("lost = %v, want %v", lost, want)
	}
	if len(c.Items) != 3 || len(c.Edges) != 2 {
		t.Errorf("the catalogue passed in was changed: %+v", c)
	}
}

// A product a catalogue only names in an edge is lost from it too: the edge would
// otherwise reach the next publish and be refused as a reference to nothing.
func TestStripProductsCountsAProductNamedOnlyByAnEdge(t *testing.T) {
	c := Catalog{Items: []string{"laptop"}, Edges: []Edge{{From: "laptop", To: "dock", Kind: EdgeComposition}}}
	got, lost := StripProducts(c, map[string]bool{"dock": true})
	if len(got.Edges) != 0 {
		t.Errorf("edges = %v, want none", got.Edges)
	}
	if want := []string{"dock"}; !reflect.DeepEqual(lost, want) {
		t.Errorf("lost = %v, want %v", lost, want)
	}
}

func TestStripProductsLeavesAnUntouchedCatalogueAlone(t *testing.T) {
	c := Catalog{Items: []string{"laptop"}, Edges: []Edge{{From: "laptop", To: "dock", Kind: EdgeComposition}}}
	got, lost := StripProducts(c, map[string]bool{"phone": true})
	if lost != nil {
		t.Errorf("lost = %v, want nil", lost)
	}
	if !reflect.DeepEqual(got, c) {
		t.Errorf("catalogue changed: %+v", got)
	}
}
