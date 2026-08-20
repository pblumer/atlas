package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
)

// jobFenceProcess is one service task a job parks on, so a lease can be taken.
func jobFenceProcess(t *testing.T, h *harness, clk engine.Clock) (*engine.Processor, uint64) {
	t.Helper()
	b := compiler.NewBuilder(9, "fence", 1)
	start := b.AddStartEvent()
	st := b.AddServiceTask("send-email", 3)
	end := b.AddEndEvent()
	b.Connect(start, st)
	b.Connect(st, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	var jobKey uint64
	if err := h.store.AllActivatableJobs(func(k uint64) error { jobKey = k; return nil }); err != nil {
		t.Fatalf("AllActivatableJobs: %v", err)
	}
	if jobKey == 0 {
		t.Fatal("no job parked")
	}
	return p, jobKey
}

// Every lease moves the epoch on, so two holders of the same job — which is what a
// lease expiring under a worker that is still running produces — never present the
// same token. Assignee cannot do this: two instances of one deployment share a name.
func TestEachLeaseAdvancesTheEpoch(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	clk := &fixedClock{t: 1}
	p, jobKey := jobFenceProcess(t, h, clk)

	epochOf := func() uint64 {
		t.Helper()
		jv, ok, err := h.store.GetJob(jobKey)
		if err != nil || !ok {
			t.Fatalf("GetJob: %v ok=%v", err, ok)
		}
		return jv.LeaseEpoch
	}
	if got := epochOf(); got != 0 {
		t.Errorf("epoch = %d before any lease, want 0", got)
	}

	var epochs []uint64
	for range 3 {
		p.ActivateJob(jobKey, "mailer", 1000)
		if err := p.RunUntilIdle(); err != nil {
			t.Fatalf("RunUntilIdle: %v", err)
		}
		epochs = append(epochs, epochOf())
		// Let the lease elapse, which is exactly how a job comes back on offer while
		// the worker that held it may still be running — the case fencing exists for.
		clk.t += 5000
		if err := p.TickTimers(); err != nil {
			t.Fatalf("TickTimers: %v", err)
		}
		if err := p.RunUntilIdle(); err != nil {
			t.Fatalf("RunUntilIdle: %v", err)
		}
	}
	for i, e := range epochs {
		if e != uint64(i+1) {
			t.Errorf("lease %d got epoch %d, want %d", i+1, e, i+1)
		}
	}
}

// The epoch is frozen into the JobActivated event, not recomputed: state rebuilt by
// replaying the log must carry the identical token, or a worker holding a lease
// across a restart would be fenced out of its own job.
func TestLeaseEpochSurvivesReplay(t *testing.T) {
	dir := t.TempDir()
	h := openHarness(t, dir)
	p, jobKey := jobFenceProcess(t, h, &manualClock{})
	p.ActivateJob(jobKey, "mailer", 1000)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	live, ok, err := h.store.GetJob(jobKey)
	if err != nil || !ok {
		t.Fatalf("GetJob: %v ok=%v", err, ok)
	}
	h.close(t)

	replayed := openHarness(t, dir)
	defer replayed.close(t)
	p2 := engine.New(1, replayed.log, replayed.store, &manualClock{})
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	after, ok, err := replayed.store.GetJob(jobKey)
	if err != nil || !ok {
		t.Fatalf("GetJob after replay: %v ok=%v", err, ok)
	}
	if after.LeaseEpoch != live.LeaseEpoch {
		t.Errorf("epoch after replay = %d, want the recorded %d", after.LeaseEpoch, live.LeaseEpoch)
	}
	if after.Assignee != live.Assignee || after.LeaseExpiresAt != live.LeaseExpiresAt {
		t.Errorf("lease after replay = %+v, want %+v", after, live)
	}
}
