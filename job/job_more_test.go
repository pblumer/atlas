package job_test

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// errEngine is a stand-in Engine whose RunUntilIdle fails, so Drive's error
// propagation can be exercised without a real processor fault.
type errEngine struct{ err error }

func (e errEngine) RunUntilIdle() error                        { return e.err }
func (e errEngine) CompleteJob(uint64, ...model.VariableValue) {}
func (e errEngine) CompleteJobWithDecision(uint64, *model.DecisionEvaluationValue, ...model.VariableValue) {
}
func (e errEngine) FailJob(uint64, int32, string, int64) {}

// TestDriveSurfacesRunUntilIdleError covers Drive's first branch: an engine that
// cannot make progress aborts the drive loop with its error.
func TestDriveSurfacesRunUntilIdleError(t *testing.T) {
	dir := t.TempDir()
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	sentinel := errors.New("engine down")
	runner := job.NewRunner(store, errEngine{err: sentinel})
	if err := runner.Drive(); !errors.Is(err, sentinel) {
		t.Fatalf("Drive error = %v, want it to wrap sentinel", err)
	}
}

// twoActivatableJobs deploys the linear process, starts two instances, runs them
// to their waiting service-task jobs, and returns the runner scaffolding plus the
// two activatable job keys in scan order (ascending).
func twoActivatableJobs(t *testing.T) (*engine.Processor, *state.Store, int32, []uint64) {
	t.Helper()
	p, store, jobType, defKey := setup(t)
	p.CreateInstance(defKey)
	p.CreateInstance(defKey)
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
	if len(keys) != 2 {
		t.Fatalf("activatable jobs = %d, want 2", len(keys))
	}
	return p, store, jobType, keys
}

// TestPollOnceSkipsJobCompletedDuringScan covers the "job gone since the scan"
// branch: a job present when the keys were collected but absent by the time its
// turn to dispatch comes is skipped, not dispatched. The handler for the first
// TestAJobCancelledAfterItsClaimIsStillWorked states a consequence of running
// handlers off the run loop, rather than hiding it. Jobs are claimed while the loop
// is held and worked after it is released, so between those two moments another
// request can cancel one — and its handler still runs.
//
// That is bounded and already covered by the protocol's at-least-once contract
// (ADR-0007): the completion of a job that is gone is a no-op in the engine, so the
// round finishes cleanly and the token is not disturbed. What it costs is the side
// effect of work that turned out to be unwanted, which is the same exposure a
// worker crash-and-retry has always had. The alternative — re-reading each job
// immediately before its handler — is a live store read off the loop, which is
// precisely what this change removed.
func TestAJobCancelledAfterItsClaimIsStillWorked(t *testing.T) {
	p, store, jobType, keys := twoActivatableJobs(t)
	other := keys[1]
	ov, ok, err := store.GetJob(other)
	if err != nil || !ok {
		t.Fatalf("GetJob(other): ok=%v err=%v", ok, err)
	}

	var mu sync.Mutex
	dispatched := map[uint64]bool{}
	runner := job.NewRunner(store, p)
	runner.Handle(jobType, func(state.Reader) job.Handler {
		return func(j job.Job) error {
			mu.Lock()
			dispatched[j.Key] = true
			mu.Unlock()
			return nil
		}
	})

	// The job vanishes between the claim and the work.
	jobs, err := runner.Claim()
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("claimed %d jobs, want 2", len(jobs))
	}
	tx := store.NewTransaction()
	if err := tx.DeleteJob(other, ov); err != nil {
		t.Fatalf("DeleteJob: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := tx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	outcomes := runner.Work(jobs, store)
	runner.Submit(outcomes)

	mu.Lock()
	defer mu.Unlock()
	if !dispatched[other] {
		t.Error("the cancelled job was skipped; this test exists to record that it is not")
	}
	// And the round still completed cleanly: the vanished job's completion is a no-op.
	if err := p.RunUntilIdle(); err != nil {
		t.Errorf("RunUntilIdle after completing a job that no longer exists: %v", err)
	}
}
