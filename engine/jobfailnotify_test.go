package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/engine"
)

// A job a worker fails with retries left goes straight back on the activatable index,
// and the workers long-polling its type are told, the way they are for every other
// path that puts a job there. Without the notification a waiting worker slept out its
// whole poll while the job sat on the index.

// failedJobNotifications leases a job, fails it with the given retries and backoff, and
// returns the job types notified by the fail alone.
func failedJobNotifications(t *testing.T, retries int32, backoff int64) ([]int32, int32) {
	t.Helper()
	h := openHarness(t, t.TempDir())
	t.Cleanup(func() { h.close(t) })
	cp, jobType := linearProcess(t)
	var notified []int32
	p := engine.New(1, h.log, h.store, &fixedClock{t: 1_000})
	p.SetJobNotifier(func(jt int32) { notified = append(notified, jt) })
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	jobKey := singleActivatableJob(t, h.store, jobType)
	p.ActivateJob(jobKey, "worker-1", 60e9)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (activate): %v", err)
	}

	notified = nil
	p.FailJob(jobKey, retries, "transient", backoff)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (fail): %v", err)
	}
	return notified, jobType
}

func TestFailingAJobWithRetriesLeftWakesItsWorkers(t *testing.T) {
	notified, jobType := failedJobNotifications(t, 2, 0)
	if len(notified) != 1 || notified[0] != jobType {
		t.Fatalf("notified = %v after a retryable fail, want [%d] — the job is on the index and nobody was told", notified, jobType)
	}
}

// The two fails that leave the job off the index say nothing: a backoff notifies when
// its timer puts the job back, and an exhausted job waits for an operator.
func TestAFailThatHoldsTheJobDoesNotWakeItsWorkers(t *testing.T) {
	if notified, _ := failedJobNotifications(t, 2, 30e9); len(notified) != 0 {
		t.Errorf("notified = %v after a fail with a backoff, want none until the backoff elapses", notified)
	}
	if notified, _ := failedJobNotifications(t, 0, 0); len(notified) != 0 {
		t.Errorf("notified = %v after an exhausting fail, want none — the job waits for an operator", notified)
	}
}
