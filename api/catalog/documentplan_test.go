package catalog

import (
	"net/http"
	"strings"
	"testing"
)

// An import asks before it replaces anything, and changes nothing twice
// (ADR-draft-application-package-export).

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
