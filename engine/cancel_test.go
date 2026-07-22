package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// TestCancelInstanceTerminates cancels an instance parked at a service task: the
// instance and its element vanish from active state and it is recorded in history
// as terminated. Cancelling again is a no-op.
func TestCancelInstanceTerminates(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, _ := linearProcess(t) // Start → ServiceTask → End

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("after start: process=%d element=%d, want 1 and 1 (parked at task)", pi, ei)
	}

	piKey := model.NewKey(1, 1)
	p.CancelInstance(piKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (cancel): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after cancel: process=%d element=%d, want 0 and 0", pi, ei)
	}
	done := completedInstances(t, h.store)
	if v, ok := done[piKey]; !ok || v.State != model.PITerminated {
		t.Fatalf("history[%d] = %+v (ok=%v), want a terminated record", piKey, v, ok)
	}

	// Cancelling an already-gone instance does nothing (no panic, still 0/0).
	p.CancelInstance(piKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (double cancel): %v", err)
	}
	if pi, _ := counts(t, h.store); pi != 0 {
		t.Fatalf("after double cancel: process=%d, want 0", pi)
	}
}

// TestCancelInstanceRecovers proves cancellation is durable: replaying the log
// into a fresh store rebuilds the same terminated history (invariant I4).
func TestCancelInstanceRecovers(t *testing.T) {
	dir := t.TempDir()
	cp, _ := linearProcess(t)
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	piKey := model.NewKey(1, 1)
	p1.CancelInstance(piKey)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (cancel): %v", err)
	}
	h1.close(t)

	// Replay into a fresh store.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() { _ = store2.Close(); _ = log2.Close() }()
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	if pi, ei := counts(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after replay: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if v, ok := completedInstances(t, store2)[piKey]; !ok || v.State != model.PITerminated {
		t.Fatalf("replayed history[%d] = %+v (ok=%v), want terminated", piKey, v, ok)
	}
}
