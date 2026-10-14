package catalog

import "testing"

// The form a product asks for (ADR-0358).
//
// The catalogue stores an id and interprets nothing: the form store belongs to the
// api package and this one cannot see it, exactly as it cannot see which processes
// are deployed. So there is one static check, and it is the same one the two
// provisioning bindings get — the string has to be a string somebody could have
// meant.

func withForm(id, form string) Item {
	it := item(id)
	it.ConfigForm = form
	return it
}

// TestABlankConfigurationFormIsRefused.
//
// A form id of nothing but spaces is a product that asks a question nobody can
// answer: the portal looks for a form under a name no form has, and the orderer is
// stopped by a blank that cannot be filled in. Empty is a different statement —
// "this product needs no details" — and is the ordinary case.
func TestABlankConfigurationFormIsRefused(t *testing.T) {
	contains(t, problemsOf(withForm("laptop", "   ")), "blank configuration form")

	if got := problemsOf(withForm("laptop", "form_laptop")); len(got) != 0 {
		t.Errorf("a product naming a form does not publish: %+v", got)
	}
	if got := problemsOf(withForm("laptop", "")); len(got) != 0 {
		t.Errorf("a product naming no form does not publish: %+v", got)
	}
}

// TestTheFormTravelsIntoTheRelease: an order is placed against a release, and the
// line copies the form id from it. A release that dropped it would place every
// order with no record of what was asked.
func TestTheFormTravelsIntoTheRelease(t *testing.T) {
	rel, problems := Publish(Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"laptop"}}},
		Items:    []Item{withForm("laptop", "form_laptop")},
	})
	if len(problems) != 0 {
		t.Fatalf("publish refused: %v", problems)
	}
	if len(rel.Items) != 1 || rel.Items[0].ConfigForm != "form_laptop" {
		t.Errorf("the release carries %+v; the form has to travel with the frozen copy",
			rel.Items)
	}
}
