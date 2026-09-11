package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// TestAStrangerCannotChangeSomebodyElsesCatalogue is the hole this closes. A
// stranger cannot see it either, so the refusal is 404: telling them it exists
// and is merely not theirs answers the question the gate was meant to withhold.
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
			if rec.Code != http.StatusNotFound {
				t.Fatalf("code = %d (%s), want 404", rec.Code, rec.Body)
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
	// A non-member cannot see it, so it reads as absent rather than refused.
	if rec := as(t, s.HandleUpdateCatalog, user("usr_c", "grp_other"), "PATCH", `{"rank":4}`, "id", cat.ID); rec.Code != http.StatusNotFound {
		t.Errorf("non-member got %d, want 404", rec.Code)
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
// without an identity must not be treated as an unowned catalogue's owner, and it
// is told nothing about the catalogue at all.
func TestNoPrincipalChangesNothing(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_a"))

	if rec := as(t, s.HandleUpdateCatalog, nil, "PATCH", `{"rank":4}`, "id", cat.ID); rec.Code != http.StatusNotFound {
		t.Fatalf("anonymous got %d, want 404", rec.Code)
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
	// A stranger cannot see the home catalogue either, so the product reads as
	// absent rather than as somebody else's.
	if rec := as(t, s.HandleSaveItem, user("usr_stranger"), "POST", item); rec.Code != http.StatusNotFound {
		t.Fatalf("stranger got %d, want 404", rec.Code)
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

// productBody renders a saveable product.
func productBody(id, home string) string {
	return `{"id":"` + id + `","homeCatalog":"` + home + `","state":"active",` +
		`"texts":{"de":"X"},"approval":{"kind":"none"},` +
		`"provisionProcess":"p","deprovisionProcess":"d"}`
}

// Reading is not open either. A catalogue carries its audience, its approval
// rules, its product list and its process bindings; at ten catalogues named after
// customers, the *listing alone* tells one customer who the others are. So there
// are two sights of the same store: the portal one, which is the catalogue you
// are the audience for, and the maintenance one, which is the catalogues you
// maintain.

// TestPortalReadFollowsTheAudience: you read the catalogue you may order from.
func TestPortalReadFollowsTheAudience(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_owner"))
	as(t, s.HandleUpdateCatalog, user("usr_owner"), "PATCH", `{"groups":["grp_staff"]}`, "id", cat.ID)

	staff := &httpapi.Principal{UserID: "usr_s", Roles: []string{"user"}, GroupIDs: []string{"grp_staff"}}
	if rec := as(t, s.HandleGetCatalog, staff, "GET", "", "id", cat.ID); rec.Code != http.StatusOK {
		t.Errorf("the audience got %d, want 200", rec.Code)
	}
	if rec := as(t, s.HandleListReleases, staff, "GET", "", "id", cat.ID); rec.Code != http.StatusOK {
		t.Errorf("the audience reading releases got %d, want 200", rec.Code)
	}
}

// TestAnOutsiderIsToldNothing — 404, not 403. A refusal that distinguishes "not
// yours" from "no such thing" answers the question it was meant to withhold.
func TestAnOutsiderIsToldNothing(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_owner"))
	as(t, s.HandleUpdateCatalog, user("usr_owner"), "PATCH", `{"groups":["grp_staff"]}`, "id", cat.ID)

	outsider := &httpapi.Principal{UserID: "usr_x", Roles: []string{"user"}, GroupIDs: []string{"grp_other"}}
	for _, tt := range []struct {
		name   string
		h      http.HandlerFunc
		method string
		body   string
	}{
		{"get", s.HandleGetCatalog, "GET", ""},
		{"releases", s.HandleListReleases, "GET", ""},
		{"update", s.HandleUpdateCatalog, "PATCH", `{"rank":9}`},
		{"publish", s.HandlePublish, "POST", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := as(t, tt.h, outsider, tt.method, tt.body, "id", cat.ID)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("code = %d (%s), want 404", rec.Code, rec.Body)
			}
		})
	}
}

// TestTheAudienceMayReadButNotWrite: 403 once you can see it, because then its
// existence is no longer the secret — only the authority is.
func TestTheAudienceMayReadButNotWrite(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_owner"))
	as(t, s.HandleUpdateCatalog, user("usr_owner"), "PATCH", `{"groups":["grp_staff"]}`, "id", cat.ID)

	staff := &httpapi.Principal{UserID: "usr_s", Roles: []string{"user"}, GroupIDs: []string{"grp_staff"}}
	if rec := as(t, s.HandleUpdateCatalog, staff, "PATCH", `{"rank":9}`, "id", cat.ID); rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
}

// TestListingShowsOnlyWhatYouMaintain: the management sight. A customer must not
// learn from a list which other customers exist.
func TestListingShowsOnlyWhatYouMaintain(t *testing.T) {
	s := serviceWithAdmin(t)
	mine := makeCatalog(t, s, user("usr_a"))
	makeCatalog(t, s, user("usr_b"))

	got := decode[[]Catalog](t, as(t, s.HandleListCatalogs, user("usr_a"), "GET", ""))
	if len(got) != 1 || got[0].ID != mine.ID {
		t.Fatalf("listing = %v, want only my own", got)
	}

	// An administrator sees all of them, as everywhere else.
	admin := &httpapi.Principal{UserID: "usr_root", Roles: []string{"admin"}}
	if n := len(decode[[]Catalog](t, as(t, s.HandleListCatalogs, admin, "GET", ""))); n != 2 {
		t.Fatalf("admin sees %d, want 2", n)
	}

	// Being the audience is not maintaining: it puts nothing in this list.
	as(t, s.HandleUpdateCatalog, user("usr_a"), "PATCH", `{"groups":["grp_staff"]}`, "id", mine.ID)
	staff := &httpapi.Principal{UserID: "usr_s", Roles: []string{"user"}, GroupIDs: []string{"grp_staff"}}
	if n := len(decode[[]Catalog](t, as(t, s.HandleListCatalogs, staff, "GET", ""))); n != 0 {
		t.Fatalf("the audience sees %d catalogues in the maintenance listing, want 0", n)
	}
}

// TestProductListingFollowsTheHomeCatalogue: a product's rules — its approval,
// its process bindings — belong to whoever maintains it.
func TestProductListingFollowsTheHomeCatalogue(t *testing.T) {
	s := serviceWithAdmin(t)
	mine := makeCatalog(t, s, user("usr_a"))
	theirs := makeCatalog(t, s, user("usr_b"))
	as(t, s.HandleSaveItem, user("usr_a"), "POST", productBody("mine", mine.ID))
	as(t, s.HandleSaveItem, user("usr_b"), "POST", productBody("theirs", theirs.ID))

	got := decode[[]Item](t, as(t, s.HandleListItems, user("usr_a"), "GET", ""))
	if len(got) != 1 || got[0].ID != "mine" {
		t.Fatalf("products = %v, want only mine", got)
	}
}

// TestGrantedRoleWithNoPrincipal: the pure half of the gate, checked directly
// because every handler leans on it failing closed.
func TestGrantedRoleWithNoPrincipal(t *testing.T) {
	c := Catalog{ID: "c", Members: []Member{
		{Ref: PrincipalRef{Type: "user", ID: "usr_a"}, Role: RoleEditor}}}
	if got := grantedRole(c, nil); got != "" {
		t.Fatalf("grantedRole(nil) = %q, want none", got)
	}
}

// TestEditorOutranksViewer: somebody granted both directly and through a group
// gets the stronger, not whichever entry comes first.
func TestEditorOutranksViewer(t *testing.T) {
	p := &httpapi.Principal{UserID: "usr_a", GroupIDs: []string{"grp_pm"}}
	c := Catalog{ID: "c", Members: []Member{
		{Ref: PrincipalRef{Type: "user", ID: "usr_a"}, Role: RoleViewer},
		{Ref: PrincipalRef{Type: "group", ID: "grp_pm"}, Role: RoleEditor},
	}}
	if got := grantedRole(c, p); got != RoleEditor {
		t.Fatalf("grantedRole = %q, want editor", got)
	}

	// And the other way round, so the answer does not depend on the order.
	c.Members[0], c.Members[1] = c.Members[1], c.Members[0]
	if got := grantedRole(c, p); got != RoleEditor {
		t.Fatalf("grantedRole = %q with the entries swapped, want editor", got)
	}
}

// TestProductListingReportsAnUnreadableCatalogueStore: the products load, the
// home lookup does not, and that must surface as an error rather than as a short
// list somebody reads as "you maintain nothing".
func TestProductListingReportsAnUnreadableCatalogueStore(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SaveItem(newItem("a")); err != nil {
		t.Fatalf("SaveItem: %v", err)
	}
	// Only the catalogue directory is broken.
	p := filepath.Join(dir, "catalogs")
	if err := os.RemoveAll(p); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if err := os.WriteFile(p, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })
	s := New(loop, store, func() int64 { return 1700 },
		func(pr *httpapi.Principal) bool { return pr.HasRole("admin") })

	if rec := as(t, s.HandleListItems, user("usr_a"), "GET", ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d (%s), want 500", rec.Code, rec.Body)
	}
	// Saving one hits the same lookup.
	if rec := as(t, s.HandleSaveItem, user("usr_a"), "POST", productBody("b", "cat_1")); rec.Code != http.StatusInternalServerError {
		t.Fatalf("save code = %d, want 500", rec.Code)
	}
}

// TestAViewerOfTheHomeCatalogueCannotSaveProducts: once somebody can see the
// catalogue, hiding the product would be pretending. The refusal is plain.
func TestAViewerOfTheHomeCatalogueCannotSaveProducts(t *testing.T) {
	s := serviceWithAdmin(t)
	home := makeCatalog(t, s, user("usr_a"))
	as(t, s.HandleUpdateCatalog, user("usr_a"), "PATCH",
		`{"members":[{"ref":{"type":"user","id":"usr_v"},"role":"viewer"}]}`, "id", home.ID)

	if rec := as(t, s.HandleSaveItem, user("usr_v"), "POST", productBody("laptop", home.ID)); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer got %d, want 403", rec.Code)
	}
}

// TestRehomingOutOfACatalogueYouOnlyRead: the losing side is visible, so this is
// a refusal rather than a disappearance — and the product does not move.
func TestRehomingOutOfACatalogueYouOnlyRead(t *testing.T) {
	s := serviceWithAdmin(t)
	from := makeCatalog(t, s, user("usr_a"))
	to := makeCatalog(t, s, user("usr_b"))
	as(t, s.HandleSaveItem, user("usr_a"), "POST", productBody("laptop", from.ID))

	// usr_b may read the losing catalogue but not change it.
	as(t, s.HandleUpdateCatalog, user("usr_a"), "PATCH",
		`{"members":[{"ref":{"type":"user","id":"usr_b"},"role":"viewer"}]}`, "id", from.ID)

	if rec := as(t, s.HandleSaveItem, user("usr_b"), "POST", productBody("laptop", to.ID)); rec.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403", rec.Code)
	}
	items := decode[[]Item](t, as(t, s.HandleListItems, user("usr_a"), "GET", ""))
	if len(items) != 1 || items[0].HomeCatalog != from.ID {
		t.Fatalf("product moved: %v", items)
	}
}

// TestPublishingACatalogueYouOnlyRead is the same rule on the publish path.
func TestPublishingACatalogueYouOnlyRead(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_a"))
	as(t, s.HandleUpdateCatalog, user("usr_a"), "PATCH",
		`{"members":[{"ref":{"type":"user","id":"usr_v"},"role":"viewer"}]}`, "id", cat.ID)

	if rec := as(t, s.HandlePublish, user("usr_v"), "POST", "", "id", cat.ID); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer publishing got %d, want 403", rec.Code)
	}
}
