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
	cp := mustCompile(t, Scenarios[0])
	deployables := []compiler.Deployable{{Process: cp}}
	if _, err := replayLog(live, replay, deployables, cp); err == nil {
		t.Fatal("replayLog ignored a store-open failure; want an error")
	}
}

// TestPickRootErrors covers root selection for a multi-process model: it is
// ambiguous with no Root named, an error for an unknown Root, and resolves the
// named one.
func TestPickRootErrors(t *testing.T) {
	xml, err := scenarioByName(t, "call-activity").load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	deployables, err := compiler.ParseAll(defKey, 1, bytes.NewReader(xml))
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	if _, err := pickRoot(deployables, ""); err == nil {
		t.Error("pickRoot with no Root on a multi-process model should error")
	}
	if _, err := pickRoot(deployables, "nope"); err == nil {
		t.Error("pickRoot with an unknown Root should error")
	}
	cp, err := pickRoot(deployables, "call-parent")
	if err != nil || cp.ProcessId() != "call-parent" {
		t.Errorf("pickRoot(call-parent) = %v, %v; want the parent process", cp, err)
	}
}
