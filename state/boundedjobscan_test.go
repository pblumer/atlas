package state_test

import (
	"errors"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// errEnough stops a scan once the caller has what it asked for.
var errEnough = errors.New("enough")

// TestActivatableJobsStopsWhenTheCallerHasEnough is the mechanism behind bounded
// polling. The scan's contract is that a non-nil error from the callback ends it,
// and what makes a poll cheap is the caller *using* that rather than reading on and
// discarding — which is what the worker pull did, so asking for one job walked every
// job of that type (ADR-0270).
//
// The proof is the visit count, not the result: at 1, 100 and 10,000 waiting jobs a
// caller that wants one visits one. That is what "the cost of a heartbeat does not
// grow with the backlog" means, and it is a property of the index, so it holds at a
// hundred thousand for the same reason it holds here.
func TestActivatableJobsStopsWhenTheCallerHasEnough(t *testing.T) {
	for _, waiting := range []int{1, 100, 10_000} {
		s := openStore(t)
		commit(t, s, func(tx *state.Tx) error {
			for i := 0; i < waiting; i++ {
				if err := tx.PutJob(model.NewKey(1, uint64(100+i)), &model.JobValue{JobType: 3, Retries: 1}); err != nil {
					return err
				}
			}
			return nil
		})

		visited := 0
		err := s.ActivatableJobs(3, func(uint64) error {
			visited++
			return errEnough
		})
		if !errors.Is(err, errEnough) {
			t.Fatalf("%d waiting: ActivatableJobs = %v, want the caller's sentinel back", waiting, err)
		}
		if visited != 1 {
			t.Errorf("%d waiting: visited %d entries, want 1", waiting, visited)
		}

		// And an uninterrupted scan still sees everything: stopping early is the
		// caller's choice, not a cap the index imposes.
		all := 0
		if err := s.ActivatableJobs(3, func(uint64) error { all++; return nil }); err != nil {
			t.Fatalf("%d waiting: full scan: %v", waiting, err)
		}
		if all != waiting {
			t.Errorf("%d waiting: full scan saw %d", waiting, all)
		}
	}
}
