package state_test

import (
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// TestJobByElementReverseIndex covers the element→job reverse lookup that lets an
// interrupting boundary event find and cancel its host activity's job: PutJob
// records it, JobOfElement finds it (and reports absence for an element with no
// job), and DeleteJob clears it.
func TestJobByElementReverseIndex(t *testing.T) {
	s := openStore(t)
	jobKey := model.NewKey(1, 5)
	elKey := model.NewKey(1, 9)

	commit(t, s, func(tx *state.Tx) error {
		return tx.PutJob(jobKey, &model.JobValue{ElementInstanceKey: elKey, JobType: 3})
	})

	tx := s.NewTransaction()
	got, ok, err := tx.JobOfElement(elKey)
	if err != nil || !ok || got != jobKey {
		t.Fatalf("JobOfElement(el) = (%d, %v, %v), want (%d, true, nil)", got, ok, err, jobKey)
	}
	if _, ok, _ := tx.JobOfElement(model.NewKey(1, 99)); ok {
		t.Error("JobOfElement for an element with no job reported present")
	}
	_ = tx.Close()

	commit(t, s, func(tx *state.Tx) error {
		return tx.DeleteJob(jobKey, &model.JobValue{ElementInstanceKey: elKey, JobType: 3})
	})
	tx2 := s.NewTransaction()
	if _, ok, _ := tx2.JobOfElement(elKey); ok {
		t.Error("reverse entry survived DeleteJob")
	}
	_ = tx2.Close()
}
