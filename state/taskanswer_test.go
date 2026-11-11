package state_test

import (
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

func answered(t *testing.T, s *state.Store, completer string) []uint64 {
	t.Helper()
	var keys []uint64
	if err := s.TaskAnswersBy(completer, state.TaskAnswerCursor{}, 0, 0, func(a state.TaskAnswer) error {
		keys = append(keys, a.Value.JobKey)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return keys
}

// A person's answered tasks are listed newest first, paged by a cursor, bounded by time,
// readable by key, kept apart from another person's, and gone with their instance's
// history (ADR-0554 §3).
func TestTaskAnswersAreListedPerPersonAndPurgedWithTheirInstance(t *testing.T) {
	s := openStore(t)
	i1, i2 := model.NewKey(1, 10), model.NewKey(1, 11)
	j1, j2, j3, j4 := model.NewKey(1, 30), model.NewKey(1, 31), model.NewKey(1, 32), model.NewKey(1, 33)
	commit(t, s, func(tx *state.Tx) error {
		for _, a := range []struct {
			at int64
			v  model.TaskAnswerValue
		}{
			{100, model.TaskAnswerValue{ProcessInstanceKey: i1, JobKey: j1, Completer: "usr_a", FormRevision: "sha256:1"}},
			{300, model.TaskAnswerValue{ProcessInstanceKey: i2, JobKey: j2, Completer: "usr_a"}},
			{200, model.TaskAnswerValue{ProcessInstanceKey: i1, JobKey: j3, Completer: "usr_a"}},
			{250, model.TaskAnswerValue{ProcessInstanceKey: i1, JobKey: j4, Completer: "usr_ab"}},
			// Not a person's answer: never indexed.
			{260, model.TaskAnswerValue{ProcessInstanceKey: i1, JobKey: model.NewKey(1, 34)}},
		} {
			v := a.v
			if err := tx.RecordTaskAnswer(a.at, uint64(a.at)+1, &v); err != nil {
				return err
			}
		}
		return nil
	})

	if got := answered(t, s, "usr_a"); len(got) != 3 || got[0] != j2 || got[1] != j3 || got[2] != j1 {
		t.Fatalf("usr_a = %v, want newest first [%d %d %d]", got, j2, j3, j1)
	}
	if got := answered(t, s, "usr_ab"); len(got) != 1 || got[0] != j4 {
		t.Fatalf("usr_ab = %v: one principal's prefix must not reach another's", got)
	}

	// A page continues strictly after its cursor.
	var page []uint64
	if err := s.TaskAnswersBy("usr_a", state.TaskAnswerCursor{At: 200, JobKey: j3}, 0, 0, func(a state.TaskAnswer) error {
		page = append(page, a.Value.JobKey)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0] != j1 {
		t.Fatalf("after cursor = %v, want [%d]", page, j1)
	}
	// A time window is inclusive below, exclusive above.
	var window []uint64
	if err := s.TaskAnswersBy("usr_a", state.TaskAnswerCursor{}, 200, 300, func(a state.TaskAnswer) error {
		window = append(window, a.Value.JobKey)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(window) != 1 || window[0] != j3 {
		t.Fatalf("window = %v, want [%d]", window, j3)
	}

	a, ok, err := s.TaskAnswer(j1)
	if err != nil || !ok || a.At != 100 || a.Position != 101 || a.Value.FormRevision != "sha256:1" || a.Value.Completer != "usr_a" {
		t.Fatalf("by key = %+v, %v, %v", a, ok, err)
	}

	commit(t, s, func(tx *state.Tx) error { return tx.PurgeInstanceHistory(i1, 1, 0) })
	if got := answered(t, s, "usr_a"); len(got) != 1 || got[0] != j2 {
		t.Fatalf("after purge usr_a = %v, want only the other instance's [%d]", got, j2)
	}
	if got := answered(t, s, "usr_ab"); len(got) != 0 {
		t.Fatalf("after purge usr_ab = %v, want none", got)
	}
	if _, ok, err := s.TaskAnswer(j1); err != nil || ok {
		t.Fatalf("by key after purge = %v, %v; want gone", ok, err)
	}
	if _, ok, err := s.TaskAnswer(j2); err != nil || !ok {
		t.Fatalf("the other instance's answer = %v, %v; want kept", ok, err)
	}
}
