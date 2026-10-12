package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// A catalogue leaves one installation and arrives on another through the whole
// stack (ADR-draft-catalogue-export): exported with the processes its product is
// bound to, imported by somebody who may deploy them, and deployed before the
// catalogue is written — once, because a second import finds them unchanged.
func TestACatalogueMovesBetweenServersWithItsProcesses(t *testing.T) {
	// The source: two processes deployed, a catalogue whose product binds them.
	src, _ := newAuthServer(t, "root", "rootpassword")
	srcAdmin := newClient(t)
	if login(t, srcAdmin, src, "root", "rootpassword") != http.StatusOK {
		t.Fatal("source admin login failed")
	}
	for _, id := range []string{"prov-badge", "deprov-badge"} {
		if code, body := cReqCT(t, srcAdmin, src, "POST", "/api/v1/deployments", deployableBPMN(id), "application/xml"); code != http.StatusOK {
			t.Fatalf("deploy %s = %d (%s)", id, code, body)
		}
	}
	doc := `{"catalogs":[{"id":"cat-amt","texts":{"de":"Amt"},"rank":3,"languages":["de"],"items":["badge"]}],
	  "products":[{"id":"badge","homeCatalog":"cat-amt","state":"active","texts":{"de":"Badge"},
	    "approval":{"kind":"none"},"provisionProcess":"prov-badge","deprovisionProcess":"deprov-badge"}]}`
	if code, body := cReq(t, srcAdmin, src, "POST", "/api/v1/catalogs/import", doc); code != http.StatusOK {
		t.Fatalf("source import = %d (%s)", code, body)
	}
	code, exported := cReq(t, srcAdmin, src, "GET", "/api/v1/catalogs/cat-amt/document?processes=true", "")
	if code != http.StatusOK || strings.Count(string(exported), `"xml"`) != 2 {
		t.Fatalf("export = %d (%s), want the catalogue with two processes", code, exported)
	}

	// The target: a product manager alone may import a catalogue, not deploy.
	dst, _ := newAuthServer(t, "root", "rootpassword")
	dstAdmin := newClient(t)
	if login(t, dstAdmin, dst, "root", "rootpassword") != http.StatusOK {
		t.Fatal("target admin login failed")
	}
	anna := aProductManager(t, dst, dstAdmin, "anna")
	if code, body := cReq(t, anna, dst, "POST", "/api/v1/catalogs/import", string(exported)); code != http.StatusForbidden ||
		!strings.Contains(string(body), "modeler") {
		t.Fatalf("a product manager importing processes = %d (%s), want 403", code, body)
	}
	if code, _ := cReq(t, anna, dst, "GET", "/api/v1/catalogs/cat-amt", ""); code != http.StatusNotFound {
		t.Fatalf("a refused import wrote the catalogue: %d", code)
	}

	createUserWithRoles(t, dstAdmin, dst.URL, "mara", `["productmanager","modeler"]`)
	mara := signInAs(t, dst.URL, "mara", "a-password-that-is-long")
	var result struct {
		Created   []string `json:"created"`
		Deployed  []string `json:"deployed"`
		Unchanged []string `json:"unchanged"`
	}
	code, body := cReq(t, mara, dst, "POST", "/api/v1/catalogs/import", string(exported))
	if err := json.Unmarshal(body, &result); code != http.StatusOK || err != nil ||
		len(result.Created) != 2 || len(result.Deployed) != 2 {
		t.Fatalf("mara's import = %d (%s), want the catalogue, the product and two processes", code, body)
	}
	if code, body := cReq(t, mara, dst, "GET", "/api/v1/processes", ""); code != http.StatusOK ||
		!strings.Contains(string(body), "prov-badge") || !strings.Contains(string(body), "deprov-badge") {
		t.Fatalf("the target's processes = %d (%s)", code, body)
	}
	result.Deployed = nil
	code, body = cReq(t, mara, dst, "POST", "/api/v1/catalogs/import", string(exported))
	if err := json.Unmarshal(body, &result); code != http.StatusOK || err != nil ||
		len(result.Deployed) != 0 || len(result.Unchanged) != 2 {
		t.Fatalf("importing again = %d (%s), want both processes left unchanged", code, body)
	}

	// A model that deploys a different process than it is filed under is refused
	// before anything is written.
	wrong := strings.Replace(string(exported), `"id":"prov-badge"`, `"id":"prov-other"`, 1)
	if code, body := cReq(t, mara, dst, "POST", "/api/v1/catalogs/import", wrong); code != http.StatusUnprocessableEntity ||
		!strings.Contains(string(body), "deploys no process prov-other") {
		t.Fatalf("a mislabelled process = %d (%s), want 422", code, body)
	}
}

// Chosen products leave one catalogue and land in another one the importer
// maintains, through the whole stack.
func TestChosenProductsMoveIntoAnotherCatalogue(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	anna := aProductManager(t, ts, admin, "anna")
	doc := `{"catalogs":[{"id":"cat-a","texts":{"de":"A"},"rank":3,"languages":["de"],"items":["badge","key"],
	    "edges":[{"from":"key","to":"badge","kind":"requires"}]},
	  {"id":"cat-b","texts":{"de":"B"},"rank":4,"languages":["de"]}],
	  "products":[{"id":"badge","homeCatalog":"cat-a","state":"active","texts":{"de":"Badge"}},
	    {"id":"key","homeCatalog":"cat-a","state":"active","texts":{"de":"Schlüssel"}}]}`
	if code, body := cReq(t, anna, ts, "POST", "/api/v1/catalogs/import", doc); code != http.StatusOK {
		t.Fatalf("seed = %d (%s)", code, body)
	}
	code, products := cReq(t, anna, ts, "GET", "/api/v1/catalog-products/document?ids=badge,key", "")
	if code != http.StatusOK || !strings.Contains(string(products), `"edges"`) {
		t.Fatalf("product export = %d (%s)", code, products)
	}
	if code, body := cReq(t, anna, ts, "POST", "/api/v1/catalogs/import?into=cat-b", string(products)); code != http.StatusOK {
		t.Fatalf("import into cat-b = %d (%s)", code, body)
	}
	code, body := cReq(t, anna, ts, "GET", "/api/v1/catalogs/cat-b", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"items":["badge","key"]`) || !strings.Contains(string(body), `"from":"key"`) {
		t.Fatalf("cat-b = %d (%s), want both products and their edge", code, body)
	}
}
