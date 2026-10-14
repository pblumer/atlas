package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// A whole shop as one document, through the whole stack
// (ADR-0436): the route is a product
// manager's, the catalogues it writes are theirs, and a second product manager can
// neither re-import over them nor take a product from them.
func TestAProductManagerImportsAShopAsOneDocument(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	anna := aProductManager(t, ts, admin, "anna")
	bert := aProductManager(t, ts, admin, "bert")
	plain := twoUsers(t, ts, admin, "paula")[0]

	doc := `{"publish":true,
	  "catalogs":[{"id":"cat-amt","texts":{"de":"Amt"},"rank":3,"languages":["de"],"items":["badge"]}],
	  "products":[{"id":"badge","homeCatalog":"cat-amt","state":"active","texts":{"de":"Badge"},
	    "approval":{"kind":"none"},"provisionProcess":"p","deprovisionProcess":"d"}]}`
	if code, body := cReq(t, plain, ts, "POST", "/api/v1/catalogs/import", doc); code != http.StatusForbidden {
		t.Fatalf("a plain user importing = %d (%s), want 403", code, body)
	}
	code, body := cReq(t, anna, ts, "POST", "/api/v1/catalogs/import", doc)
	if code != http.StatusOK {
		t.Fatalf("anna's import = %d (%s)", code, body)
	}
	var result struct {
		Created  []string `json:"created"`
		Releases []struct {
			CatalogID string `json:"catalogId"`
		} `json:"releases"`
	}
	if err := json.Unmarshal(body, &result); err != nil || len(result.Created) != 2 ||
		len(result.Releases) != 1 || result.Releases[0].CatalogID != "cat-amt" {
		t.Fatalf("anna's result = %s", body)
	}
	if code, body := cReq(t, anna, ts, "GET", "/api/v1/catalogs/cat-amt", ""); code != http.StatusOK ||
		!strings.Contains(string(body), `"ownerId"`) {
		t.Fatalf("anna reading her catalogue = %d (%s)", code, body)
	}

	if code, body := cReq(t, bert, ts, "POST", "/api/v1/catalogs/import", doc); code != http.StatusForbidden {
		t.Fatalf("bert re-importing anna's catalogue = %d (%s), want 403", code, body)
	}
	steal := `{"catalogs":[{"id":"cat-bert","texts":{"de":"Bert"},"rank":4,"languages":["de"]}],
	  "products":[{"id":"badge","homeCatalog":"cat-bert","state":"active"}]}`
	if code, body := cReq(t, bert, ts, "POST", "/api/v1/catalogs/import", steal); code != http.StatusForbidden {
		t.Fatalf("bert taking anna's product = %d (%s), want 403", code, body)
	}
	if code, _ := cReq(t, bert, ts, "GET", "/api/v1/catalogs/cat-bert", ""); code != http.StatusNotFound {
		t.Fatalf("a refused import wrote bert's catalogue anyway: %d", code)
	}
}
