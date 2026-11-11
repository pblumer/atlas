package state

import (
	"testing"

	"github.com/pblumer/atlas/model"
)

// A damaged row is reported, never read as some other task's answer, and a fact that
// is not a person's answer is never indexed (ADR-0554 §3).
func TestADamagedTaskAnswerIsReportedNotMisread(t *testing.T) {
	s := openStore(t)
	const pi, job = uint64(10), uint64(30)

	// Nothing to list for nobody, and a fact without a completer or keys is skipped.
	if err := s.TaskAnswersBy("", TaskAnswerCursor{}, 0, 0, func(TaskAnswer) error {
		t.Fatal("listed answers for nobody")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	tx := s.NewTransaction()
	for _, v := range []model.TaskAnswerValue{
		{ProcessInstanceKey: pi, JobKey: job},
		{Completer: "usr_a", JobKey: job},
		{Completer: "usr_a", ProcessInstanceKey: pi},
	} {
		v := v
		must(t, tx.RecordTaskAnswer(1, 2, &v))
	}
	commit(t, tx)
	if _, ok, err := s.TaskAnswer(job); err != nil || ok {
		t.Fatalf("an incomplete fact was indexed: %v, %v", ok, err)
	}

	// A by-key entry of the wrong width.
	tx = s.NewTransaction()
	must(t, tx.b.Set(keyTaskAnswerJob(job), []byte{1, 2, 3}, nil))
	commit(t, tx)
	if _, _, err := s.TaskAnswer(job); err == nil {
		t.Error("a by-key entry of three bytes was read")
	}

	// A by-key entry naming a row that is not there is "not held".
	tx = s.NewTransaction()
	must(t, tx.b.Set(keyTaskAnswerJob(job), []byte{0, 0, 0, 0, 0, 0, 0, 99}, nil))
	commit(t, tx)
	if _, ok, err := s.TaskAnswer(job); err != nil || ok {
		t.Errorf("a dangling by-key entry = %v, %v; want not held", ok, err)
	}

	// A fact row cut short, read by key, by the list, and by the purge.
	tx = s.NewTransaction()
	must(t, tx.b.Set(keyTaskAnswer(99, job), []byte{1, 2, 3}, nil))
	commit(t, tx)
	if _, _, err := s.TaskAnswer(job); err == nil {
		t.Error("a fact of three bytes was read")
	}
	tx = s.NewTransaction()
	must(t, tx.b.Set(keyTaskAnswer(98, job), make([]byte, 20), nil))
	commit(t, tx)
	if _, err := decodeTaskAnswer(make([]byte, 20)); err == nil {
		t.Error("a fact whose value is cut short decoded")
	}
	tx = s.NewTransaction()
	if err := tx.deleteTaskAnswerIndexes(99); err == nil {
		t.Error("the purge read past a damaged fact")
	}
	if err := tx.PurgeInstanceHistory(99, 1, 0); err == nil {
		t.Error("the instance purge went past a damaged fact")
	}
	tx.Close()

	// A list entry of the wrong width, and one whose key is cut short.
	tx = s.NewTransaction()
	must(t, tx.b.Set(keyTaskAnswerBy("usr_b", 5, job), []byte{1}, nil))
	commit(t, tx)
	if err := s.TaskAnswersBy("usr_b", TaskAnswerCursor{}, 0, 0, func(TaskAnswer) error { return nil }); err == nil {
		t.Error("a list entry of one byte was read")
	}
	tx = s.NewTransaction()
	must(t, tx.b.Set(append(taskAnswerByPrefix("usr_c"), 1, 2), []byte{0, 0, 0, 0, 0, 0, 0, 10}, nil))
	commit(t, tx)
	if err := s.TaskAnswersBy("usr_c", TaskAnswerCursor{}, 0, 0, func(TaskAnswer) error { return nil }); err == nil {
		t.Error("a list key cut short was read")
	}
	// A list entry naming a fact that is gone is skipped.
	tx = s.NewTransaction()
	must(t, tx.b.Set(keyTaskAnswerBy("usr_d", 5, 77), []byte{0, 0, 0, 0, 0, 0, 0, 55}, nil))
	commit(t, tx)
	n := 0
	if err := s.TaskAnswersBy("usr_d", TaskAnswerCursor{}, 0, 0, func(TaskAnswer) error { n++; return nil }); err != nil || n != 0 {
		t.Errorf("a dangling list entry = %d rows, %v; want skipped", n, err)
	}
}
