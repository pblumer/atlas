package catalog

import "testing"

// The heading a product sits under (ADR-0360).
//
// A heading and nothing else: no ordering and no entity. So the catalogue has
// exactly one thing to check about the string itself, the same one a price and a
// form id get — that a product claiming to carry one carries something readable.
//
// The heading's wording per language, which ADR-0360 did not have and which
// ADR-0412 added beside this string, is checked in
// categorytexts_test.go. This file is about the key.

func grouped(id, category string) Item {
	it := item(id)
	it.Category = category
	return it
}

// TestABlankCategoryIsRefused.
//
// A heading of spaces is one nobody can read and nobody can group by: the portal
// renders an empty column head, and a second product with a different number of
// spaces sits under a different one.
func TestABlankCategoryIsRefused(t *testing.T) {
	contains(t, problemsOf(grouped("laptop", "  ")), "blank category")

	for _, c := range []string{"Arbeitsplatz", "Kommunikation & Zusammenarbeit", ""} {
		if got := problemsOf(grouped("laptop", c)); len(got) != 0 {
			t.Errorf("a category of %q does not publish: %+v", c, got)
		}
	}
}

// TestTheCategoryTravelsIntoTheRelease: the portal groups what the release says,
// so a release that dropped it would put every product under one heading.
func TestTheCategoryTravelsIntoTheRelease(t *testing.T) {
	rel, problems := Publish(Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"laptop"}}},
		Items:    []Item{grouped("laptop", "Arbeitsplatz")},
	})
	if len(problems) != 0 {
		t.Fatalf("publish refused: %v", problems)
	}
	if len(rel.Items) != 1 || rel.Items[0].Category != "Arbeitsplatz" {
		t.Errorf("the release carries %+v; without the heading the portal groups "+
			"everything under one", rel.Items)
	}
}

// TestTwoSpellingsAreTwoCategories.
//
// Not a defect to fix here — it is the cost of the decision, written down so that
// nobody reads the absence of a check as an oversight. A string has no identity
// beyond itself, and a catalogue that folded "Arbeitsplatz" into "Arbeitsplätze"
// would be inventing the entity the decision refused.
func TestTwoSpellingsAreTwoCategories(t *testing.T) {
	problems := problemsOf(
		grouped("laptop", "Arbeitsplatz"),
		grouped("screen", "Arbeitsplätze"),
	)
	if len(problems) != 0 {
		t.Errorf("two spellings of one heading were refused: %v.\n"+
			"They are two headings, and the catalogue is right not to guess otherwise", problems)
	}
}
