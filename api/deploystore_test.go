package api

import (
	"os"
	"path/filepath"
	"testing"
)

func sampleRecord(key uint64) persistedDeployment {
	return persistedDeployment{
		Key:        key,
		ProcessID:  "order",
		Name:       "Order fulfillment",
		Version:    int32(key),
		DeployedAt: 1700000000,
		XML:        `<definitions><process id="order"/></definitions>`,
	}
}

// TestDeployStoreRoundTrip saves a few records and reads them back, sorted by
// key regardless of save order.
func TestDeployStoreRoundTrip(t *testing.T) {
	ds, err := newDeployStore(filepath.Join(t.TempDir(), "deployments"))
	if err != nil {
		t.Fatalf("newDeployStore: %v", err)
	}
	// Save out of order to prove loadAll sorts.
	for _, k := range []uint64{3, 1, 2} {
		if err := ds.save(sampleRecord(k)); err != nil {
			t.Fatalf("save %d: %v", k, err)
		}
	}
	got, err := ds.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("loadAll returned %d records, want 3", len(got))
	}
	for i, r := range got {
		wantKey := uint64(i + 1)
		if r.Key != wantKey {
			t.Errorf("record %d: key = %d, want %d (not sorted)", i, r.Key, wantKey)
		}
		if r.ProcessID != "order" || r.XML == "" {
			t.Errorf("record %d: fields not round-tripped: %+v", i, r)
		}
	}
}

// TestDeployStoreOverwrite proves a re-save of the same key replaces the record
// rather than duplicating it.
func TestDeployStoreOverwrite(t *testing.T) {
	ds, err := newDeployStore(filepath.Join(t.TempDir(), "deployments"))
	if err != nil {
		t.Fatalf("newDeployStore: %v", err)
	}
	if err := ds.save(sampleRecord(1)); err != nil {
		t.Fatalf("save: %v", err)
	}
	r := sampleRecord(1)
	r.Name = "Renamed"
	if err := ds.save(r); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	got, err := ds.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 record after overwrite, got %d", len(got))
	}
	if got[0].Name != "Renamed" {
		t.Errorf("name = %q, want the overwritten value", got[0].Name)
	}
}

// TestDeployStoreDelete removes a record's file so it does not come back.
func TestDeployStoreDelete(t *testing.T) {
	ds, err := newDeployStore(filepath.Join(t.TempDir(), "deployments"))
	if err != nil {
		t.Fatalf("newDeployStore: %v", err)
	}
	for _, k := range []uint64{1, 2} {
		if err := ds.save(sampleRecord(k)); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	if err := ds.delete(1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, err := ds.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(got) != 1 || got[0].Key != 2 {
		t.Fatalf("after delete want only key 2, got %+v", got)
	}
	// Deleting an absent key is not an error (idempotent cleanup).
	if err := ds.delete(999); err != nil {
		t.Errorf("delete of absent key returned %v, want nil", err)
	}
}

// TestDeployStoreLoadEmpty loads a freshly-created (empty) store.
func TestDeployStoreLoadEmpty(t *testing.T) {
	ds, err := newDeployStore(filepath.Join(t.TempDir(), "deployments"))
	if err != nil {
		t.Fatalf("newDeployStore: %v", err)
	}
	got, err := ds.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no records, got %d", len(got))
	}
}

// TestNewDeployStoreMkdirFails covers the MkdirAll error branch: a store dir
// whose parent is a regular file cannot be created.
func TestNewDeployStoreMkdirFails(t *testing.T) {
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := newDeployStore(filepath.Join(f, "deployments")); err == nil {
		t.Fatal("newDeployStore under a file: want error, got nil")
	}
}

// TestDeployStoreSaveFailsWithoutDir covers save's open-temp error branch: if the
// directory is gone, writing the temp file fails.
func TestDeployStoreSaveFailsWithoutDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "deployments")
	ds, err := newDeployStore(dir)
	if err != nil {
		t.Fatalf("newDeployStore: %v", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove dir: %v", err)
	}
	if err := ds.save(sampleRecord(1)); err == nil {
		t.Fatal("save with no dir: want error, got nil")
	}
	// delete and loadAll on a missing dir also surface errors, not panics.
	if err := ds.delete(1); err == nil {
		t.Fatal("delete with no dir: want error, got nil")
	}
	if _, err := ds.loadAll(); err == nil {
		t.Fatal("loadAll with no dir: want error, got nil")
	}
}

// TestDeployStoreLoadAllDecodeError covers the decode error branch: a record file
// with invalid JSON fails the load rather than being silently skipped.
func TestDeployStoreLoadAllDecodeError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "deployments")
	ds, err := newDeployStore(dir)
	if err != nil {
		t.Fatalf("newDeployStore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "1.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write bad record: %v", err)
	}
	if _, err := ds.loadAll(); err == nil {
		t.Fatal("loadAll of invalid JSON: want error, got nil")
	}
}

// TestDeployStoreIgnoresStrayFiles proves loadAll skips non-record files rather
// than failing on them.
func TestDeployStoreIgnoresStrayFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "deployments")
	ds, err := newDeployStore(dir)
	if err != nil {
		t.Fatalf("newDeployStore: %v", err)
	}
	if err := ds.save(sampleRecord(1)); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Stray files: a non-.json file and a .json whose stem is not a numeric key.
	// Both are skipped rather than decoded.
	if err := os.WriteFile(filepath.Join(dir, "README.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write stray: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.json"), []byte("also not a record"), 0o644); err != nil {
		t.Fatalf("write stray json: %v", err)
	}
	got, err := ds.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 record, got %d", len(got))
	}
}
