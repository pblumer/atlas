package api

import "testing"

// TestConfiguredWorkerStoreSharesConnectorPersistence pins ADR-0203's staged
// migration: configured Worker is the canonical in-process term, while connector
// remains only a compatibility name over the same persisted record and store.
func TestConfiguredWorkerStoreSharesConnectorPersistence(t *testing.T) {
	dir := t.TempDir()

	workers, err := newConfiguredWorkerStore(dir)
	if err != nil {
		t.Fatalf("new configured worker store: %v", err)
	}
	legacy, err := newConnectorStore(dir)
	if err != nil {
		t.Fatalf("new connector store: %v", err)
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
		t.Fatalf("get through connector compatibility store: %v", err)
	}
	if !ok {
		t.Fatal("configured worker saved through canonical store is not visible through connector compatibility store")
	}
	if got != want {
		t.Fatalf("connector compatibility record = %+v, want %+v", got, want)
	}

	got.Name = "Renamed through legacy API"
	if err := legacy.Save(got); err != nil {
		t.Fatalf("save through connector compatibility store: %v", err)
	}
	updated, ok, err := workers.Get(want.ID)
	if err != nil {
		t.Fatalf("get configured worker after legacy update: %v", err)
	}
	if !ok || updated.Name != got.Name {
		t.Fatalf("configured worker after legacy update = %+v ok=%v, want name %q", updated, ok, got.Name)
	}
}
