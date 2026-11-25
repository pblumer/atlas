package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// A person's shop as an OAuth resource of its own, and a catalogue maintained in the
// system that holds the grant (ADR-0565).
//
// The connected system of ADR-0512 is the one that asks: an administrator connects it
// with pre-consent, a person's browser is sent through for `…/shop` and comes back
// with a code without a page, and the token the system then holds shops, follows
// orders and works tasks as that person — and reaches nothing else. A product manager's
// grant of the same system imports a catalogue document, which marks the catalogue as
// maintained there: the Console's own edits are refused with the system's name until an
// administrator takes it back.

// wikiCatalogue is a document as the wiki publishes it.
const wikiCatalogue = `{"catalogs":[{"id":"cat-orbit","texts":{"de":"Orbit"},"rank":42,"languages":["de"],"items":["notebook"],
  "maintainedIn":{"url":"https://wiki.example.org/seiten/katalog"}}],
  "products":[{"id":"notebook","homeCatalog":"cat-orbit","state":"draft","texts":{"de":"Notebook"},"approval":{"kind":"none"}}]}`

func TestAShopGrantShopsAsThePersonAndPublishesTheCataloguesItMaintains(t *testing.T) {
	ts, admin := connectedAtlas(t)
	sys := newFakeSystem(t)

	// The resource is published, so a system can find what to ask for.
	code, meta := csGetJSON(t, http.DefaultClient, ts.URL+"/.well-known/oauth-protected-resource/shop")
	if code != http.StatusOK || meta["resource"] != ts.URL+"/shop" {
		t.Fatalf("…/shop metadata = %d %v, want 200 naming %s/shop", code, meta, ts.URL)
	}

	// Pre-consent applies to the shop as it does to the tasks (ADR-0512 §3).
	sys.connect(t, admin, ts.URL, []map[string]any{{"scope": "tasklist"}}, true)
	createUserWithRoles(t, admin, ts.URL, "pam", `["user","productmanager"]`)
	pam := signInAs(t, ts.URL, "pam", "a-password-that-is-long")
	shop := sys.authorizeQuery(ts.URL+"/shop", "")
	shop.Set("prompt", "none")
	status, to := visit(t, pam, ts.URL, shop)
	if status != http.StatusFound || !strings.Contains(to, "code=") {
		t.Fatalf("pre-consented request for …/shop → %d %q, want a code without a page", status, to)
	}
	grant := sys.exchange(t, ts.URL, to)["access_token"].(string)

	// The shop, the orders, the approvals and the tasks, as pam.
	for _, path := range []string{"/api/v1/auth/me", "/api/v1/shop/catalogs", "/api/v1/orders", "/api/v1/approvals", "/api/v1/tasks", "/api/v1/catalogs"} {
		if code, body := bearerJSON(t, ts.URL, "GET", path, grant); code != http.StatusOK {
			t.Errorf("the shop grant on GET %s = %d %s, want 200", path, code, body)
		}
	}
	// Nothing else, whatever pam's roles allow: not the engine, not the people, not the
	// catalogue editor's own writes, and not the order routes kept off MCP.
	for _, w := range []struct{ method, path string }{
		{"GET", "/api/v1/processes"},
		{"GET", "/api/v1/instances"},
		{"GET", "/api/v1/users"},
		{"POST", "/api/v1/catalogs"},
		{"PATCH", "/api/v1/catalogs/cat-orbit"},
		{"POST", "/api/v1/catalogs/cat-orbit/releases"},
		{"POST", "/api/v1/orders/o/lines/i/start"},
		{"POST", "/api/v1/orders/o/lines/i/decision"},
		{"POST", "/api/v1/orders/o/lines/i"},
		{"GET", "/mcp"},
	} {
		if code, _ := bearerJSON(t, ts.URL, w.method, w.path, grant); code != http.StatusForbidden {
			t.Errorf("the shop grant on %s %s = %d, want 403", w.method, w.path, code)
		}
	}

	// pam publishes a catalogue through the grant: it is marked as maintained in the
	// system, by its client id and the name it shows.
	code, body := bearerDo(t, ts.URL, "POST", "/api/v1/catalogs/import", wikiCatalogue, grant)
	if code != http.StatusOK {
		t.Fatalf("import through the shop grant = %d %s", code, body)
	}
	code, body = cReq(t, admin, ts, "GET", "/api/v1/catalogs/cat-orbit", "")
	var cat struct {
		MaintainedIn map[string]string `json:"maintainedIn"`
	}
	if err := json.Unmarshal(body, &cat); code != http.StatusOK || err != nil ||
		cat.MaintainedIn["client"] != sys.id || cat.MaintainedIn["name"] != "Example wiki" ||
		cat.MaintainedIn["url"] != "https://wiki.example.org/seiten/katalog" {
		t.Fatalf("the imported catalogue = %d %s, want it maintained in the system", code, body)
	}

	// The Console's edits are refused with the system's name, the administrator's too,
	// and so is deleting it for good.
	for _, w := range []struct{ method, path, body string }{
		{"PATCH", "/api/v1/catalogs/cat-orbit", `{"rank":43}`},
		{"POST", "/api/v1/catalog-products", `{"id":"notebook","homeCatalog":"cat-orbit","state":"draft","texts":{"de":"Notebook X"},"approval":{"kind":"none"}}`},
		{"POST", "/api/v1/catalogs/cat-orbit/releases", ""},
		{"DELETE", "/api/v1/catalogs/cat-orbit?confirm=cat-orbit", ""},
		{"DELETE", "/api/v1/catalog-products/notebook?confirm=notebook", ""},
	} {
		code, body := cReq(t, admin, ts, w.method, w.path, w.body)
		if code != http.StatusConflict || !strings.Contains(string(body), "Example wiki") || !strings.Contains(string(body), `"maintainedIn"`) {
			t.Errorf("%s %s while maintained in the wiki = %d %s, want 409 naming it", w.method, w.path, code, body)
		}
	}

	// Taking it back is an administrator's.
	if code, _ := cReq(t, pam, ts, "DELETE", "/api/v1/catalogs/cat-orbit/maintainer", ""); code != http.StatusForbidden {
		t.Errorf("a product manager taking it back = %d, want 403", code)
	}
	if code, _ := bearerJSON(t, ts.URL, "DELETE", "/api/v1/catalogs/cat-orbit/maintainer", grant); code != http.StatusForbidden {
		t.Errorf("the shop grant taking it back = %d, want 403", code)
	}
	code, body = cReq(t, admin, ts, "DELETE", "/api/v1/catalogs/cat-orbit/maintainer", "")
	if code != http.StatusOK || strings.Contains(string(body), "maintainedIn") {
		t.Fatalf("the administrator taking it back = %d %s", code, body)
	}
	if code, body := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/cat-orbit", `{"rank":43}`); code != http.StatusOK {
		t.Errorf("PATCH after the take-back = %d %s, want 200", code, body)
	}
}

// TestTheShopScopeIsNotMintable: `shop` follows from the resource a person approved, as
// `tasks` does; no token request can ask for it.
func TestTheShopScopeIsNotMintable(t *testing.T) {
	ts, admin := apiTokenServer(t)
	if code, body := cReq(t, admin, ts, "POST", "/api/v1/api-tokens", `{"name":"x","scope":"shop","expiresInDays":30}`); code != http.StatusBadRequest {
		t.Errorf("minting a shop token = %d %s, want 400", code, body)
	}
}
