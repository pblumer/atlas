package catalog

import "testing"

// What publishing would change, which is the question the screen could not answer.
//
// The portal serves a release and the maintainer edits the records beside it, so
// the two drift apart on purpose. The one case nobody could see is a product taken
// out of the catalogue: it leaves every screen the maintainer has and stays on the
// one they do not.

// offering builds a catalogue offering the given ids.
func offering(ids ...string) Catalog {
	return Catalog{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: ids}
}

// at builds an item at a given revision, so "changed since the release" has
// something to be decided on.
func at(id string, rev int64) Item {
	it := item(id)
	it.Revision = rev
	return it
}

func TestNothingPublishedYetIsNotNothingToPublish(t *testing.T) {
	// A catalogue nobody has released offers nothing to anybody, and the whole of
	// its offering is what the first release would carry. Reporting "no changes"
	// here would be true of the release and useless to the reader.
	got := unpublishedFor(offering("vpn", "laptop"), []Item{at("vpn", 1), at("laptop", 1)}, nil)

	if got.Released {
		t.Errorf("a catalogue with no releases reports one: %+v", got)
	}
	if len(got.Added) != 2 {
		t.Errorf("added = %+v, want both products", got.Added)
	}
	if !got.Any() {
		t.Error("an unpublished catalogue with products says there is nothing to publish")
	}
}

func TestACatalogueServedAsItStandsSaysSo(t *testing.T) {
	items := []Item{at("vpn", 4)}
	rel := Release{ID: "rel_1", CreatedAt: 99, Items: items}

	got := unpublishedFor(offering("vpn"), items, []Release{rel})

	if !got.Released || got.ReleaseID != "rel_1" || got.ReleasedAt != 99 {
		t.Errorf("the release is not named: %+v", got)
	}
	if got.Any() {
		t.Errorf("a catalogue that matches its release reports a difference: %+v", got)
	}
}

// TestAProductTakenOutOfTheCatalogueIsStillOnThePortal is the case this exists
// for. It is gone from the maintainer's list and present in the release, which is
// what the portal reads — so the only place it can be reported is here.
func TestAProductTakenOutOfTheCatalogueIsStillOnThePortal(t *testing.T) {
	was := at("alt", 2)
	was.Texts = map[string]string{"de": "Alter Account"}
	rel := Release{ID: "rel_1", Items: []Item{at("vpn", 1), was}}

	// Renamed since, and deliberately: the two names have to differ or the
	// assertion below cannot tell which copy answered. The live record still
	// exists here — dropping a product from a catalogue does not delete it, and
	// the point is that the offering no longer carries it.
	now := at("alt", 3)
	now.Texts = map[string]string{"de": "Umbenannter Account"}

	got := unpublishedFor(offering("vpn"), []Item{at("vpn", 1), now}, []Release{rel})

	if len(got.Removed) != 1 || got.Removed[0].ID != "alt" {
		t.Fatalf("removed = %+v, want the product the portal still offers", got.Removed)
	}
	// Named as the portal names it, from the frozen copy: the live record may say
	// something else by now, and the reader is looking for what is on screen.
	if got.Removed[0].Texts["de"] != "Alter Account" {
		t.Errorf("the removed product is named %q, want the name the portal shows: %+v",
			got.Removed[0].Texts["de"], got.Removed[0])
	}
	if len(got.Added) != 0 || len(got.Changed) != 0 {
		t.Errorf("a removal was reported as something else too: %+v", got)
	}
}

func TestAProductEditedSinceTheReleaseIsReported(t *testing.T) {
	// The portal is showing an older name, price or state than the catalogue holds.
	// Decided on the revision, which every writer advances, rather than on a
	// comparison of the fields this package happened to think of.
	rel := Release{ID: "rel_1", Items: []Item{at("vpn", 1)}}

	got := unpublishedFor(offering("vpn"), []Item{at("vpn", 2)}, []Release{rel})

	if len(got.Changed) != 1 || got.Changed[0].ID != "vpn" {
		t.Fatalf("changed = %+v, want the edited product", got.Changed)
	}
	if len(got.Added) != 0 || len(got.Removed) != 0 {
		t.Errorf("an edit was reported as an addition or a removal: %+v", got)
	}
}

// TestAProductOfferedButNotReadableHereIsNotCalledChanged keeps ADR-0315's rule.
// A product homed in a catalogue this caller may not maintain is offered
// legitimately; it is not theirs to compare, and reporting it would ask them to
// publish away a difference they cannot see.
func TestAProductOfferedButNotReadableHereIsNotCalledChanged(t *testing.T) {
	rel := Release{ID: "rel_1", Items: []Item{at("fremd", 1)}}

	// The live list does not carry it: HandleListItems filters by the home
	// catalogue, and this caller may not edit that one.
	got := unpublishedFor(offering("fremd"), nil, []Release{rel})

	if len(got.Changed) != 0 {
		t.Errorf("a product this caller cannot read was reported as changed: %+v", got.Changed)
	}
	if got.Any() {
		t.Errorf("a catalogue that matches its release reports a difference: %+v", got)
	}
}

// TestAnOfferedProductWithNoRecordStillAppears is the other half of that: an id in
// the offering that no release carries has to be reported even when its record is
// not readable here, because that is exactly the state somebody needs to see.
func TestAnOfferedProductWithNoRecordStillAppears(t *testing.T) {
	rel := Release{ID: "rel_1", Items: []Item{}}

	got := unpublishedFor(offering("geist"), nil, []Release{rel})

	if len(got.Added) != 1 || got.Added[0].ID != "geist" {
		t.Fatalf("added = %+v, want the id with no readable record", got.Added)
	}
}

func TestTheNewestReleaseIsTheOneCompared(t *testing.T) {
	// ReleasesOf answers newest first and the portal reads [0]. Comparing against
	// the oldest would report every change ever made as unpublished.
	newest := Release{ID: "rel_2", CreatedAt: 200, Items: []Item{at("vpn", 2)}}
	oldest := Release{ID: "rel_1", CreatedAt: 100, Items: []Item{at("vpn", 1)}}

	got := unpublishedFor(offering("vpn"), []Item{at("vpn", 2)}, []Release{newest, oldest})

	if got.ReleaseID != "rel_2" {
		t.Errorf("compared against %q, want the newest", got.ReleaseID)
	}
	if got.Any() {
		t.Errorf("the catalogue matches its newest release and a difference was reported: %+v", got)
	}
}

func TestTheListsAreOrderedByID(t *testing.T) {
	// Two reads of one catalogue must answer the same way, or the panel reshuffles
	// under somebody reading it.
	rel := Release{ID: "rel_1", Items: []Item{at("b", 1), at("a", 1)}}

	got := unpublishedFor(offering("z", "y"), []Item{at("z", 1), at("y", 1)}, []Release{rel})

	if len(got.Added) != 2 || got.Added[0].ID != "y" || got.Added[1].ID != "z" {
		t.Errorf("added is not in id order: %+v", got.Added)
	}
	if len(got.Removed) != 2 || got.Removed[0].ID != "a" || got.Removed[1].ID != "b" {
		t.Errorf("removed is not in id order: %+v", got.Removed)
	}
}
