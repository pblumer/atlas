package job_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/state"
)

// The dispatch gate (ADR-0340). The runner knows nothing about Workers or targets —
// it asks a predicate whether a candidate may go out, and tells the same predicate how
// the ones that did ended. Everything about *why* a job is held lives on the other side
// of this interface.

// heldGate holds the job keys it is given and records every outcome reported to it.
type heldGate struct {
	mu       sync.Mutex
	holds    map[int32]bool
	deny     map[uint64]bool
	asked    []uint64
	failed   []uint64
	complete []uint64
}

func newHeldGate() *heldGate {
	return &heldGate{holds: map[int32]bool{}, deny: map[uint64]bool{}}
}

func (g *heldGate) Holding(jobType int32) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.holds[jobType]
}

func (g *heldGate) Allow(_ int32, jobKey uint64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.asked = append(g.asked, jobKey)
	return !g.deny[jobKey]
}

func (g *heldGate) Failed(_ int32, jobKey uint64, _ string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failed = append(g.failed, jobKey)
}

func (g *heldGate) Succeeded(_ int32, jobKey uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.complete = append(g.complete, jobKey)
}

func (g *heldGate) counts() (asked, failed, complete int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.asked), len(g.failed), len(g.complete)
}

// backlog starts n instances and returns their activatable job keys in scan order.
func backlog(t *testing.T, p *engine.Processor, store *state.Store, jobType int32, defKey uint64, n int) []uint64 {
	t.Helper()
	for i := 0; i < n; i++ {
		p.CreateInstance(defKey)
	}
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	var keys []uint64
	if err := store.ActivatableJobs(jobType, func(k uint64) error {
		keys = append(keys, k)
		return nil
	}); err != nil {
		t.Fatalf("ActivatableJobs: %v", err)
	}
	if len(keys) != n {
		t.Fatalf("activatable jobs = %d, want %d", len(keys), n)
	}
	return keys
}

// TestAHeldJobIsNotHandedOutAndCostsNothing is the property the whole decision rests
// on. A job the gate refuses is not leased, not worked, and not failed: it stays
// activatable with its retry budget untouched, waiting exactly as it waits for a worker
// that has not polled yet.
func TestAHeldJobIsNotHandedOutAndCostsNothing(t *testing.T) {
	p, store, jobType, defKey := setup(t)
	r := job.NewRunner(store, p)
	var worked int
	var mu sync.Mutex
	r.Handle(jobType, func(state.Reader) job.Handler {
		return func(job.Job) error { mu.Lock(); worked++; mu.Unlock(); return nil }
	})
	keys := backlog(t, p, store, jobType, defKey, 3)

	gate := newHeldGate()
	gate.holds[jobType] = true
	for _, k := range keys {
		gate.deny[k] = true
	}
	r.SetGate(gate)

	n, err := r.PollOnce()
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if n != 0 {
		t.Errorf("a round worked %d held jobs, want none", n)
	}
	mu.Lock()
	got := worked
	mu.Unlock()
	if got != 0 {
		t.Errorf("%d handlers ran against a held target, want none", got)
	}
	// Still there, still activatable, still owing nobody a retry.
	for _, k := range keys {
		jv, ok, err := store.GetJob(k)
		if err != nil || !ok {
			t.Fatalf("job %d: ok=%v err=%v, want it untouched", k, ok, err)
		}
		if jv.LeaseExpiresAt != 0 {
			t.Errorf("job %d was leased to a worker that must not have it", k)
		}
	}
	var still int
	if err := store.ActivatableJobs(jobType, func(uint64) error { still++; return nil }); err != nil {
		t.Fatalf("ActivatableJobs: %v", err)
	}
	if still != len(keys) {
		t.Errorf("%d jobs still activatable, want all %d", still, len(keys))
	}
}

// TestAHealthyWorkersJobsGoOutFromBehindAHeldBacklog is the starvation property, and
// the reason a held type is scanned newest-first.
//
// The activatable index is ordered by key, so a held target's backlog sits *in front
// of* every job created after it. A gate applied after the round's candidates were
// collected would hand out nothing at all; an oldest-first bounded scan would spend
// every round re-reading the backlog and reach the jobs behind it only after as many
// rounds as the backlog is budgets deep. Here the backlog is deeper than one round's
// budget, so an implementation that got either of those wrong works nothing.
func TestAHealthyWorkersJobsGoOutFromBehindAHeldBacklog(t *testing.T) {
	p, store, jobType, defKey := setup(t)
	r := job.NewRunner(store, p)
	var mu sync.Mutex
	worked := map[uint64]bool{}
	r.Handle(jobType, func(state.Reader) job.Handler {
		return func(j job.Job) error { mu.Lock(); worked[j.Key] = true; mu.Unlock(); return nil }
	})

	// A backlog two budgets deep belonging to a target that is down, and three jobs
	// created after it that belong to one that is not.
	const held = 2*job.GatedScanBudget + 7
	keys := backlog(t, p, store, jobType, defKey, held+3)
	gate := newHeldGate()
	gate.holds[jobType] = true
	for _, k := range keys[:held] {
		gate.deny[k] = true
	}
	r.SetGate(gate)

	// One round is enough: the three runnable jobs are the newest, which is where a
	// held type's scan starts.
	n, err := r.PollOnce()
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if n != 3 {
		t.Fatalf("one round worked %d jobs, want the three that are runnable", n)
	}
	mu.Lock()
	got := len(worked)
	missing := []uint64{}
	for _, k := range keys[held:] {
		if !worked[k] {
			missing = append(missing, k)
		}
	}
	mu.Unlock()
	if got != 3 || len(missing) != 0 {
		t.Errorf("worked %d jobs, missing %v — want exactly the three behind the backlog", got, missing)
	}
	// And the held backlog is still there, unleased.
	var still int
	if err := store.ActivatableJobs(jobType, func(uint64) error { still++; return nil }); err != nil {
		t.Fatalf("ActivatableJobs: %v", err)
	}
	if still != held {
		t.Errorf("%d jobs left activatable, want the %d that are held", still, held)
	}
}

// TestAGatedRoundIsBounded keeps the filter from undoing ADR-0270. Asking the gate
// costs a read per candidate, so a round that walked a hundred-thousand-job backlog
// looking for one dispatchable job would hold the single writer for all of it — which
// is the cost the bounded poll was introduced to remove.
func TestAGatedRoundIsBounded(t *testing.T) {
	p, store, jobType, defKey := setup(t)
	r := job.NewRunner(store, p)
	r.Handle(jobType, func(state.Reader) job.Handler { return func(job.Job) error { return nil } })

	const waiting = job.GatedScanBudget + 50
	keys := backlog(t, p, store, jobType, defKey, waiting)
	gate := newHeldGate()
	gate.holds[jobType] = true
	for _, k := range keys {
		gate.deny[k] = true
	}
	r.SetGate(gate)

	if _, err := r.Claim(); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	asked, _, _ := gate.counts()
	if asked != job.GatedScanBudget {
		t.Errorf("one round asked about %d candidates, want the budget of %d", asked, job.GatedScanBudget)
	}
	gate.mu.Lock()
	first := gate.asked[0]
	gate.mu.Unlock()
	if first != keys[waiting-1] {
		t.Errorf("the round began at %d, want the newest job %d", first, keys[waiting-1])
	}
	// The next round resumes below where this one stopped rather than re-reading the
	// same newest page.
	if _, err := r.Claim(); err != nil {
		t.Fatalf("second Claim: %v", err)
	}
	gate.mu.Lock()
	second := gate.asked[job.GatedScanBudget]
	gate.mu.Unlock()
	if want := keys[waiting-1-job.GatedScanBudget]; second != want {
		t.Errorf("the second round started at %d, want it to resume at %d", second, want)
	}
}

// TestAGatedScanStartsOverOnceItReachesTheEnd: the cursor is a rotation, not a
// high-water mark. A round that reached the last job must leave the next one starting
// from the beginning, or jobs created at low keys after it would never be seen again.
func TestAGatedScanStartsOverOnceItReachesTheEnd(t *testing.T) {
	p, store, jobType, defKey := setup(t)
	r := job.NewRunner(store, p)
	r.Handle(jobType, func(state.Reader) job.Handler { return func(job.Job) error { return nil } })
	keys := backlog(t, p, store, jobType, defKey, 3)

	gate := newHeldGate()
	gate.holds[jobType] = true
	for _, k := range keys {
		gate.deny[k] = true
	}
	r.SetGate(gate)

	for round := 0; round < 2; round++ {
		if _, err := r.Claim(); err != nil {
			t.Fatalf("round %d: Claim: %v", round, err)
		}
	}
	gate.mu.Lock()
	asked := append([]uint64(nil), gate.asked...)
	gate.mu.Unlock()
	if len(asked) != 6 {
		t.Fatalf("two rounds asked about %v, want both rounds to see all three", asked)
	}
	if asked[3] != keys[2] {
		t.Errorf("the second round began at %d, want it to start over at the newest, %d", asked[3], keys[2])
	}
}

// TestAnUngatedRunnerAsksNothingAndKeepsItsOrder. The gate is absent on a server with
// nothing wrong, and a runner without one behaves exactly as it did before there was a
// gate at all — in particular it keeps the oldest-first order, which the cursor only
// gives up while something is held.
func TestAnUngatedRunnerAsksNothingAndKeepsItsOrder(t *testing.T) {
	p, store, jobType, defKey := setup(t)
	r := job.NewRunner(store, p)
	r.Handle(jobType, func(state.Reader) job.Handler { return func(job.Job) error { return nil } })
	keys := backlog(t, p, store, jobType, defKey, 5)

	gate := newHeldGate() // installed, but holding nothing
	r.SetGate(gate)

	jobs, err := r.Claim()
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if asked, _, _ := gate.counts(); asked != 0 {
		t.Errorf("an unheld job type asked the gate about %d candidates, want none", asked)
	}
	if len(jobs) != len(keys) {
		t.Fatalf("claimed %d jobs, want %d", len(jobs), len(keys))
	}
	for i, k := range keys {
		if jobs[i].Key != k {
			t.Errorf("claimed[%d] = %d, want %d — oldest first", i, jobs[i].Key, k)
		}
	}
}

// TestOutcomesReachTheGate is the reporting half. The gate cannot judge a target it is
// never told about, and both verdicts have to arrive: a failure is what trips a
// breaker, a completion is what closes one.
func TestOutcomesReachTheGate(t *testing.T) {
	p, store, jobType, defKey := setup(t)
	r := job.NewRunner(store, p)
	boom := errors.New("dial tcp: connection refused")
	var mu sync.Mutex
	failNext := true
	r.Handle(jobType, func(state.Reader) job.Handler {
		return func(job.Job) error {
			mu.Lock()
			defer mu.Unlock()
			if failNext {
				failNext = false
				return boom
			}
			return nil
		}
	})
	backlog(t, p, store, jobType, defKey, 2)

	gate := newHeldGate()
	r.SetGate(gate)
	if _, err := r.PollOnce(); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}

	_, failed, complete := gate.counts()
	if failed != 1 || complete != 1 {
		t.Errorf("gate heard %d failures and %d completions, want one of each", failed, complete)
	}
}

// TestARecoveredTypeForgetsWhereItsScanStopped. The resume cursor is a rotation
// through a *held* backlog; once a target recovers there is no backlog to rotate
// through, and keeping the cursor would start the next outage part-way down the index
// instead of at the newest jobs — where the work another Worker is waiting on is.
func TestARecoveredTypeForgetsWhereItsScanStopped(t *testing.T) {
	p, store, jobType, defKey := setup(t)
	r := job.NewRunner(store, p)
	r.SetClaimBatch(1)
	r.Handle(jobType, func(state.Reader) job.Handler { return func(job.Job) error { return nil } })

	const waiting = job.GatedScanBudget + 20
	keys := backlog(t, p, store, jobType, defKey, waiting)
	gate := newHeldGate()
	gate.holds[jobType] = true
	for _, k := range keys {
		gate.deny[k] = true
	}
	r.SetGate(gate)

	// One held round leaves the cursor part-way down the index.
	if _, err := r.Claim(); err != nil {
		t.Fatalf("held Claim: %v", err)
	}
	// The target recovers, and an ordinary round runs.
	gate.holds[jobType] = false
	if _, err := r.Claim(); err != nil {
		t.Fatalf("recovered Claim: %v", err)
	}
	// It fails again later. The new outage must start at the newest jobs.
	gate.mu.Lock()
	gate.holds[jobType] = true
	mark := len(gate.asked)
	gate.mu.Unlock()
	if _, err := r.Claim(); err != nil {
		t.Fatalf("second outage Claim: %v", err)
	}

	gate.mu.Lock()
	resumed := gate.asked[mark]
	gate.mu.Unlock()
	if resumed != keys[waiting-1] {
		t.Errorf("the second outage resumed at %d, want the newest job %d — the old cursor outlived the recovery",
			resumed, keys[waiting-1])
	}
}
