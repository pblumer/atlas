package state_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// TestActivatableJobsDesc covers the newest-first, cursor-paged scan the task inbox
// pages with: descending key order, the ?before= cursor as an exclusive upper bound,
// and job-type scoping. It exercises ActivatableJobsDesc and scanRangeDesc.
func TestActivatableJobsDesc(t *testing.T) {
	s := openStore(t)
	const jobType int32 = 42
	a, b, c := model.NewKey(1, 1), model.NewKey(1, 2), model.NewKey(1, 3)

	commit(t, s, func(tx *state.Tx) error {
		for _, k := range []uint64{a, b, c} {
			if err := tx.PutJob(k, &model.JobValue{JobType: jobType, Retries: 1}); err != nil {
				return err
			}
		}
		// A different type must never appear in the jobType==42 scan.
		return tx.PutJob(model.NewKey(1, 4), &model.JobValue{JobType: 7, Retries: 1})
	})

	collect := func(before uint64) []uint64 {
		var got []uint64
		if err := s.ActivatableJobsDesc(jobType, before, func(k uint64) error {
			got = append(got, k)
			return nil
		}); err != nil {
			t.Fatalf("scan desc (before=%d): %v", before, err)
		}
		return got
	}

	// before == 0 starts from the newest key: full set, descending.
	if got := collect(0); !reflect.DeepEqual(got, []uint64{c, b, a}) {
		t.Errorf("desc from newest = %v, want [%d %d %d]", got, c, b, a)
	}
	// before == b is an exclusive upper bound: only keys strictly below b (just a).
	if got := collect(b); !reflect.DeepEqual(got, []uint64{a}) {
		t.Errorf("desc before b = %v, want [%d]", got, a)
	}
	// A cursor below the smallest key yields nothing.
	if got := collect(a); len(got) != 0 {
		t.Errorf("desc before a = %v, want empty", got)
	}

	// An error from the callback aborts the scan and propagates unchanged.
	sentinel := errors.New("stop")
	if err := s.ActivatableJobsDesc(jobType, 0, func(uint64) error { return sentinel }); err != sentinel {
		t.Errorf("callback error = %v, want %v", err, sentinel)
	}
}

// TestStoreJobOfElement covers the read-side element→job lookup the per-instance task
// list uses: it returns the job held by an element, and reports absence otherwise.
func TestStoreJobOfElement(t *testing.T) {
	s := openStore(t)
	jobKey, elKey := model.NewKey(1, 5), model.NewKey(1, 9)

	commit(t, s, func(tx *state.Tx) error {
		return tx.PutJob(jobKey, &model.JobValue{ElementInstanceKey: elKey, JobType: 3, Retries: 1})
	})

	if got, ok, err := s.JobOfElement(elKey); err != nil || !ok || got != jobKey {
		t.Fatalf("JobOfElement(el) = (%d, %v, %v), want (%d, true, nil)", got, ok, err, jobKey)
	}
	if _, ok, err := s.JobOfElement(model.NewKey(1, 99)); err != nil || ok {
		t.Fatalf("JobOfElement(missing) = (_, %v, %v), want (false, nil)", ok, err)
	}
}
