package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

)

func pendingBlueprintRequest(s *Server, id, version, match string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/blueprints/"+id+"/releases/"+version+"/installations", nil)
	r.SetPathValue("id", id)
	r.SetPathValue("version", version)
	if match != "" {
		r.Header.Set("If-Match", match)
	}
	w := httptest.NewRecorder()
	s.handleBeginBlueprintInstallation(w, r)
	return w
}

func TestBeginBlueprintInstallJournalsSignedCataloguesBeforeNativeWrites(t *testing.T) {
	dir := t.TempDir()
	srv, err := newServerAtDir(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	f := sampleBlueprintSource(t)
	var b bytes.Buffer
	if err := writeSourceArchive(&b, []sourceFile{
		{Path: "atlas.json", Data: []byte(`{"formatVersion":1,"key":"purchase","name":"Purchase","processes":[],"forms":[],"decisions":[]}`)},
		{Path: "katalog.json", Data: []byte(`{"catalogs":[{"id":"catalog-two"},{"id":"catalog-one"}],"products":[]}`)},
	}); err != nil {
		t.Fatal(err)
	}
	f.packageBytes = b.Bytes()
	d := sha256.Sum256(f.packageBytes)
	f.release.Artifact.SizeBytes = int64(len(f.packageBytes))
	f.release.Artifact.Digest = "sha256:" + hex.EncodeToString(d[:])
	f.index.Releases[0] = f.release
	srv.blueprints = f
	srv.now = func() int64 { return 42_000_000_000 }
	match := "\"" + f.release.Artifact.Digest + "\""

	if got := pendingBlueprintRequest(srv, f.release.BlueprintID, f.release.Version, ""); got.Code != http.StatusPreconditionRequired || f.fetches != 0 {
		t.Fatalf("missing If-Match: %d, fetches=%d", got.Code, f.fetches)
	}
	if got := pendingBlueprintRequest(srv, f.release.BlueprintID, f.release.Version, "\"sha256:wrong\""); got.Code != http.StatusPreconditionFailed {
		t.Fatalf("wrong digest: %d", got.Code)
	}
	none, err := srv.blueprintInstalls.LoadAll()
	if err != nil || len(none) != 0 {
		t.Fatalf("precondition failure wrote journal: %v, %v", none, err)
	}

	got := pendingBlueprintRequest(srv, f.release.BlueprintID, f.release.Version, match)
	if got.Code != http.StatusOK {
		t.Fatalf("begin: status %d: %s", got.Code, got.Body.String())
	}
	rec, ok, err := srv.blueprintInstalls.Get("purchase")
	if err != nil || !ok {
		t.Fatalf("journal record: found=%t err=%v", ok, err)
	}
	if rec.Status != "pending" || rec.RegistryID != f.index.RegistryID ||
		rec.Digest != f.release.Artifact.Digest || rec.RequestedAt != 42 ||
		len(rec.CatalogueIDs) != 2 || rec.CatalogueIDs[0] != "catalog-one" ||
		rec.CatalogueIDs[1] != "catalog-two" || rec.SignerFingerprint == "" {
		t.Fatalf("signed application/catalogue selection not durably pinned: %+v", rec)
	}
	// A duplicate begin does not churn the durable record.
	srv.now = func() int64 { return 99_000_000_000 }
	if response := pendingBlueprintRequest(srv, f.release.BlueprintID, f.release.Version, match); response.Code != http.StatusOK {
		t.Fatalf("idempotent begin: %d", response.Code)
	}
	reopened, err := newBlueprintInstallationStore(filepath.Join(dir, "blueprint-installations"))
	if err != nil {
		t.Fatal(err)
	}
	after, ok, err := reopened.Get("purchase")
	if err != nil || !ok || after.RequestedAt != 42 || after.Status != "pending" {
		t.Fatalf("recovered pending record changed or vanished: %+v exists=%t err=%v", after, ok, err)
	}
	// Even if the original HTTPS registry disappears, a locally persisted
	// pending installation remains visible for reconciliation.
	srv.blueprints = nil
	list := httptest.NewRecorder()
	srv.handleListBlueprintInstallations(list, httptest.NewRequest(http.MethodGet, "/api/v1/blueprints/installations", nil))
	if list.Code != http.StatusOK || !bytes.Contains(list.Body.Bytes(), []byte(`"status":"pending"`)) {
		t.Fatalf("offline journal listing: %d %s", list.Code, list.Body.String())
	}
}

func TestBeginBlueprintInstallRejectsConflictingPinWithoutOverwritingJournal(t *testing.T) {
	srv, err := newServerAtDir(t, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	f := sampleBlueprintSource(t)
	srv.blueprints = f
	match := "\"" + f.release.Artifact.Digest + "\""
	if got := pendingBlueprintRequest(srv, f.release.BlueprintID, f.release.Version, match); got.Code != http.StatusOK {
		t.Fatalf("first pin: %d %s", got.Code, got.Body.String())
	}
	f.release.Version = "2.0.0"
	f.index.Releases[0] = f.release
	if got := pendingBlueprintRequest(srv, f.release.BlueprintID, f.release.Version, match); got.Code != http.StatusConflict {
		t.Fatalf("overwriting a still pending pinned version: %d %s", got.Code, got.Body.String())
	}
	persisted, exists, err := srv.blueprintInstalls.Get("purchase")
	if err != nil || !exists || persisted.Version != "1.0.0" || persisted.Status != "pending" {
		t.Fatalf("old pin overwritten: %+v exists=%t err=%v", persisted, exists, err)
	}
}

func TestBeginBlueprintInstallRejectsProtectedApplicationAndTamperedArchive(t *testing.T) {
	srv, err := newServerAtDir(t, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	f := sampleBlueprintSource(t)
	srv.blueprints = f
	var storeErr error
	srv.do(func() { storeErr = srv.projects.Save(project{ID: "platform", Key: "purchase", Protected: true}) })
	if storeErr != nil {
		t.Fatal(storeErr)
	}
	match := "\"" + f.release.Artifact.Digest + "\""
	if got := pendingBlueprintRequest(srv, f.release.BlueprintID, f.release.Version, match); got.Code != http.StatusConflict {
		t.Fatalf("protected application accepted: %d %s", got.Code, got.Body.String())
	}
	srv.do(func() { storeErr = srv.projects.Delete("platform") })
	if storeErr != nil {
		t.Fatal(storeErr)
	}
	f.packageBytes = append([]byte(nil), f.packageBytes...)
	f.packageBytes[len(f.packageBytes)-1] ^= 1
	if got := pendingBlueprintRequest(srv, f.release.BlueprintID, f.release.Version, match); got.Code != http.StatusBadGateway {
		t.Fatalf("tampered archive accepted: %d %s", got.Code, got.Body.String())
	}
	list, err := srv.blueprintInstalls.LoadAll()
	if err != nil || len(list) != 0 {
		t.Fatalf("failed first install left a record: %v, %v", list, err)
	}
}

