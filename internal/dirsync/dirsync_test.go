package dirsync_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/internal/dirsync"
)

// TestAnOpenedDirectorySyncs: the whole point of the package. On Windows a
// directory handle from os.Open cannot be flushed, so this is the test that
// fails there if Open ever hands back a read-only handle again.
func TestAnOpenedDirectorySyncs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "entry"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	d, err := dirsync.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
}

// TestAMissingDirectoryIsAnError: a directory that is not there cannot be made
// durable, and saying nothing would let a caller report a write as saved.
func TestAMissingDirectoryIsAnError(t *testing.T) {
	d, err := dirsync.Open(filepath.Join(t.TempDir(), "gone"))
	if err == nil {
		d.Close()
		t.Fatal("Open of a missing directory: got nil error")
	}
}
