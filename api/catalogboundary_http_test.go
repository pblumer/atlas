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
