package catalog

import (
	"net/http"
	"strings"
	"testing"
)

// An import asks before it replaces anything, and changes nothing twice
// (ADR-0467).

func releasesOf(t *testing.T, s *Service, id string) int {
	t.Helper()
	var rels []Release
	s.loop.Do(func() { rels, _ = s.store.ReleasesOf(id) })
	return len(rels)
}

// TestAPublishingImportReleasesOnlyWhatItChanged: a document that asks to be
// published releases each catalogue the first time, and again only when the import
// changed what the release is made of — the catalogue, or a product it offers. The
// same document imported every night is not a release every night.
func TestAPublishingImportReleasesOnlyWhatItChanged(t *testing.T) {
	s := newService(t)
	doc := withPublish(shopDocument)
	if code, out := importDoc(t, s, importer, doc); code != http.StatusOK || len(out["releases"].([]any)) != 2 {
		t.Fatalf("first import = %d %v, want two releases", code, out)
	}
	if code, out := importDoc(t, s, importer, doc); code != http.StatusOK || out["releases"] != nil {
		t.Fatalf("the same document again = %d %v, want no release", code, out)
	}

	// The parking space is offered by the administration's catalogue alone.
	changed := strings.Replace(doc, `"texts":{"de":"Parkplatz"}`, `"texts":{"de":"Parkplatz Nord"}`, 1)
	if code, out := importDocWith(t, s, importer, "?keep=product:parking", changed); code != http.StatusOK || out["releases"] != nil {
		t.Fatalf("a change kept back = %d %v, want no release", code, out)
	}
	code, out := importDocWith(t, s, importer, "?overwrite=product:parking", changed)
	if code != http.StatusOK || len(out["releases"].([]any)) != 1 {
		t.Fatalf("a change taken = %d %v, want one release", code, out)
	}
	if releasesOf(t, s, "cat-verwaltung") != 2 || releasesOf(t, s, "cat-bau") != 1 {
		t.Errorf("releases: administration %d, building %d; want 2 and 1",
			releasesOf(t, s, "cat-verwaltung"), releasesOf(t, s, "cat-bau"))
	}
}

// TestADocumentsChangedProcessIsDecided: deploying is not undone, and a newer
// version deployed here is work done here, so a process that differs from the newest
// deployed version is decided like a catalogue. One that is already deployed as the
// document has it is not deployed again and asks nothing.
func TestADocumentsChangedProcessIsDecided(t *testing.T) {
	doc := `{"processes":[{"id":"prov-badge","xml":"<b/>"}],` + strings.TrimPrefix(shopDocument, "{")
	s := newService(t)
	d := &fakeDeployer{}
	s.Deployer, s.Models = d, fakeModels{"prov-badge": "<hier/>"}

	code, out := importDoc(t, s, importer, doc)
	if code != http.StatusConflict || asJSON(out["undecided"]) != `["process:prov-badge"]` || len(d.deployed) != 0 {
		t.Fatalf("an undecided process = %d %v, deployed %v", code, out, d.deployed)
	}
	var cats []Catalog
	s.loop.Do(func() { cats, _ = s.store.Catalogs() })
	if len(cats) != 0 {
		t.Fatalf("an import stopped for a decision wrote %d catalogues", len(cats))
	}
	if code, out := importDocWith(t, s, importer, "?keep=process:prov-badge", doc); code != http.StatusOK || len(d.deployed) != 0 ||
		!strings.Contains(asJSON(out["artifacts"]), `"outcome":"kept","ref":"process:prov-badge"`) {
		t.Fatalf("a kept process = %d %v, deployed %v", code, out, d.deployed)
	}
	if code, out := importDocWith(t, s, importer, "?overwrite=process:prov-badge", doc); code != http.StatusOK || len(d.deployed) != 1 {
		t.Fatalf("a process to overwrite = %d %v, deployed %v", code, out, d.deployed)
	}

	s.Models = fakeModels{"prov-badge": "<b/>"}
	d.deployed = nil
	if code, out := importDoc(t, s, importer, doc); code != http.StatusOK || len(d.deployed) != 0 {
		t.Fatalf("a process deployed as the document has it = %d %v, deployed %v", code, out, d.deployed)
	}
}

// TestRecordsCompareAsTheySay: empty and absent are one, and what the store stamps
// on a write is not a difference.
func TestRecordsCompareAsTheySay(t *testing.T) {
	a := Catalog{ID: "c", Texts: map[string]string{"de": "C"}, Items: []string{}, Revision: 4, UpdatedAt: 9, OwnerID: "usr_a"}
	b := Catalog{ID: "c", Texts: map[string]string{"de": "C"}, Revision: 1}
	if !sameCatalog(a, b) {
		t.Error("an empty list and an absent one differ")
	}
	b.Items = []string{"x"}
	if sameCatalog(a, b) {
		t.Error("a catalogue offering more compares the same")
	}
	if !sameItem(Item{ID: "p", Revision: 2, Variants: []Variant{}}, Item{ID: "p", Revision: 3}) {
		t.Error("two revisions of the same product differ")
	}
	if sameRelease(Release{ID: "r1", Waves: [][]string{{"a"}}}, Release{ID: "r2", Waves: [][]string{{"b"}}}) {
		t.Error("two releases of different waves compare the same")
	}
	if textOf(map[string]string{"fr": "Catalogue"}) != "Catalogue" || textOf(nil) != "" {
		t.Error("textOf")
	}
}

// TestTheNewestReleaseIsTheLatestMade: what a release is compared with is the one
// made last, whatever order the store lists them in.
func TestTheNewestReleaseIsTheLatestMade(t *testing.T) {
	got := newest([]Release{{ID: "r1", CreatedAt: 5}, {ID: "r3", CreatedAt: 9}, {ID: "r2", CreatedAt: 7}})
	if got.ID != "r3" {
		t.Errorf("newest = %s, want r3", got.ID)
	}
	if canonical(func() {}) == nil {
		t.Error("a value JSON cannot say is compared as nothing")
	}
}

// fakeBaselines keeps bases in memory, as the server's store keeps them on disk.
type fakeBaselines struct{ hashes map[string]string }

func (f *fakeBaselines) ImportBaselines() (map[string]string, error) {
	out := map[string]string{}
	for k, v := range f.hashes {
		out[k] = v
	}
	return out, nil
}

func (f *fakeBaselines) RecordImportBaselines(h map[string]string) error {
	if f.hashes == nil {
		f.hashes = map[string]string{}
	}
	for k, v := range h {
		f.hashes[k] = v
	}
	return nil
}

// TestACatalogueImportTellsAnUpdateFromAnEdit: with what the last import brought
// remembered, a product the document moved on and nobody touched here is taken, one
// edited here is kept while the document carries it as before, and only one both
// sides moved is asked about (ADR-0467, amended).
func TestACatalogueImportTellsAnUpdateFromAnEdit(t *testing.T) {
	s := newService(t)
	s.Baselines = &fakeBaselines{}
	if code, out := importDoc(t, s, importer, shopDocument); code != http.StatusOK {
		t.Fatalf("first import = %d %v", code, out)
	}
	newer := strings.Replace(shopDocument, `"texts":{"de":"Parkplatz"}`, `"texts":{"de":"Parkplatz Nord"}`, 1)
	code, out := importDoc(t, s, importer, newer)
	if code != http.StatusOK || asJSON(out["updated"]) != `["product:parking"]` ||
		!strings.Contains(asJSON(out["artifacts"]), `"outcome":"updated","ref":"product:parking","status":"behind"`) {
		t.Fatalf("a product the document moved on = %d %v", code, out)
	}

	// Edited here — through the store, as the Console would — and the document as
	// before: kept, and nothing asked.
	s.loop.Do(func() {
		it, _, _ := s.store.Item("parking")
		it.Texts = map[string]string{"de": "Parkplatz (hier)"}
		_ = s.store.SaveItem(it)
	})
	code, out = importDoc(t, s, importer, newer)
	if code != http.StatusOK || len(out["updated"].([]any)) != 0 ||
		!strings.Contains(asJSON(out["artifacts"]), `"outcome":"kept","ref":"product:parking","status":"ahead"`) {
		t.Fatalf("a product edited here = %d %v", code, out)
	}

	// Both moved: asked.
	newest := strings.Replace(shopDocument, `"texts":{"de":"Parkplatz"}`, `"texts":{"de":"Parkplatz Süd"}`, 1)
	if code, out := importDoc(t, s, importer, newest); code != http.StatusConflict || asJSON(out["undecided"]) != `["product:parking"]` {
		t.Fatalf("both sides moved = %d %v", code, out)
	}

	// Without a store for bases, everything that differs is asked, as before.
	s.Baselines = nil
	if code, out := importDoc(t, s, importer, newer); code != http.StatusConflict {
		t.Fatalf("no bases = %d %v, want 409", code, out)
	}
}

// TestADocumentsProcessMovedOnIsDeployed: a process deployed as the last import
// brought it, and newer in the document, is deployed without a question.
func TestADocumentsProcessMovedOnIsDeployed(t *testing.T) {
	doc := func(xml string) string {
		return `{"processes":[{"id":"prov-badge","xml":"` + xml + `"}],` + strings.TrimPrefix(shopDocument, "{")
	}
	s := newService(t)
	d := &fakeDeployer{}
	s.Deployer, s.Models, s.Baselines = d, fakeModels{}, &fakeBaselines{}
	if code, out := importDoc(t, s, importer, doc("<v1/>")); code != http.StatusOK || len(d.deployed) != 1 {
		t.Fatalf("first import = %d %v", code, out)
	}
	s.Models = fakeModels{"prov-badge": "<v1/>"}
	d.deployed = nil
	code, out := importDoc(t, s, importer, doc("<v2/>"))
	if code != http.StatusOK || len(d.deployed) != 1 ||
		!strings.Contains(asJSON(out["artifacts"]), `"outcome":"updated","ref":"process:prov-badge","status":"behind"`) {
		t.Fatalf("a process the document moved on = %d %v, deployed %v", code, out, d.deployed)
	}
}
