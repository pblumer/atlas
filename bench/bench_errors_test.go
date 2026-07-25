package bench_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/bench"
	"github.com/pblumer/atlas/bench/scenarios"
)

// blockPath plants a regular file where a subdirectory is expected, so opening
// that store/log fails — the cheapest reliable way to exercise the error paths.
func blockPath(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
		t.Fatalf("blockPath: %v", err)
	}
}

// TestOpenStateFailure covers Open's second failure mode: the wal opens but the
// state store does not. Open must return an error and not leak the wal handle.
func TestOpenStateFailure(t *testing.T) {
	dir := t.TempDir()
	blockPath(t, dir, "state")
	if _, err := bench.Open(dir); err == nil {
		t.Fatal("Open with a file-blocked state dir: got nil, want error")
	}
}

// TestBuildLogOpenFailure covers BuildLog's early return when the engine cannot
// open at all.
func TestBuildLogOpenFailure(t *testing.T) {
	dir := t.TempDir()
	blockPath(t, dir, "wal")
	cp, jobTypes := scenarios.Linear().Compile(7)
	if _, err := bench.BuildLog(dir, cp, jobTypes, 1, nil); err == nil {
		t.Fatal("BuildLog with a file-blocked wal dir: got nil, want error")
	}
}

// TestRecoverFailures covers Recover's two failure modes: a log that will not
// open, and a store that will not open once the log has.
func TestRecoverFailures(t *testing.T) {
	cp, jobTypes := scenarios.Linear().Compile(7)

	t.Run("wal", func(t *testing.T) {
		logDir := t.TempDir()
		blockPath(t, logDir, "wal")
		if _, err := bench.Recover(logDir, t.TempDir(), cp); err == nil {
			t.Fatal("Recover with a file-blocked wal: got nil, want error")
		}
	})

	t.Run("state", func(t *testing.T) {
		// A real log to open, but a blocked store to replay into.
		logDir := t.TempDir()
		if _, err := bench.BuildLog(logDir, cp, jobTypes, 1, nil); err != nil {
			t.Fatalf("BuildLog: %v", err)
		}
		storeDir := t.TempDir()
		blockPath(t, storeDir, "state")
		if _, err := bench.Recover(logDir, storeDir, cp); err == nil {
			t.Fatal("Recover with a file-blocked store: got nil, want error")
		}
	})
}
