package catalog

import "testing"

// Words a product can be found by (ADR-0355).

// TestABlankKeywordIsRefused.
//
// The one static check a keyword list admits, and it is the opposite of a search
// term: an empty string is contained in every query, so a product carrying one
// would surface for everything anybody typed. A catalogue where one product
// answers every search is a catalogue with no search.
func TestABlankKeywordIsRefused(t *testing.T) {
	withKeywords := func(id string, kw ...string) Item {
		it := item(id)
		it.Keywords = kw
		return it
	}

	contains(t, problemsOf(withKeywords("vpn", "Fernzugriff", "  ")), "blank keyword")

	// And an ordinary list publishes.
	if got := problemsOf(withKeywords("vpn", "Fernzugriff", "remote", "M365")); len(got) != 0 {
		t.Errorf("a product with keywords does not publish: %+v", got)
	}
	// As does one with none — every catalogue that exists has none today, and a
	// check that required them would refuse every one of them.
	if got := problemsOf(item("vpn")); len(got) != 0 {
		t.Errorf("a product without keywords does not publish: %+v", got)
	}
}

// TestKeywordsTravelIntoTheRelease.
//
// Frozen like everything else here. It matters less for a search than for a rule
// — nobody is harmed by finding a product through a word added yesterday — and it
// is still the property that makes a release a release: what an order was placed
// against does not change underneath it.
func TestKeywordsTravelIntoTheRelease(t *testing.T) {
	it := item("vpn")
	it.Keywords = []string{"Fernzugriff", "remote"}
	rel, problems := Publish(Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"vpn"}}},
		Items:    []Item{it},
	})
	if len(problems) != 0 {
		t.Fatalf("publish: %+v", problems)
	}
	if len(rel.Items) != 1 || len(rel.Items[0].Keywords) != 2 {
		t.Fatalf("the release carries %+v, want the keywords with it", rel.Items)
	}
	// That the copy is a copy and not a reference is proved structurally for every
	// field at once, in TestAReleaseSharesNothingWithTheCatalogue.
}
