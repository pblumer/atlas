package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/engine"
)

// ADR-0007 (programme F): an external worker leases a job.
//
// Until now a job sat on the activatable index until someone completed it, and nothing
// recorded that a worker had taken it. Two workers pulling the same type both saw the
// same job, and a worker that crashed mid-job left it looking available while it was
// arguably in flight — the engine could not tell the difference, because there was
// nothing to tell.
//
// A lease is that difference: activating a job takes it off the index for a bounded time
// and records who holds it. The bound is what makes it safe — a crashed worker's job
// comes back on its own, with no operator involved.
//
// The mechanism is deliberately the one ADR-0111 already proved for retry backoff: hold
// the job off the index, arm a timer, and let the timer put it back. The interaction
// between the two is the subtle part, and it has a test of its own below.

// leasedJob deploys the linear service-task process on a fixed clock and parks a job.
func leasedJob(t *testing.T, h *harness, clk *fixedClock) (*engine.Processor, uint64, int32) {
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

// TestActivatingAJobLeasesItOffTheIndex is the core of the protocol: while a worker holds
// a job, no other worker is offered it, and the engine records who holds it and until
// when.
func TestActivatingAJobLeasesItOffTheIndex(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	clk := &fixedClock{t: 1_000}
	p, jobKey, jobType := leasedJob(t, h, clk)

	const lease = int64(60e9)
	p.ActivateJob(jobKey, "worker-1", lease)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (activate): %v", err)
	}

	if got := activatableJobs(t, h.store, jobType); len(got) != 0 {
		t.Fatalf("activatable=%v after activation, want none — a second worker would get the same job", got)
	}
	jv, ok, err := h.store.GetJob(jobKey)
	if err != nil || !ok {
		t.Fatalf("GetJob: %v, ok=%v — the job must still exist, just held", err, ok)
	}
	if jv.Assignee != "worker-1" {
		t.Errorf("Assignee = %q, want worker-1", jv.Assignee)
	}
	if want := int64(1_000) + lease; jv.LeaseExpiresAt != want {
		t.Errorf("LeaseExpiresAt = %d, want %d (the clock read at command time, frozen into the event)",
			jv.LeaseExpiresAt, want)
	}
}

// TestALeaseExpiryReturnsTheJobToWorkers is what makes a lease safe to hand out. A worker
// that crashes, hangs, or loses its network never reports anything; if the job did not
// come back on its own, it would sit held forever and only an operator poking at state
// would free it.
func TestALeaseExpiryReturnsTheJobToWorkers(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	clk := &fixedClock{t: 1_000}
	p, jobKey, jobType := leasedJob(t, h, clk)

	const lease = int64(60e9)
	p.ActivateJob(jobKey, "worker-1", lease)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (activate): %v", err)
	}

	// Before the lease elapses the job stays held: a worker that is merely slow must not
	// have its work handed to someone else.
	clk.t = 1_000 + lease - 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers (early): %v", err)
	}
	if got := activatableJobs(t, h.store, jobType); len(got) != 0 {
		t.Fatalf("before the lease elapsed: activatable=%v, want none", got)
	}

	clk.t = 1_000 + lease + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers (due): %v", err)
	}
	if got := activatableJobs(t, h.store, jobType); len(got) != 1 || got[0] != jobKey {
		t.Fatalf("after the lease elapsed: activatable=%v, want the job %d offered again", got, jobKey)
	}
	// The lease is cleared, not merely elapsed: a stale assignee on an available job
	// would read as "someone is working on this" for the rest of its life.
	jv, ok, err := h.store.GetJob(jobKey)
	if err != nil || !ok {
		t.Fatalf("GetJob: %v ok=%v", err, ok)
	}
	if jv.Assignee != "" || jv.LeaseExpiresAt != 0 {
		t.Errorf("after expiry: Assignee=%q LeaseExpiresAt=%d, want both cleared", jv.Assignee, jv.LeaseExpiresAt)
	}
}

// TestALeasedJobCompletesNormally: the lease is a loan, not a new lifecycle. Completing
// a leased job finishes the instance exactly as completing an unleased one does.
func TestALeasedJobCompletesNormally(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	clk := &fixedClock{t: 1_000}
	p, jobKey, _ := leasedJob(t, h, clk)

	p.ActivateJob(jobKey, "worker-1", 60e9)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (activate): %v", err)
	}
	p.CompleteJob(jobKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (complete): %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 0 {
		t.Fatalf("after completing a leased job: active=%d, want 0", pi)
	}
}

// TestAnExpiredLeaseOnACompletedJobIsANoOp: the lease timer outlives the job whenever a
// worker finishes before its deadline, which is the normal case. It must find nothing and
// retire quietly rather than resurrecting anything.
func TestAnExpiredLeaseOnACompletedJobIsANoOp(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	clk := &fixedClock{t: 1_000}
	p, jobKey, jobType := leasedJob(t, h, clk)

	const lease = int64(60e9)
	p.ActivateJob(jobKey, "worker-1", lease)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (activate): %v", err)
	}
	p.CompleteJob(jobKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (complete): %v", err)
	}

	clk.t = 1_000 + lease + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if got := activatableJobs(t, h.store, jobType); len(got) != 0 {
		t.Fatalf("a completed job came back from its lease timer: activatable=%v", got)
	}
	if _, ok, err := h.store.GetJob(jobKey); err != nil || ok {
		t.Fatalf("GetJob after completion: ok=%v err=%v, want the job gone", ok, err)
	}
}

// TestALeaseExpiryDoesNotJumpARetryBackoff is the interaction worth the most care. Both a
// lease and a retry backoff hold a job off the index, and both arm a timer to put it
// back. A worker can lease a job and then fail it with a backoff, leaving two timers on
// one job — and the lease timer, which fires first, must not hand the job out early and
// defeat the backoff the worker asked for.
//
// The composition is what makes this safe rather than a special case: each timer clears
// only its own hold, and the job returns to the index when *nothing* holds it.
func TestALeaseExpiryDoesNotJumpARetryBackoff(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	clk := &fixedClock{t: 1_000}
	p, jobKey, jobType := leasedJob(t, h, clk)

	const lease = int64(30e9)
	const backoff = int64(120e9) // deliberately outlasts the lease
	p.ActivateJob(jobKey, "worker-1", lease)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (activate): %v", err)
	}
	p.FailJob(jobKey, 2, "transient", backoff)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (fail): %v", err)
	}

	// The lease elapses first. The backoff still holds the job.
	clk.t = 1_000 + lease + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers (lease due): %v", err)
	}
	if got := activatableJobs(t, h.store, jobType); len(got) != 0 {
		t.Fatalf("the lease expiry jumped the retry backoff: activatable=%v, want none until %d",
			got, 1_000+backoff)
	}

	// Only when the backoff elapses too does the job come back.
	clk.t = 1_000 + backoff + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers (backoff due): %v", err)
	}
	if got := activatableJobs(t, h.store, jobType); len(got) != 1 || got[0] != jobKey {
		t.Fatalf("after the backoff: activatable=%v, want the job %d offered again", got, jobKey)
	}
}

// TestASecondWorkerCannotTakeAHeldJob. The activatable index is what offers a job, and a
// held job is not on it, so this only happens on a racing or replayed command — but the
// engine is where the guarantee has to live, because the index is an optimisation and the
// job record is the truth. Losing it would let a late activation silently move a job from
// the worker doing it to one that is not.
func TestASecondWorkerCannotTakeAHeldJob(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	clk := &fixedClock{t: 1_000}
	p, jobKey, _ := leasedJob(t, h, clk)

	const lease = int64(60e9)
	p.ActivateJob(jobKey, "worker-1", lease)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (first): %v", err)
	}

	// A second worker tries, with a lease that would run much longer.
	clk.t = 2_000
	p.ActivateJob(jobKey, "worker-2", 10*lease)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (second): %v", err)
	}

	jv, ok, err := h.store.GetJob(jobKey)
	if err != nil || !ok {
		t.Fatalf("GetJob: %v ok=%v", err, ok)
	}
	if jv.Assignee != "worker-1" {
		t.Errorf("Assignee = %q, want worker-1 — the holder was displaced", jv.Assignee)
	}
	if want := int64(1_000) + lease; jv.LeaseExpiresAt != want {
		t.Errorf("LeaseExpiresAt = %d, want %d — the second activation extended the lease",
			jv.LeaseExpiresAt, want)
	}
}

// TestActivatingAJobThatIsGoneIsANoOp: a worker's activation races every other way a job
// can end — a boundary event, a cancellation, another worker. Losing that race is
// ordinary, not an error.
func TestActivatingAJobThatIsGoneIsANoOp(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	clk := &fixedClock{t: 1_000}
	p, jobKey, _ := leasedJob(t, h, clk)

	p.CompleteJob(jobKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (complete): %v", err)
	}
	p.ActivateJob(jobKey, "worker-1", 60e9)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (activate a gone job): %v", err)
	}
	if _, ok, err := h.store.GetJob(jobKey); err != nil || ok {
		t.Fatalf("activating a completed job recreated it: ok=%v err=%v", ok, err)
	}
}

// TestALeaseSurvivesARestart is the recovery test this slice owes. A lease is durable
// state: it is written by applyToState from the JobActivated event, so a replay must land
// on exactly the same held job and the same pending expiry — otherwise a restart would
// either hand out work a worker is still doing, or hold work forever with no timer left
// to free it.
func TestALeaseSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	clk := &fixedClock{t: 1_000}
	const lease = int64(60e9)

	h1 := openHarness(t, dir)
	p1, jobKey, jobType := leasedJob(t, h1, clk)
	p1.ActivateJob(jobKey, "worker-1", lease)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (activate): %v", err)
	}
	h1.close(t)

	// Restart over the same directory: the log alone rebuilds the lease.
	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clk)
	p2.SetJobNotifier(func(int32) {})
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	if got := activatableJobs(t, h2.store, jobType); len(got) != 0 {
		t.Fatalf("after restart: activatable=%v, want none — the lease must survive", got)
	}
	jv, ok, err := h2.store.GetJob(jobKey)
	if err != nil || !ok {
		t.Fatalf("GetJob after restart: %v ok=%v", err, ok)
	}
	if jv.Assignee != "worker-1" || jv.LeaseExpiresAt != 1_000+lease {
		t.Errorf("after restart: Assignee=%q LeaseExpiresAt=%d, want worker-1 and %d",
			jv.Assignee, jv.LeaseExpiresAt, 1_000+lease)
	}

	// And the expiry still fires, so a worker that died with the old process does not
	// hold the job forever.
	clk.t = 1_000 + lease + 1
	if err := p2.TickTimers(); err != nil {
		t.Fatalf("TickTimers after restart: %v", err)
	}
	if got := activatableJobs(t, h2.store, jobType); len(got) != 1 || got[0] != jobKey {
		t.Fatalf("after restart and expiry: activatable=%v, want the job %d offered again", got, jobKey)
	}
}
