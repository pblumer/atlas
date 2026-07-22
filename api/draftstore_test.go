package api

import (
	"os"
	"path/filepath"
	"testing"
)

func newDrafts(t *testing.T) *draftStore {
	t.Helper()
	ds, err := newDraftStore(filepath.Join(t.TempDir(), "drafts"))
	if err != nil {
		t.Fatalf("newDraftStore: %v", err)
	}
	return ds
}

// TestDraftStoreRoundTripAndOrder saves drafts and reads them back most-recently
// saved first, and proves get finds a specific one.
func TestDraftStoreRoundTripAndOrder(t *testing.T) {
	ds := newDrafts(t)
	if err := ds.save(draft{ProcessID: "a", Name: "A", SavedAt: 100, XML: "<a/>"}); err != nil {
		t.Fatalf("save a: %v", err)
	}
	if err := ds.save(draft{ProcessID: "b", Name: "B", SavedAt: 300, XML: "<b/>"}); err != nil {
		t.Fatalf("save b: %v", err)
	}
	if err := ds.save(draft{ProcessID: "c", Name: "C", SavedAt: 200, XML: "<c/>"}); err != nil {
		t.Fatalf("save c: %v", err)
	}
	got, err := ds.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	wantOrder := []string{"b", "c", "a"} // by SavedAt descending
	if len(got) != 3 {
		t.Fatalf("loadAll returned %d, want 3", len(got))
	}
	for i, w := range wantOrder {
		if got[i].ProcessID != w {
			t.Errorf("position %d = %q, want %q (not sorted by savedAt desc)", i, got[i].ProcessID, w)
		}
	}
	rec, ok, err := ds.get("b")
	if err != nil || !ok || rec.XML != "<b/>" {
		t.Fatalf("get b = (%+v, %v, %v), want the saved record", rec, ok, err)
	}
}

// TestDraftStoreOverwrite proves re-saving a process id replaces the draft rather
// than piling up files.
func TestDraftStoreOverwrite(t *testing.T) {
	ds := newDrafts(t)
	if err := ds.save(draft{ProcessID: "p", Name: "First", SavedAt: 1, XML: "<one/>"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := ds.save(draft{ProcessID: "p", Name: "Second", SavedAt: 2, XML: "<two/>"}); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	got, err := ds.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Second" || got[0].XML != "<two/>" {
		t.Fatalf("after overwrite want one 'Second' record, got %+v", got)
	}
}

// TestDraftStoreGetMissAndDelete covers a missing get and idempotent delete.
func TestDraftStoreGetMissAndDelete(t *testing.T) {
	ds := newDrafts(t)
	if _, ok, err := ds.get("nope"); err != nil || ok {
		t.Fatalf("get missing = (ok=%v, err=%v), want (false, nil)", ok, err)
	}
	if err := ds.save(draft{ProcessID: "p", SavedAt: 1, XML: "<x/>"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := ds.delete("p"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := ds.get("p"); ok {
		t.Fatal("draft still present after delete")
	}
	if err := ds.delete("p"); err != nil {
		t.Errorf("delete of absent draft = %v, want nil (idempotent)", err)
	}
}

// TestDraftStoreProcessIdWithUnsafeChars proves hex-keyed filenames handle ids
// that would be unsafe as raw filenames.
func TestDraftStoreProcessIdWithUnsafeChars(t *testing.T) {
	ds := newDrafts(t)
	id := "weird/id.with:chars"
	if err := ds.save(draft{ProcessID: id, SavedAt: 1, XML: "<x/>"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	rec, ok, err := ds.get(id)
	if err != nil || !ok || rec.ProcessID != id {
		t.Fatalf("get unsafe id = (%+v, %v, %v)", rec, ok, err)
	}
}

// TestDraftStoreErrorPaths covers the mkdir, missing-dir, decode, and stray-file
// branches.
func TestDraftStoreErrorPaths(t *testing.T) {
	// newDraftStore under a regular file cannot create its directory.
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := newDraftStore(filepath.Join(f, "drafts")); err == nil {
		t.Fatal("newDraftStore under a file: want error")
	}

	dir := filepath.Join(t.TempDir(), "drafts")
	ds, err := newDraftStore(dir)
	if err != nil {
		t.Fatalf("newDraftStore: %v", err)
	}

	// Invalid JSON in a hex-named record fails loadAll and get.
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

	// A non-hex .json file is skipped by loadAll.
	if err := os.WriteFile(filepath.Join(dir, "notes.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write stray: %v", err)
	}
	if _, err := ds.loadAll(); err == nil {
		t.Fatal("loadAll still errors from the bad record") // sanity: bad record dominates
	}
	// Remove the bad record; now the stray is simply ignored and load succeeds.
	if err := os.Remove(bad); err != nil {
		t.Fatalf("remove bad: %v", err)
	}
	got, err := ds.loadAll()
	if err != nil {
		t.Fatalf("loadAll after cleanup: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("stray .json should be ignored, got %d records", len(got))
	}

	// save's rename branch fails when the destination path is a directory.
	ds2, err := newDraftStore(filepath.Join(t.TempDir(), "d2"))
	if err != nil {
		t.Fatalf("newDraftStore: %v", err)
	}
	if err := os.MkdirAll(ds2.fileFor("blocked"), 0o755); err != nil {
		t.Fatalf("make dir at record path: %v", err)
	}
	if err := ds2.save(draft{ProcessID: "blocked", XML: "<x/>"}); err == nil {
		t.Error("save onto a directory path: want rename error, got nil")
	}

	// With the directory gone, save/delete/loadAll surface errors, not panics.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove dir: %v", err)
	}
	if err := ds.save(draft{ProcessID: "p", XML: "<x/>"}); err == nil {
		t.Fatal("save with no dir: want error")
	}
	if err := ds.delete("p"); err == nil {
		t.Fatal("delete with no dir: want error")
	}
	if _, err := ds.loadAll(); err == nil {
		t.Fatal("loadAll with no dir: want error")
	}
}
