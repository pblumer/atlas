package catalog

import (
	"net/http"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// One catalogue per person, resolved from the groups they carry and the rank the
// catalogues carry (decision 4). Rank breaks the many-to-many: a person is in
// dozens of directory groups and several may point at catalogues, so the highest
// one they reach wins — deterministically, because publishing refuses a tie.

func cat(id string, rank int, groups ...string) Catalog {
	return Catalog{ID: id, Rank: rank, Groups: groups}
}

func TestTheHighestRankReached(t *testing.T) {
	cats := []Catalog{
		cat("c_low", 1, "grp_all"),
		cat("c_mid", 5, "grp_staff"),
		cat("c_high", 9, "grp_board"),
	}

	got, ok := Resolve(cats, []string{"grp_all", "grp_staff"})
	if !ok || got.ID != "c_mid" {
		t.Fatalf("got %q (found %v), want c_mid", got.ID, ok)
	}

	got, ok = Resolve(cats, []string{"grp_all", "grp_staff", "grp_board"})
	if !ok || got.ID != "c_high" {
		t.Fatalf("got %q, want c_high", got.ID)
	}
}

// TestReachingNothingIsAnAnswer: somebody in none of the groups has no catalogue,
// and that has to be distinguishable from an empty one.
func TestReachingNothingIsAnAnswer(t *testing.T) {
	if _, ok := Resolve([]Catalog{cat("c", 1, "grp_a")}, []string{"grp_b"}); ok {
		t.Fatal("a stranger reached a catalogue")
	}
	if _, ok := Resolve(nil, []string{"grp_a"}); ok {
		t.Fatal("resolved a catalogue out of none")
	}
}

// TestACatalogueWithNoAudienceReachesNobody. Fail closed: a freshly created
// catalogue has no groups yet, and the dangerous default is the one where it is
// visible to everybody while somebody is still filling it.
func TestACatalogueWithNoAudienceReachesNobody(t *testing.T) {
	if _, ok := Resolve([]Catalog{cat("c", 1)}, []string{"grp_a"}); ok {
		t.Fatal("a catalogue with no audience was reached")
	}
	if _, ok := Resolve([]Catalog{cat("c", 1)}, nil); ok {
		t.Fatal("a catalogue with no audience was reached by somebody with no groups")
	}
}

// TestMayOrderFromFollowsTheAudience, not the role: which catalogue somebody
// orders from is a fact about them, not about their authority.
func TestMayOrderFromFollowsTheAudience(t *testing.T) {
	s := serviceWithAdmin(t)
	mine := makeCatalog(t, s, user("usr_owner"))

	share := `{"groups":["grp_staff"]}`
	if rec := as(t, s.HandleUpdateCatalog, user("usr_owner"), "PATCH", share, "id", mine.ID); rec.Code != http.StatusOK {
		t.Fatalf("set audience = %d (%s)", rec.Code, rec.Body)
	}

	staff := &httpapi.Principal{UserID: "usr_s", Roles: []string{"user"}, GroupIDs: []string{"grp_staff"}}
	other := &httpapi.Principal{UserID: "usr_o", Roles: []string{"user"}, GroupIDs: []string{"grp_other"}}

	if ok, err := s.MayOrderFrom(staff, mine.ID); err != nil || !ok {
		t.Errorf("a member of the audience may not order: %v, %v", ok, err)
	}
	if ok, err := s.MayOrderFrom(other, mine.ID); err != nil || ok {
		t.Errorf("somebody outside the audience may order: %v, %v", ok, err)
	}
}

// TestWhoeverMaintainsACatalogueMayOrderFromIt: otherwise nobody can check what
// they built, and the first real order is the test.
func TestWhoeverMaintainsACatalogueMayOrderFromIt(t *testing.T) {
	s := serviceWithAdmin(t)
	mine := makeCatalog(t, s, user("usr_owner"))
	as(t, s.HandleUpdateCatalog, user("usr_owner"), "PATCH", `{"groups":["grp_staff"]}`, "id", mine.ID)

	if ok, err := s.MayOrderFrom(user("usr_owner"), mine.ID); err != nil || !ok {
		t.Errorf("the owner may not order from their own catalogue: %v, %v", ok, err)
	}
	if ok, err := s.MayOrderFrom(user("usr_stranger"), mine.ID); err != nil || ok {
		t.Errorf("a stranger with no group may order: %v, %v", ok, err)
	}
}

func TestMayOrderFromAnUnknownCatalogue(t *testing.T) {
	s := serviceWithAdmin(t)
	if ok, err := s.MayOrderFrom(user("usr_a"), "cat_nope"); err != nil || ok {
		t.Fatalf("got %v, %v; want false and no error", ok, err)
	}
}

func TestMayOrderFromWithNoIdentity(t *testing.T) {
	s := serviceWithAdmin(t)
	mine := makeCatalog(t, s, user("usr_owner"))
	if ok, err := s.MayOrderFrom(nil, mine.ID); err != nil || ok {
		t.Fatalf("got %v, %v; want false and no error", ok, err)
	}
}

// TestThePortalAnswersWhichCatalogueIsMine — the first question of every portal
// session, and the endpoint the ordering check is built on.
func TestThePortalAnswersWhichCatalogueIsMine(t *testing.T) {
	s := serviceWithAdmin(t)
	low := makeCatalog(t, s, user("usr_owner"))
	high := makeCatalog(t, s, user("usr_owner"))
	as(t, s.HandleUpdateCatalog, user("usr_owner"), "PATCH", `{"rank":1,"groups":["grp_all"]}`, "id", low.ID)
	as(t, s.HandleUpdateCatalog, user("usr_owner"), "PATCH", `{"rank":9,"groups":["grp_board"]}`, "id", high.ID)

	member := &httpapi.Principal{UserID: "usr_b", Roles: []string{"user"},
		GroupIDs: []string{"grp_all", "grp_board"}}
	rec := as(t, s.HandleMyCatalog, member, "GET", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("mine = %d (%s)", rec.Code, rec.Body)
	}
	if got := decode[Catalog](t, rec); got.ID != high.ID {
		t.Fatalf("got %s, want the higher-ranked %s", got.ID, high.ID)
	}

	// Somebody in neither group has none, and says so rather than showing an
	// arbitrary one.
	nobody := &httpapi.Principal{UserID: "usr_n", Roles: []string{"user"}}
	if rec := as(t, s.HandleMyCatalog, nobody, "GET", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("mine = %d, want 404", rec.Code)
	}
}
