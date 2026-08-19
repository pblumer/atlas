package sidecar

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type item struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	SavedAt int64  `json:"savedAt"`
}

func itemKey(i item) string { return i.ID }

func newItemStore(t *testing.T, opts ...Option[item]) *Store[item] {
	t.Helper()
	s, err := NewStore(filepath.Join(t.TempDir(), "items"), "itemstore", itemKey, opts...)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

// TestStoreRoundTrip is the core contract every design-time store relies on: a
// saved record reads back by its key.
func TestStoreRoundTrip(t *testing.T) {
	s := newItemStore(t)
	want := item{ID: "order-process", Name: "Order", SavedAt: 7}
	if err := s.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := s.Get("order-process")
	if err != nil || !ok {
		t.Fatalf("Get: %+v ok=%v err=%v", got, ok, err)
	}
	if got != want {
		t.Errorf("Get = %+v, want %+v", got, want)
	}
}

// TestStoreGetMiss proves an absent key is a clean miss, not an error — callers
// branch on ok, and a missing record is a normal state.
func TestStoreGetMiss(t *testing.T) {
	s := newItemStore(t)
	got, ok, err := s.Get("nope")
	if err != nil || ok || got != (item{}) {
		t.Fatalf("Get miss = %+v, ok=%v, err=%v; want zero,false,nil", got, ok, err)
	}
}

// TestStoreSaveOverwrites proves re-saving the same key replaces the record
// rather than piling up versions — the property every "save the draft again"
// path depends on.
func TestStoreSaveOverwrites(t *testing.T) {
	s := newItemStore(t)
	if err := s.Save(item{ID: "a", Name: "first"}); err != nil {
		t.Fatalf("save first: %v", err)
	}
	if err := s.Save(item{ID: "a", Name: "second"}); err != nil {
		t.Fatalf("save second: %v", err)
	}
	got, _, err := s.Get("a")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "second" {
		t.Errorf("Name = %q, want %q", got.Name, "second")
	}
	all, err := s.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("LoadAll returned %d records, want 1", len(all))
	}
}

// TestStoreDeleteIsIdempotent covers cleanup: deleting twice is not an error, so
// a caller need not check existence first.
func TestStoreDeleteIsIdempotent(t *testing.T) {
	s := newItemStore(t)
	if err := s.Save(item{ID: "a"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Delete("a"); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	if err := s.Delete("a"); err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if _, ok, _ := s.Get("a"); ok {
		t.Error("record still present after delete")
	}
}

// TestStoreLoadAllEmpty proves an empty store yields no records and no error.
func TestStoreLoadAllEmpty(t *testing.T) {
	all, err := newItemStore(t).LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("LoadAll = %v, want empty", all)
	}
}

// TestStoreLoadAllOrder proves the Order option decides the listing order — the
// one thing the stores genuinely differ on (newest-first for drafts, oldest-first
// for projects).
func TestStoreLoadAllOrder(t *testing.T) {
	s := newItemStore(t, Order(func(a, b item) bool { return a.SavedAt > b.SavedAt }))
	for _, rec := range []item{{ID: "a", SavedAt: 1}, {ID: "b", SavedAt: 3}, {ID: "c", SavedAt: 2}} {
		if err := s.Save(rec); err != nil {
			t.Fatalf("Save %s: %v", rec.ID, err)
		}
	}
	all, err := s.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	var ids []string
	for _, rec := range all {
		ids = append(ids, rec.ID)
	}
	if strings.Join(ids, ",") != "b,c,a" {
		t.Errorf("order = %v, want [b c a]", ids)
	}
}

// TestStoreLoadAllIgnoresForeignFiles proves a directory holding anything else —
// a stray temp file, a non-record name, a subdirectory — still lists cleanly.
func TestStoreLoadAllIgnoresForeignFiles(t *testing.T) {
	s := newItemStore(t)
	if err := s.Save(item{ID: "real"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	for name, data := range map[string]string{
		"notes.txt":      "hello",
		"zzz.json":       `{"id":"nothex"}`,
		"deadbeef.tmp":   "{}",
		"6e6f746a736f6e": "{}",
	} {
		if err := os.WriteFile(filepath.Join(s.Dir(), name), []byte(data), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(s.Dir(), hex.EncodeToString([]byte("dir"))+".json"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	all, err := s.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(all) != 1 || all[0].ID != "real" {
		t.Errorf("LoadAll = %+v, want just the real record", all)
	}
}

// TestStoreKeysAreFilenameSafe proves a key containing path separators and other
// hostile characters still round-trips, because keys are hex-encoded rather than
// used as filenames directly.
func TestStoreKeysAreFilenameSafe(t *testing.T) {
	s := newItemStore(t)
	key := "../../etc/passwd\x00 weird"
	if err := s.Save(item{ID: key, Name: "safe"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := s.Get(key)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Name != "safe" {
		t.Errorf("Name = %q, want safe", got.Name)
	}
	entries, err := os.ReadDir(s.Dir())
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("wrote %d files, want 1", len(entries))
	}
	if strings.ContainsAny(entries[0].Name(), "/.\x00 "[:3]) && !strings.HasSuffix(entries[0].Name(), ".json") {
		t.Errorf("filename %q is not safe", entries[0].Name())
	}
}

// TestStoreNamesOption proves a store can opt out of hex filenames — the public
// links store names files by an already-hex token, and the deployment store by a
// decimal key.
func TestStoreNamesOption(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "items")
	s, err := NewStore(dir, "itemstore", itemKey,
		Names[item](func(k string) string { return k }, func(stem string) bool { return stem != "skipme" }))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.Save(item{ID: "plain", Name: "n"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "plain.json")); statErr != nil {
		t.Fatalf("expected plain.json: %v", statErr)
	}
	if err := os.WriteFile(filepath.Join(dir, "skipme.json"), []byte(`{"id":"skipme"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	all, err := s.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(all) != 1 || all[0].ID != "plain" {
		t.Errorf("LoadAll = %+v, want just plain", all)
	}
}

// TestStoreErrorsCarryTheStoreName proves failures name the store they came from,
// so a message stays as diagnosable as the hand-written stores' were.
func TestStoreErrorsCarryTheStoreName(t *testing.T) {
	s := newItemStore(t)
	if err := os.WriteFile(s.FileFor("bad"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, _, err := s.Get("bad")
	if err == nil || !strings.HasPrefix(err.Error(), "itemstore: decode") {
		t.Fatalf("Get err = %v, want an itemstore: decode error", err)
	}
	if _, err := s.LoadAll(); err == nil || !strings.HasPrefix(err.Error(), "itemstore: decode") {
		t.Fatalf("LoadAll err = %v, want an itemstore: decode error", err)
	}
}

// TestStoreReadErrors covers the non-NotExist read branches on a single record,
// reached when its path is a directory rather than a file. A listing skips such
// an entry (see below); a direct Get or Delete of it does not.
func TestStoreReadErrors(t *testing.T) {
	s := newItemStore(t)
	if err := os.Mkdir(s.FileFor("dir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Non-empty, so os.Remove cannot succeed on it either.
	if err := os.WriteFile(filepath.Join(s.FileFor("dir"), "occupant"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write occupant: %v", err)
	}
	if _, _, err := s.Get("dir"); err == nil || !strings.Contains(err.Error(), "itemstore: read") {
		t.Fatalf("Get err = %v, want an itemstore: read error", err)
	}
	if err := s.Delete("dir"); err == nil || !strings.Contains(err.Error(), "itemstore: remove") {
		t.Fatalf("Delete err = %v, want an itemstore: remove error", err)
	}
	// A directory whose name looks like a record is not one, so a listing ignores
	// it rather than failing.
	all, err := s.LoadAll()
	if err != nil || len(all) != 0 {
		t.Fatalf("LoadAll = %+v, err = %v; want empty and nil", all, err)
	}
}

// TestStoreLoadAllReadError covers the listing's read-error branch with an entry
// that passes the name filter and is not a directory, but cannot be read — a
// dangling symlink, the shape a half-cleaned data directory leaves behind.
func TestStoreLoadAllReadError(t *testing.T) {
	s := newItemStore(t)
	link := s.FileFor("dangling")
	if err := os.Symlink(filepath.Join(s.Dir(), "no-such-target"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := s.LoadAll(); err == nil || !strings.Contains(err.Error(), "itemstore: read ") {
		t.Fatalf("LoadAll err = %v, want an itemstore: read <file> error", err)
	}
}

// TestStoreLoadAllDirError covers the read-dir branch: a store whose directory
// vanished reports it rather than returning an empty listing.
func TestStoreLoadAllDirError(t *testing.T) {
	s := newItemStore(t)
	if err := os.RemoveAll(s.Dir()); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := s.LoadAll(); err == nil || !strings.Contains(err.Error(), "itemstore: read dir") {
		t.Fatalf("LoadAll err = %v, want an itemstore: read dir error", err)
	}
}

// TestNewStoreDirError covers the MkdirAll failure branch: a parent path
// component that is a file, not a directory.
func TestNewStoreDirError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := NewStore(filepath.Join(file, "items"), "itemstore", itemKey)
	if err == nil || !strings.Contains(err.Error(), "itemstore: create dir") {
		t.Fatalf("NewStore err = %v, want an itemstore: create dir error", err)
	}
}

// TestStoreRejectsUnaddressableKeys is the guard that makes a store naming files
// by the key itself safe. The listing predicate decides which filenames are this
// store's records; the same predicate has to hold on the way *in*, or a key that
// could never have produced a record — "../secret" — still addresses a file.
//
// The default hex encoding cannot produce such a key (any string hex-encodes to
// valid hex), so this only bites the stores that opt out of it.
func TestStoreRejectsUnaddressableKeys(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "items")
	isHex := func(stem string) bool {
		if stem == "" || len(stem)%2 != 0 {
			return false
		}
		_, err := hex.DecodeString(stem)
		return err == nil
	}
	s, err := NewStore(dir, "itemstore", itemKey,
		Names[item](func(k string) string { return k }, isHex))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// A record sitting one directory up, which a traversing key would otherwise
	// reach.
	outside := filepath.Join(filepath.Dir(dir), "secret.json")
	if err := os.WriteFile(outside, []byte(`{"id":"secret"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	for _, key := range []string{"../secret", "../../etc/passwd", "not-hex", ""} {
		if got, ok, err := s.Get(key); ok || err != nil {
			t.Errorf("Get(%q) = %+v, ok %v, err %v; want a clean miss", key, got, ok, err)
		}
		if err := s.Delete(key); err != nil {
			t.Errorf("Delete(%q) = %v; want nil", key, err)
		}
		if err := s.Save(item{ID: key}); err == nil {
			t.Errorf("Save(%q) succeeded; want a refusal", key)
		}
	}

	// The file one directory up is untouched.
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("the outside record was disturbed: %v", err)
	}
	// A well-formed key still works.
	if err := s.Save(item{ID: "beef", Name: "ok"}); err != nil {
		t.Fatalf("Save valid key: %v", err)
	}
	if got, ok, err := s.Get("beef"); !ok || err != nil || got.Name != "ok" {
		t.Errorf("Get(beef) = %+v, ok %v, err %v", got, ok, err)
	}
}
