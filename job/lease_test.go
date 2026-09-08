package job_test

import (
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/state"
)

// TestTwoRoundsNeverClaimTheSameJob is what makes the server's mutex narrowable.
// The runner used to dispatch whatever was activatable and take no claim on it, so
// two callers driving at once were handed the same job and worked it twice — and the
// only thing standing between that and production was a mutex held across every
// handler's outbound call (ADR-0274).
//
// A claim now leases: the activation takes the job off the activatable index before
// Claim returns, so the second claim cannot see it.
func TestTwoRoundsNeverClaimTheSameJob(t *testing.T) {
	p, store, jobType, defKey := setup(t)
	r := job.NewRunner(store, p)
	r.Handle(jobType, func(state.Reader) job.Handler { return func(job.Job) error { return nil } })

	const backlog = 6
	for i := 0; i < backlog; i++ {
		p.CreateInstance(defKey)
	}
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	first, err := r.Claim()
	if err != nil {
		t.Fatalf("first Claim: %v", err)
	}
	if len(first) != backlog {
		t.Fatalf("first claim took %d of %d, want all of them", len(first), backlog)
	}
	second, err := r.Claim()
	if err != nil {
		t.Fatalf("second Claim: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("a second claim was handed %d job(s) the first already holds", len(second))
	}
	// And every claimed job carries the epoch it was leased under, which is what the
	// outcome is fenced against.
	for _, j := range first {
		if j.LeaseEpoch == 0 {
			t.Errorf("job %d was claimed without a lease epoch", j.Key)
		}
	}
}

// TestConcurrentDriversWorkEachJobOnce is the same guarantee under the concurrency
// the narrowed mutex allows. Two drivers race over one backlog; each job must be
// handled exactly once between them.
func TestConcurrentDriversWorkEachJobOnce(t *testing.T) {
	p, store, jobType, defKey := setup(t)
	r := job.NewRunner(store, p)

	var mu sync.Mutex
	seen := map[uint64]int{}
	r.Handle(jobType, func(state.Reader) job.Handler {
		return func(j job.Job) error {
			mu.Lock()
			seen[j.Key]++
			mu.Unlock()
			return nil
		}
	})

	const backlog = 12
	for i := 0; i < backlog; i++ {
		p.CreateInstance(defKey)
	}
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// Claim twice before working either round: this is the interleaving the mutex
	// used to make impossible, done deterministically rather than by racing.
	roundA, err := r.Claim()
	if err != nil {
		t.Fatalf("Claim A: %v", err)
	}
	roundB, err := r.Claim()
	if err != nil {
		t.Fatalf("Claim B: %v", err)
	}
	r.Submit(r.Work(roundA, store))
	r.Submit(r.Work(roundB, store))

	if len(seen) != backlog {
		t.Fatalf("handled %d distinct jobs, want all %d", len(seen), backlog)
	}
	for key, n := range seen {
		if n != 1 {
			t.Errorf("job %d was handled %d times, want once", key, n)
		}
	}
}

// TestAnOutcomeFromALostLeaseIsDropped: a lease is a bound, not a lock. A handler
// that outlives its lease has had the job handed on, and applying its outcome then
// would complete work somebody else is now doing — the double execution the fence
// exists to prevent. The epoch is the token; a job re-leased since carries a
// different one.
func TestAnOutcomeFromALostLeaseIsDropped(t *testing.T) {
	p, store, jobType, defKey := setup(t)
	r := job.NewRunner(store, p)
	r.Handle(jobType, func(state.Reader) job.Handler { return func(job.Job) error { return nil } })

	p.CreateInstance(defKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	claimed, err := r.Claim()
	if err != nil || len(claimed) != 1 {
		t.Fatalf("Claim: %v (%d jobs)", err, len(claimed))
	}

	// The round reports under an epoch the job has moved past.
	stale := claimed[0]
	stale.LeaseEpoch++
	r.Submit([]job.Outcome{{Job: stale}})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if _, ok, err := store.GetJob(stale.Key); err != nil || !ok {
		t.Fatalf("a stale outcome completed the job: GetJob ok=%v err=%v", ok, err)
	}

	// The holder's own report still lands.
	r.Submit([]job.Outcome{{Job: claimed[0]}})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if _, ok, err := store.GetJob(claimed[0].Key); err != nil || ok {
		t.Fatalf("the lease holder's completion was dropped: GetJob ok=%v err=%v", ok, err)
	}
}

// TestLeaseDefaultsWhenUnset: zero or less means [job.DefaultLease]. A lease of
// zero would expire the instant it was taken, which is a claim that claims nothing.
func TestLeaseDefaultsWhenUnset(t *testing.T) {
	p, store, jobType, defKey := setup(t)
	r := job.NewRunner(store, p)
	r.SetLease(0)
	r.Handle(jobType, func(state.Reader) job.Handler { return func(job.Job) error { return nil } })
	p.CreateInstance(defKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	claimed, err := r.Claim()
	if err != nil || len(claimed) != 1 {
		t.Fatalf("Claim with the default lease: %v (%d jobs)", err, len(claimed))
	}

	// An explicit lease is used as given, and is what the job records.
	p2, store2, jobType2, defKey2 := setup(t)
	r2 := job.NewRunner(store2, p2)
	r2.SetLease(int64(90 * time.Second))
	r2.Handle(jobType2, func(state.Reader) job.Handler { return func(job.Job) error { return nil } })
	p2.CreateInstance(defKey2)
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	held, err := r2.Claim()
	if err != nil || len(held) != 1 {
		t.Fatalf("Claim with an explicit lease: %v (%d jobs)", err, len(held))
	}
	jv, ok, err := store2.GetJob(held[0].Key)
	if err != nil || !ok {
		t.Fatalf("GetJob: %v (ok=%v)", err, ok)
	}
	if jv.LeaseExpiresAt == 0 || jv.Assignee != job.InProcessWorker {
		t.Errorf("job = assignee %q lease %d, want it held by %q", jv.Assignee, jv.LeaseExpiresAt, job.InProcessWorker)
	}
}
