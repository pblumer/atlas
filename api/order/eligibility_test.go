package order

import (
	"testing"

	"github.com/pblumer/atlas/api/catalog"
)

// Who may receive what (ADR-0347).

func aReleaseWith(items ...catalog.Item) catalog.Release {
	return catalog.Release{ID: "rel_1", Items: items}
}

func anItem(id string, eligible ...string) catalog.Item {
	return catalog.Item{ID: id, Eligible: eligible}
}

// TestAnItemNamingNoGroupIsOrderableByAnybodyTheCatalogueReaches.
//
// The single most important case, because getting it wrong breaks every
// catalogue that exists. This list *narrows* an audience that is already
// fail-closed; it does not replace it. An empty list inheriting the catalogue's
// restriction is the opposite of a catalogue with no groups, which reaches
// nobody on purpose.
func TestAnItemNamingNoGroupIsOrderableByAnybodyTheCatalogueReaches(t *testing.T) {
	rel := aReleaseWith(anItem("vpn"), anItem("laptop"))
	if got := ineligibleIn(rel, []string{"vpn", "laptop"}, nil); got != nil {
		t.Errorf("refused %+v for a recipient in no groups; an unrestricted product "+
			"must stay orderable or every existing catalogue stops working", got)
	}
}

// TestARestrictedItemNeedsOneOfItsGroups.
func TestARestrictedItemNeedsOneOfItsGroups(t *testing.T) {
	rel := aReleaseWith(anItem("domain-admin", "grp_it", "grp_ops"))

	if got := ineligibleIn(rel, []string{"domain-admin"}, []string{"grp_sales"}); got == nil {
		t.Error("a recipient in none of the named groups was allowed to receive a " +
			"restricted product")
	} else if got.ItemID != "domain-admin" {
		t.Errorf("refused %q, want the restricted product", got.ItemID)
	}
	// Any one of them is enough: several groups on one product means "these people",
	// not "people in all of these".
	if got := ineligibleIn(rel, []string{"domain-admin"}, []string{"grp_ops", "grp_x"}); got != nil {
		t.Errorf("refused %+v for somebody in one of the named groups", got)
	}
}

// TestTheRefusalNamesTheWholeWhenThePartWasNeverChosen.
//
// The difference between a refusal somebody can act on and one that reads as a
// bug. An integral part is never deselectable, so telling a person "you may not
// receive a licence" about a licence they never chose leaves them concluding the
// portal is broken.
func TestTheRefusalNamesTheWholeWhenThePartWasNeverChosen(t *testing.T) {
	rel := aReleaseWith(anItem("workplace"), anItem("licence", "grp_staff"))
	rel.Includes = map[string][]string{"workplace": {"licence"}}

	got := ineligibleIn(rel, []string{"workplace", "licence"}, []string{"grp_contractors"})
	if got == nil {
		t.Fatal("an ineligible integral part was allowed through with its whole")
	}
	if got.IncludedBy != "workplace" {
		t.Errorf("includedBy = %q, want the product that carries it — without it the "+
			"refusal names something the person never asked for", got.IncludedBy)
	}
	if reason := got.reason(); reason == "" ||
		!contains(reason, "workplace") || !contains(reason, "licence") {
		t.Errorf("the refusal must name both the part and the whole, got %q", reason)
	}

	// And when the same product is asked for directly, there is no whole to blame.
	direct := ineligibleIn(rel, []string{"licence"}, []string{"grp_contractors"})
	if direct == nil || direct.IncludedBy != "" {
		t.Errorf("= %+v, want a refusal that blames nothing but the product asked for", direct)
	}
}

// TestTheSameBasketIsRefusedTheSameWayTwice.
//
// A refusal that moved between two equally-refused products would make the second
// attempt look like a second, different problem — and a person fixing one would
// be told about the other as though it were new.
func TestTheSameBasketIsRefusedTheSameWayTwice(t *testing.T) {
	rel := aReleaseWith(
		anItem("zebra", "grp_staff"), anItem("alpha", "grp_staff"), anItem("vpn"))

	first := ineligibleIn(rel, []string{"zebra", "alpha", "vpn"}, []string{"grp_x"})
	second := ineligibleIn(rel, []string{"vpn", "alpha", "zebra"}, []string{"grp_x"})
	if first == nil || second == nil {
		t.Fatalf("one of the passes allowed a restricted basket: %+v / %+v", first, second)
	}
	if first.ItemID != second.ItemID {
		t.Errorf("the same basket was refused as %q and then %q", first.ItemID, second.ItemID)
	}
	if first.ItemID != "alpha" {
		t.Errorf("refused %q; the walk is sorted so the answer does not depend on the "+
			"order the basket happened to arrive in", first.ItemID)
	}
}

// TestARestrictionOnSomethingNotOrderedIsNotARefusal.
func TestARestrictionOnSomethingNotOrderedIsNotARefusal(t *testing.T) {
	rel := aReleaseWith(anItem("vpn"), anItem("domain-admin", "grp_it"))
	if got := ineligibleIn(rel, []string{"vpn"}, nil); got != nil {
		t.Errorf("refused %+v over a product nobody asked for", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestAConflictRefusalNamesBothSidesAndSaysWhichIsHeld.
//
// The two sentences differ in what the reader can do next: one half already in
// the estate is a return or an access review, both halves in one basket is a
// choice to make before ordering. A refusal that read the same either way would
// send somebody looking for a right they do not have.
func TestAConflictRefusalNamesBothSidesAndSaysWhichIsHeld(t *testing.T) {
	held := conflict{Ordered: "approve-payment", Other: "create-supplier", Held: true}.reason()
	if !contains(held, "approve-payment") || !contains(held, "create-supplier") {
		t.Errorf("the refusal does not name both sides: %q", held)
	}
	if !contains(held, "already") {
		t.Errorf("a conflict against something the recipient holds does not say so, so "+
			"the reader cannot tell it from a basket they can still change: %q", held)
	}

	basket := conflict{Ordered: "approve-payment", Other: "create-supplier"}.reason()
	if !contains(basket, "both") {
		t.Errorf("a conflict inside one basket does not say the order asks for both: %q", basket)
	}
	if contains(basket, "already") {
		t.Errorf("a basket conflict claims the recipient already holds one half: %q", basket)
	}
}
