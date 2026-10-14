package ownerfile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/internal/ownerfile"
)

// TestARestrictedFileIsOwnerOnly: after Restrict, Check agrees the file is this
// account's alone — mode 0600 on Unix, a protected DACL with one entry for this
// account on Windows.
func TestARestrictedFileIsOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := ownerfile.Restrict(path); err != nil {
		t.Fatalf("Restrict: %v", err)
	}
	if err := ownerfile.Check(path); err != nil {
		t.Fatalf("Check after Restrict: %v", err)
	}
}

// TestAnOrdinaryFileIsNotOwnerOnly: Check has to be able to say no, or the test above
// proves nothing. A file created the ordinary way is readable beyond its owner — mode
// 0644 on Unix, the directory's inherited DACL on Windows.
func TestAnOrdinaryFileIsNotOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ordinary")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil { // past any umask
		t.Fatalf("Chmod: %v", err)
	}
	if err := ownerfile.Check(path); err == nil {
		t.Fatal("Check of an ordinary file = nil, want an error")
	}
}

// TestAMissingFileIsAnError: neither side of the pair reports success for a file
// that is not there.
func TestAMissingFileIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gone")
	if err := ownerfile.Restrict(path); err == nil {
		t.Error("Restrict of a missing file = nil, want an error")
	}
	if err := ownerfile.Check(path); err == nil {
		t.Error("Check of a missing file = nil, want an error")
	}
}
