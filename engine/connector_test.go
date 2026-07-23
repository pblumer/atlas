package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
)

// TestConnectorTaskJobLifecycle drives a connector task through the engine: on
// activation it creates a job (carrying the reserved connector job type) and
// waits, and completing that job — the connector worker's job in production —
// drives the token onward, exactly like a service task (ADR-0036). It exercises
// the behavior in the engine package itself, without the clio worker.
func TestConnectorTaskJobLifecycle(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(7, "conn", 1)
	start := b.AddStartEvent()
	ct := b.AddClioWriteTask("orders-clio", "orders/new", "OrderPlaced", 3)
	end := b.AddEndEvent()
	b.Connect(start, ct)
	b.Connect(ct, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// The connector task created a job and parked on it.
	if pi := activeProcs(t, h.store); pi != 1 {
		t.Fatalf("after activation: active=%d, want 1 (parked on the connector job)", pi)
	}

	jobType := cp.ConnectorTask(cp.Node(ct).Detail).JobType
	jobKey := singleActivatableJob(t, h.store, jobType)

	// Completing the job (what the connector worker does on success) finishes it.
	p.CompleteJob(jobKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after complete): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after job completion: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

func singleActivatableJob(t *testing.T, s *state.Store, jobType int32) uint64 {
	t.Helper()
	var keys []uint64
	if err := s.ActivatableJobs(jobType, func(k uint64) error {
		keys = append(keys, k)
		return nil
	}); err != nil {
		t.Fatalf("ActivatableJobs: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("activatable jobs = %d, want exactly 1", len(keys))
	}
	return keys[0]
}
