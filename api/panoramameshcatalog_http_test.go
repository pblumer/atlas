package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The service catalogue on the starmap (#1022), through the whole stack.
//
// api/panorama proves the derivation as arithmetic. What it cannot see is the
// wiring: whether the catalogue store is read at all, whether a product's name
// survives being multilingual, whether the process a product binds is joined to the
// process the engine deployed, and — the half that matters most — whether the picture
// is filtered per reader before it leaves the server.

const catalogueProvisionBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="provision-vpn" name="Provision a VPN account" isExecutable="true">
    <startEvent id="s"/><endEvent id="e"/>
    <sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`

// TestTheStarmapDrawsTheCatalogueAndWhatProvisionsIt.
//
// The join is the point. A catalogue promises a VPN account and a laptop; the engine
// has a process for one of them and nothing for the other, and until now no single
// surface could say so — the catalogue screen cannot see the engine, and Operations
// has never heard of the catalogue.
func TestTheStarmapDrawsTheCatalogueAndWhatProvisionsIt(t *testing.T) {
	ts := newTestServer(t)

	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/deployments",
		catalogueProvisionBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, b)
	}
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Arbeitsplatz"}}`, "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d (%s)", code, body)
	}
	var cat struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
	}
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatalf("decode catalogue: %v (%s)", err, body)
	}
	for _, product := range []string{
		`{"id":"vpn","homeCatalog":"` + cat.ID + `","state":"active","texts":{"de":"VPN-Zugang"},` +
			`"approval":{"kind":"none"},"provisionProcess":"provision-vpn","deprovisionProcess":"revoke-vpn"}`,
		`{"id":"laptop","homeCatalog":"` + cat.ID + `","state":"active","texts":{"de":"Notebook"},` +
			`"approval":{"kind":"none"},"provisionProcess":"","deprovisionProcess":""}`,
	} {
		if code, b := doReq(t, ts, http.MethodPost, "/api/v1/catalog-products", product, "application/json"); code != http.StatusOK {
			t.Fatalf("save product: %d (%s)", code, b)
		}
	}
	if code, b := doReq(t, ts, http.MethodPatch, "/api/v1/catalogs/"+cat.ID,
		`{"items":["laptop","vpn"],"edges":[{"from":"laptop","to":"vpn","kind":"aggregation"}]}`,
		"application/json"); code != http.StatusOK {
		t.Fatalf("offer the products: %d (%s)", code, b)
	}

	g := getMesh(t, ts)

	if n := meshNodeByID(t, g, "catalog:"+cat.ID); n.Kind != "catalog" || n.Name != "Arbeitsplatz" {
		t.Errorf("catalogue node = %+v, want the catalogue named in its own language", n)
	}
	if n := meshNodeByID(t, g, "product:vpn"); n.Name != "VPN-Zugang" {
		t.Errorf("product node = %+v", n)
	}
	for _, id := range []string{"product:vpn", "product:laptop"} {
		if !meshHasEdge(g, "catalog:"+cat.ID, id, "offers") {
			t.Errorf("the catalogue does not offer %s: %+v", id, g.Edges)
		}
	}
	if !meshHasEdge(g, "product:laptop", "product:vpn", "aggregation") {
		t.Errorf("the arrangement is missing from %+v", g.Edges)
	}

	// The process that exists is joined to the product that names it; the one that
	// does not is the finding this picture exists to make.
	var provision string
	for _, n := range g.Nodes {
		if n.ProcessID == "provision-vpn" {
			provision = n.ID
		}
	}
	if provision == "" {
		t.Fatalf("the deployed process is not on the picture: %+v", g.Nodes)
	}
	if !meshHasEdge(g, "product:vpn", provision, "uses") {
		t.Errorf("the product is not joined to the process that provisions it: %+v", g.Edges)
	}
	if !meshHasEdge(g, "product:vpn", "unresolved:process:revoke-vpn", "uses") {
		t.Errorf("a product bound to a process nobody deployed must say so: %+v", g.Edges)
	}
	// A product that binds nothing points at nothing, rather than at an unresolved
	// dependency named "".
	for _, e := range g.Edges {
		if e.From == "product:laptop" && e.Kind == "uses" {
			t.Errorf("a product with no process bound produced %+v", e)
		}
	}
}

// TestTheStarmapDrawsOnlyTheCataloguesYouMaintain.
//
// A catalogue carries its audience, its approval rules and its price list, and two
// product managers on one server maintain different ones (ADR-0315). The starmap is
// derived per reader and has to honour that, or it becomes the way around it.
func TestTheStarmapDrawsOnlyTheCataloguesYouMaintain(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	// Both maintain catalogues; only Anna reads the starmap, which needs the modeler
	// role. That combination is the honest case: the picture is an architect's, the
	// catalogue is a product manager's, and whoever is looking at both is holding two
	// roles at once.
	anna := aProductManager(t, ts, admin, "anna")
	grantModeler(t, ts, admin, "anna")
	bruno := aProductManager(t, ts, admin, "bruno")
	annas := ownCatalogue(t, ts, anna, "Annas Katalog")
	brunos := ownCatalogue(t, ts, bruno, "Brunos Katalog")

	if code, b := cReq(t, bruno, ts, "POST", "/api/v1/catalog-products",
		`{"id":"secret","homeCatalog":"`+brunos+`","state":"active","texts":{"de":"Brunos Produkt"},`+
			`"approval":{"kind":"none"},"provisionProcess":"p","deprovisionProcess":"d"}`); code != http.StatusOK {
		t.Fatalf("bruno saves a product: %d (%s)", code, b)
	}
	if code, b := cReq(t, bruno, ts, "PATCH", "/api/v1/catalogs/"+brunos,
		`{"items":["secret"]}`); code != http.StatusOK {
		t.Fatalf("bruno offers it: %d (%s)", code, b)
	}

	code, body := cReq(t, anna, ts, "GET", "/api/v1/panorama/mesh", "")
	if code != http.StatusOK {
		t.Fatalf("anna's mesh: %d (%s)", code, body)
	}
	var g meshGraph
	if err := json.Unmarshal(body, &g); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	for _, n := range g.Nodes {
		if n.ID == "catalog:"+brunos || n.ID == "product:secret" || n.Name == "Brunos Produkt" {
			t.Fatalf("anna's starmap carries bruno's catalogue: %+v", n)
		}
	}
	if n := meshNodeByID(t, g, "catalog:"+annas); n.Name != "Annas Katalog" {
		t.Errorf("anna's own catalogue = %+v", n)
	}
}

// grantModeler adds the role the starmap is behind, keeping the ones the account has.
func grantModeler(t *testing.T, ts *httptest.Server, admin *http.Client, name string) {
	t.Helper()
	id := userID(t, ts, admin, name)
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/users/"+id,
		`{"roles":["user","productmanager","modeler"]}`); code != http.StatusOK {
		t.Fatalf("grant modeler to %s: %d (%s)", name, code, b)
	}
}
