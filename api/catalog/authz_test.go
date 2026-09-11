package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
)

// The second axis (ADR-0278). The role says a caller may maintain catalogues at
// all; this says which ones. Without it every product manager can rebuild and
// publish every customer's catalogue, and the only thing in the way is not
// knowing an id — which ADR-0278 states plainly is not an access control.

func serviceWithAdmin(t *testing.T) *Service {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })
	return New(loop, store, func() int64 { return 1700 },
		func(p *httpapi.Principal) bool { return p.HasRole("admin") })
}

// as runs a handler as a given principal.
func as(t *testing.T, h http.HandlerFunc, p *httpapi.Principal, method, body string, vals ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/x", strings.NewReader(body))
	if p != nil {
		req = req.WithContext(httpapi.WithPrincipal(context.Background(), p))
	}
	for i := 0; i+1 < len(vals); i += 2 {
		req.SetPathValue(vals[i], vals[i+1])
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func user(id string, groups ...string) *httpapi.Principal {
	return &httpapi.Principal{UserID: id, Roles: []string{"productmanager"}, GroupIDs: groups}
}

// makeCatalog creates one owned by owner and returns it.
func makeCatalog(t *testing.T, s *Service, owner *httpapi.Principal) Catalog {
	t.Helper()
	rec := as(t, s.HandleCreateCatalog, owner, "POST", `{"rank":1,"languages":["de"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d (%s)", rec.Code, rec.Body)
	}
	return decode[Catalog](t, rec)
}

// TestTheCreatorOwnsTheCatalogue: ownership has to come from somewhere, and the
// only moment nobody has to be asked is creation.
func TestTheCreatorOwnsTheCatalogue(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_a"))
	if cat.OwnerID != "usr_a" {
		t.Fatalf("owner = %q, want usr_a", cat.OwnerID)
	}
}

// TestAStrangerCannotChangeSomebodyElsesCatalogue is the hole this closes.
func TestAStrangerCannotChangeSomebodyElsesCatalogue(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_a"))

	for _, tt := range []struct {
		name string
		h    http.HandlerFunc
		body string
	}{
		{"update", s.HandleUpdateCatalog, `{"rank":9}`},
		{"publish", s.HandlePublish, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := as(t, tt.h, user("usr_stranger"), "POST", tt.body, "id", cat.ID)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("code = %d (%s), want 403", rec.Code, rec.Body)
			}
		})
	}

	// And nothing changed.
	got := decode[Catalog](t, as(t, s.HandleGetCatalog, user("usr_a"), "GET", "", "id", cat.ID))
	if got.Rank != 1 {
		t.Fatalf("rank = %d, want the original 1", got.Rank)
	}
}

// TestAnEditorMayChangeIt, and a viewer may not: the two member roles ADR-0071
// defines mean what they say here too.
func TestAnEditorMayChangeItAndAViewerMayNot(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_a"))

	share := `{"members":[{"ref":{"type":"user","id":"usr_ed"},"role":"editor"},` +
		`{"ref":{"type":"user","id":"usr_view"},"role":"viewer"}]}`
	if rec := as(t, s.HandleUpdateCatalog, user("usr_a"), "PATCH", share, "id", cat.ID); rec.Code != http.StatusOK {
		t.Fatalf("share = %d (%s)", rec.Code, rec.Body)
	}

	if rec := as(t, s.HandleUpdateCatalog, user("usr_ed"), "PATCH", `{"rank":4}`, "id", cat.ID); rec.Code != http.StatusOK {
		t.Errorf("editor got %d (%s), want 200", rec.Code, rec.Body)
	}
	if rec := as(t, s.HandleUpdateCatalog, user("usr_view"), "PATCH", `{"rank":5}`, "id", cat.ID); rec.Code != http.StatusForbidden {
		t.Errorf("viewer got %d, want 403", rec.Code)
	}
}

// TestAGroupGrantReachesItsMembers (ADR-0180): sharing with a team is what makes
// the mechanism usable, and membership is on the principal already.
func TestAGroupGrantReachesItsMembers(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_a"))

	share := `{"members":[{"ref":{"type":"group","id":"grp_pm"},"role":"editor"}]}`
	as(t, s.HandleUpdateCatalog, user("usr_a"), "PATCH", share, "id", cat.ID)

	if rec := as(t, s.HandleUpdateCatalog, user("usr_b", "grp_pm"), "PATCH", `{"rank":4}`, "id", cat.ID); rec.Code != http.StatusOK {
		t.Errorf("group member got %d (%s), want 200", rec.Code, rec.Body)
	}
	if rec := as(t, s.HandleUpdateCatalog, user("usr_c", "grp_other"), "PATCH", `{"rank":4}`, "id", cat.ID); rec.Code != http.StatusForbidden {
		t.Errorf("non-member got %d, want 403", rec.Code)
	}
}

// TestAdminReachesEveryCatalogue: the one role that is a superset, for the reason
// ADR-0209 gives — an instance where the administrator cannot fix a thing on the
// day its usual holder is unreachable is not administered.
func TestAdminReachesEveryCatalogue(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_a"))

	admin := &httpapi.Principal{UserID: "usr_root", Roles: []string{"admin"}}
	if rec := as(t, s.HandleUpdateCatalog, admin, "PATCH", `{"rank":4}`, "id", cat.ID); rec.Code != http.StatusOK {
		t.Fatalf("admin got %d (%s), want 200", rec.Code, rec.Body)
	}
}

// TestNoPrincipalChangesNothing: the gate fails closed. A request that arrived
// without an identity must not be treated as an unowned catalogue's owner.
func TestNoPrincipalChangesNothing(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_a"))

	if rec := as(t, s.HandleUpdateCatalog, nil, "PATCH", `{"rank":4}`, "id", cat.ID); rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous got %d, want 403", rec.Code)
	}
}

// TestAProductIsEditedThroughItsHomeCatalogue (the roles record): a product is
// referenced by several catalogues and changed through exactly one, so a
// catalogue cannot alter a product another catalogue depends on.
func TestAProductIsEditedThroughItsHomeCatalogue(t *testing.T) {
	s := serviceWithAdmin(t)
	home := makeCatalog(t, s, user("usr_a"))

	item := `{"id":"laptop","homeCatalog":"` + home.ID + `","state":"active",` +
		`"texts":{"de":"L"},"approval":{"kind":"none"},` +
		`"provisionProcess":"p","deprovisionProcess":"d"}`

	if rec := as(t, s.HandleSaveItem, user("usr_a"), "POST", item); rec.Code != http.StatusOK {
		t.Fatalf("owner got %d (%s), want 200", rec.Code, rec.Body)
	}
	if rec := as(t, s.HandleSaveItem, user("usr_stranger"), "POST", item); rec.Code != http.StatusForbidden {
		t.Fatalf("stranger got %d, want 403", rec.Code)
	}
}

// TestAProductNeedsAHomeThatExists: without this a product manager could create
// products belonging to nothing, which is a product nobody is responsible for.
func TestAProductNeedsAHomeThatExists(t *testing.T) {
	s := serviceWithAdmin(t)
	item := `{"id":"ghost","homeCatalog":"cat_nope","state":"active",` +
		`"texts":{"de":"G"},"approval":{"kind":"none"},` +
		`"provisionProcess":"p","deprovisionProcess":"d"}`

	rec := as(t, s.HandleSaveItem, user("usr_a"), "POST", item)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d (%s), want 404", rec.Code, rec.Body)
	}
}

// TestReadingStaysOpen: a portal user browses a catalogue, so reads are not
// gated on the write axis. What they may *see* is the rank-and-groups question,
// which is a different record and a different endpoint.
func TestReadingStaysOpen(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_a"))

	stranger := &httpapi.Principal{UserID: "usr_x", Roles: []string{"user"}}
	for _, tt := range []struct {
		name string
		h    http.HandlerFunc
	}{
		{"get", s.HandleGetCatalog},
		{"releases", s.HandleListReleases},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if rec := as(t, tt.h, stranger, "GET", "", "id", cat.ID); rec.Code != http.StatusOK {
				t.Fatalf("code = %d, want 200", rec.Code)
			}
		})
	}
}
