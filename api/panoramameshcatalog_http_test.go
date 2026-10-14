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

// getProductMap reads the other picture: the catalogue, what each product is
// assembled from, and the processes that provision it (?view=products).
func getProductMap(t *testing.T, ts *httptest.Server) meshGraph {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/panorama/mesh?view=products", "", "")
	if code != http.StatusOK {
		t.Fatalf("GET product map status = %d, body = %s", code, body)
	}
	var g meshGraph
	if err := json.Unmarshal(body, &g); err != nil {
		t.Fatalf("decode product map: %v (%s)", err, body)
	}
	return g
}

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

	g := getProductMap(t, ts)

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

	code, body := cReq(t, anna, ts, "GET", "/api/v1/panorama/mesh?view=products", "")
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

// TestTheLandscapeKeepsTheCatalogueOffIt is the correction #1022's first cut needed,
// proved over HTTP: the same server that answers a product map answers the landscape
// without a single product on it. Two pictures, one derivation, and the picture an
// operator opens for a stuck process is the estate.
func TestTheLandscapeKeepsTheCatalogueOffIt(t *testing.T) {
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
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatalf("decode catalogue: %v (%s)", err, body)
	}
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/catalog-products",
		`{"id":"vpn","homeCatalog":"`+cat.ID+`","state":"active","texts":{"de":"VPN-Zugang"},`+
			`"approval":{"kind":"none"},"provisionProcess":"provision-vpn","deprovisionProcess":""}`,
		"application/json"); code != http.StatusOK {
		t.Fatalf("save product: %d (%s)", code, b)
	}
	if code, b := doReq(t, ts, http.MethodPatch, "/api/v1/catalogs/"+cat.ID,
		`{"items":["vpn"]}`, "application/json"); code != http.StatusOK {
		t.Fatalf("offer the product: %d (%s)", code, b)
	}

	landscape := getMesh(t, ts)
	for _, n := range landscape.Nodes {
		if n.Kind == "catalog" || n.Kind == "product" {
			t.Errorf("the landscape carries %s %q; the catalogue is the other picture", n.Kind, n.ID)
		}
	}
	// And the estate is still there — the correction removed the catalogue, not the
	// landscape's own subject.
	var deployed bool
	for _, n := range landscape.Nodes {
		if n.ProcessID == "provision-vpn" {
			deployed = true
		}
	}
	if !deployed {
		t.Errorf("the landscape lost the deployed process: %+v", landscape.Nodes)
	}

	// The same server, asked the other question, draws it.
	products := getProductMap(t, ts)
	if n := meshNodeByID(t, products, "catalog:"+cat.ID); n.Name != "Arbeitsplatz" {
		t.Errorf("the product map does not carry the catalogue: %+v", products.Nodes)
	}
}

// TestTheProductMapCarriesTheCatalogueFacets is the wiring half of #1067: whether
// what the catalogue store holds about an offering reaches the browser at all.
//
// api/panorama proves the derivation puts the two facets on the node when the
// landscape carries them. What it cannot see is the step before that — the
// collector reading them off the stored item — and that step is exactly where a
// facet goes missing without anything failing: the picture still draws, every
// product is still there, and the control that would narrow by them simply never
// appears. A filter with no boxes looks like a filter with nothing to offer.
func TestTheProductMapCarriesTheCatalogueFacets(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Arbeitsplatz"}}`, "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d (%s)", code, body)
	}
	var cat struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatalf("decode catalogue: %v (%s)", err, body)
	}
	// One product per answer worth telling apart: live and needing a signature,
	// live and not, and one that was retired.
	for _, product := range []string{
		`{"id":"vpn","homeCatalog":"` + cat.ID + `","state":"active","texts":{"de":"VPN-Zugang"},` +
			`"approval":{"kind":"superior"},"provisionProcess":"","deprovisionProcess":""}`,
		`{"id":"mouse","homeCatalog":"` + cat.ID + `","state":"active","texts":{"de":"Maus"},` +
			`"approval":{"kind":"none"},"provisionProcess":"","deprovisionProcess":""}`,
		`{"id":"pager","homeCatalog":"` + cat.ID + `","state":"withdrawn","texts":{"de":"Pager"},` +
			`"approval":{"kind":"none"},"provisionProcess":"","deprovisionProcess":""}`,
	} {
		if code, b := doReq(t, ts, http.MethodPost, "/api/v1/catalog-products", product, "application/json"); code != http.StatusOK {
			t.Fatalf("save product: %d (%s)", code, b)
		}
	}
	if code, b := doReq(t, ts, http.MethodPatch, "/api/v1/catalogs/"+cat.ID,
		`{"items":["vpn","mouse","pager"]}`, "application/json"); code != http.StatusOK {
		t.Fatalf("offer the products: %d (%s)", code, b)
	}

	g := getProductMap(t, ts)

	for _, want := range []struct{ id, state, approval string }{
		{"product:vpn", "active", "superior"},
		{"product:mouse", "active", "none"},
		{"product:pager", "withdrawn", "none"},
	} {
		n := meshNodeByID(t, g, want.id)
		if n.ProductState != want.state || n.ApprovalKind != want.approval {
			t.Errorf("%s reached the browser as state %q approval %q; want %q and %q",
				want.id, n.ProductState, n.ApprovalKind, want.state, want.approval)
		}
	}
	// And nothing that is not an offering claims to be one. The approval facet is
	// the one that matters here: "no approval rule" is indistinguishable from "not
	// a product" to anything reading the field alone, so a catalogue node that
	// carried an empty ApprovalKind as a *value* would be filtered out by a reader
	// asking for the products that need a signature.
	for _, n := range g.Nodes {
		if n.Kind == "product" {
			continue
		}
		if n.ProductState != "" || n.ApprovalKind != "" {
			t.Errorf("a %s node carries a catalogue facet: %+v", n.Kind, n)
		}
	}
}
