package api

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadAllSkipsAndDecodeError covers loadAll's filtering branches (a
// subdirectory and a non-key .json file are skipped) and its decode-error branch
// (a well-named record whose contents are not valid JSON).
func TestLoadAllSkipsAndDecodeError(t *testing.T) {
	dir := t.TempDir()
	ds, err := newDeployStore(dir)
	if err != nil {
		t.Fatalf("newDeployStore: %v", err)
	}

	// A subdirectory and a non-key .json file must both be ignored.
	if err := os.Mkdir(filepath.Join(dir, "sub.json"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}
	// A valid record loads fine on its own.
	if err := ds.save(persistedDeployment{Key: 3, ProcessID: "p", Version: 1}); err != nil {
		t.Fatalf("save: %v", err)
	}
	recs, err := ds.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(recs) != 1 || recs[0].Key != 3 {
		t.Fatalf("loadAll = %+v, want just key 3", recs)
	}

	// A <key>.json file with invalid JSON is a hard decode error.
	if err := os.WriteFile(ds.fileFor(4), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write bad record: %v", err)
	}
	if _, err := ds.loadAll(); err == nil {
		t.Fatal("loadAll with a corrupt record: want an error, got nil")
	}
}
