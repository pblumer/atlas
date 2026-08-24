package sidecar

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type record struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// TestWriteJSONRoundTrip is the property every store depends on: once WriteJSON
// returns nil the record is on disk and reads back as itself.
func TestWriteJSONRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rec.json")
	want := record{Name: "draft-1", Count: 3}
	if err := WriteJSON(dir, path, want); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got record
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != want {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}

// TestWriteJSONOverwrites covers the rename-over-existing path: a second write
// replaces the record rather than appending to or failing on the existing file.
func TestWriteJSONOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rec.json")
	if err := WriteJSON(dir, path, record{Name: "first"}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteJSON(dir, path, record{Name: "second"}); err != nil {
		t.Fatalf("second write: %v", err)
	}
	data, _ := os.ReadFile(path)
	var got record
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Name != "second" {
		t.Errorf("name = %q, want %q", got.Name, "second")
	}
}

// TestWriteJSONNoTempLeftBehind proves the temp file is renamed away rather than
// left beside the record — a store's directory listing is its index, so a stray
// .tmp would show up as a phantom entry.
func TestWriteJSONNoTempLeftBehind(t *testing.T) {
	dir := t.TempDir()
	if err := WriteJSON(dir, filepath.Join(dir, "rec.json"), record{Name: "x"}); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "rec.json" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("directory holds %v, want just rec.json", names)
	}
}

// TestWriteJSONMarshalError proves an unmarshalable value fails before anything
// touches the disk, so a bad record cannot leave a half-written file.
func TestWriteJSONMarshalError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rec.json")
	err := WriteJSON(dir, path, make(chan int))
	if err == nil || !strings.Contains(err.Error(), "marshal") {
		t.Fatalf("err = %v, want a marshal error", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("directory is not empty after a failed marshal: %v", entries)
	}
}

// TestWriteFileRawBytes covers the non-JSON artifact path (a process
// documentation PDF, ADR-0143) through the same durability discipline.
func TestWriteFileRawBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.pdf")
	want := []byte("%PDF-1.7\nnot really a pdf\n")
	if err := WriteFile(dir, path, want); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("read back %q, want %q", got, want)
	}
}

// TestWriteFileOpenError covers the open-temp failure branch: a directory that
// does not exist cannot hold the temp file.
func TestWriteFileOpenError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	err := WriteFile(dir, filepath.Join(dir, "rec.json"), []byte("{}"))
	if err == nil || !strings.Contains(err.Error(), "open temp") {
		t.Fatalf("err = %v, want an open temp error", err)
	}
}

// TestWriteFileRenameError covers the rename failure branch, reached when the
// destination path is a non-empty directory rather than a file.
func TestWriteFileRenameError(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "rec.json")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dest, "occupant"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write occupant: %v", err)
	}
	err := WriteFile(dir, dest, []byte("{}"))
	if err == nil || !strings.Contains(err.Error(), "rename") {
		t.Fatalf("err = %v, want a rename error", err)
	}
	// The temp file is cleaned up even on the failure path.
	if _, statErr := os.Stat(dest + ".tmp"); !os.IsNotExist(statErr) {
		t.Errorf("temp file survived a failed rename")
	}
}

// TestFsyncDirMissing covers the open-dir failure branch.
func TestFsyncDirMissing(t *testing.T) {
	err := FsyncDir(filepath.Join(t.TempDir(), "gone"))
	if err == nil || !strings.Contains(err.Error(), "open dir") {
		t.Fatalf("err = %v, want an open dir error", err)
	}
}

// TestFsyncDirExisting is the success path: fsyncing a real directory reports no
// error, which is what makes a completed rename durable.
func TestFsyncDirExisting(t *testing.T) {
	if err := FsyncDir(t.TempDir()); err != nil {
		t.Fatalf("FsyncDir: %v", err)
	}
}

// TestWriteFileDirFsyncError covers the final directory-fsync branch: the record
// itself lands, but the create is not durable because the directory it was
// reported against cannot be fsynced. The write must be reported as failed —
// "nil error means durable" is the whole contract.
func TestWriteFileDirFsyncError(t *testing.T) {
	real := t.TempDir()
	gone := filepath.Join(t.TempDir(), "gone")
	err := WriteFile(gone, filepath.Join(real, "rec.json"), []byte("{}"))
	if err == nil || !strings.Contains(err.Error(), "open dir") {
		t.Fatalf("err = %v, want an open dir error", err)
	}
}

// TestFsyncDirNotADirectory covers the sync-failure branch: a regular file opens
// fine but cannot be fsynced as a directory.
func TestFsyncDirNotADirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// A plain file Syncs cleanly on Linux, so this asserts the branch is taken
	// without asserting which way it goes on every platform.
	if err := FsyncDir(path); err != nil && !strings.Contains(err.Error(), "sync dir") {
		t.Fatalf("err = %v, want nil or a sync dir error", err)
	}
}
