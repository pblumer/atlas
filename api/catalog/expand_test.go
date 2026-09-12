package catalog

import (
	"strings"
	"testing"
)

// Ordering a product orders what it is made of. A composition is integral —
// always included, never deselectable — and an aggregation is offered alongside
// it. A release that carried only the precedence edges could schedule what was
// ordered but not work out what ordering a product actually means, so it carries
// the structure too.

func compose(from, to string) Edge { return Edge{From: from, To: to, Kind: EdgeComposition} }
func aggregate(from, to string) Edge {
	return Edge{From: from, To: to, Kind: EdgeAggregation}
}

// workplace is composed of an account and a laptop; a second screen is optional.
func workplaceRelease(t *testing.T) Release {
	t.Helper()
	ids := []string{"workplace", "account", "laptop", "screen"}
	var items []Item
	for _, id := range ids {
		items = append(items, newItem(id))
	}
	return publishOne(t, Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: ids}},
		Items:    items,
		Edges: []Edge{
			compose("workplace", "account"),
			compose("workplace", "laptop"),
			aggregate("workplace", "screen"),
			requires("laptop", "account"),
		},
	})
}

// TestReleaseCarriesTheStructure: both kinds, kept apart, because one is a
// consequence of ordering the whole and the other is an offer.
func TestReleaseCarriesTheStructure(t *testing.T) {
	rel := workplaceRelease(t)

	if got := strings.Join(rel.Includes["workplace"], ","); got != "account,laptop" {
		t.Errorf("Includes[workplace] = %s, want account,laptop", got)
	}
	if got := strings.Join(rel.Options["workplace"], ","); got != "screen" {
		t.Errorf("Options[workplace] = %s, want screen", got)
	}
	if _, ok := rel.Includes["laptop"]; ok {
		t.Error("a product made of nothing carries no entry")
	}
}

// TestOrderingAProductOrdersWhatItIsMadeOf is the rule the basket runs on.
func TestOrderingAProductOrdersWhatItIsMadeOf(t *testing.T) {
	rel := workplaceRelease(t)

	got := rel.Expand([]string{"workplace"})
	if strings.Join(got, ",") != "account,laptop,workplace" {
		t.Fatalf("Expand(workplace) = %v, want the whole and both its parts", got)
	}
}

// TestAnOptionIsNotPulledInUnasked: an aggregation is an offer. Including it
// automatically would order a second screen for everybody.
func TestAnOptionIsNotPulledInUnasked(t *testing.T) {
	rel := workplaceRelease(t)
	for _, id := range rel.Expand([]string{"workplace"}) {
		if id == "screen" {
			t.Fatal("the optional screen was pulled in without being chosen")
		}
	}

	// Chosen explicitly, it comes along.
	got := rel.Expand([]string{"workplace", "screen"})
	if strings.Join(got, ",") != "account,laptop,screen,workplace" {
		t.Fatalf("Expand with the option = %v", got)
	}
}

// TestCompositionIsFollowedAllTheWayDown: a bundle inside a bundle is still
// integral, and stopping at the first level would half-provision it.
func TestCompositionIsFollowedAllTheWayDown(t *testing.T) {
	ids := []string{"a", "b", "c", "d"}
	var items []Item
	for _, id := range ids {
		items = append(items, newItem(id))
	}
	rel := publishOne(t, Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: ids}},
		Items:    items,
		Edges:    []Edge{compose("a", "b"), compose("b", "c"), compose("c", "d")},
	})

	if got := strings.Join(rel.Expand([]string{"a"}), ","); got != "a,b,c,d" {
		t.Fatalf("Expand(a) = %s, want the whole chain", got)
	}
}

// TestAServiceInTwoProductsIsOrderedOnce: the same service legitimately appears
// in several products, and ordering both must not provision it twice.
func TestAServiceInTwoProductsIsOrderedOnce(t *testing.T) {
	ids := []string{"p1", "p2", "shared"}
	var items []Item
	for _, id := range ids {
		items = append(items, newItem(id))
	}
	rel := publishOne(t, Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: ids}},
		Items:    items,
		Edges:    []Edge{compose("p1", "shared"), compose("p2", "shared")},
	})

	if got := strings.Join(rel.Expand([]string{"p1", "p2"}), ","); got != "p1,p2,shared" {
		t.Fatalf("Expand = %s, want shared once", got)
	}
}

// TestExpandIgnoresWhatTheReleaseDoesNotCarry: a request naming a product that
// was withdrawn, or never existed, must not invent a line for it.
func TestExpandIgnoresWhatTheReleaseDoesNotCarry(t *testing.T) {
	rel := workplaceRelease(t)
	got := rel.Expand([]string{"workplace", "ghost"})
	for _, id := range got {
		if id == "ghost" {
			t.Fatal("Expand invented a line for a product the release does not carry")
		}
	}
	if len(rel.Expand(nil)) != 0 {
		t.Error("Expand(nothing) must be nothing")
	}
}
