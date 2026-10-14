package state

import (
	"runtime"
	"testing"

	"github.com/pblumer/atlas/model"
)

// TestDefaultTuningIsNotPebblesDefault: opening a store with no options must still
// raise the one setting whose Pebble default is wrong for an engine under write load —
// compaction concurrency. Pebble ships a single compaction goroutine, so on a store
// that is being written to continuously the L0 backlog grows until Pebble stalls
// writes, and a stalled write stalls the single-writer run loop with it.
func TestDefaultTuningIsNotPebblesDefault(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	tuning := s.Tuning()
	if tuning.MaxConcurrentCompactions < 2 {
		t.Errorf("MaxConcurrentCompactions = %d, want at least 2 — Pebble's default of 1 is the "+
			"setting that turns a compaction backlog into a write stall", tuning.MaxConcurrentCompactions)
	}
	if want := max(2, runtime.NumCPU()/2); tuning.MaxConcurrentCompactions != want {
		t.Errorf("MaxConcurrentCompactions = %d, want %d", tuning.MaxConcurrentCompactions, want)
	}
	// The two that cost resident memory stay at Pebble's default unless asked for: a
	// process may hold several stores (the Playground opens one per session), so the
	// server raises them for its own store rather than every store paying for it.
	if tuning.BlockCacheBytes != 0 {
		t.Errorf("BlockCacheBytes = %d by default, want 0 (Pebble's own default)", tuning.BlockCacheBytes)
	}
	if tuning.MemtableBytes != 0 {
		t.Errorf("MemtableBytes = %d by default, want 0 (Pebble's own default)", tuning.MemtableBytes)
	}
}

// TestTuningOptionsReachPebble: the options a caller sets are the options the store
// runs with. The translation is the only place this can go wrong, and silently — a
// mis-set cache reads as "the tuning did not help" rather than as a bug.
func TestTuningOptionsReachPebble(t *testing.T) {
	s, err := Open(t.TempDir(),
		WithBlockCacheBytes(32<<20),
		WithMemtableBytes(8<<20),
		WithMaxConcurrentCompactions(3),
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	got := s.Tuning()
	want := Tuning{BlockCacheBytes: 32 << 20, MemtableBytes: 8 << 20, MaxConcurrentCompactions: 3}
	if got != want {
		t.Errorf("Tuning = %+v, want %+v", got, want)
	}

	// And it is still a working store: tuning must not change what it stores.
	tx := s.NewTransaction()
	if err := tx.PutProcessInstance(1001, &model.ProcessInstanceValue{ProcessDefKey: 7}); err != nil {
		t.Fatalf("PutProcessInstance: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()
	pi, ok, err := s.ProcessInstance(1001)
	if err != nil || !ok || pi.ProcessDefKey != 7 {
		t.Fatalf("ProcessInstance = %+v, %v, %v; want the value just written", pi, ok, err)
	}
}

// TestTuningRejectsNonsense: a zero or negative value means "leave Pebble's default",
// not "set it to zero" — a store opened with a zero-byte cache would be unusable, and
// an operator passing 0 to a flag means "unset".
func TestTuningRejectsNonsense(t *testing.T) {
	s, err := Open(t.TempDir(),
		WithBlockCacheBytes(0),
		WithMemtableBytes(-1),
		WithMaxConcurrentCompactions(0),
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	got := s.Tuning()
	if got.BlockCacheBytes != 0 || got.MemtableBytes != 0 {
		t.Errorf("Tuning = %+v, want the memory settings left at Pebble's default", got)
	}
	if got.MaxConcurrentCompactions < 2 {
		t.Errorf("MaxConcurrentCompactions = %d, want the package default rather than 0",
			got.MaxConcurrentCompactions)
	}
}
