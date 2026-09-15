package catalog

import "testing"

// What a product says it costs (ADR-0361).
//
// Displayed and never computed, so the catalogue has exactly one thing to check:
// that a product claiming to name a cost names one. Everything else a price could
// be wrong about — a currency, a rate, a rounding rule — is absent on purpose.

func priced(id, price string) Item {
	it := item(id)
	it.Price = price
	return it
}

// TestABlankPriceIsRefused.
//
// Worse than saying nothing: the portal renders an empty field where a figure
// belongs, and a reader cannot tell "we do not say" from "somebody left it blank".
func TestABlankPriceIsRefused(t *testing.T) {
	contains(t, problemsOf(priced("laptop", "   ")), "blank price")

	for _, p := range []string{"CHF 1'200.–", "49.– / Monat", "im Grundpaket enthalten", ""} {
		if got := problemsOf(priced("laptop", p)); len(got) != 0 {
			t.Errorf("a price of %q does not publish: %+v", p, got)
		}
	}
}

// TestThePriceTravelsIntoTheRelease.
//
// The whole reason it is frozen: an approver saw a figure and decided on it, and a
// catalogue edit afterwards must not make the record show a different one than the
// one that was approved. That is the same sentence as the approval rule's.
func TestThePriceTravelsIntoTheRelease(t *testing.T) {
	rel, problems := Publish(Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"laptop"}}},
		Items:    []Item{priced("laptop", "CHF 1'200.–")},
	})
	if len(problems) != 0 {
		t.Fatalf("publish refused: %v", problems)
	}
	if len(rel.Items) != 1 || rel.Items[0].Price != "CHF 1'200.–" {
		t.Errorf("the release carries %+v; the price has to travel with the frozen copy "+
			"or an approver's figure changes under them", rel.Items)
	}
}

// TestThePriceIsKeptAsWritten: not normalised, not trimmed into a number, not
// reformatted. It is a sentence somebody chose, and the catalogue has no money
// model to translate it into.
func TestThePriceIsKeptAsWritten(t *testing.T) {
	written := "ab CHF 49.– / Monat, ab 10 Stück CHF 39.–"
	rel, problems := Publish(Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"laptop"}}},
		Items:    []Item{priced("laptop", written)},
	})
	if len(problems) != 0 {
		t.Fatalf("publish refused: %v", problems)
	}
	if rel.Items[0].Price != written {
		t.Errorf("the release rewrote the price as %q, want it as written", rel.Items[0].Price)
	}
}
