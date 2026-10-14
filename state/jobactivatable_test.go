package state_test

import (
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// TestJobActivatable proves the point lookup answers exactly what the activatable
// index holds: a pullable job is on it, while a leased one, one waiting out a retry
// backoff, one whose retries are exhausted, a deleted one and an absent one are
// not. It is the per-job read the instance job listing uses in place of walking the
// whole index, so it must agree with that index case for case.
func TestJobActivatable(t *testing.T) {
	s := openStore(t)
	const jobType int32 = 7
	open := model.NewKey(1, 10)
	leased := model.NewKey(1, 11)
	backoff := model.NewKey(1, 12)
	exhausted := model.NewKey(1, 13)
	deleted := model.NewKey(1, 14)
	jobs := map[uint64]*model.JobValue{
		open:      {ElementInstanceKey: model.NewKey(1, 20), JobType: jobType, Retries: 3},
		leased:    {ElementInstanceKey: model.NewKey(1, 21), JobType: jobType, Retries: 3, LeaseExpiresAt: 1},
		backoff:   {ElementInstanceKey: model.NewKey(1, 22), JobType: jobType, Retries: 3, RetryDueDate: 1},
		exhausted: {ElementInstanceKey: model.NewKey(1, 23), JobType: jobType, Retries: 0},
		deleted:   {ElementInstanceKey: model.NewKey(1, 24), JobType: jobType, Retries: 3},
	}
	commit(t, s, func(tx *state.Tx) error {
		for k, v := range jobs {
			if err := tx.PutJob(k, v); err != nil {
				return err
			}
		}
		return nil
	})
	commit(t, s, func(tx *state.Tx) error { return tx.DeleteJob(deleted, jobs[deleted]) })

	onIndex := map[uint64]bool{}
	if err := s.AllActivatableJobs(func(k uint64) error { onIndex[k] = true; return nil }); err != nil {
		t.Fatalf("AllActivatableJobs: %v", err)
	}
	for _, tc := range []struct {
		name string
		key  uint64
		want bool
	}{
		{"open", open, true},
		{"leased", leased, false},
		{"backing off", backoff, false},
		{"retries exhausted", exhausted, false},
		{"deleted", deleted, false},
		{"absent", model.NewKey(1, 99), false},
	} {
		got, err := s.JobActivatable(jobType, tc.key)
		if err != nil {
			t.Fatalf("%s: JobActivatable: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: JobActivatable = %v, want %v", tc.name, got, tc.want)
		}
		if got != onIndex[tc.key] {
			t.Errorf("%s: JobActivatable = %v but the index scan says %v", tc.name, got, onIndex[tc.key])
		}
	}

	// The lookup is keyed by the job's own type: the same key under another type is
	// not on the index.
	if got, err := s.JobActivatable(jobType+1, open); err != nil || got {
		t.Errorf("JobActivatable under the wrong type = (%v, %v), want (false, nil)", got, err)
	}

	// A read view answers the same, from the same code.
	rv := s.ReadView()
	defer rv.Close()
	if got, err := rv.JobActivatable(jobType, open); err != nil || !got {
		t.Errorf("ReadView.JobActivatable(open) = (%v, %v), want (true, nil)", got, err)
	}
}
