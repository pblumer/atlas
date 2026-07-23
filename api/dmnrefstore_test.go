package api

import (
	"os"
	"path/filepath"
	"testing"
)

func newDmnRefs(t *testing.T) *dmnRefStore {
	t.Helper()
	ds, err := newDmnRefStore(filepath.Join(t.TempDir(), "dmnrefs"))
	if err != nil {
		t.Fatalf("newDmnRefStore: %v", err)
	}
	return ds
}

// TestDmnRefStoreRoundTripAndOrder saves references out of order and reads them
// back oldest-first, and proves get finds a specific one.
func TestDmnRefStoreRoundTripAndOrder(t *testing.T) {
	ds := newDmnRefs(t)
	if err := ds.save(dmnRef{ID: "b", Name: "Beta", ModelRef: "pricing", CreatedAt: 200}); err != nil {
		t.Fatalf("save b: %v", err)
	}
	if err := ds.save(dmnRef{ID: "a", Name: "Alpha", ModelRef: "risk", CreatedAt: 100}); err != nil {
		t.Fatalf("save a: %v", err)
	}
	if err := ds.save(dmnRef{ID: "c", Name: "Gamma", ModelRef: "routing", CreatedAt: 300}); err != nil {
		t.Fatalf("save c: %v", err)
	}
	got, err := ds.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	want := []string{"a", "b", "c"} // createdAt ascending
	if len(got) != 3 {
		t.Fatalf("loadAll returned %d, want 3", len(got))
	}
	for i, w := range want {
		if got[i].ID != w {
			t.Errorf("position %d = %q, want %q", i, got[i].ID, w)
		}
	}
	rec, ok, err := ds.get("a")
	if err != nil || !ok || rec.ModelRef != "risk" {
		t.Fatalf("get a = (%+v, %v, %v), want the saved record", rec, ok, err)
	}
}

// TestDmnRefStoreTieBreakByID proves references created in the same second sort
// deterministically by id.
func TestDmnRefStoreTieBreakByID(t *testing.T) {
	ds := newDmnRefs(t)
	for _, id := range []string{"c", "a", "b"} {
		if err := ds.save(dmnRef{ID: id, CreatedAt: 42}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	got, err := ds.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	for i, w := range []string{"a", "b", "c"} {
		if got[i].ID != w {
			t.Errorf("position %d = %q, want %q (tie-break by id)", i, got[i].ID, w)
		}
	}
}

// TestDmnRefStoreOverwriteAndDelete proves re-saving an id replaces the record
// and delete is idempotent.
func TestDmnRefStoreOverwriteAndDelete(t *testing.T) {
	ds := newDmnRefs(t)
	if err := ds.save(dmnRef{ID: "p", Name: "First", ModelRef: "m1"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := ds.save(dmnRef{ID: "p", Name: "Second", ModelRef: "m2"}); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	got, err := ds.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Second" || got[0].ModelRef != "m2" {
		t.Fatalf("after overwrite want one 'Second/m2' record, got %+v", got)
	}
	if err := ds.delete("p"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := ds.get("p"); ok {
		t.Fatal("reference still present after delete")
	}
	if err := ds.delete("p"); err != nil {
		t.Errorf("delete of absent reference = %v, want nil (idempotent)", err)
	}
}

// TestDmnRefStoreErrorPaths covers mkdir, decode, stray-file, and missing-dir
// branches.
func TestDmnRefStoreErrorPaths(t *testing.T) {
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := newDmnRefStore(filepath.Join(f, "dmnrefs")); err == nil {
		t.Fatal("newDmnRefStore under a file: want error")
	}

	dir := filepath.Join(t.TempDir(), "dmnrefs")
	ds, err := newDmnRefStore(dir)
	if err != nil {
		t.Fatalf("newDmnRefStore: %v", err)
	}

	bad := ds.fileFor("p")
	if err := os.WriteFile(bad, []byte("{nope"), 0o644); err != nil {
		t.Fatalf("write bad: %v", err)
	}
	if _, err := ds.loadAll(); err == nil {
		t.Fatal("loadAll of invalid JSON: want error")
	}
	if _, _, err := ds.get("p"); err == nil {
		t.Fatal("get of invalid JSON: want error")
	}

	if err := os.Remove(bad); err != nil {
		t.Fatalf("remove bad: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write stray: %v", err)
	}
	got, err := ds.loadAll()
	if err != nil {
		t.Fatalf("loadAll after cleanup: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("stray .json should be ignored, got %d records", len(got))
	}

	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove dir: %v", err)
	}
	if err := ds.save(dmnRef{ID: "p"}); err == nil {
		t.Fatal("save with no dir: want error")
	}
	if err := ds.delete("p"); err == nil {
		t.Fatal("delete with no dir: want error")
	}
	if _, err := ds.loadAll(); err == nil {
		t.Fatal("loadAll with no dir: want error")
	}
}
