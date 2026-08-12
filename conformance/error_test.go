package conformance

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRunRejectsMalformedModel is the negative/adversarial case: a model that is
// not valid BPMN must be rejected up front (at compile), not run into garbage.
// This exercises Run's compile-error path and stands in for the negative-model
// category the collection will grow.
func TestRunRejectsMalformedModel(t *testing.T) {
	garbage := []byte("<definitions><process id=\"x\"><startEvent id=\"s\"/>")
	if _, err := Run(t.TempDir(), garbage, Start{}, nil); err == nil {
		t.Fatal("Run accepted a malformed model; want a compile error")
	}
}

// TestRunSurfacesIOError proves Run propagates a store-open failure rather than
// swallowing it: pointing the run at a path already occupied by a regular file
// makes the working directories impossible to create.
func TestRunSurfacesIOError(t *testing.T) {
	base := t.TempDir()
	// Occupy the "live" child path with a file so opening the wal/state under it
	// fails.
	if err := os.WriteFile(filepath.Join(base, "live"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	model, err := Scenarios[0].load()
	if err != nil {
		t.Fatalf("load model: %v", err)
	}
	if _, err := Run(base, model, Start{}, nil); err == nil {
		t.Fatal("Run ignored an I/O failure; want an error")
	}
}
