package catalog

import "testing"

// What a product's claim about the target systems owes
// (ADR-0333).
//
// These references are what a commissioning load joins an observed right to a
// product by, and everything they can get wrong is silent at the moment it is
// written and expensive months later — so they are proved when the catalogue is
// published, which is I5 applied to one more thing.

// claiming returns a publishable item claiming the given references.
func claiming(id string, refs ...TargetRef) Item {
	it := item(id)
	it.Targets = refs
	return it
}

func problemsOf(items ...Item) []Problem {
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	_, problems := Publish(Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: ids}},
		Items:    items,
	})
	return problems
}

// TestTwoProductsMayNotClaimTheSameRight is the refusal that matters.
//
// A right found under a reference two products claim cannot be attributed to
// either, so a load reports it and writes nothing — the right stays out of the
// inventory, and the reconciliation the load exists to make meaningful reports it as
// a discrepancy anyway. The failure is a hole in the evidence that nothing points
// at. Refusing it at publish turns that into a sentence somebody reads while they
// are looking at the catalogue.
func TestTwoProductsMayNotClaimTheSameRight(t *testing.T) {
	problems := problemsOf(
		claiming("vpn", TargetRef{System: "ad", Ref: "CN=VPN-Users"}),
		claiming("remote", TargetRef{System: "ad", Ref: "CN=VPN-Users"}),
	)
	contains(t, problems, "already claimed by product")
	contains(t, problems, "attributed to neither")
}

// TestTheSameRightInTwoSystemsIsNotAClash: the system is half the reference.
//
// "Administrators" is a group in every directory ever built, and two products
// claiming that name in two different systems are saying two different things.
func TestTheSameRightInTwoSystemsIsNotAClash(t *testing.T) {
	problems := problemsOf(
		claiming("a", TargetRef{System: "ad", Ref: "Administrators"}),
		claiming("b", TargetRef{System: "jira", Ref: "Administrators"}),
	)
	if len(problems) != 0 {
		t.Errorf("two systems' identically named groups were refused as a clash: %v", problems)
	}
}

// TestOneProductMayClaimSeveralRights: a service is legitimately two groups, and a
// catalogue that could not say so would force it to be modelled as two products.
func TestOneProductMayClaimSeveralRights(t *testing.T) {
	problems := problemsOf(claiming("vpn",
		TargetRef{System: "ad", Ref: "CN=VPN-Users"},
		TargetRef{System: "ad", Ref: "CN=VPN-Legacy"},
		TargetRef{System: "entra", Ref: "ENTERPRISEPACK"},
	))
	if len(problems) != 0 {
		t.Errorf("a product claiming three references was refused: %v", problems)
	}
}

// TestAHalfWrittenReferenceIsRefused: a reference missing either half matches
// nothing, forever, without ever saying so.
func TestAHalfWrittenReferenceIsRefused(t *testing.T) {
	contains(t, problemsOf(claiming("a", TargetRef{Ref: "CN=X"})), "names no system")
	contains(t, problemsOf(claiming("b", TargetRef{System: "ad"})), "names nothing")
	// Whitespace counts as absent, or a reference of one space would be a reference
	// nothing reports and nobody can see is wrong.
	contains(t, problemsOf(claiming("c", TargetRef{System: "  ", Ref: "CN=X"})), "names no system")
}

// TestAReferenceListedTwiceOnOneProductIsNamed: it says the same thing twice, so it
// is not a conflict — but it is almost always a half-finished edit, and a catalogue
// that stayed silent about it would let the duplicate outlive whoever made it.
func TestAReferenceListedTwiceOnOneProductIsNamed(t *testing.T) {
	contains(t, problemsOf(claiming("a",
		TargetRef{System: "ad", Ref: "CN=X"},
		TargetRef{System: "ad", Ref: "CN=X"},
	)), "listed twice on this product")
}

// TestAProductWithNoTargetsPublishes: the ordinary case is a product nothing outside
// Atlas grants. Requiring a reference would make every catalogue carry a field its
// author has no answer for, which is how a field comes to be filled in with
// anything.
func TestAProductWithNoTargetsPublishes(t *testing.T) {
	if problems := problemsOf(item("plain")); len(problems) != 0 {
		t.Errorf("a product claiming nothing was refused: %v", problems)
	}
}

// TestAClaimSurvivesIntoTheRelease: a release is a frozen copy, and a copy that
// dropped the references would leave a load unable to attribute anything against a
// published catalogue.
func TestAClaimSurvivesIntoTheRelease(t *testing.T) {
	rel, problems := Publish(Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"vpn"}}},
		Items:    []Item{claiming("vpn", TargetRef{System: "ad", Ref: "CN=VPN-Users"})},
	})
	if len(problems) != 0 {
		t.Fatalf("publish refused: %v", problems)
	}
	if len(rel.Items) != 1 || len(rel.Items[0].Targets) != 1 ||
		rel.Items[0].Targets[0].Ref != "CN=VPN-Users" {
		t.Errorf("the release carries %+v; the references have to travel with the frozen "+
			"copy or a load against a published catalogue attributes nothing", rel.Items)
	}
}
