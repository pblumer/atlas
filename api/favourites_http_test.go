package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/limits"
)

// Marking a product, end to end (ADR-0348).

type favsResp struct {
	Principal string   `json:"principal"`
	ItemIDs   []string `json:"itemIds"`
}

func favsOf(t *testing.T, ts *httptest.Server, c *http.Client) favsResp {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", "/api/v1/portal/favourites", "")
	if code != http.StatusOK {
		t.Fatalf("read favourites: %d (%s)", code, body)
	}
	var got favsResp
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode favourites: %v (%s)", err, body)
	}
	return got
}

// TestAMarkSurvivesAndIsTheCallersOwn.
//
// Two people marking things, and neither seeing the other's. A favourites list
// is per account and there is deliberately no way to ask about anybody else —
// so the test that matters is that two accounts do not collide in the store.
func TestAMarkSurvivesAndIsTheCallersOwn(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	other := twoUsers(t, ts, admin, "mallory")[0]

	if got := favsOf(t, ts, admin); len(got.ItemIDs) != 0 {
		t.Fatalf("a fresh account has %v marked, want nothing", got.ItemIDs)
	}

	for _, id := range []string{"vpn", "laptop"} {
		if code, b := cReq(t, admin, ts, "PUT",
			"/api/v1/portal/favourites/"+id, ""); code != http.StatusOK {
			t.Fatalf("mark %s: %d (%s)", id, code, b)
		}
	}
	mine := favsOf(t, ts, admin)
	if len(mine.ItemIDs) != 2 || mine.ItemIDs[0] != "laptop" || mine.ItemIDs[1] != "vpn" {
		t.Fatalf("= %v, want both, sorted", mine.ItemIDs)
	}

	// The other account sees its own empty list, not this one.
	if got := favsOf(t, ts, other); len(got.ItemIDs) != 0 {
		t.Errorf("another account reads %v; a favourites list is one person's", got.ItemIDs)
	}
	if code, b := cReq(t, other, ts, "PUT",
		"/api/v1/portal/favourites/copilot", ""); code != http.StatusOK {
		t.Fatalf("the other account cannot mark: %d (%s)", code, b)
	}
	if got := favsOf(t, ts, admin); len(got.ItemIDs) != 2 {
		t.Errorf("= %v; another account's mark reached this list", got.ItemIDs)
	}

	// Unmarking takes one away and leaves the rest.
	if code, b := cReq(t, admin, ts, "DELETE",
		"/api/v1/portal/favourites/vpn", ""); code != http.StatusOK {
		t.Fatalf("unmark: %d (%s)", code, b)
	}
	if got := favsOf(t, ts, admin); len(got.ItemIDs) != 1 || got.ItemIDs[0] != "laptop" {
		t.Errorf("= %v, want only the laptop left", got.ItemIDs)
	}
}

// TestAMarkNeedsNoCatalogueToResolveTo.
//
// A favourite is a bookmark and never an entitlement: it stores an id and says
// "show me this again", not "I may have this". So marking a product that no
// released catalogue carries is accepted — the mark is about finding something,
// and everything that decides whether it may be *ordered* is asked elsewhere, by
// the routes that already decide it.
//
// The alternative would have been to validate against the caller's catalogue,
// which sounds tidier and is a trap: a catalogue reassignment would then start
// refusing marks the person already has, and a withdrawn product would make an
// existing list unwritable.
func TestAMarkNeedsNoCatalogueToResolveTo(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	if code, b := cReq(t, admin, ts, "PUT",
		"/api/v1/portal/favourites/gibt-es-nicht", ""); code != http.StatusOK {
		t.Fatalf("marking an unresolvable product = %d (%s), want 200", code, b)
	}
	if got := favsOf(t, ts, admin); len(got.ItemIDs) != 1 {
		t.Errorf("= %v, want the mark kept", got.ItemIDs)
	}
}

// TestTheFavouriteCeilingRefusesRatherThanGrowing.
//
// A ceiling nobody tested is a ceiling that might not be there. This one is a
// product judgement as much as a budget — a shortcut list nobody can scan has
// stopped being a shortcut — but it is a budget too: without it one account grows
// a stored file without bound by pressing a star.
//
// The refusal has to say what to do about it. "422" on a star is the kind of
// thing somebody reports as the page being broken.
func TestTheFavouriteCeilingRefusesRatherThanGrowing(t *testing.T) {
	// A ceiling of two, so the test is about the rule and not about patience.
	budgets := limits.Default()
	budgets.Favourites = 2
	ts, _ := newAuthServerWith(t, "root", "rootpassword", api.WithLimits(budgets))
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}

	for _, id := range []string{"a", "b"} {
		if code, b := cReq(t, admin, ts, "PUT",
			"/api/v1/portal/favourites/"+id, ""); code != http.StatusOK {
			t.Fatalf("mark %s: %d (%s)", id, code, b)
		}
	}
	code, body := cReq(t, admin, ts, "PUT", "/api/v1/portal/favourites/c", "")
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("marking past the ceiling = %d (%s), want 422", code, body)
	}
	if !contains(string(body), "clear one") {
		t.Errorf("the refusal does not say what to do about it: %s", body)
	}
	// And the list is untouched: a refused mark must not have half-written.
	if got := favsOf(t, ts, admin); len(got.ItemIDs) != 2 {
		t.Errorf("= %v, want the two that fit", got.ItemIDs)
	}
	// Re-marking one that is already there stays fine at the ceiling — it writes
	// nothing, so it cannot exceed anything.
	if code, b := cReq(t, admin, ts, "PUT",
		"/api/v1/portal/favourites/a", ""); code != http.StatusOK {
		t.Errorf("re-marking at the ceiling = %d (%s), want 200: it adds nothing", code, b)
	}
}

// TestFavouritesNeedAnAccount.
//
// With enforcement off there is nobody to be, and a favourites list belongs to
// somebody. Answering an empty list for "nobody" would put every single-user
// installation's marks in one shared bucket keyed by the empty string.
func TestFavouritesNeedAnAccount(t *testing.T) {
	ts := newTestServer(t)
	c := newClient(t)
	if code, _ := cReq(t, c, ts, "GET", "/api/v1/portal/favourites", ""); code != http.StatusBadRequest {
		t.Errorf("favourites with nobody signed in = %d, want 400", code)
	}
}
