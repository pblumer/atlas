package api

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDeployStoreSaveError covers save's failure branch: when the temp file path
// is already a directory, opening it for writing fails (EISDIR) and save returns
// an error rather than reporting the record as durable. Using a directory works
// regardless of the test's uid (a read-only-dir trick doesn't bind root).
func TestDeployStoreSaveError(t *testing.T) {
	dir := t.TempDir()
	ds, err := newDeployStore(dir)
	if err != nil {
		t.Fatalf("newDeployStore: %v", err)
	}
	// Occupy the temp path (final + ".tmp") with a directory so os.OpenFile fails.
	tmp := ds.fileFor(1) + ".tmp"
	if err := os.Mkdir(tmp, 0o755); err != nil {
		t.Fatalf("mkdir temp path: %v", err)
	}

	if err := ds.save(persistedDeployment{Key: 1, ProcessID: "p", Version: 1}); err == nil {
		t.Fatal("save with a directory at the temp path: want an error, got nil")
	}
}

// TestDeployStoreSaveRenameError covers save's rename branch: the temp file is
// written and fsynced fine, but renaming it over a path already occupied by a
// non-empty directory fails, so save returns an error.
func TestDeployStoreSaveRenameError(t *testing.T) {
	dir := t.TempDir()
	ds, err := newDeployStore(dir)
	if err != nil {
		t.Fatalf("newDeployStore: %v", err)
	}
	// Occupy the final record path with a non-empty directory.
	final := ds.fileFor(2)
	if err := os.Mkdir(final, 0o755); err != nil {
		t.Fatalf("mkdir final: %v", err)
	}
	if err := os.WriteFile(filepath.Join(final, "child"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write child: %v", err)
	}

	if err := ds.save(persistedDeployment{Key: 2, ProcessID: "p", Version: 1}); err == nil {
		t.Fatal("save renaming over a non-empty directory: want an error, got nil")
	}
}

// TestDeployStoreDeleteError covers delete's non-IsNotExist error branch: when
// the record path is a non-empty directory, os.Remove fails with something other
// than "not exist" and delete surfaces it.
func TestDeployStoreDeleteError(t *testing.T) {
	dir := t.TempDir()
	ds, err := newDeployStore(dir)
	if err != nil {
		t.Fatalf("newDeployStore: %v", err)
	}
	// Put a non-empty directory where the key's record file would be, so removing
	// it fails (a directory with contents can't be removed by os.Remove).
	recPath := ds.fileFor(7)
	if err := os.Mkdir(recPath, 0o755); err != nil {
		t.Fatalf("mkdir record path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(recPath, "child"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write child: %v", err)
	}

	if err := ds.delete(7); err == nil {
		t.Fatal("delete of a non-empty directory: want an error, got nil")
	}
}
