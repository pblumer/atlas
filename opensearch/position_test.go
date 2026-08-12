package opensearch_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/opensearch"
)

func TestPositionStoreLoadAbsent(t *testing.T) {
	p := opensearch.NewPositionStore(filepath.Join(t.TempDir(), "exporter"))
	got, err := p.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != 0 {
		t.Errorf("Load on absent = %d, want 0", got)
	}
}

func TestPositionStoreRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "exporter") // not pre-created: Save must mkdir
	p := opensearch.NewPositionStore(dir)
	if err := p.Save(42); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := p.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != 42 {
		t.Errorf("Load = %d, want 42", got)
	}
	// A fresh store over the same dir sees the persisted value (restart resume).
	if got2, _ := opensearch.NewPositionStore(dir).Load(); got2 != 42 {
		t.Errorf("reopened Load = %d, want 42", got2)
	}
	// Overwrite replaces atomically.
	if err := p.Save(100); err != nil {
		t.Fatalf("Save 2: %v", err)
	}
	if got, _ := p.Load(); got != 100 {
		t.Errorf("Load after overwrite = %d, want 100", got)
	}
}

func TestPositionStoreEmptyFileIsZero(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "opensearch.pos"), []byte("  \n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := opensearch.NewPositionStore(dir).Load()
	if err != nil || got != 0 {
		t.Fatalf("Load of empty file = (%d, %v), want (0, nil)", got, err)
	}
}

// TestPositionStoreSaveMkdirError: Save surfaces a directory-creation failure —
// here the target dir path is occupied by a regular file.
func TestPositionStoreSaveMkdirError(t *testing.T) {
	base := t.TempDir()
	blocker := filepath.Join(base, "blocked")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := opensearch.NewPositionStore(filepath.Join(blocker, "exporter")).Save(1); err == nil {
		t.Fatalf("Save into a blocked path should error")
	}
}

// TestPositionStoreSaveWriteError: the temp path being occupied by a directory
// makes the write fail.
func TestPositionStoreSaveWriteError(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "opensearch.pos.tmp"), 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := opensearch.NewPositionStore(dir).Save(1); err == nil {
		t.Fatalf("Save with a blocked temp path should error")
	}
}

// TestPositionStorePathIsDirectory: when the target file path is itself a
// directory, both Load (read) and Save (rename over it) surface an error rather
// than misbehaving.
func TestPositionStorePathIsDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "opensearch.pos"), 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	p := opensearch.NewPositionStore(dir)
	if _, err := p.Load(); err == nil {
		t.Fatalf("Load of a directory path should error")
	}
	if err := p.Save(1); err == nil {
		t.Fatalf("Save renaming over a directory should error")
	}
}

func TestPositionStoreCorruptIsError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "opensearch.pos"), []byte("not-a-number"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := opensearch.NewPositionStore(dir).Load(); err == nil {
		t.Fatalf("Load of corrupt file should error")
	}
}
