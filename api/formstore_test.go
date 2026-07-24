package api

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNewFormStoreError covers newFormStore's failure branch: a file where the
// directory should be makes MkdirAll fail.
func TestNewFormStoreError(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "forms")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	if _, err := newFormStore(blocker); err == nil {
		t.Fatal("newFormStore over a file: want an error")
	}
}

func newForms(t *testing.T) *formStore {
	t.Helper()
	fs, err := newFormStore(filepath.Join(t.TempDir(), "forms"))
	if err != nil {
		t.Fatalf("newFormStore: %v", err)
	}
	return fs
}

// TestFormStoreRoundTripAndOrder saves forms and reads them back most-recently
// saved first, and proves get finds a specific one.
func TestFormStoreRoundTripAndOrder(t *testing.T) {
	fs := newForms(t)
	for _, r := range []form{
		{ID: "a", Name: "A", SavedAt: 100, Schema: `{"a":1}`},
		{ID: "b", Name: "B", SavedAt: 300, Schema: `{"b":1}`},
		{ID: "c", Name: "C", SavedAt: 200, Schema: `{"c":1}`},
	} {
		if err := fs.save(r); err != nil {
			t.Fatalf("save %s: %v", r.ID, err)
		}
	}
	got, err := fs.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	want := []string{"b", "c", "a"} // SavedAt descending
	if len(got) != 3 {
		t.Fatalf("loadAll returned %d, want 3", len(got))
	}
	for i, w := range want {
		if got[i].ID != w {
			t.Errorf("position %d = %q, want %q", i, got[i].ID, w)
		}
	}
	rec, ok, err := fs.get("b")
	if err != nil || !ok || rec.Schema != `{"b":1}` {
		t.Fatalf("get b = (%+v, %v, %v), want the saved record", rec, ok, err)
	}
}

// TestFormStoreOverwrite proves re-saving an id replaces the record.
func TestFormStoreOverwrite(t *testing.T) {
	fs := newForms(t)
	if err := fs.save(form{ID: "p", Name: "First", SavedAt: 1, Schema: `{"v":1}`}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := fs.save(form{ID: "p", Name: "Second", SavedAt: 2, Schema: `{"v":2}`}); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	got, err := fs.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Second" {
		t.Fatalf("after overwrite want one 'Second' record, got %+v", got)
	}
}

// TestFormStoreGetMissAndDelete covers a missing get and idempotent delete.
func TestFormStoreGetMissAndDelete(t *testing.T) {
	fs := newForms(t)
	if _, ok, err := fs.get("nope"); err != nil || ok {
		t.Fatalf("get missing = (ok=%v, err=%v), want (false, nil)", ok, err)
	}
	if err := fs.delete("nope"); err != nil {
		t.Fatalf("delete missing: %v, want nil (idempotent)", err)
	}
	if err := fs.save(form{ID: "p", SavedAt: 1, Schema: `{}`}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := fs.delete("p"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := fs.get("p"); ok {
		t.Fatal("get after delete: still present")
	}
}

// TestFormStoreLoadAllSkipsForeignFiles proves loadAll ignores files that are
// not hex-named form records.
func TestFormStoreLoadAllSkipsForeignFiles(t *testing.T) {
	fs := newForms(t)
	if err := fs.save(form{ID: "real", SavedAt: 1, Schema: `{}`}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// A non-hex-named .json and a non-.json file are both ignored.
	if err := os.WriteFile(filepath.Join(fs.dir, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fs.dir, "zzz.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write non-hex json: %v", err)
	}
	got, err := fs.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(got) != 1 || got[0].ID != "real" {
		t.Fatalf("loadAll = %+v, want only the real record", got)
	}
}

// TestFormStoreDecodeErrors covers get's and loadAll's JSON-decode failure paths
// by planting a corrupt record at a valid hex-named path.
func TestFormStoreDecodeErrors(t *testing.T) {
	fs := newForms(t)
	if err := os.WriteFile(fs.fileFor("bad"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	if _, _, err := fs.get("bad"); err == nil {
		t.Error("get on corrupt record: want an error")
	}
	if _, err := fs.loadAll(); err == nil {
		t.Error("loadAll with a corrupt record: want an error")
	}
}

// TestFormStoreReadAndDeleteErrors covers the non-IsNotExist read branch of get
// and the remove-failure branch of delete, by occupying a record path with a
// non-empty directory (ReadFile and Remove both fail on it).
func TestFormStoreReadAndDeleteErrors(t *testing.T) {
	fs := newForms(t)
	path := fs.fileFor("dir")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("mkdir record path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "child"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write child: %v", err)
	}
	if _, _, err := fs.get("dir"); err == nil {
		t.Error("get on a directory path: want an error")
	}
	if err := fs.delete("dir"); err == nil {
		t.Error("delete of a non-empty directory: want an error")
	}
}
