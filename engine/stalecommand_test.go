package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// The engine accepts commands that name work which no longer exists. That is not
// a defensive nicety — it is the normal consequence of how commands reach it. A
// worker holding a job key can call back after its instance was cancelled; an
// operator can resolve an incident from a page rendered before the run finished;
// a retry-backoff timer keeps ticking after the job it would retry is gone. The
// command was valid when it was sent and is stale by the time the single-writer
// loop reaches it.
//
// Each of these must be a no-op: the loop drives every partition, so a command
// that panicked or wedged on a missing record would take down every other
// instance in it. The tests below drive each such command with work that is
// genuinely gone and assert the processor stays healthy and keeps running.

// TestStaleJobCommandsAreNoOps covers the job commands an external worker or UI
// can send with a key the engine no longer knows: a key that never existed, and a
// key whose job has already completed. Neither may fail the processor.
func TestStaleJobCommandsAreNoOps(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp, jobType := linearProcess(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.SetJobNotifier(func(int32) {})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	jobKey := singleActivatableJob(t, h.store, jobType)

	// Complete the job normally, so the key below is one that was real and is now
	// gone — the exact state a worker that timed out and retried would hold.
	p.CompleteJob(jobKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (complete): %v", err)
	}
	if n := activeProcs(t, h.store); n != 0 {
		t.Fatalf("active instances = %d, want the run finished", n)
	}

	const neverExisted = uint64(999_999)
	for _, key := range []uint64{jobKey, neverExisted} {
		p.AssignJob(key, "anja")
		p.ThrowJobError(key, "BOOM")
		p.CompleteJob(key)
		p.FailJob(key, 1, "late", 0)
	}
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (stale commands): %v", err)
	}

	// Nothing was resurrected, and no incident was invented for work that is gone.
	if n := activeProcs(t, h.store); n != 0 {
		t.Errorf("active instances = %d after stale commands, want 0", n)
	}
	if got := incidents(t, h.store); len(got) != 0 {
		t.Errorf("stale commands raised %d incidents, want 0", len(got))
	}
	if got := activatableJobs(t, h.store, jobType); len(got) != 0 {
		t.Errorf("stale commands put %v back on the activatable index, want none", got)
	}

	// The processor is still usable afterwards: a fresh instance runs to completion.
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (fresh instance): %v", err)
	}
	next := activatableJobs(t, h.store, jobType)
	if len(next) != 1 {
		t.Fatalf("fresh instance jobs = %v, want exactly one", next)
	}
	p.CompleteJob(next[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (fresh complete): %v", err)
	}
	if n := activeProcs(t, h.store); n != 0 {
		t.Errorf("active instances = %d, want the fresh run finished too", n)
	}
}

// TestRetryBackoffTimerOutlivesItsJob covers the retry-backoff timer firing after
// the job it would retry is gone (ADR-0111). Cancelling an instance does not chase
// down the timers it left armed — they are self-retiring, which only works if
// firing one whose job has vanished does nothing.
func TestRetryBackoffTimerOutlivesItsJob(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	clk := &fixedClock{t: 1_000}
	p, jobKey, jobType := backoffJob(t, h, clk)

	// Fail with a backoff, so a retry timer is armed for this job.
	const backoff = int64(30e9)
	p.FailJob(jobKey, 2, "transient", backoff)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (fail): %v", err)
	}

	// Cancel the instance while the backoff is still running: the job goes with it,
	// the timer does not.
	p.CancelInstance(model.NewKey(1, 1))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (cancel): %v", err)
	}
	if n := activeProcs(t, h.store); n != 0 {
		t.Fatalf("active instances = %d after cancel, want 0", n)
	}

	// The backoff elapses and the orphaned timer fires.
	clk.t = 1_000 + backoff + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers (due): %v", err)
	}

	// It retired quietly: no job came back, and the cancelled instance stayed dead.
	if got := activatableJobs(t, h.store, jobType); len(got) != 0 {
		t.Errorf("the orphaned retry timer re-activated %v, want nothing", got)
	}
	if n := activeProcs(t, h.store); n != 0 {
		t.Errorf("active instances = %d, want the cancelled instance to stay cancelled", n)
	}
	if got := incidents(t, h.store); len(got) != 0 {
		t.Errorf("the orphaned retry timer raised %d incidents, want 0", len(got))
	}
}

// TestResolveTimerIncidentAfterCancel covers the resolve an operator sends from a
// page rendered before someone else cancelled the instance (ADR-0061/0064).
//
// It also pins the invariant that makes that safe: terminating an element clears
// the incident it carried, so by the time the resolve is processed there is no
// incident left to act on and it stops at the lookup. That ordering is what keeps
// a resolve from re-arming a timer on a terminated element — the deeper no-op in
// rearmTimerElement is unreachable precisely because of it, not because nobody
// has managed to hit it.
func TestResolveTimerIncidentAfterCancel(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	// A catch whose FEEL duration reads a variable the instance never gets, so
	// arming it fails and parks the element behind a job-less incident.
	cp := feelCatchProcess(t, 921, compiler.TimerFeelDuration, "timeout")
	p := engine.New(1, h.log, h.store, &fixedClock{t: 1_000})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	inc := incidents(t, h.store)
	if len(inc) != 1 {
		t.Fatalf("incidents = %d, want 1 (the unresolvable schedule)", len(inc))
	}
	var elementKey uint64
	for k, v := range inc {
		elementKey = k
		if v.JobKey != 0 {
			t.Fatalf("incident carries job %d; this test needs the job-less timer path", v.JobKey)
		}
	}

	// The instance is cancelled out from under the incident.
	p.CancelInstance(model.NewKey(1, 1))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (cancel): %v", err)
	}
	if n := activeProcs(t, h.store); n != 0 {
		t.Fatalf("active instances = %d after cancel, want 0", n)
	}

	// The cancel took the incident with it, so the resolve has nothing to act on.
	if got := incidents(t, h.store); len(got) != 0 {
		t.Fatalf("incidents after cancel = %d, want 0 — terminating an element clears its incident", len(got))
	}

	// Now the resolve lands. It must not resurrect the element or fail the loop.
	p.ResolveIncident(elementKey, 3)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (resolve): %v", err)
	}
	if n := activeProcs(t, h.store); n != 0 {
		t.Errorf("active instances = %d, want the cancelled instance to stay cancelled", n)
	}
	if got := incidents(t, h.store); len(got) != 0 {
		t.Errorf("incidents after resolve = %d, want 0", len(got))
	}
}
