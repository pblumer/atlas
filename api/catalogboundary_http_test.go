package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The catalogue's object axis, through the whole stack.
//
// api/catalog holds this boundary and tests it thoroughly in its own package. What
// those tests cannot see is the wiring: whether the route was registered with the
// role it meant, whether the principal reaches the service at all, whether the
// screen's own calls land on the handlers that check. This branch has found three
// defects of exactly that shape — a guard that was right in one package and
// contradicted in another — so the boundary is proven here as well, over real
// HTTP, with two real accounts.
//
// ADR-0315 draws the line: the role says whether somebody may maintain catalogues,
// the catalogue's own members say which ones. Without the second, one product
// manager rebuilds and publishes every customer's catalogue, with nothing in the
// way but not knowing an id — which ADR-0278 states plainly is not an access
// control.

// aProductManager creates an account, gives it the role, and signs it in.
func aProductManager(t *testing.T, ts *httptest.Server, admin *http.Client, name string) *http.Client {
	t.Helper()
	c := twoUsers(t, ts, admin, name)[0]
	id := userID(t, ts, admin, name)
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/users/"+id,
		`{"roles":["user","productmanager"]}`); code != http.StatusOK {
		t.Fatalf("grant productmanager to %s: %d (%s)", name, code, b)
	}
	return c
}

// ownCatalogue has the given client create a catalogue of its own and returns its id.
func ownCatalogue(t *testing.T, ts *httptest.Server, c *http.Client, name string) string {
	t.Helper()
	code, body := cReq(t, c, ts, "POST", "/api/v1/catalogs",
		`{"rank":`+fmt.Sprint(len(name))+`,"languages":["de"],"texts":{"de":"`+name+`"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create %s: %d (%s)", name, code, body)
	}
	var cat struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	return cat.ID
}

func TestAProductManagerReachesOnlyTheirOwnCatalogues(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	anna := aProductManager(t, ts, admin, "anna")
	bruno := aProductManager(t, ts, admin, "bruno")

	annas := ownCatalogue(t, ts, anna, "Anna")
	brunos := ownCatalogue(t, ts, bruno, "Bruno")

	// The listing is the screen's first call, and the one that would leak the
	// existence of every other customer. It answers with what the caller
	// maintains and nothing else.
	code, body := cReq(t, anna, ts, "GET", "/api/v1/catalogs", "")
	if code != http.StatusOK {
		t.Fatalf("list: %d (%s)", code, body)
	}
	var seen []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &seen); err != nil {
		t.Fatalf("decode listing: %v (%s)", err, body)
	}
	if len(seen) != 1 || seen[0].ID != annas {
		t.Fatalf("anna's listing = %v, want only her own %s", seen, annas)
	}

	// Knowing an id is no qualification, and the refusal says "no such thing"
	// rather than "not yours": a 403 here would answer the question it withholds.
	if code, _ := cReq(t, anna, ts, "GET", "/api/v1/catalogs/"+brunos, ""); code != http.StatusNotFound {
		t.Errorf("reading bruno's catalogue = %d, want 404", code)
	}
	if code, _ := cReq(t, anna, ts, "PATCH", "/api/v1/catalogs/"+brunos,
		`{"rank":99}`); code == http.StatusOK {
		t.Error("anna changed bruno's catalogue")
	}
	// Publishing is the act with the blast radius: it is what the portal serves.
	if code, _ := cReq(t, anna, ts, "POST", "/api/v1/catalogs/"+brunos+"/releases", ""); code == http.StatusOK ||
		code == http.StatusCreated {
		t.Error("anna published bruno's catalogue")
	}
	if code, _ := cReq(t, anna, ts, "GET", "/api/v1/catalogs/"+brunos+"/releases", ""); code == http.StatusOK {
		t.Error("anna read bruno's releases")
	}

	// A product is edited through its home catalogue, so naming somebody else's
	// home must not be a way in.
	if code, _ := cReq(t, anna, ts, "POST", "/api/v1/catalog-products",
		`{"id":"schleichweg","homeCatalog":"`+brunos+`","state":"draft","texts":{"de":"x"},`+
			`"approval":{"kind":"none"},"provisionProcess":"prov","deprovisionProcess":"deprov"}`); code == http.StatusOK {
		t.Error("anna created a product in bruno's catalogue")
	}

	// And the product listing follows the same axis: bruno's products are his.
	if code, b := cReq(t, bruno, ts, "POST", "/api/v1/catalog-products",
		`{"id":"brunos-laptop","homeCatalog":"`+brunos+`","state":"draft","texts":{"de":"Notebook"},`+
			`"approval":{"kind":"none"},"provisionProcess":"prov","deprovisionProcess":"deprov"}`); code != http.StatusOK {
		t.Fatalf("bruno's own product: %d (%s)", code, b)
	}
	code, body = cReq(t, anna, ts, "GET", "/api/v1/catalog-products", "")
	if code != http.StatusOK {
		t.Fatalf("product listing: %d (%s)", code, body)
	}
	var products []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &products); err != nil {
		t.Fatalf("decode products: %v (%s)", err, body)
	}
	for _, p := range products {
		if p.ID == "brunos-laptop" {
			t.Error("anna's product listing carries bruno's product")
		}
	}
}

// The role alone is not the boundary, and the boundary alone is not the role.
// Somebody without the role cannot maintain a catalogue even if they own nothing
// to be kept out of — which is what makes the two axes two.
func TestWithoutTheRoleThereIsNoCatalogueMaintenanceAtAll(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	plain := twoUsers(t, ts, admin, "paula")[0]

	if code, _ := cReq(t, plain, ts, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Versuch"}}`); code != http.StatusForbidden {
		t.Errorf("a plain user created a catalogue: %d, want 403", code)
	}
	if code, _ := cReq(t, plain, ts, "POST", "/api/v1/catalog-products",
		`{"id":"x","state":"draft"}`); code != http.StatusForbidden {
		t.Errorf("a plain user saved a product: %d, want 403", code)
	}
}

// Sharing is the owner's, and an editor's write right does not carry it.
//
// This was open until it was looked for. An editor could rewrite the member list,
// which makes the grant self-amplifying: whoever is given editor hands editor to
// anybody, and the owner's choice of who maintains their catalogue stops being the
// owner's. ADR-0071 says the owner "can read, write, share (edit membership)" and
// the role beneath it is read/write — and projects already enforce exactly that
// with checkProjectRole(..., ScopeRoleOwner). The catalogue was the one object that
// did not.
//
// The refusal is loud rather than a silent drop: a maintainer told "saved" would
// believe a grant exists that does not, which is worse than being refused.
func TestSharingACatalogueIsTheOwnersAndNotAnEditors(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	anna := aProductManager(t, ts, admin, "anna")
	bruno := aProductManager(t, ts, admin, "bruno")
	carla := aProductManager(t, ts, admin, "carla")
	brunoID := userID(t, ts, admin, "bruno")
	carlaID := userID(t, ts, admin, "carla")

	annas := ownCatalogue(t, ts, anna, "Anna")
	member := func(id, role string) string {
		return `{"ref":{"type":"user","id":"` + id + `"},"role":"` + role + `"}`
	}

	// The owner shares. That is hers.
	if code, b := cReq(t, anna, ts, "PATCH", "/api/v1/catalogs/"+annas,
		`{"members":[`+member(brunoID, "editor")+`]}`); code != http.StatusOK {
		t.Fatalf("the owner could not share: %d (%s)", code, b)
	}

	// The editor may change the catalogue…
	if code, b := cReq(t, bruno, ts, "PATCH", "/api/v1/catalogs/"+annas, `{"rank":42}`); code != http.StatusOK {
		t.Fatalf("an editor could not change the catalogue: %d (%s)", code, b)
	}
	// …and may not change who else may.
	code, body := cReq(t, bruno, ts, "PATCH", "/api/v1/catalogs/"+annas,
		`{"members":[`+member(brunoID, "editor")+`,`+member(carlaID, "editor")+`]}`)
	if code != http.StatusForbidden {
		t.Errorf("an editor rewrote the member list: %d (%s)", code, body)
	}

	// The grant did not happen, which is the half a status code alone would not
	// prove: carla was never chosen by the owner and cannot write.
	if c, _ := cReq(t, carla, ts, "PATCH", "/api/v1/catalogs/"+annas, `{"rank":77}`); c == http.StatusOK {
		t.Error("somebody the owner never chose can change the catalogue")
	}

	// An administrator still can, as everywhere.
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+annas,
		`{"members":[`+member(carlaID, "viewer")+`]}`); code != http.StatusOK {
		t.Errorf("an administrator could not share: %d (%s)", code, b)
	}
}

// With enforcement off there is nobody to be, not nobody who may.
//
// Atlas's default single-binary build runs without authentication, and the
// catalogue read that as "no identity, therefore no rights": a catalogue could be
// created — 201, with an id — and from the next request onward it answered 404 to
// its own author. The screen for filling a catalogue was unusable on the default
// configuration, and the failure looked like the catalogue had vanished rather
// than like a refusal.
//
// Every other area of Atlas already reads enforcement-off as "everything is
// permitted": Server.isAdmin and requireAdmin both return true, and the drawer's
// mayUse offers every app. This holds the catalogue to the same rule.
func TestWithAuthenticationOffACatalogueCanStillBeMaintained(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Arbeitsplatz"}}`, "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create: %d (%s)", code, body)
	}
	var cat struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}

	// The three the screen does next, each of which answered 404 before.
	if c, b := doReq(t, ts, "GET", "/api/v1/catalogs/"+cat.ID, "", ""); c != http.StatusOK {
		t.Errorf("read it back: %d (%s) — a catalogue that hides from its own author", c, b)
	}
	if c, b := doReq(t, ts, "PATCH", "/api/v1/catalogs/"+cat.ID, `{"rank":5}`, "application/json"); c != http.StatusOK {
		t.Errorf("change it: %d (%s)", c, b)
	}
	if c, b := doReq(t, ts, "PATCH", "/api/v1/catalogs/"+cat.ID,
		`{"members":[{"ref":{"type":"user","id":"usr_x"},"role":"editor"}]}`, "application/json"); c != http.StatusOK {
		t.Errorf("share it: %d (%s)", c, b)
	}
	// And it is in the listing the screen opens with.
	c, list := doReq(t, ts, "GET", "/api/v1/catalogs", "", "")
	if c != http.StatusOK {
		t.Fatalf("list: %d (%s)", c, list)
	}
	var seen []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(list, &seen); err != nil {
		t.Fatalf("decode listing: %v (%s)", err, list)
	}
	if len(seen) != 1 || seen[0].ID != cat.ID {
		t.Errorf("listing = %v, want the catalogue that was just created", seen)
	}
}
