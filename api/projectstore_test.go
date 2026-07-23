package api

import (
	"os"
	"path/filepath"
	"testing"
)

func newProjects(t *testing.T) *projectStore {
	t.Helper()
	ps, err := newProjectStore(filepath.Join(t.TempDir(), "projects"))
	if err != nil {
		t.Fatalf("newProjectStore: %v", err)
	}
	return ps
}

// TestProjectStoreRoundTripAndOrder saves projects out of order and reads them
// back oldest-first (creation order), and proves get finds a specific one.
func TestProjectStoreRoundTripAndOrder(t *testing.T) {
	ps := newProjects(t)
	if err := ps.save(project{ID: "p2", Name: "Beta", CreatedAt: 200, UpdatedAt: 200}); err != nil {
		t.Fatalf("save p2: %v", err)
	}
	if err := ps.save(project{ID: "p1", Name: "Alpha", CreatedAt: 100, UpdatedAt: 150}); err != nil {
		t.Fatalf("save p1: %v", err)
	}
	if err := ps.save(project{ID: "p3", Name: "Gamma", CreatedAt: 300, UpdatedAt: 300}); err != nil {
		t.Fatalf("save p3: %v", err)
	}
	got, err := ps.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	want := []string{"p1", "p2", "p3"} // CreatedAt ascending
	if len(got) != 3 {
		t.Fatalf("loadAll returned %d, want 3", len(got))
	}
	for i, w := range want {
		if got[i].ID != w {
			t.Errorf("position %d = %q, want %q (not sorted by createdAt asc)", i, got[i].ID, w)
		}
	}
	rec, ok, err := ps.get("p1")
	if err != nil || !ok || rec.Name != "Alpha" {
		t.Fatalf("get p1 = (%+v, %v, %v), want the saved record", rec, ok, err)
	}
}

// TestProjectStoreTieBreakByID proves projects created in the same second sort
// deterministically by id.
func TestProjectStoreTieBreakByID(t *testing.T) {
	ps := newProjects(t)
	for _, id := range []string{"c", "a", "b"} {
		if err := ps.save(project{ID: id, CreatedAt: 42}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	got, err := ps.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	want := []string{"a", "b", "c"}
	for i, w := range want {
		if got[i].ID != w {
			t.Errorf("position %d = %q, want %q (tie-break by id)", i, got[i].ID, w)
		}
	}
}

// TestProjectStoreOverwriteAndDelete proves re-saving an id replaces the record
// (the rename path) and delete is idempotent.
func TestProjectStoreOverwriteAndDelete(t *testing.T) {
	ps := newProjects(t)
	if err := ps.save(project{ID: "p", Name: "First", CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := ps.save(project{ID: "p", Name: "Renamed", CreatedAt: 1, UpdatedAt: 2}); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	got, err := ps.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Renamed" {
		t.Fatalf("after overwrite want one 'Renamed' record, got %+v", got)
	}
	if err := ps.delete("p"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := ps.get("p"); ok {
		t.Fatal("project still present after delete")
	}
	if err := ps.delete("p"); err != nil {
		t.Errorf("delete of absent project = %v, want nil (idempotent)", err)
	}
}

// TestProjectStoreErrorPaths covers mkdir, decode, stray-file, and missing-dir
// branches, mirroring the draft store's error coverage.
func TestProjectStoreErrorPaths(t *testing.T) {
	// newProjectStore under a regular file cannot create its directory.
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := newProjectStore(filepath.Join(f, "projects")); err == nil {
		t.Fatal("newProjectStore under a file: want error")
	}

	dir := filepath.Join(t.TempDir(), "projects")
	ps, err := newProjectStore(dir)
	if err != nil {
		t.Fatalf("newProjectStore: %v", err)
	}

	// Invalid JSON in a hex-named record fails loadAll and get.
	bad := ps.fileFor("p")
	if err := os.WriteFile(bad, []byte("{nope"), 0o644); err != nil {
		t.Fatalf("write bad: %v", err)
	}
	if _, err := ps.loadAll(); err == nil {
		t.Fatal("loadAll of invalid JSON: want error")
	}
	if _, _, err := ps.get("p"); err == nil {
		t.Fatal("get of invalid JSON: want error")
	}

	// A non-hex .json file is skipped once the bad record is gone.
	if err := os.Remove(bad); err != nil {
		t.Fatalf("remove bad: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write stray: %v", err)
	}
	got, err := ps.loadAll()
	if err != nil {
		t.Fatalf("loadAll after cleanup: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("stray .json should be ignored, got %d records", len(got))
	}

	// With the directory gone, save/delete/loadAll surface errors, not panics.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove dir: %v", err)
	}
	if err := ps.save(project{ID: "p"}); err == nil {
		t.Fatal("save with no dir: want error")
	}
	if err := ps.delete("p"); err == nil {
		t.Fatal("delete with no dir: want error")
	}
	if _, err := ps.loadAll(); err == nil {
		t.Fatal("loadAll with no dir: want error")
	}
}
