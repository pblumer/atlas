package job_test

import (
	"errors"
	"path/filepath"
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
// job deletes the second before the loop reaches it.
func TestPollOnceSkipsJobCompletedDuringScan(t *testing.T) {
	p, store, jobType, keys := twoActivatableJobs(t)
	other := keys[1]
	ov, ok, err := store.GetJob(other)
	if err != nil || !ok {
		t.Fatalf("GetJob(other): ok=%v err=%v", ok, err)
	}

	dispatched := map[uint64]bool{}
	runner := job.NewRunner(store, p)
	runner.Handle(jobType, func(j job.Job) error {
		dispatched[j.Key] = true
		if j.Key == keys[0] {
			// Remove the not-yet-dispatched job so its GetJob returns not-found.
			tx := store.NewTransaction()
			if err := tx.DeleteJob(other, ov); err != nil {
				return err
			}
			if err := tx.Commit(); err != nil {
				return err
			}
			return tx.Close()
		}
		return nil
	})

	n, err := runner.PollOnce()
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if n != 1 {
		t.Fatalf("dispatched = %d, want 1 (second job vanished before its turn)", n)
	}
	if dispatched[other] {
		t.Error("deleted job was dispatched, want it skipped")
	}
}
