package catalog

import (
	"net/http"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// The portal in the mode that has no identities (ADR-draft-portal-without-identity).
//
// With --auth=false there is no principal, so there are no groups, so ReachedBy
// answers false for every catalogue and the portal could never resolve one: the
// documented development and demo mode showed an empty page telling somebody a
// catalogue had not been assigned to them, when there is no "them" to assign one
// to.
//
// The relaxation is narrow and these hold its edges. A catalogue with no audience
// still reaches nobody wherever there *is* somebody, and an administrator who is
// signed in still gets their own audience's catalogue rather than the top-ranked
// one — being allowed to read every catalogue is not the same as being the
// audience for one.

// twoCatalogues makes a low-ranked and a high-ranked catalogue, neither of which
// names an audience.
func twoCatalogues(t *testing.T, s *Service, owner *httpapi.Principal) (low, high Catalog) {
	t.Helper()
	rec := as(t, s.HandleCreateCatalog, owner, "POST", `{"rank":1,"languages":["de"],"texts":{"de":"Klein"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create low = %d (%s)", rec.Code, rec.Body)
	}
	low = decode[Catalog](t, rec)
	rec = as(t, s.HandleCreateCatalog, owner, "POST", `{"rank":9,"languages":["de"],"texts":{"de":"Gross"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create high = %d (%s)", rec.Code, rec.Body)
	}
	high = decode[Catalog](t, rec)
	return low, high
}

// TestWithNobodyToBeThePortalResolvesTheHighestRankedCatalogue.
//
// Rank is the tie-break the product already uses to decide which of several
// catalogues somebody sees, so it is what "which catalogue" means when there is no
// audience question left to ask.
func TestWithNobodyToBeThePortalResolvesTheHighestRankedCatalogue(t *testing.T) {
	s := openService(t)
	_, high := twoCatalogues(t, s, user("usr_owner"))

	rec := as(t, s.HandleMyCatalog, nil, "GET", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("the portal answered %d (%s) in the mode it is documented for", rec.Code, rec.Body)
	}
	if got := decode[Catalog](t, rec); got.ID != high.ID {
		t.Errorf("resolved %q, want the highest-ranked %q", got.ID, high.ID)
	}
}

// TestWithNobodyToBeAndNoCataloguesTheAnswerIsStillNothing.
//
// The relaxation says which catalogue, not that there is one. An installation
// with none must still say so, or the page reports a failure where the honest
// answer is that nobody has built a catalogue yet.
func TestWithNobodyToBeAndNoCataloguesTheAnswerIsStillNothing(t *testing.T) {
	s := openService(t)
	if rec := as(t, s.HandleMyCatalog, nil, "GET", ""); rec.Code != http.StatusNotFound {
		t.Errorf("an installation with no catalogues answered %d, want 404", rec.Code)
	}
}

// TestWhereThereIsSomebodyTheAudienceStillDecides.
//
// Both edges of the relaxation, in one case each.
//
// With enforcement on, a caller with no session reaches nothing — the fail-closed
// rule this package is otherwise strict about, unchanged. And an administrator who
// *is* signed in gets the catalogue their groups reach, not the top-ranked one: the
// portal asks which shop is theirs, and reading every shop is a different right.
func TestWhereThereIsSomebodyTheAudienceStillDecides(t *testing.T) {
	s := serviceWithAdmin(t)
	owner := user("usr_owner")
	low, high := twoCatalogues(t, s, owner)

	// Nobody signed in, enforcement on: nothing.
	if rec := as(t, s.HandleMyCatalog, nil, "GET", ""); rec.Code != http.StatusNotFound {
		t.Errorf("with enforcement on, a caller with no session got %d (%s)", rec.Code, rec.Body)
	}

	// The low-ranked catalogue names an audience; the high-ranked one does not.
	if rec := as(t, s.HandleUpdateCatalog, owner, "PATCH",
		`{"groups":["grp_inside"]}`, "id", low.ID); rec.Code != http.StatusOK {
		t.Fatalf("assign audience = %d (%s)", rec.Code, rec.Body)
	}
	admin := &httpapi.Principal{UserID: "usr_admin", Roles: []string{"admin"}, GroupIDs: []string{"grp_inside"}}
	rec := as(t, s.HandleMyCatalog, admin, "GET", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("a signed-in administrator in the audience got %d (%s)", rec.Code, rec.Body)
	}
	if got := decode[Catalog](t, rec); got.ID != low.ID {
		t.Errorf("resolved %q, want the one their groups reach (%q) rather than the "+
			"highest-ranked (%q)", got.ID, low.ID, high.ID)
	}
}

// TestSomebodyPresentOutranksTheModeTheyArePresentIn.
//
// The condition is "nobody is signed in **and** there is nobody to be", and the
// first half carries its own weight even though today it cannot be false while the
// second is true: with --auth=false no principal is ever built. It is the guard
// that keeps this a relaxation about *absence* rather than about the mode, so that
// a future single-user mode which synthesised a principal — the option this
// decision turned down — would find the audience still deciding rather than
// silently handing everybody the top-ranked catalogue.
//
// Asked directly, because no configuration reaches it: an open service, and a
// principal all the same.
func TestSomebodyPresentOutranksTheModeTheyArePresentIn(t *testing.T) {
	s := openService(t)
	owner := user("usr_owner")
	low, high := twoCatalogues(t, s, owner)
	if rec := as(t, s.HandleUpdateCatalog, owner, "PATCH",
		`{"groups":["grp_inside"]}`, "id", low.ID); rec.Code != http.StatusOK {
		t.Fatalf("assign audience = %d (%s)", rec.Code, rec.Body)
	}

	rec := as(t, s.HandleMyCatalog, user("usr_someone", "grp_inside"), "GET", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("a principal in the audience got %d (%s)", rec.Code, rec.Body)
	}
	if got := decode[Catalog](t, rec); got.ID != low.ID {
		t.Errorf("resolved %q, want the audience's %q rather than the highest-ranked %q",
			got.ID, low.ID, high.ID)
	}
	// And somebody present who reaches nothing reaches nothing, in this mode too.
	if rec := as(t, s.HandleMyCatalog, user("usr_outsider", "grp_elsewhere"), "GET", ""); rec.Code != http.StatusNotFound {
		t.Errorf("a principal outside every audience got %d (%s)", rec.Code, rec.Body)
	}
}

// TestHighestIsTheSameRuleResolveUses.
//
// Resolve is now Highest over the reached ones, which is worth holding: two
// orderings would mean the catalogue somebody sees depends on whether an audience
// was asked about, and the difference would only show up in an installation with
// several catalogues at different ranks.
func TestHighestIsTheSameRuleResolveUses(t *testing.T) {
	all := []Catalog{
		{ID: "cat_b", Rank: 5, Groups: []string{"g"}},
		{ID: "cat_a", Rank: 9, Groups: []string{"g"}},
		{ID: "cat_c", Rank: 1, Groups: []string{"g"}},
	}
	byAudience, ok := Resolve(all, []string{"g"})
	if !ok {
		t.Fatal("Resolve found nothing for a group every catalogue names")
	}
	byRank, ok := Highest(all)
	if !ok {
		t.Fatal("Highest found nothing in a non-empty set")
	}
	if byAudience.ID != byRank.ID || byRank.ID != "cat_a" {
		t.Errorf("Resolve says %q and Highest says %q, want cat_a from both",
			byAudience.ID, byRank.ID)
	}
	if _, ok := Highest(nil); ok {
		t.Error("Highest found a catalogue in an empty set")
	}
}
