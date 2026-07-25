package bench_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/bench"
	"github.com/pblumer/atlas/bench/scenarios"
	"github.com/pblumer/atlas/model"
)

// countInstances reports how many process instances the store records as active
// and as completed — the terminal-state signal the behavioral tests assert on.
func countInstances(t *testing.T, e *bench.Engine) (active, completed int) {
	t.Helper()
	a, err := e.Store.ActiveProcessInstanceCount()
	if err != nil {
		t.Fatalf("ActiveProcessInstanceCount: %v", err)
	}
	if err := e.Store.CompletedProcessInstances(func(uint64, *model.ProcessInstanceValue) error {
		completed++
		return nil
	}); err != nil {
		t.Fatalf("CompletedProcessInstances: %v", err)
	}
	return a, completed
}

// TestScenariosRunToCompletion drives every catalogue scenario through the
// level-1 harness and asserts each instance reaches an end state with nothing
// left active. This is the behavioral half of the anti-drift guarantee: the
// programmatic build actually executes, not merely compiles.
func TestScenariosRunToCompletion(t *testing.T) {
	const n = 5
	for _, s := range scenarios.All() {
		t.Run(s.Name, func(t *testing.T) {
			e, err := bench.Open(t.TempDir())
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer e.Close()

			cp, jobTypes := s.Compile(7)
			e.Proc.Deploy(cp)
			if err := e.RunInstances(cp.Key, jobTypes, n, s.StartVars); err != nil {
				t.Fatalf("RunInstances: %v", err)
			}

			active, completed := countInstances(t, e)
			if active != 0 {
				t.Errorf("%d instances still active, want 0", active)
			}
			if completed != n {
				t.Errorf("completed = %d, want %d", completed, n)
			}
		})
	}
}

// TestRecoveryReproducesState builds a durable log by running instances, then
// replays it into a fresh empty store and asserts the recovered state matches
// what was built live (invariant I4): same completed count, nothing active. This
// exercises the path the recovery benchmark times.
func TestRecoveryReproducesState(t *testing.T) {
	const n = 8
	for _, s := range scenarios.All() {
		t.Run(s.Name, func(t *testing.T) {
			logDir := t.TempDir()
			cp, jobTypes := s.Compile(7)

			events, err := bench.BuildLog(logDir, cp, jobTypes, n, s.StartVars)
			if err != nil {
				t.Fatalf("BuildLog: %v", err)
			}
			if events == 0 {
				t.Fatal("BuildLog reported 0 durable events")
			}

			// Replay the populated log into a brand-new store directory.
			storeDir := filepath.Join(t.TempDir(), "replayed")
			e, err := bench.Recover(logDir, storeDir, cp)
			if err != nil {
				t.Fatalf("Recover: %v", err)
			}
			defer e.Close()

			active, completed := countInstances(t, e)
			if active != 0 {
				t.Errorf("after replay: %d active, want 0", active)
			}
			if completed != n {
				t.Errorf("after replay: completed = %d, want %d", completed, n)
			}
		})
	}
}

// TestOpenErrorsSurface confirms Open fails cleanly when its directory cannot be
// used, so a broken benchmark environment reports an error rather than panicking
// mid-measurement.
func TestOpenErrorsSurface(t *testing.T) {
	// A regular file where the wal directory should be makes wal.Open fail.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "wal"), []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := bench.Open(dir); err == nil {
		t.Fatal("Open on a file-blocked wal dir: got nil error, want failure")
	}
}
