package catalog

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// A catalogue document is a shop as one file: catalogues, their products and the
// edges between them, imported all or nothing
// (ADR-0436).

// shopDocument is a small, publishable shop: a catalogue that maintains a badge and a
// parking space and offers both, and a second catalogue offering the badge it does not
// maintain.
const shopDocument = `{
  "catalogs": [
    {"id":"cat-verwaltung","texts":{"de":"Verwaltung"},"rank":1,"languages":["de"],
     "items":["badge","parking"],"groups":["grp-alle"],
     "edges":[{"from":"parking","to":"badge","kind":"requires"}]},
    {"id":"cat-bau","texts":{"de":"Bauamt"},"rank":2,"languages":["de"],"items":["badge"]}
  ],
  "products": [
    {"id":"badge","homeCatalog":"cat-verwaltung","state":"active","texts":{"de":"Zutrittsbadge"},
     "approval":{"kind":"none"},"provisionProcess":"prov-badge","deprovisionProcess":"deprov-badge"},
    {"id":"parking","homeCatalog":"cat-verwaltung","state":"active","texts":{"de":"Parkplatz"},
     "approval":{"kind":"fixed","ref":"usr_facility"},"maxDays":365,
     "provisionProcess":"prov-parking","deprovisionProcess":"deprov-parking"}
  ]
}`

// importDoc imports a document as the given principal.
func importDoc(t *testing.T, s *Service, p *httpapi.Principal, body string) (int, map[string]any) {
	t.Helper()
	rec := as(t, s.HandleImportDocument, p, "POST", body)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

var importer = &httpapi.Principal{UserID: "usr_pm", Roles: []string{"productmanager"}}

// withPublish adds "publish":true to a document.
func withPublish(doc string) string {
	return strings.Replace(doc, `{
  "catalogs"`, `{"publish":true,
  "catalogs"`, 1)
}

// TestADocumentImportsAWholeShopAndPublishesIt: catalogues, products and edges go in
// in one request, owned by whoever imported them; with publish, each catalogue has a
// release, and the second catalogue's release carries the product it does not
// maintain.
func TestADocumentImportsAWholeShopAndPublishesIt(t *testing.T) {
	s := newService(t)
	code, out := importDoc(t, s, importer, withPublish(shopDocument))
	if code != http.StatusOK {
		t.Fatalf("import = %d %v", code, out)
	}
	created, _ := out["created"].([]any)
	if len(created) != 4 || len(out["releases"].([]any)) != 2 {
		t.Fatalf("result = %v, want four records created and two releases", out)
	}
	var (
		cat   Catalog
		badge Item
		rels  []Release
	)
	s.loop.Do(func() {
		cat, _, _ = s.store.Catalog("cat-verwaltung")
		badge, _, _ = s.store.Item("badge")
		rels, _ = s.store.ReleasesOf("cat-bau")
	})
	if cat.OwnerID != "usr_pm" || cat.Revision != 1 || len(cat.Edges) != 1 || cat.Groups[0] != "grp-alle" {
		t.Fatalf("the catalogue = %+v", cat)
	}
	if badge.Revision != 1 || badge.HomeCatalog != "cat-verwaltung" {
		t.Fatalf("the badge = %+v", badge)
	}
	if len(rels) != 1 || len(rels[0].Items) != 1 || rels[0].Items[0].ID != "badge" {
		t.Fatalf("the Bauamt's release = %+v", rels)
	}
}

// TestImportingTheSameDocumentAgainUpdatesWhatItCreated: the ids are the document's,
// so a second import finds its records and updates them — revisions move on, the owner
// and the creation stay — and nothing is created twice.
func TestImportingTheSameDocumentAgainUpdatesWhatItCreated(t *testing.T) {
	s := newService(t)
	if code, out := importDoc(t, s, importer, shopDocument); code != http.StatusOK {
		t.Fatalf("first import = %d %v", code, out)
	}
	changed := strings.Replace(shopDocument, `"texts":{"de":"Parkplatz"}`, `"texts":{"de":"Parkplatz Nord"}`, 1)
	code, out := importDoc(t, s, importer, changed)
	if code != http.StatusOK || len(out["updated"].([]any)) != 4 || len(out["created"].([]any)) != 0 {
		t.Fatalf("second import = %d %v, want four updated", code, out)
	}
	var (
		parking Item
		cat     Catalog
		all     []Catalog
	)
	s.loop.Do(func() {
		parking, _, _ = s.store.Item("parking")
		cat, _, _ = s.store.Catalog("cat-verwaltung")
		all, _ = s.store.Catalogs()
	})
	if parking.Texts["de"] != "Parkplatz Nord" || parking.Revision != 2 || cat.Revision != 2 || cat.OwnerID != "usr_pm" || len(all) != 2 {
		t.Fatalf("after the second import: parking %+v, catalogue %+v, %d catalogues", parking, cat, len(all))
	}
}

// TestAnImportPublishesBesideWhatTheServerHolds: a document need not carry the whole
// shop. A catalogue it adds may offer a product already on the server, and its release
// is computed from the stored catalogues and products as well as the document's.
func TestAnImportPublishesBesideWhatTheServerHolds(t *testing.T) {
	s := newService(t)
	if code, out := importDoc(t, s, importer, shopDocument); code != http.StatusOK {
		t.Fatalf("first import = %d %v", code, out)
	}
	extra := `{"publish":true,"catalogs":[{"id":"cat-extra","texts":{"de":"Extra"},"rank":3,"languages":["de"],"items":["badge"]}]}`
	code, out := importDoc(t, s, importer, extra)
	if code != http.StatusOK || len(out["releases"].([]any)) != 1 {
		t.Fatalf("a catalogue offering a stored product = %d %v, want one release", code, out)
	}
	var rels []Release
	s.loop.Do(func() { rels, _ = s.store.ReleasesOf("cat-extra") })
	if len(rels) != 1 || len(rels[0].Items) != 1 || rels[0].Items[0].ID != "badge" || rels[0].Items[0].Texts["de"] != "Zutrittsbadge" {
		t.Fatalf("the release = %+v, want the stored badge", rels)
	}
}

// TestAnImportPublishesOnlyWhatIsDeployed: a product bound to a lifecycle process the
// server has not deployed refuses the document, as a single publish refuses it, and
// nothing is written.
func TestAnImportPublishesOnlyWhatIsDeployed(t *testing.T) {
	s := newService(t)
	s.EntryPoints = fakeEntryPoints{}
	doc := `{"publish":true,
	  "catalogs":[{"id":"cat-it","texts":{"de":"IT"},"rank":1,"languages":["de"],"items":["laptop"]}],
	  "products":[{"id":"laptop","homeCatalog":"cat-it","state":"active","texts":{"de":"Laptop"},
	    "approval":{"kind":"none"},"lifecycleProcess":"laptop-lifecycle",
	    "operations":{"provision":"laptop.provision","deprovision":"laptop.deprovision"}}]}`
	code, out := importDoc(t, s, importer, doc)
	if code != http.StatusUnprocessableEntity || !strings.Contains(asJSON(out), "laptop-lifecycle is not deployed") {
		t.Fatalf("a product bound to an undeployed process = %d %v, want 422", code, out)
	}
	var cats []Catalog
	s.loop.Do(func() { cats, _ = s.store.Catalogs() })
	if len(cats) != 0 {
		t.Fatalf("a refused document wrote %d catalogues", len(cats))
	}
	s.EntryPoints = fakeEntryPoints{"laptop-lifecycle": {messages: []string{"laptop.provision", "laptop.deprovision"}}}
	if code, out := importDoc(t, s, importer, doc); code != http.StatusOK {
		t.Fatalf("once deployed = %d %v", code, out)
	}
}

// TestADocumentThatWouldNotPublishWritesNothing: with publish asked, a product that
// cannot be published refuses the whole document with every problem — and the
// catalogues and products that were fine are not written either.
func TestADocumentThatWouldNotPublishWritesNothing(t *testing.T) {
	s := newService(t)
	broken := strings.Replace(withPublish(shopDocument), `"approval":{"kind":"fixed","ref":"usr_facility"}`, `"approval":{"kind":"fixed"}`, 1)
	code, out := importDoc(t, s, importer, broken)
	if code != http.StatusUnprocessableEntity || !strings.Contains(asJSON(out), "catalog:cat-verwaltung") {
		t.Fatalf("a document that does not publish = %d %v, want 422 naming the catalogue", code, out)
	}
	var (
		cats  []Catalog
		items []Item
	)
	s.loop.Do(func() {
		cats, _ = s.store.Catalogs()
		items, _ = s.store.Items()
	})
	if len(cats) != 0 || len(items) != 0 {
		t.Fatalf("a refused document wrote %d catalogues and %d products", len(cats), len(items))
	}
}

// TestADocumentIsCheckedBeforeTheStoreIsAsked: ids, duplicates, a product without a
// home, a language that is not one and a theme are refused as the document's own
// faults, every one at once.
func TestADocumentIsCheckedBeforeTheStoreIsAsked(t *testing.T) {
	doc := Document{
		Catalogs: []Catalog{
			{ID: "bad id"}, {ID: "c1", Languages: []string{"not a tag"}}, {ID: "c1"},
			{ID: "c2", Theme: Theme{Accent: "#123456"}},
		},
		Products: []Item{{ID: "p1"}, {ID: "p1", HomeCatalog: "c2"}, {ID: "/etc"}},
	}
	problems := doc.Check()
	got := asJSON(problems)
	for _, want := range []string{"catalog:bad id", "catalog:c1", "names this catalogue twice", "theme", "product:p1",
		"needs a homeCatalog", "names this product twice", "product:/etc", "is not a language tag"} {
		if !strings.Contains(got, want) {
			t.Errorf("problems %s do not name %q", got, want)
		}
	}
	if empty := (Document{}).Check(); len(empty) != 1 || empty[0].Subject != "document" {
		t.Errorf("an empty document = %v", empty)
	}

	s := newService(t)
	code, out := importDoc(t, s, importer, `{"catalogs":[{"id":"bad id"}]}`)
	if code != http.StatusBadRequest || !strings.Contains(asJSON(out), "catalog:bad id") {
		t.Fatalf("over the route = %d %v", code, out)
	}
	if code, _ := importDoc(t, s, importer, `not json`); code != http.StatusBadRequest {
		t.Fatalf("malformed JSON = %d, want 400", code)
	}
}

// TestADocumentCannotWriteWhatItsImporterDoesNotMaintain: a catalogue that exists and
// that the importer does not maintain is refused 403, and so is a product being moved
// away from one; a product whose home the importer cannot see is refused as homeless. An editor may import
// a catalogue but not change its members; a product's home must exist somewhere, and
// a catalogue cannot offer a product nobody has.
func TestADocumentCannotWriteWhatItsImporterDoesNotMaintain(t *testing.T) {
	s := newService(t)
	owner := &httpapi.Principal{UserID: "usr_owner", Roles: []string{"productmanager"}}
	editor := &httpapi.Principal{UserID: "usr_editor", Roles: []string{"productmanager"}}
	if code, out := importDoc(t, s, owner, shopDocument); code != http.StatusOK {
		t.Fatalf("owner's import = %d %v", code, out)
	}
	if code, out := importDoc(t, s, importer, shopDocument); code != http.StatusForbidden ||
		!strings.Contains(asJSON(out), problemNotYours) {
		t.Fatalf("a stranger re-importing = %d %v, want 403", code, out)
	}
	stolen := `{"catalogs":[{"id":"cat-mine","texts":{"de":"Meins"},"rank":9,"languages":["de"]}],
	  "products":[{"id":"badge","homeCatalog":"cat-mine","state":"active"}]}`
	if code, out := importDoc(t, s, importer, stolen); code != http.StatusForbidden {
		t.Fatalf("taking a product from a catalogue the importer does not maintain = %d %v, want 403", code, out)
	}
	// A home the importer cannot see reads as absent, as a single save says: a refusal
	// that said "it exists, it is not yours" would answer what the gate withholds.
	if code, out := importDoc(t, s, importer, `{"products":[{"id":"x","homeCatalog":"cat-verwaltung"}]}`); code != http.StatusUnprocessableEntity ||
		!strings.Contains(asJSON(out), "no home catalogue cat-verwaltung") {
		t.Fatalf("a product in a catalogue the importer cannot see = %d %v", code, out)
	}

	// The owner shares the catalogue with an editor, who may import it as it is but
	// may not change who maintains it.
	shared := strings.Replace(shopDocument, `"groups":["grp-alle"],`,
		`"groups":["grp-alle"],"members":[{"ref":{"type":"user","id":"usr_editor"},"role":"editor"}],`, 1)
	if code, out := importDoc(t, s, owner, shared); code != http.StatusOK {
		t.Fatalf("sharing = %d %v", code, out)
	}
	only := `{"catalogs":[` + catalogOf(t, shared, "cat-verwaltung") + `]}`
	if code, out := importDoc(t, s, editor, only); code != http.StatusOK {
		t.Fatalf("an editor importing the catalogue unchanged = %d %v", code, out)
	}
	if code, out := importDoc(t, s, editor, `{"catalogs":[`+catalogOf(t, shopDocument, "cat-verwaltung")+`]}`); code != http.StatusUnprocessableEntity ||
		!strings.Contains(asJSON(out), "owner's") {
		t.Fatalf("an editor dropping a member = %d %v, want refused", code, out)
	}

	// Reading a catalogue is not maintaining it: a viewer sees the home and is still
	// refused a product in it.
	viewed := strings.Replace(shared, `"role":"editor"}]`, `"role":"editor"},{"ref":{"type":"user","id":"usr_pm"},"role":"viewer"}]`, 1)
	if code, out := importDoc(t, s, owner, viewed); code != http.StatusOK {
		t.Fatalf("granting a viewer = %d %v", code, out)
	}
	if code, out := importDoc(t, s, importer, `{"products":[{"id":"x","homeCatalog":"cat-verwaltung"}]}`); code != http.StatusForbidden ||
		!strings.Contains(asJSON(out), problemNotYours) {
		t.Fatalf("a viewer adding a product = %d %v, want 403", code, out)
	}

	if code, out := importDoc(t, s, importer, `{"products":[{"id":"y","homeCatalog":"cat-nowhere"}]}`); code != http.StatusUnprocessableEntity ||
		!strings.Contains(asJSON(out), "no home catalogue cat-nowhere") {
		t.Fatalf("a product with no home = %d %v", code, out)
	}
	if code, out := importDoc(t, s, importer, `{"catalogs":[{"id":"cat-x","rank":7,"languages":["de"],"items":["ghost"]}]}`); code != http.StatusUnprocessableEntity ||
		!strings.Contains(asJSON(out), "offers ghost") {
		t.Fatalf("offering a product nobody has = %d %v", code, out)
	}
}

// TestAnImportFailsOverAnUnreadableStore: a store that cannot be read is the server's
// fault and answers 500 without writing.
func TestAnImportFailsOverAnUnreadableStore(t *testing.T) {
	for _, broken := range []string{"catalogs", "items"} {
		t.Run(broken, func(t *testing.T) {
			s := newService(t)
			breakStoreDir(t, s, broken)
			if code, out := importDoc(t, s, importer, withPublish(shopDocument)); code != http.StatusInternalServerError {
				t.Fatalf("over an unreadable %s = %d %v", broken, code, out)
			}
		})
	}
	if sameMembers([]Member{{Role: RoleEditor}}, nil) || !sameMembers(nil, nil) ||
		sameMembers([]Member{{Role: RoleEditor}}, []Member{{Role: RoleViewer}}) {
		t.Fatal("sameMembers miscounts")
	}
}

// catalogOf extracts one catalogue of a document as JSON.
func catalogOf(t *testing.T, doc, id string) string {
	t.Helper()
	var d Document
	if err := json.Unmarshal([]byte(doc), &d); err != nil {
		t.Fatalf("decode document: %v", err)
	}
	for _, c := range d.Catalogs {
		if c.ID == id {
			b, _ := json.Marshal(c)
			return string(b)
		}
	}
	t.Fatalf("no catalogue %s", id)
	return ""
}

func asJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// breakStoreDir replaces one of the store's directories with a file.
func breakStoreDir(t *testing.T, s *Service, name string) {
	t.Helper()
	p := filepath.Join(filepath.Dir(s.store.logos), name)
	if err := os.RemoveAll(p); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if err := os.WriteFile(p, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}
