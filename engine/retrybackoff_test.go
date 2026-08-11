package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/engine"
)

// backoffJob deploys the linear service-task process on a fixed clock, parks a job, and
// returns the processor, the clock, the parked job key, and the job type — the setup a
// retry-backoff test needs to control time and fire the retry timer (ADR-0111).
func backoffJob(t *testing.T, h *harness, clk *fixedClock) (*engine.Processor, uint64, int32) {
	t.Helper()
	cp, jobType := linearProcess(t)
	p := engine.New(1, h.log, h.store, clk)
	p.SetJobNotifier(func(int32) {})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	return p, singleActivatableJob(t, h.store, jobType), jobType
}

// TestJobRetryBackoffHoldsThenReactivates: a failed job with retries left and a backoff is
// held off the activatable index until the backoff elapses, then a retry timer re-activates
// it — no incident, and it completes normally afterward (ADR-0111).
func TestJobRetryBackoffHoldsThenReactivates(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	clk := &fixedClock{t: 1_000}
	p, jobKey, jobType := backoffJob(t, h, clk)

	const backoff = int64(30e9)
	p.FailJob(jobKey, 2, "transient", backoff)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (fail): %v", err)
	}

	// During backoff: off the activatable index, no incident (retries remain).
	if got := activatableJobs(t, h.store, jobType); len(got) != 0 {
		t.Fatalf("during backoff: activatable=%v, want none (held off the index)", got)
	}
	if n := len(incidents(t, h.store)); n != 0 {
		t.Fatalf("backoff raised %d incidents, want 0", n)
	}

	// Ticking before the backoff elapses does not re-activate it.
	clk.t = 1_000 + backoff - 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers (early): %v", err)
	}
	if got := activatableJobs(t, h.store, jobType); len(got) != 0 {
		t.Fatalf("before backoff elapsed: activatable=%v, want none", got)
	}

	// After the backoff, the retry timer fires and returns the job to the index.
	clk.t = 1_000 + backoff + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers (due): %v", err)
	}
	if got := activatableJobs(t, h.store, jobType); len(got) != 1 || got[0] != jobKey {
		t.Fatalf("after backoff: activatable=%v, want the job %d re-activated", got, jobKey)
	}

	// The re-activated job completes and finishes the instance.
	p.CompleteJob(jobKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (complete): %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 0 {
		t.Fatalf("after completing the retried job: active=%d, want 0", pi)
	}
}

// TestJobRetryBackoffRecovers: a job mid-backoff survives a crash — the parked (off-index)
// job and its retry timer rebuild from the log, so the retry still fires after restart
// (ADR-0111, invariant I4/I6).
func TestJobRetryBackoffRecovers(t *testing.T) {
	dir := t.TempDir()
	clk := &fixedClock{t: 1_000}
	const backoff = int64(30e9)

	// First run: fail with a backoff, so the job is parked off the index with a retry timer.
	h1 := openHarness(t, dir)
	p1, jobKey, jobType := backoffJob(t, h1, clk)
	p1.FailJob(jobKey, 2, "transient", backoff)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	if got := activatableJobs(t, h1.store, jobType); len(got) != 0 {
		t.Fatalf("before crash: activatable=%v, want none (backing off)", got)
	}

	// Crash and recover: the parked job and its retry timer must rebuild from the log.
	h1.close(t)
	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clk)
	p2.SetJobNotifier(func(int32) {})
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	if got := activatableJobs(t, h2.store, jobType); len(got) != 0 {
		t.Fatalf("after replay: activatable=%v, want none (still backing off)", got)
	}

	// The recovered retry timer fires after the backoff and re-activates the job.
	clk.t = 1_000 + backoff + 1
	if err := p2.TickTimers(); err != nil {
		t.Fatalf("TickTimers 2: %v", err)
	}
	if got := activatableJobs(t, h2.store, jobType); len(got) != 1 || got[0] != jobKey {
		t.Fatalf("after recovered backoff: activatable=%v, want the job %d re-activated", got, jobKey)
	}
}
