package api

import (
	"reflect"
	"testing"
)

// TestConfiguredWorkerStoreSharesConnectorPersistence pins ADR-0203's staged
// migration: configured Worker is the canonical in-process term, while worker
// remains only a compatibility name over the same persisted record and store.
func TestConfiguredWorkerStoreSharesConnectorPersistence(t *testing.T) {
	dir := t.TempDir()

	workers, err := newConfiguredWorkerStore(dir)
	if err != nil {
		t.Fatalf("new configured worker store: %v", err)
	}
	legacy, err := newConnectorStore(dir)
	if err != nil {
		t.Fatalf("new worker store: %v", err)
	}

	want := configuredWorker{
		ID:             "jira-production-patrick",
		Name:           "Jira Production Patrick",
		Kind:           connectorKindJira,
		Endpoint:       "https://jira.example.test",
		CredentialsRef: "jira-patrick",
		Enabled:        true,
		CreatedAt:      42,
	}
	if err := workers.Save(want); err != nil {
		t.Fatalf("save configured worker: %v", err)
	}

	got, ok, err := legacy.Get(want.ID)
	if err != nil {
		t.Fatalf("get through worker compatibility store: %v", err)
	}
	if !ok {
		t.Fatal("configured worker saved through canonical store is not visible through worker compatibility store")
	}
	// reflect.DeepEqual rather than !=: the record carries a member list since
	// ADR-0205 gave a worker an owner, so the struct is no longer comparable.
	// What this test is about — that both names read and write the same persisted
	// record — is unchanged.
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("worker compatibility record = %+v, want %+v", got, want)
	}

	got.Name = "Renamed through legacy API"
	if err := legacy.Save(got); err != nil {
		t.Fatalf("save through worker compatibility store: %v", err)
	}
	updated, ok, err := workers.Get(want.ID)
	if err != nil {
		t.Fatalf("get configured worker after legacy update: %v", err)
	}
	if !ok || updated.Name != got.Name {
		t.Fatalf("configured worker after legacy update = %+v ok=%v, want name %q", updated, ok, got.Name)
	}
}
