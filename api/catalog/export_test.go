package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// A catalogue leaves an installation as the same document it arrives as
// (ADR-0451): a whole catalogue with its products, or a choice of
// products to be imported into another catalogue — each optionally with the
// processes the products are bound to.

// fakeModels is a ProcessSource over a fixed set of deployed models.
type fakeModels map[string]string

func (f fakeModels) ProcessXML(processID string) ([]byte, bool) {
	x, ok := f[processID]
	return []byte(x), ok
}

// fakeDeployer records what an import asked it to deploy.
type fakeDeployer struct {
	refuse   []DocumentProblem // what CheckProcesses answers
	deployed [][]Process
	fail     []DocumentProblem // what DeployProcesses answers
}

func (f *fakeDeployer) CheckProcesses(_ *http.Request, _ []Process) []DocumentProblem {
	return f.refuse
}

func (f *fakeDeployer) DeployProcesses(_ *http.Request, procs []Process) ([]string, []string, []DocumentProblem, error) {
	if len(f.fail) > 0 {
		return nil, nil, f.fail, nil
	}
	f.deployed = append(f.deployed, procs)
	var ids []string
	for _, p := range procs {
		ids = append(ids, p.ID)
	}
	return ids, nil, nil, nil
}

// call runs a handler as p against a target carrying a query.
func call(t *testing.T, h http.HandlerFunc, p *httpapi.Principal, method, target, body string, vals ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
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

// exportOf decodes an exported document, failing unless the export answered 200.
func exportOf(t *testing.T, rec *httptest.ResponseRecorder) Document {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("export = %d %s", rec.Code, rec.Body.String())
	}
	var d Document
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	return d
}

// seeded is a service holding shopDocument, imported by importer.
func seeded(t *testing.T) *Service {
	t.Helper()
	s := newService(t)
	if code, out := importDoc(t, s, importer, shopDocument); code != http.StatusOK {
		t.Fatalf("seed = %d %v", code, out)
	}
	return s
}

// TestACatalogueExportsAsADocumentAnotherServerImports: the export is the catalogue
// with every product it maintains or offers, stripped of what belongs to this
// installation — owner, revisions, times, theme — and a second server takes it as it
// stands.
func TestACatalogueExportsAsADocumentAnotherServerImports(t *testing.T) {
	s := seeded(t)
	d := exportOf(t, call(t, s.HandleExportCatalog, importer, "GET", "/x", "", "id", "cat-verwaltung"))
	if len(d.Catalogs) != 1 || d.Catalogs[0].ID != "cat-verwaltung" || len(d.Products) != 2 {
		t.Fatalf("export = %+v, want the catalogue and its two products", d)
	}
	c := d.Catalogs[0]
	if c.OwnerID != "" || c.Revision != 0 || c.CreatedAt != 0 || c.UpdatedAt != 0 || len(c.Edges) != 1 {
		t.Fatalf("the exported catalogue = %+v, want no owner, revision or times, and its edge", c)
	}
	if d.Products[0].ID != "badge" || d.Products[1].ID != "parking" || d.Products[0].Revision != 0 {
		t.Fatalf("the exported products = %+v, want both, in id order, without revisions", d.Products)
	}
	if len(d.Processes) != 0 || d.Publish {
		t.Fatalf("an export without processes = %+v", d)
	}

	// The Bauamt offers the badge it does not maintain; its export carries it, with
	// the home it has.
	bau := exportOf(t, call(t, s.HandleExportCatalog, importer, "GET", "/x", "", "id", "cat-bau"))
	if len(bau.Products) != 1 || bau.Products[0].HomeCatalog != "cat-verwaltung" {
		t.Fatalf("the Bauamt's export = %+v", bau)
	}

	body, _ := json.Marshal(d)
	other := newService(t)
	if code, out := importDoc(t, other, importer, string(body)); code != http.StatusOK || len(out["created"].([]any)) != 3 {
		t.Fatalf("importing the export elsewhere = %d %v, want three records created", code, out)
	}
}

// TestACatalogueExportCarriesItsProcesses: asked for, the BPMN of every process the
// products are bound to travels with them, once each; one this server has not
// deployed is named rather than silently left out.
func TestACatalogueExportCarriesItsProcesses(t *testing.T) {
	s := seeded(t)
	s.Models = fakeModels{"prov-badge": "<b1/>", "deprov-badge": "<b2/>", "prov-parking": "<p1/>"}
	d := exportOf(t, call(t, s.HandleExportCatalog, importer, "GET", "/x?processes=true", "", "id", "cat-verwaltung"))
	var ids []string
	for _, p := range d.Processes {
		ids = append(ids, p.ID)
	}
	if !slices.Equal(ids, []string{"deprov-badge", "prov-badge", "prov-parking"}) || d.Processes[1].XML != "<b1/>" {
		t.Fatalf("processes = %+v", d.Processes)
	}
	if !slices.Equal(d.MissingProcesses, []string{"deprov-parking"}) {
		t.Fatalf("missing = %v, want the one not deployed", d.MissingProcesses)
	}

	s.Models = nil
	if rec := call(t, s.HandleExportCatalog, importer, "GET", "/x?processes=true", "", "id", "cat-verwaltung"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("processes from a server that cannot read them = %d, want 503", rec.Code)
	}
}

// TestProcessesOfNamesWhatAProductRunsOn: the two bindings, the lifecycle process and
// an approval process of the catalogue's own — never Atlas's shipped approval models,
// which every installation has.
func TestProcessesOfNamesWhatAProductRunsOn(t *testing.T) {
	got := processesOf([]Item{
		{ProvisionProcess: "p", DeprovisionProcess: "d", Approval: Approval{Kind: KindFixed}},
		{LifecycleProcess: "l", Approval: Approval{Kind: "my-approval"}},
		{ProvisionProcess: "p", Approval: Approval{Kind: KindNone}},
	})
	if !slices.Equal(got, []string{"d", "l", "my-approval", "p"}) {
		t.Fatalf("processesOf = %v", got)
	}
}

// TestOnlyAMaintainerExportsACatalogue: a catalogue the caller cannot see reads as
// absent, and one they only view is refused — an export is the whole record, members
// and approval rules included, and reading the shop is not maintaining it.
func TestOnlyAMaintainerExportsACatalogue(t *testing.T) {
	s := seeded(t)
	stranger := user("usr_x")
	if rec := call(t, s.HandleExportCatalog, stranger, "GET", "/x", "", "id", "cat-verwaltung"); rec.Code != http.StatusNotFound {
		t.Fatalf("a stranger = %d, want 404", rec.Code)
	}
	if rec := call(t, s.HandleExportCatalog, importer, "GET", "/x", "", "id", "cat-nowhere"); rec.Code != http.StatusNotFound {
		t.Fatalf("no such catalogue = %d, want 404", rec.Code)
	}
	viewed := strings.Replace(shopDocument, `"groups":["grp-alle"],`,
		`"groups":["grp-alle"],"members":[{"ref":{"type":"user","id":"usr_x"},"role":"viewer"}],`, 1)
	if code, out := importDocWith(t, s, importer, "?overwrite=catalog:cat-verwaltung", viewed); code != http.StatusOK {
		t.Fatalf("granting a viewer = %d %v", code, out)
	}
	if rec := call(t, s.HandleExportCatalog, stranger, "GET", "/x", "", "id", "cat-verwaltung"); rec.Code != http.StatusForbidden {
		t.Fatalf("a viewer = %d, want 403", rec.Code)
	}
}

// TestChosenProductsExportWithTheEdgesBetweenThem: the products named, and the edges
// of their home catalogues that join two of them — an edge to a product left behind
// would name something the document does not carry.
func TestChosenProductsExportWithTheEdgesBetweenThem(t *testing.T) {
	s := seeded(t)
	s.Models = fakeModels{"prov-parking": "<p1/>", "deprov-parking": "<p2/>"}
	both := exportOf(t, call(t, s.HandleExportProducts, importer, "GET", "/x?ids=parking,badge", ""))
	if len(both.Catalogs) != 0 || len(both.Products) != 2 || len(both.Edges) != 1 || both.Edges[0].From != "parking" {
		t.Fatalf("both = %+v, want two products and the edge between them", both)
	}
	one := exportOf(t, call(t, s.HandleExportProducts, importer, "GET", "/x?ids=parking&processes=1", ""))
	if len(one.Products) != 1 || len(one.Edges) != 0 || len(one.Processes) != 2 {
		t.Fatalf("one = %+v, want the product, no edge and its two processes", one)
	}
	for _, c := range []struct {
		target string
		p      *httpapi.Principal
		want   int
	}{
		{"/x", importer, http.StatusBadRequest},
		{"/x?ids=,", importer, http.StatusBadRequest},
		{"/x?ids=ghost", importer, http.StatusNotFound},
		{"/x?ids=badge", user("usr_x"), http.StatusNotFound},
	} {
		if rec := call(t, s.HandleExportProducts, c.p, "GET", c.target, ""); rec.Code != c.want {
			t.Errorf("%s = %d %s, want %d", c.target, rec.Code, rec.Body.String(), c.want)
		}
	}
}

// TestProductsImportIntoAnotherCatalogue: with ?into=, a product document lands in
// the catalogue named — a new product homed there, every product offered there, the
// edges between them added to it — and importing it again changes nothing twice.
func TestProductsImportIntoAnotherCatalogue(t *testing.T) {
	src := seeded(t)
	rec := call(t, src.HandleExportProducts, importer, "GET", "/x?ids=badge,parking", "")
	doc := rec.Body.String()

	dst := newService(t)
	if code, out := importDoc(t, dst, importer, `{"catalogs":[{"id":"cat-ziel","texts":{"de":"Ziel"},"rank":1,"languages":["de"]}]}`); code != http.StatusOK {
		t.Fatalf("target = %d %v", code, out)
	}
	for round := 1; round <= 2; round++ {
		rec := call(t, dst.HandleImportDocument, importer, "POST", "/x?into=cat-ziel", doc)
		if rec.Code != http.StatusOK {
			t.Fatalf("round %d = %d %s", round, rec.Code, rec.Body.String())
		}
	}
	var (
		cat   Catalog
		badge Item
	)
	dst.loop.Do(func() {
		cat, _, _ = dst.store.Catalog("cat-ziel")
		badge, _, _ = dst.store.Item("badge")
	})
	if !slices.Equal(cat.Items, []string{"badge", "parking"}) || len(cat.Edges) != 1 || badge.HomeCatalog != "cat-ziel" {
		t.Fatalf("after importing into cat-ziel: catalogue %+v, badge home %q", cat, badge.HomeCatalog)
	}

	// On the server it came from, a product that already exists keeps its home: the
	// Bauamt offers it, and the Verwaltung still maintains it.
	rec = call(t, src.HandleImportDocument, importer, "POST", "/x?into=cat-bau", doc)
	if rec.Code != http.StatusOK {
		t.Fatalf("into cat-bau = %d %s", rec.Code, rec.Body.String())
	}
	src.loop.Do(func() {
		cat, _, _ = src.store.Catalog("cat-bau")
		badge, _, _ = src.store.Item("badge")
	})
	if !slices.Equal(cat.Items, []string{"badge", "parking"}) || badge.HomeCatalog != "cat-verwaltung" {
		t.Fatalf("into cat-bau: catalogue %+v, badge home %q", cat, badge.HomeCatalog)
	}
}

// TestIntoIsForProductsAlone: a document with catalogues cannot be imported into one,
// edges outside a catalogue need one to land in, and the catalogue named must exist
// and be the importer's to maintain.
func TestIntoIsForProductsAlone(t *testing.T) {
	s := seeded(t)
	products := `{"products":[{"id":"n1","state":"draft","texts":{"de":"N"}}]}`
	for _, c := range []struct {
		target, body string
		p            *httpapi.Principal
		want         int
		says         string
	}{
		{"/x?into=cat-bau", shopDocument, importer, http.StatusBadRequest, "carries no catalogue"},
		{"/x?into=bad%20id", products, importer, http.StatusBadRequest, "into"},
		{"/x", `{"products":[{"id":"n1","homeCatalog":"cat-bau"}],"edges":[{"from":"n1","to":"n1","kind":"requires"}]}`, importer, http.StatusBadRequest, "?into="},
		{"/x?into=cat-nowhere", products, importer, http.StatusUnprocessableEntity, "no catalogue cat-nowhere"},
		{"/x?into=cat-bau", products, user("usr_x"), http.StatusUnprocessableEntity, "no catalogue cat-bau"},
	} {
		rec := call(t, s.HandleImportDocument, c.p, "POST", c.target, c.body)
		if rec.Code != c.want || !strings.Contains(rec.Body.String(), c.says) {
			t.Errorf("%s = %d %s, want %d saying %q", c.target, rec.Code, rec.Body.String(), c.want, c.says)
		}
	}
}

// TestADocumentsProcessesDeployBeforeItsCatalogue: the processes are checked with
// everything else before anything is written, and deployed before the catalogue —
// so a publish in the same import finds them.
func TestADocumentsProcessesDeployBeforeItsCatalogue(t *testing.T) {
	doc := `{"processes":[{"id":"prov-badge","xml":"<b/>"}],` + strings.TrimPrefix(shopDocument, "{")

	s := newService(t)
	if code, out := importDoc(t, s, importer, doc); code != http.StatusUnprocessableEntity ||
		!strings.Contains(asJSON(out), "cannot deploy") {
		t.Fatalf("processes on a server without a deployer = %d %v", code, out)
	}

	d := &fakeDeployer{refuse: []DocumentProblem{{Subject: "process:prov-badge", Problem: ProblemMayNotDeploy}}}
	s.Deployer = d
	if code, out := importDoc(t, s, importer, doc); code != http.StatusForbidden || len(d.deployed) != 0 {
		t.Fatalf("an importer who may not deploy = %d %v, deployed %v", code, out, d.deployed)
	}

	d.refuse = nil
	d.fail = []DocumentProblem{{Subject: "process:prov-badge", Problem: "does not compile"}}
	code, out := importDoc(t, s, importer, doc)
	var cats []Catalog
	s.loop.Do(func() { cats, _ = s.store.Catalogs() })
	if code != http.StatusUnprocessableEntity || len(cats) != 0 {
		t.Fatalf("a process that fails to deploy = %d %v, %d catalogues written", code, out, len(cats))
	}

	d.fail = nil
	code, out = importDoc(t, s, importer, doc)
	if code != http.StatusOK || len(d.deployed) != 1 || asJSON(out["deployed"]) != `["prov-badge"]` {
		t.Fatalf("import with processes = %d %v", code, out)
	}
}

// TestADocumentsProcessesAndEdgesAreCheckedOnTheirOwn: a process needs an id and a
// model and is named once; an edge outside a catalogue joins two of the document's
// products.
func TestADocumentsProcessesAndEdgesAreCheckedOnTheirOwn(t *testing.T) {
	doc := Document{
		Products:  []Item{{ID: "p1", HomeCatalog: "c"}},
		Processes: []Process{{ID: "", XML: "<x/>"}, {ID: "a"}, {ID: "b", XML: "<x/>"}, {ID: "b", XML: "<y/>"}},
		Edges:     []Edge{{From: "p1", To: "ghost", Kind: EdgeRequires}},
	}
	got := asJSON(doc.Check())
	for _, want := range []string{"a process needs an id", "process:a", "carries no model", "names this process twice", "names ghost"} {
		if !strings.Contains(got, want) {
			t.Errorf("problems %s do not say %q", got, want)
		}
	}
}

// errDeployer fails the way a server that cannot write does.
type errDeployer struct{ *fakeDeployer }

func (errDeployer) DeployProcesses(*http.Request, []Process) ([]string, []string, []DocumentProblem, error) {
	return nil, nil, nil, os.ErrPermission
}

// TestTheEdgesOfExportAndImport: what an export and an import answer when the store
// cannot be read, when a deploy fails on the server's side, when a publish is refused
// after the processes deployed, and when the catalogue imported into is only viewed.
func TestTheEdgesOfExportAndImport(t *testing.T) {
	for _, broken := range []string{"catalogs", "items"} {
		s := seeded(t)
		breakStoreDir(t, s, broken)
		if rec := call(t, s.HandleExportCatalog, importer, "GET", "/x", "", "id", "cat-verwaltung"); rec.Code != http.StatusInternalServerError {
			t.Errorf("catalogue export over unreadable %s = %d", broken, rec.Code)
		}
		if rec := call(t, s.HandleExportProducts, importer, "GET", "/x?ids=badge", ""); rec.Code != http.StatusInternalServerError {
			t.Errorf("product export over unreadable %s = %d", broken, rec.Code)
		}
		if rec := call(t, s.HandleImportDocument, importer, "POST", "/x?into=cat-bau", `{"products":[{"id":"n"}]}`); rec.Code != http.StatusInternalServerError {
			t.Errorf("import into over unreadable %s = %d", broken, rec.Code)
		}
	}

	withProcs := `{"processes":[{"id":"prov-badge","xml":"<b/>"}],` + strings.TrimPrefix(shopDocument, "{")
	s := newService(t)
	s.Deployer = errDeployer{&fakeDeployer{}}
	if code, out := importDoc(t, s, importer, withProcs); code != http.StatusInternalServerError {
		t.Fatalf("a deploy failing on the server = %d %v, want 500", code, out)
	}

	// Deployed, then refused at publish: the refusal names what stays deployed.
	d := &fakeDeployer{}
	s.Deployer = d
	broken := strings.Replace(`{"publish":true,`+strings.TrimPrefix(withProcs, "{"), `"approval":{"kind":"fixed","ref":"usr_facility"}`, `"approval":{"kind":"fixed"}`, 1)
	code, out := importDoc(t, s, importer, broken)
	if code != http.StatusUnprocessableEntity || asJSON(out["deployed"]) != `["prov-badge"]` {
		t.Fatalf("a publish refused after deploying = %d %v, want 422 naming prov-badge", code, out)
	}

	// Viewing the catalogue products are imported into is not maintaining it.
	s = seeded(t)
	viewed := strings.Replace(shopDocument, `"groups":["grp-alle"],`,
		`"groups":["grp-alle"],"members":[{"ref":{"type":"user","id":"usr_x"},"role":"viewer"}],`, 1)
	if code, out := importDocWith(t, s, importer, "?overwrite=catalog:cat-verwaltung", viewed); code != http.StatusOK {
		t.Fatalf("granting a viewer = %d %v", code, out)
	}
	rec := call(t, s.HandleImportDocument, user("usr_x"), "POST", "/x?into=cat-verwaltung", `{"products":[{"id":"n"}]}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a viewer importing into the catalogue = %d %s, want 403", rec.Code, rec.Body.String())
	}
}
