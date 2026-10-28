package blueprintregistry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// This checks an actual Process Application and its product catalogues against
// the signed v1 transport contract. The application's executable import and
// repeat-import behaviour remain covered by cmd/atlas/importpkg_test.go.
func TestSignedExampleBundleCarriesProcessesAndCatalogues(t *testing.T) {
	const directory = "../../examples/verwaltung-dienstleistungen"
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	files := []packageTestEntry{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "README.md" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, packageTestEntry{name: entry.Name(), data: string(data)})
	}
	archive := packageFixture(t, files...)
	release := packageRelease(archive)
	release.BlueprintID = "org.example.verwaltung-services"
	release.PublisherID = "org.example"
	release.Version = "1.0.0"
	release.Title = "Administration services with product catalogues"
	release.Description = "Complete public demo of application and shop content"
	release.License = "AGPL-3.0-only"
	release.Atlas = Compatibility{MinVersion: "0.1.0"}
	release.Needs = Needs{WorkerTypes: []string{}, ServerConstants: []string{}}

	raw, err := json.Marshal(Index{
		SchemaVersion: 1,
		RegistryID:    "org.example.blueprints",
		Releases:      []Release{release},
	})
	if err != nil {
		t.Fatal(err)
	}
	sig, pinnedKey := signIndex(raw) // Deterministic test-only signing key.
	index, err := VerifySignedIndex(raw, sig, pinnedKey)
	if err != nil {
		t.Fatalf("signed example registry: %v", err)
	}
	inspected, err := InspectPackage(index.Releases[0], archive)
	if err != nil {
		t.Fatalf("signed example bundle: %v", err)
	}
	if inspected.ApplicationKey != "beispiel-dienstleistungen-der-verwaltung" {
		t.Fatalf("unexpected application: %q", inspected.ApplicationKey)
	}
	seen := map[string]bool{}
	for _, file := range inspected.Files {
		seen[file.Path] = true
	}
	for _, name := range []string{"atlas.json", "katalog.json", "fragen.json", "zutrittsbadge-lebenszyklus.bpmn", "parkplatz-zuteilen.bpmn", "geoportal-einrichten.bpmn"} {
		if !seen[name] {
			t.Errorf("signed package omitted %s", name)
		}
	}
	var shop struct {
		Catalogs []struct {
			ID string `json:"id"`
		} `json:"catalogs"`
		Products []struct {
			HomeCatalog string `json:"homeCatalog"`
		} `json:"products"`
	}
	shopRaw, err := os.ReadFile(filepath.Join(directory, "katalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(shopRaw, &shop); err != nil {
		t.Fatal(err)
	}
	if len(shop.Catalogs) != 2 || len(shop.Products) != 3 {
		t.Fatalf("expected two catalogues and three products, got %d and %d", len(shop.Catalogs), len(shop.Products))
	}
	knownCatalogues := map[string]bool{}
	for _, c := range shop.Catalogs {
		knownCatalogues[c.ID] = true
	}
	for _, product := range shop.Products {
		if !knownCatalogues[product.HomeCatalog] {
			t.Errorf("unknown product home catalogue: %q", product.HomeCatalog)
		}
	}

	// Neither altered index bytes nor altered compressed bytes may pass as the
	// previously reviewed immutable release, irrespective of importer behaviour.
	changedIndex := append([]byte(nil), raw...)
	changedIndex[len(changedIndex)-1] ^= 1
	if _, err := VerifySignedIndex(changedIndex, sig, pinnedKey); err == nil {
		t.Fatal("tampered index passed the pinned signature")
	}
	changedArchive := append([]byte(nil), archive...)
	changedArchive[len(changedArchive)-1] ^= 1
	if _, err := InspectPackage(index.Releases[0], changedArchive); err == nil {
		t.Fatal("tampered package passed the pinned digest")
	}
}
