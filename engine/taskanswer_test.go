package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// twoTaskProcess is start → user task → user task → end: the first task is answered by
// a person, the second is forced by an operator, so one instance exercises both.
func twoTaskProcess(t *testing.T) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(defKey, "freigabe", 1)
	start := b.AddStartEvent()
	first := b.AddUserTask("Freigeben", compiler.Assignment{Literal: "ada"}, compiler.Assignment{Literal: "hr,it"}, "freigabe-form", 0, 0, 3)
	second := b.AddUserTask("Quittieren", compiler.Assignment{Literal: ""}, compiler.Assignment{Literal: ""}, "", 0, 0, 3)
	end := b.AddEndEvent()
	b.Connect(start, first)
	b.Connect(first, second)
	b.Connect(second, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, first
}

// TestAPersonsAnswerIsAFactAndReplaysIntoTheSameIndex covers ADR-0554 §1: a user task
// completed by a person appends a TaskAnswered beside JobCompleted, naming who, through
// which form revision and where the task sat; the person's list is folded from it, and
// a replay into an empty store rebuilds the same rows (I4/I6). A forced completion is an
// operator's act and appends none.
func TestAPersonsAnswerIsAFactAndReplaysIntoTheSameIndex(t *testing.T) {
	dir := t.TempDir()
	h := openHarness(t, dir)
	cp, first := twoTaskProcess(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	jobs := activatableJobs(t, h.store, compiler.UserTaskJobTypeIndex)
	if len(jobs) != 1 {
		t.Fatalf("open user tasks = %d, want 1", len(jobs))
	}
	answeredJob := jobs[0]
	p.CompleteUserTask(answeredJob, "usr_ada", "sha256:abc",
		model.VariableValue{Name: "approved", Kind: model.VarBool, Bool: true})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (answer): %v", err)
	}
	jobs = activatableJobs(t, h.store, compiler.UserTaskJobTypeIndex)
	if len(jobs) != 1 || jobs[0] == answeredJob {
		t.Fatalf("after the answer, open user tasks = %v, want the second task", jobs)
	}
	// Forced by an operator: the operator's trail, not a person's answer.
	p.CompleteJobManually(jobs[0], "operator", "done by phone")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (forced): %v", err)
	}
	piKey := model.NewKey(1, 1)

	check := func(s *state.Store, what string) {
		t.Helper()
		var got []state.TaskAnswer
		if err := s.TaskAnswersBy("usr_ada", state.TaskAnswerCursor{}, 0, 0, func(a state.TaskAnswer) error {
			got = append(got, a)
			return nil
		}); err != nil {
			t.Fatalf("%s: TaskAnswersBy: %v", what, err)
		}
		if len(got) != 1 {
			t.Fatalf("%s: answers = %d, want 1", what, len(got))
		}
		a := got[0].Value
		if a.JobKey != answeredJob || a.ProcessInstanceKey != piKey || a.ProcessDefKey != cp.Key ||
			a.ElementId != first || a.FormRevision != "sha256:abc" || a.Completer != "usr_ada" ||
			a.Assignee != "ada" || a.CandidateGroups != "hr,it" || a.ElementInstanceKey == 0 || got[0].At == 0 || got[0].Position == 0 {
			t.Errorf("%s: answer = %+v at %d", what, a, got[0].At)
		}
		if n := countAnswers(t, s, "operator"); n != 0 {
			t.Errorf("%s: the forced completion was listed as the operator's answer", what)
		}
	}
	check(h.store, "live")
	h.close(t)

	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("reopen wal: %v", err)
	}
	store2, err := state.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatalf("fresh state.Open: %v", err)
	}
	defer func() {
		if err := store2.Close(); err != nil {
			t.Errorf("store2.Close: %v", err)
		}
		if err := log2.Close(); err != nil {
			t.Errorf("log2.Close: %v", err)
		}
	}()
	rp := engine.New(1, log2, store2, &manualClock{})
	rp.Deploy(cp)
	if err := rp.Recover(); err != nil {
		t.Fatalf("recovery Recover: %v", err)
	}
	check(store2, "after replay")
}

// TestACompletionWithoutACompleterIsNotAnAnswer: with nobody to name (auth off), a
// person's completion appends no answer, and completes the task all the same.
func TestACompletionWithoutACompleterIsNotAnAnswer(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, _ := twoTaskProcess(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatal(err)
	}
	jobs := activatableJobs(t, h.store, compiler.UserTaskJobTypeIndex)
	p.CompleteUserTask(jobs[0], "", "sha256:abc")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := h.store.TaskAnswer(jobs[0]); err != nil || ok {
		t.Fatalf("TaskAnswer = %v, %v; want none", ok, err)
	}
	if next := activatableJobs(t, h.store, compiler.UserTaskJobTypeIndex); len(next) != 1 || next[0] == jobs[0] {
		t.Fatalf("the task was not completed: open = %v", next)
	}
}

func countAnswers(t *testing.T, s *state.Store, completer string) int {
	t.Helper()
	n := 0
	if err := s.TaskAnswersBy(completer, state.TaskAnswerCursor{}, 0, 0, func(state.TaskAnswer) error { n++; return nil }); err != nil {
		t.Fatal(err)
	}
	return n
}
