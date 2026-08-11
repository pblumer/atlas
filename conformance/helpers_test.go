package conformance

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

func mustCompile(t *testing.T, sc Scenario) *compiler.CompiledProcess {
	t.Helper()
	xml, err := sc.load()
	if err != nil {
		t.Fatalf("load %s: %v", sc.Name, err)
	}
	cp, err := compiler.Parse(defKey, 1, bytes.NewReader(xml))
	if err != nil {
		t.Fatalf("compile %s: %v", sc.Name, err)
	}
	return cp
}

// TestOpenStoresStateError covers the branch where the write-ahead log opens but
// the state store does not: the log must be closed so it does not leak.
func TestOpenStoresStateError(t *testing.T) {
	dir := t.TempDir()
	// wal.Open creates dir/wal; occupy dir/state with a regular file so
	// state.Open fails.
	if err := os.WriteFile(filepath.Join(dir, "state"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if _, _, err := openStores(dir); err == nil {
		t.Fatal("openStores ignored a state-open failure; want an error")
	}
}

// TestReplayLogOpenError covers replayLog's store-open failure path, including the
// log cleanup on that error.
func TestReplayLogOpenError(t *testing.T) {
	live := t.TempDir()
	replay := t.TempDir()
	if err := os.WriteFile(filepath.Join(replay, "state"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if _, err := replayLog(live, replay, mustCompile(t, Scenarios[0])); err == nil {
		t.Fatal("replayLog ignored a store-open failure; want an error")
	}
}
