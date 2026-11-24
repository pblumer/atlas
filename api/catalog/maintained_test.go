package catalog

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// A catalogue maintained in another system
// (ADR-draft-a-shop-and-its-catalogues-from-another-system): a wiki publishes it
// through the document import with a person's grant for its shop, the import marks
// it, and every other write is refused with the wiki's name and address until an
// administrator takes it back.

// wikiDocument is one catalogue with one product, as a wiki publishes it. It names a
// client and a name of its own, which the import must not believe.
const wikiDocument = `{
  "catalogs": [
    {"id":"cat-wiki","texts":{"de":"Wiki-Katalog"},"rank":7,"languages":["de"],"items":["laptop"],
     "maintainedIn":{"client":"https://evil.example/forged","name":"Forged","url":"https://wiki.example.org/katalog"}}
  ],
  "products": [
    {"id":"laptop","homeCatalog":"cat-wiki","state":"active","texts":{"de":"Laptop"},"approval":{"kind":"none"}}
  ]
}`

const (
	wikiClient = "https://wiki.example.org/oauth/client.json"
	wikiName   = "Orbithek"
)

// shopGrantOf is a product manager acting through a connected system's grant for the
// shop: what the server's Publisher recognises.
func shopGrantOf(client, name string) *httpapi.Principal {
	return &httpapi.Principal{UserID: "usr_pm", Roles: []string{"productmanager"}, Scope: "shop",
		ClientID: client, ClientName: name}
}

// maintainedService is a service whose Publisher reads a shop grant as the server's
// does, with the wiki's catalogue imported through the wiki's grant.
func maintainedService(t *testing.T) *Service {
	t.Helper()
	s := newService(t)
	s.Publisher = func(p *httpapi.Principal) (string, string) {
		if p == nil || p.Scope != "shop" {
			return "", ""
		}
		return p.ClientID, p.ClientName
	}
	if code, out := importDoc(t, s, shopGrantOf(wikiClient, wikiName), wikiDocument); code != http.StatusOK {
		t.Fatalf("the wiki's import = %d %v", code, out)
	}
	return s
}

func storedCatalog(t *testing.T, s *Service, id string) Catalog {
	t.Helper()
	var c Catalog
	s.loop.Do(func() { c, _, _ = s.store.Catalog(id) })
	return c
}

func TestAnImportThroughASystemsShopGrantMarksWhereTheCatalogueIsMaintained(t *testing.T) {
	s := maintainedService(t)
	got := storedCatalog(t, s, "cat-wiki").MaintainedIn
	want := Maintainer{Client: wikiClient, Name: wikiName, URL: "https://wiki.example.org/katalog"}
	if got == nil || *got != want {
		t.Fatalf("maintainedIn = %+v, want %+v: client and name from the grant, the address from the document", got, want)
	}
	// The export carries it, so whoever holds the file sees where the catalogue lives.
	doc := exportOf(t, do(t, s.HandleExportCatalog, "GET", "/x", "", "id", "cat-wiki"))
	if m := doc.Catalogs[0].MaintainedIn; m == nil || *m != want {
		t.Errorf("the export's maintainedIn = %+v, want %+v", m, want)
	}
	// The same system publishing again is its own write.
	again := strings.Replace(wikiDocument, `"Laptop"`, `"Laptop 14"`, 1)
	if code, out := importDocWith(t, s, shopGrantOf(wikiClient, wikiName), "?overwrite=*", again); code != http.StatusOK {
		t.Fatalf("the wiki publishing again = %d %v, want 200", code, out)
	}
}

func TestAnImportThroughAnythingElseIgnoresWhatTheDocumentSaysAboutMaintenance(t *testing.T) {
	s := newService(t)
	if code, out := importDoc(t, s, importer, wikiDocument); code != http.StatusOK {
		t.Fatalf("import = %d %v", code, out)
	}
	if m := storedCatalog(t, s, "cat-wiki").MaintainedIn; m != nil {
		t.Errorf("a product manager's own import marked the catalogue maintained in %+v", m)
	}
	// And a service without a Publisher marks nothing, whatever the principal says.
	s2 := newService(t)
	if code, out := importDoc(t, s2, shopGrantOf(wikiClient, wikiName), wikiDocument); code != http.StatusOK {
		t.Fatalf("import = %d %v", code, out)
	}
	if m := storedCatalog(t, s2, "cat-wiki").MaintainedIn; m != nil {
		t.Errorf("without a Publisher the catalogue was marked maintained in %+v", m)
	}
}

func TestTheAddressOfMaintenanceIsHTTPSOrNothing(t *testing.T) {
	for _, tc := range []struct {
		url  string
		want int
	}{
		{"", http.StatusOK},
		{"https://wiki.example.org/seite?x=1", http.StatusOK},
		{"http://wiki.example.org/katalog", http.StatusUnprocessableEntity},
		{"javascript:alert(1)", http.StatusUnprocessableEntity},
		{"https://user:pw@wiki.example.org/", http.StatusUnprocessableEntity},
		{"https:///no-host", http.StatusUnprocessableEntity},
		{"/relative", http.StatusUnprocessableEntity},
	} {
		s := newService(t)
		s.Publisher = func(p *httpapi.Principal) (string, string) { return p.ClientID, p.ClientName }
		doc := strings.Replace(wikiDocument, `"url":"https://wiki.example.org/katalog"`, `"url":`+jsonString(tc.url), 1)
		code, out := importDoc(t, s, shopGrantOf(wikiClient, wikiName), doc)
		if code != tc.want {
			t.Errorf("maintainedIn.url %q = %d %v, want %d", tc.url, code, out, tc.want)
		}
		if tc.want != http.StatusOK && !strings.Contains(asJSON(out), problemMaintainerURL) {
			t.Errorf("maintainedIn.url %q refused without saying why: %v", tc.url, out)
		}
	}
}

func jsonString(v string) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func TestAnImportMayNotOverwriteACatalogueMaintainedElsewhere(t *testing.T) {
	s := maintainedService(t)
	edited := strings.Replace(wikiDocument, `"Laptop"`, `"Laptop (hier geändert)"`, 1)
	productOnly := `{"products":[{"id":"laptop","homeCatalog":"cat-wiki","state":"active","texts":{"de":"Laptop X"},"approval":{"kind":"none"}}]}`
	newInHome := `{"products":[{"id":"mouse","homeCatalog":"cat-wiki","state":"active","texts":{"de":"Maus"},"approval":{"kind":"none"}}]}`

	for _, tc := range []struct {
		name, query, doc string
		who              *httpapi.Principal
	}{
		{"a product manager's own import", "?overwrite=*", edited, importer},
		{"an administrator's own import", "?overwrite=*", edited, admin()},
		{"its dry run", "?dryRun=true", edited, importer},
		{"another system's grant for the shop", "?overwrite=*", edited, shopGrantOf("https://other.example/c.json", "Anderes Wiki")},
		{"a product of it alone", "?overwrite=*", productOnly, importer},
		{"a new product homed in it", "", newInHome, importer},
		{"products imported into it", "?into=cat-wiki", newInHome, importer},
	} {
		code, out := importDocWith(t, s, tc.who, tc.query, tc.doc)
		if code != http.StatusConflict {
			t.Errorf("%s = %d %v, want 409", tc.name, code, out)
			continue
		}
		body := asJSON(out)
		if !strings.Contains(body, `"maintainedIn":{"client":"`+wikiClient+`"`) || !strings.Contains(body, "is maintained in "+wikiName) {
			t.Errorf("%s refused without naming the system: %s", tc.name, body)
		}
	}
	if got := storedCatalog(t, s, "cat-wiki"); got.Revision != 1 || got.MaintainedIn == nil || got.MaintainedIn.Client != wikiClient {
		t.Errorf("a refused import changed the catalogue: %+v", got)
	}
}

// A product moved from a catalogue maintained elsewhere is taken from it, so the import
// is refused on that side too.
func TestAnImportMayNotTakeAProductFromACatalogueMaintainedElsewhere(t *testing.T) {
	s := maintainedService(t)
	if rec := do(t, s.HandleCreateCatalog, "POST", "/x", `{"rank":2,"languages":["de"],"texts":{"de":"Hier"}}`); rec.Code != http.StatusCreated {
		t.Fatalf("create = %d", rec.Code)
	} else {
		here := decode[Catalog](t, rec).ID
		moved := `{"products":[{"id":"laptop","homeCatalog":"` + here + `","state":"active","texts":{"de":"Laptop"},"approval":{"kind":"none"}}]}`
		if code, out := importDocWith(t, s, admin(), "?overwrite=*", moved); code != http.StatusConflict ||
			!strings.Contains(asJSON(out), "product laptop of catalogue cat-wiki") {
			t.Errorf("moving the product out = %d %v, want 409 naming its home", code, out)
		}
	}
}

// heldWrites are every write of this package to a catalogue or a product, as the
// administrator's tests run them: each must be refused while the catalogue is
// maintained elsewhere, and go through once it is taken back.
func heldWrites() []struct {
	name string
	run  func(t *testing.T, s *Service, p *httpapi.Principal) *httptest.ResponseRecorder
} {
	type write = struct {
		name string
		run  func(t *testing.T, s *Service, p *httpapi.Principal) *httptest.ResponseRecorder
	}
	return []write{
		{"PATCH the catalogue", func(t *testing.T, s *Service, p *httpapi.Principal) *httptest.ResponseRecorder {
			return as(t, s.HandleUpdateCatalog, p, "PATCH", `{"rank":9}`, "id", "cat-wiki")
		}},
		{"save one of its products", func(t *testing.T, s *Service, p *httpapi.Principal) *httptest.ResponseRecorder {
			return as(t, s.HandleSaveItem, p, "POST",
				`{"id":"laptop","homeCatalog":"cat-wiki","state":"active","texts":{"de":"Laptop neu"},"approval":{"kind":"none"}}`)
		}},
		{"create a product homed in it", func(t *testing.T, s *Service, p *httpapi.Principal) *httptest.ResponseRecorder {
			return as(t, s.HandleSaveItem, p, "POST",
				`{"id":"mouse","homeCatalog":"cat-wiki","state":"draft","texts":{"de":"Maus"},"approval":{"kind":"none"}}`)
		}},
		{"publish it", func(t *testing.T, s *Service, p *httpapi.Principal) *httptest.ResponseRecorder {
			return as(t, s.HandlePublish, p, "POST", "", "id", "cat-wiki")
		}},
		{"set a product's picture", func(t *testing.T, s *Service, p *httpapi.Principal) *httptest.ResponseRecorder {
			return asTyped(t, s.HandleSetPicture, p, "PUT", "image/png", onePNG, "id", "laptop")
		}},
		{"remove a product's picture", func(t *testing.T, s *Service, p *httpapi.Principal) *httptest.ResponseRecorder {
			return as(t, s.HandleDeletePicture, p, "DELETE", "", "id", "laptop")
		}},
		{"link a product's content", func(t *testing.T, s *Service, p *httpapi.Principal) *httptest.ResponseRecorder {
			return as(t, s.HandleContentLink, p, "POST", `{"pages":{"de":"x"}}`, "id", "laptop")
		}},
		{"unlink a product's content", func(t *testing.T, s *Service, p *httpapi.Principal) *httptest.ResponseRecorder {
			return as(t, s.HandleContentUnlink, p, "POST", `{"reason":"x"}`, "id", "laptop")
		}},
		{"import an ArchiMate model into it", func(t *testing.T, s *Service, p *httpapi.Principal) *httptest.ResponseRecorder {
			return as(t, s.HandleImport, p, "POST", archimateMinimal, "id", "cat-wiki")
		}},
	}
}

// archimateMinimal is a model the ArchiMate import parses: no element at all, which is
// enough to reach the gate.
const archimateMinimal = `<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/" identifier="m"><name>m</name><elements/></model>`

func TestEveryWriteToACatalogueMaintainedElsewhereIsRefusedWithTheSystem(t *testing.T) {
	for _, w := range heldWrites() {
		t.Run(w.name, func(t *testing.T) {
			s := maintainedService(t)
			rec := w.run(t, s, admin())
			if rec.Code != http.StatusConflict {
				t.Fatalf("%s = %d (%s), want 409", w.name, rec.Code, rec.Body)
			}
			var body struct {
				Error        string      `json:"error"`
				MaintainedIn *Maintainer `json:"maintainedIn"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.MaintainedIn == nil ||
				body.MaintainedIn.Client != wikiClient || !strings.Contains(body.Error, wikiName) ||
				!strings.Contains(body.Error, "https://wiki.example.org/katalog") ||
				!strings.Contains(body.Error, "DELETE /api/v1/catalogs/cat-wiki/maintainer") {
				t.Errorf("%s refused as %s, want the system, its address and the way back", w.name, rec.Body)
			}
			// A refusal of authority still comes first: a stranger learns nothing about
			// where the catalogue is maintained.
			if rec := w.run(t, s, user("usr_stranger")); rec.Code == http.StatusConflict {
				t.Errorf("%s answered a stranger 409 (%s), telling them where it is maintained", w.name, rec.Body)
			}

			// Taken back, the same write is no longer held there.
			if _, was, code, msg := s.TakeBack(admin(), "cat-wiki"); code != 0 || was == nil {
				t.Fatalf("take back = %d %s (was %+v)", code, msg, was)
			}
			if rec := w.run(t, s, admin()); rec.Code == http.StatusConflict && strings.Contains(rec.Body.String(), "maintainedIn") {
				t.Errorf("%s still refused after the catalogue was taken back: %s", w.name, rec.Body)
			}
		})
	}
}

// The system's own grant reaches the picture route (the import does not carry
// pictures), and that write is its own.
func TestTheMaintainingSystemSetsItsProductsPicture(t *testing.T) {
	s := maintainedService(t)
	if rec := asTyped(t, s.HandleSetPicture, shopGrantOf(wikiClient, wikiName), "PUT", "image/png", onePNG, "id", "laptop"); rec.Code != http.StatusNoContent {
		t.Errorf("the wiki setting its product's picture = %d (%s), want 204", rec.Code, rec.Body)
	}
	if rec := asTyped(t, s.HandleSetPicture, shopGrantOf("https://other.example/c.json", "Anderes"), "PUT", "image/png", onePNG, "id", "laptop"); rec.Code != http.StatusConflict {
		t.Errorf("another system setting it = %d (%s), want 409", rec.Code, rec.Body)
	}
}

// The appearance is an administrator's (ADR-0316) and stays open: a document carries
// no theme or logo, so nothing the wiki publishes overwrites it.
func TestTheAppearanceOfACatalogueMaintainedElsewhereStaysTheAdministrators(t *testing.T) {
	s := maintainedService(t)
	if rec := as(t, s.HandleSetTheme, admin(), "PUT", `{"accent":"#d52b1e"}`, "id", "cat-wiki"); rec.Code != http.StatusOK {
		t.Errorf("set theme = %d (%s), want 200", rec.Code, rec.Body)
	}
	if rec := asTyped(t, s.HandleSetLogo, admin(), "PUT", "image/png", string(onePixelPNG), "id", "cat-wiki"); rec.Code != http.StatusNoContent {
		t.Errorf("set logo = %d (%s), want 204", rec.Code, rec.Body)
	}
}

func TestOnlyAnAdministratorTakesACatalogueBack(t *testing.T) {
	s := maintainedService(t)
	// The owner — the product manager whose grant imported it — may not.
	if _, _, code, _ := s.TakeBack(&httpapi.Principal{UserID: "usr_pm", Roles: []string{"productmanager"}}, "cat-wiki"); code != http.StatusForbidden {
		t.Errorf("the owner taking it back = %d, want 403", code)
	}
	if _, _, code, _ := s.TakeBack(user("usr_stranger"), "cat-wiki"); code != http.StatusNotFound {
		t.Errorf("a stranger taking it back = %d, want 404", code)
	}
	if _, _, code, _ := s.TakeBack(admin(), "nope"); code != http.StatusNotFound {
		t.Errorf("an unknown catalogue = %d, want 404", code)
	}
	before := storedCatalog(t, s, "cat-wiki").Revision
	got, was, code, _ := s.TakeBack(admin(), "cat-wiki")
	if code != 0 || was == nil || was.Client != wikiClient || got.MaintainedIn != nil || got.Revision != before+1 {
		t.Fatalf("take back = %d, was %+v, now %+v (revision %d, was %d)", code, was, got.MaintainedIn, got.Revision, before)
	}
	if storedCatalog(t, s, "cat-wiki").MaintainedIn != nil {
		t.Error("the take-back was not stored")
	}
	// Again: maintained here already, answered as it is and nothing written.
	if again, was, code, _ := s.TakeBack(admin(), "cat-wiki"); code != 0 || was != nil || again.Revision != got.Revision {
		t.Errorf("taking back a catalogue maintained here = %d, was %+v, revision %d", code, was, again.Revision)
	}
}

// After a take-back the system's next publish is an import like any other: compared
// with what is stored, and a change asked about rather than written over.
func TestAfterATakeBackTheSystemsPublishIsAskedAbout(t *testing.T) {
	s := maintainedService(t)
	if _, _, code, msg := s.TakeBack(admin(), "cat-wiki"); code != 0 {
		t.Fatalf("take back = %d %s", code, msg)
	}
	if code, out := importDoc(t, s, shopGrantOf(wikiClient, wikiName), wikiDocument); code != http.StatusConflict ||
		!strings.Contains(asJSON(out["undecided"]), "catalog:cat-wiki") {
		t.Errorf("the wiki's next publish = %d %v, want 409 asking about the catalogue", code, out)
	}
}

func TestTheRefusalNamesTheClientWhenTheGrantNamesNothing(t *testing.T) {
	m := &Maintainer{Client: "https://wiki.example.org/c.json"}
	got := HeldMessage("catalogue x", "x", m)
	if !strings.Contains(got, "maintained in https://wiki.example.org/c.json;") || strings.Contains(got, "()") {
		t.Errorf("message = %q", got)
	}
}

// A catalogue is design-time state in its sidecar store, not the engine's log, so
// where it is maintained survives a restart by being written with the record: reopened
// from its directory, the store reads it back as it was saved.
func TestWhereACatalogueIsMaintainedSurvivesReopeningTheStore(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	want := Maintainer{Client: wikiClient, Name: wikiName, URL: "https://wiki.example.org/katalog"}
	if err := store.SaveCatalog(Catalog{ID: "cat-wiki", Revision: 1, MaintainedIn: &want}); err != nil {
		t.Fatalf("save: %v", err)
	}
	reopened, err := NewStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, found, err := reopened.Catalog("cat-wiki")
	if err != nil || !found || got.MaintainedIn == nil || *got.MaintainedIn != want {
		t.Fatalf("after reopening = %+v (found %v, err %v), want maintained in %+v", got.MaintainedIn, found, err, want)
	}
}

// HeldElsewhere is the same decision for the server's own writes outside this
// package (a purge): held for anyone but the maintaining system's grant, open for
// that grant and for every caller once the catalogue is Atlas's again.
func TestHeldElsewhereAnswersTheMaintainingSystemToTheServer(t *testing.T) {
	s := maintainedService(t)
	c := storedCatalog(t, s, "cat-wiki")
	if m := s.HeldElsewhere(c, admin()); m == nil || m.Client != wikiClient || m.Name != wikiName {
		t.Fatalf("an administrator's write is held by %+v, want the wiki", m)
	}
	if m := s.HeldElsewhere(c, shopGrantOf("https://other.example/c.json", "Anderes")); m == nil {
		t.Error("another system's grant is not held")
	}
	if m := s.HeldElsewhere(c, shopGrantOf(wikiClient, wikiName)); m != nil {
		t.Errorf("the maintaining system's own grant is held by %+v", m)
	}
	c.MaintainedIn = nil
	if m := s.HeldElsewhere(c, admin()); m != nil {
		t.Errorf("a catalogue maintained here is held by %+v", m)
	}
}

// A product whose home catalogue is gone reads as absent to the picture's gate, as
// it does everywhere else: there is no catalogue to ask whether it is maintained
// here, so nothing is written.
func TestAPictureOfAProductWithoutItsHomeIsNotFound(t *testing.T) {
	s := maintainedService(t)
	var saveErr error
	s.loop.Do(func() {
		it, _, _ := s.store.Item("laptop")
		it.ID, it.HomeCatalog = "orphan", "cat-gone"
		saveErr = s.store.SaveItem(it)
	})
	if saveErr != nil {
		t.Fatal(saveErr)
	}
	if rec := asTyped(t, s.HandleSetPicture, admin(), "PUT", "image/png", onePNG, "id", "orphan"); rec.Code != http.StatusNotFound {
		t.Fatalf("a picture of a product without its home = %d (%s), want 404", rec.Code, rec.Body)
	}
}
