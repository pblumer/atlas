package job_test

import (
	"sync"
	"testing"

	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/state"
)

// TestClaimTakesARoundsWorthNotTheWholeBacklog: Claim runs on the single writer and
// reads a record per job, so an uncapped claim against a backlog held the writer for
// all of it — and every other instance, timer and health probe waited behind a burst
// that one round was never going to finish anyway
// (ADR-draft-bounded-job-polling).
//
// The cap costs a round, not a job: every caller drives in a loop until a claim
// comes back empty, which the second half of this test is.
func TestClaimTakesARoundsWorthNotTheWholeBacklog(t *testing.T) {
	p, store, jobType, defKey := setup(t)
	r := job.NewRunner(store, p)
	r.SetClaimBatch(4)
	// Handlers run concurrently, one goroutine per job, so the counter they share
	// needs a lock — the round's whole point is that they do not run in sequence.
	var mu sync.Mutex
	worked := 0
	r.Handle(jobType, func(state.Reader) job.Handler {
		return func(job.Job) error {
			mu.Lock()
			worked++
			mu.Unlock()
			return nil
		}
	})

	const backlog = 17
	for i := 0; i < backlog; i++ {
		p.CreateInstance(defKey)
	}
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// One round: claimed, worked and submitted. It takes the cap, not the backlog.
	n, err := r.PollOnce()
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if n != 4 {
		t.Fatalf("one round took %d of %d waiting jobs, want the cap of 4", n, backlog)
	}

	// Driving to idle works every one of the rest: the cap bounds a round, not the
	// backlog, and nothing is left behind.
	if err := r.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	mu.Lock()
	total := worked
	mu.Unlock()
	if total != backlog {
		t.Errorf("worked %d jobs, want all %d", total, backlog)
	}
	if left, err := r.Claim(); err != nil || len(left) != 0 {
		t.Errorf("after Drive: claimed %d more jobs (err=%v), want none", len(left), err)
	}
}

// TestEachTypeGetsAShareOfTheRound: ranging a map is randomly ordered, so leaving
// the split to chance would be fair *on average* — and "on average" is not what a
// job type flooded by its neighbour needs. Each served type gets an equal share of
// the round, so the quiet type is claimed in the same round as the loud one.
func TestEachTypeGetsAShareOfTheRound(t *testing.T) {
	p, store, jobType, defKey := setup(t)
	r := job.NewRunner(store, p)
	r.SetClaimBatch(4)
	// A second registered type with nothing waiting: the share it does not use is not
	// handed to the first, which is what makes the split a share rather than a race.
	r.Handle(jobType, func(state.Reader) job.Handler { return func(job.Job) error { return nil } })
	r.Handle(jobType+1000, func(state.Reader) job.Handler { return func(job.Job) error { return nil } })

	for i := 0; i < 10; i++ {
		p.CreateInstance(defKey)
	}
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	claimed, err := r.Claim()
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed %d jobs, want 2 — half of a round of four, because two types share it", len(claimed))
	}
}

// TestClaimBatchDefaults: zero or less restores the default rather than meaning
// "unbounded", which is the behaviour the cap exists to remove.
func TestClaimBatchDefaults(t *testing.T) {
	p, store, jobType, defKey := setup(t)
	r := job.NewRunner(store, p)
	r.SetClaimBatch(0)
	r.Handle(jobType, func(state.Reader) job.Handler { return func(job.Job) error { return nil } })

	for i := 0; i < 3; i++ {
		p.CreateInstance(defKey)
	}
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	claimed, err := r.Claim()
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 3 {
		t.Errorf("claimed %d of 3, want all of them under the default batch of %d", len(claimed), job.DefaultClaimBatch)
	}
}
