package api

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestBlueprintPendingInstallationSurvivesRestartWithoutClaimingSuccess(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "blueprint-installations")
	s, err := newBlueprintInstallationStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	pending := blueprintInstallation{
		ApplicationKey: "office", RegistryID: "org.example.community",
		RegistryOrigin:    "https://registry.example.org/blueprints/index.json",
		SignerFingerprint: "sha256:trust", PublisherID: "org.example",
		BlueprintID: "org.example.office", Version: "1.0.0",
		Digest: "sha256:package", CatalogueIDs: []string{"cat-a", "cat-b"},
		Status: "pending", RequestedBy: "alice", RequestedAt: 42,
	}
	if err := s.Save(pending); err != nil {
		t.Fatal(err)
	}
	// NewStore models process restart: nothing remains in process memory.
	reopened, err := newBlueprintInstallationStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, exists, err := reopened.Get("office")
	if err != nil || !exists {
		t.Fatalf("reload after restart: found=%v, error=%v", exists, err)
	}
	if !reflect.DeepEqual(got, pending) || got.Status != "pending" {
		t.Fatalf("reloaded an unverified install as complete or lost trust metadata: %+v", got)
	}
}
