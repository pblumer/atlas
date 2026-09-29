package sharedread_test

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/internal/sharedread"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestReadFileReadsTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rec.json")
	writeFile(t, path, "{}")
	got, err := sharedread.ReadFile(path)
	if err != nil || string(got) != "{}" {
		t.Fatalf("ReadFile = %q, %v; want {}", got, err)
	}
}

// TestAMissingFileReadsAsNotExist: callers treat a record that is not there as a
// normal state, so the error has to say "not there" exactly as os.ReadFile does.
func TestAMissingFileReadsAsNotExist(t *testing.T) {
	_, err := sharedread.ReadFile(filepath.Join(t.TempDir(), "gone.json"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ReadFile of a missing file = %v, want fs.ErrNotExist", err)
	}
}

// TestAnOpenFileCanBeRemoved is the point of the package. On Windows a handle from
// os.Open stops anyone else from deleting the file, so a reader in the middle of a
// listing made the writer's delete fail.
func TestAnOpenFileCanBeRemoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rec.json")
	writeFile(t, path, "old")
	f, err := sharedread.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove while a reader holds the file: %v", err)
	}
	if got, err := io.ReadAll(f); err != nil || string(got) != "old" {
		t.Fatalf("the reader's read = %q, %v; want the content it opened", got, err)
	}
}

// TestAnOpenFileCanBeReplaced: a save renames a finished temp file over the record,
// which on Windows a reader's os.Open handle refused just as it refused a delete.
func TestAnOpenFileCanBeReplaced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rec.json")
	writeFile(t, path, "old")
	f, err := sharedread.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()
	tmp := filepath.Join(dir, "rec.json.tmp")
	writeFile(t, tmp, "new")
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("Rename over a file a reader holds: %v", err)
	}
	if got, err := io.ReadAll(f); err != nil || string(got) != "old" {
		t.Fatalf("the reader's read = %q, %v; want the content it opened", got, err)
	}
	if got, err := sharedread.ReadFile(path); err != nil || string(got) != "new" {
		t.Fatalf("a read after the rename = %q, %v; want new", got, err)
	}
}
