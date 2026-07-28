package state_test

import (
	"errors"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// TestAllActivatableJobs proves the cross-type scan returns every open job
// regardless of job type — where the type-scoped ActivatableJobs returns only one
// type's slice — and that a propagated callback error stops the scan.
func TestAllActivatableJobs(t *testing.T) {
	s := openStore(t)
	want := map[uint64]int32{ // job key → job type
		model.NewKey(1, 10): 3,
		model.NewKey(1, 11): 3,
		model.NewKey(1, 12): 7,
	}
	commit(t, s, func(tx *state.Tx) error {
		for k, jt := range want {
			if err := tx.PutJob(k, &model.JobValue{JobType: jt, Retries: 1}); err != nil {
				return err
			}
		}
		return nil
	})

	got := map[uint64]bool{}
	if err := s.AllActivatableJobs(func(k uint64) error { got[k] = true; return nil }); err != nil {
		t.Fatalf("AllActivatableJobs: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("AllActivatableJobs returned %d keys, want %d (%v)", len(got), len(want), got)
	}
	for k := range want {
		if !got[k] {
			t.Errorf("AllActivatableJobs missing key %d", k)
		}
	}

	// The type-scoped scan still returns only its own slice.
	n := 0
	if err := s.ActivatableJobs(3, func(uint64) error { n++; return nil }); err != nil {
		t.Fatalf("ActivatableJobs(3): %v", err)
	}
	if n != 2 {
		t.Errorf("ActivatableJobs(3) = %d, want 2", n)
	}

	// A callback error propagates and stops the scan.
	sentinel := errors.New("stop")
	if err := s.AllActivatableJobs(func(uint64) error { return sentinel }); err != sentinel {
		t.Errorf("AllActivatableJobs error = %v, want sentinel", err)
	}
}
