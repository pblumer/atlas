package api

import (
	"testing"

	"github.com/pblumer/atlas/api/catalog"
)

// Reading the graph backwards (ADR-draft-product-usage).
//
// The forward direction is exercised everywhere — the portal, the basket, the
// fulfilment schedule are all built on it. These hold the *reverse*, which is
// the question a maintainer asks and nothing else answered.

// aRelease builds a release carrying the named items.
func aRelease(items ...string) catalog.Release {
	rel := catalog.Release{ID: "rel_1"}
	for _, id := range items {
		rel.Items = append(rel.Items, catalog.Item{ID: id})
	}
	return rel
}

// TestWhatNeedsAServiceIsNotWhatItNeeds.
//
// The two directions of `requires`, and the whole reason this view exists.
// Forwards is on every catalogue screen: a product names its preconditions.
// Backwards is on none, and it is the one that matters when a service is about
// to be retired — nobody reading the VPN's own page learns that the laptop
// cannot be provisioned without it.
func TestWhatNeedsAServiceIsNotWhatItNeeds(t *testing.T) {
	rel := aRelease("account", "vpn", "laptop")
	rel.Requires = map[string][]string{
		"vpn":    {"account"},
		"laptop": {"vpn"},
	}

	rep := usageFromReleases(t, "vpn", map[string]catalog.Release{"cat_1": rel})

	if len(rep.Needs) != 1 || rep.Needs[0] != "account" {
		t.Errorf("needs = %v, want the account it sits on", rep.Needs)
	}
	if len(rep.NeededBy) != 1 || rep.NeededBy[0] != "laptop" {
		t.Errorf("neededBy = %v, want the laptop that cannot be provisioned without it — "+
			"the direction no forward view shows", rep.NeededBy)
	}
}

// TestAnIntegralPartIsDistinguishedFromAnOptionalOne.
//
// Retiring the two has different consequences: an integral part cannot be
// removed without changing what the whole *is*, an optional one can. A view that
// merged them would tell a maintainer the same thing about two different
// situations.
func TestAnIntegralPartIsDistinguishedFromAnOptionalOne(t *testing.T) {
	rel := aRelease("workplace", "licence", "extra")
	rel.Includes = map[string][]string{"workplace": {"licence"}}
	rel.Options = map[string][]string{"workplace": {"extra"}}

	integral := usageFromReleases(t, "licence", map[string]catalog.Release{"cat_1": rel})
	if len(integral.PartOf) != 1 || integral.PartOf[0].Kind != string(catalog.EdgeComposition) {
		t.Errorf("an integral part reads as %+v, want composition", integral.PartOf)
	}

	optional := usageFromReleases(t, "extra", map[string]catalog.Release{"cat_1": rel})
	if len(optional.PartOf) != 1 || optional.PartOf[0].Kind != string(catalog.EdgeAggregation) {
		t.Errorf("an optional part reads as %+v, want aggregation", optional.PartOf)
	}
}

// TestAServiceCarriedByTwoCataloguesIsOneAnswer.
//
// Merged rather than answered per catalogue, because a service does not belong
// to a catalogue: the same product carried by two of them is one thing somebody
// is about to change. A per-catalogue answer would let a maintainer fix one
// estate and break another without ever seeing the second.
func TestAServiceCarriedByTwoCataloguesIsOneAnswer(t *testing.T) {
	a := aRelease("workplace", "vpn")
	a.Includes = map[string][]string{"workplace": {"vpn"}}
	b := aRelease("homeoffice", "vpn")
	b.Options = map[string][]string{"homeoffice": {"vpn"}}

	rep := usageFromReleases(t, "vpn", map[string]catalog.Release{"cat_a": a, "cat_b": b})

	if len(rep.OfferedBy) != 2 {
		t.Fatalf("offeredBy = %v, want both catalogues", rep.OfferedBy)
	}
	if len(rep.PartOf) != 2 {
		t.Fatalf("partOf = %+v, want both wholes", rep.PartOf)
	}
	// Sorted, so two reads of an unchanged catalogue agree. A view that reordered
	// itself would read as a change nobody made.
	if rep.PartOf[0].ItemID != "homeoffice" || rep.PartOf[1].ItemID != "workplace" {
		t.Errorf("partOf order = %+v, want it stable and sorted", rep.PartOf)
	}
	if rep.OfferedBy[0] != "cat_a" || rep.OfferedBy[1] != "cat_b" {
		t.Errorf("offeredBy order = %v, want it stable and sorted", rep.OfferedBy)
	}
}

// TestAProductUsedByNothingSaysSoRatherThanAnsweringEmpty.
//
// An empty answer reads as "safe to retire", and it is also what an unpublished
// product looks like. The two are different and a maintainer must not read the
// second as the first.
func TestAProductUsedByNothingSaysSoRatherThanAnsweringEmpty(t *testing.T) {
	rel := aRelease("standalone")
	rep := usageFromReleases(t, "standalone", map[string]catalog.Release{"cat_1": rel})

	if len(rep.PartOf) != 0 || len(rep.NeededBy) != 0 {
		t.Fatalf("= %+v, want nothing carrying it", rep)
	}
	if len(rep.OfferedBy) != 1 {
		t.Errorf("offeredBy = %v; a product nothing contains is still offered, and saying "+
			"otherwise would hide it from the person maintaining it", rep.OfferedBy)
	}
}

// TestAnExclusionReadsTheSameFromEitherSide.
func TestAnExclusionReadsTheSameFromEitherSide(t *testing.T) {
	rel := aRelease("create-supplier", "approve-payment")
	// The release stores both directions, which is what makes a one-sided read
	// impossible (ADR-0342).
	rel.Excludes = map[string][]string{
		"create-supplier": {"approve-payment"},
		"approve-payment": {"create-supplier"},
	}
	for _, id := range []string{"create-supplier", "approve-payment"} {
		rep := usageFromReleases(t, id, map[string]catalog.Release{"cat_1": rel})
		if len(rep.ExcludedWith) != 1 {
			t.Errorf("%s: excludedWith = %v, want the other half", id, rep.ExcludedWith)
		}
	}
}

// usageFromReleases runs the walk over a set of catalogues, and fails the test
// when the product is in none of them — every case here supplies it.
func usageFromReleases(t *testing.T, id string, releases map[string]catalog.Release) usageReport {
	t.Helper()
	rep, known := usageAcross(id, releases)
	if !known {
		t.Fatalf("no release carries %q; the fixture does not test what it claims", id)
	}
	return rep
}
