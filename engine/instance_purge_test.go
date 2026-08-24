package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// finishLinearInstance starts a linearProcess instance and completes its job, so it
// lands in history (PICompleted) with a populated per-instance timeline. Returns the
// instance key.
func finishLinearInstance(t *testing.T, h *harness, p *engine.Processor, jobType int32) uint64 {
	t.Helper()
	p.CreateInstance(defKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (start): %v", err)
	}
	job := singleActivatableJob(t, h.store, jobType)
	p.CompleteJob(job)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (complete): %v", err)
	}
	return model.NewKey(1, 1)
}

// TestPurgeDeletesFinishedInstanceHistory finishes an instance, confirms it and its
// per-instance history are present, purges it, and confirms every family is gone —
// while the per-definition counters (monotonic finished count) are untouched (ADR-0115).
func TestPurgeDeletesFinishedInstanceHistory(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType := linearProcess(t)
	clk := &fixedClock{t: 1_000}

	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	piKey := finishLinearInstance(t, h, p, jobType)

	// Present in history, with a recorded terminal position and a non-empty timeline.
	done := completedInstances(t, h.store)
	hist, ok := done[piKey]
	if !ok || hist.State != model.PICompleted {
		t.Fatalf("history[%d] = %+v (ok=%v), want a completed record", piKey, hist, ok)
	}
	if hist.CompletedPosition == 0 {
		t.Fatalf("completed record carries no terminal position, want > 0")
	}
	if len(elementSteps(t, h.store, piKey)) == 0 {
		t.Fatalf("expected a non-empty element-step timeline before purge")
	}
	if len(elementVisits(t, h.store, defKey)) == 0 {
		t.Fatalf("expected element-visit history before purge")
	}
	finishedBefore, err := h.store.DefCompletedCount(defKey)
	if err != nil {
		t.Fatalf("DefCompletedCount: %v", err)
	}

	// Purge it.
	p.PurgeInstance(piKey, &hist)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (purge): %v", err)
	}

	// Every per-instance family is gone.
	if got := completedInstances(t, h.store); len(got) != 0 {
		t.Fatalf("history after purge = %v, want empty", got)
	}
	if got := elementSteps(t, h.store, piKey); len(got) != 0 {
		t.Fatalf("element steps after purge = %v, want empty", got)
	}
	if got := elementVisits(t, h.store, defKey); len(got) != 0 {
		t.Fatalf("element visits after purge = %v, want empty", got)
	}
	// The monotonic finished count is not rewound by a purge (ADR-0083/0115).
	if got, _ := h.store.DefCompletedCount(defKey); got != finishedBefore {
		t.Fatalf("finished count changed by purge: %d, want %d", got, finishedBefore)
	}
}

// TestPurgeReplays proves the delete is durable: replaying the whole log into a fresh,
// empty store reproduces the purge (state == replay(log), I4/I6) — the history is gone,
// not rebuilt.
func TestPurgeReplays(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType := linearProcess(t)
	clk := &fixedClock{t: 1_000}

	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	piKey := finishLinearInstance(t, h, p, jobType)
	hist := completedInstances(t, h.store)[piKey]
	p.PurgeInstance(piKey, &hist)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (purge): %v", err)
	}

	// Rebuild state from genesis into a fresh store over the same log.
	fresh, err := state.Open(filepath.Join(t.TempDir(), "replay-state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer fresh.Close()
	p2 := engine.New(1, h.log, fresh, clk)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover (replay): %v", err)
	}
	if got := completedInstances(t, fresh); len(got) != 0 {
		t.Fatalf("replayed history = %v, want empty (purge reproduced)", got)
	}
	if got := elementSteps(t, fresh, piKey); len(got) != 0 {
		t.Fatalf("replayed element steps = %v, want empty", got)
	}
}

// TestPurgeMissingInstanceIsNoop confirms purging an instance that is not in history
// (never finished, or already purged) neither errors nor corrupts state.
func TestPurgeMissingInstanceIsNoop(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, _ := linearProcess(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	// Nothing finished yet: purge a made-up key.
	p.PurgeInstance(model.NewKey(1, 999), &model.ProcessInstanceValue{ProcessDefKey: defKey})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if got := completedInstances(t, h.store); len(got) != 0 {
		t.Fatalf("history = %v, want empty", got)
	}
}
