package job_test

import (
	"testing"

	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
)

// TestRunnerWritesHandlerOutput covers HandleWithOutput end to end: a worker
// registered with HandleWithOutput returns output variables, the runner rides them
// along on the CompleteJob command, and they land in the job's process instance
// scope — the path the DMN worker uses to surface a decision result.
func TestRunnerWritesHandlerOutput(t *testing.T) {
	p, store, jobType, defKey := setup(t)

	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, func(job.Job) ([]model.VariableValue, error) {
		return []model.VariableValue{{Name: "answer", Kind: model.VarNumber, Text: "42"}}, nil
	})

	p.CreateInstance(defKey)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}

	if pi, ei := active(t, store); pi != 0 || ei != 0 {
		t.Fatalf("after Drive: process=%d element=%d, want 0 and 0", pi, ei)
	}

	pi := model.NewKey(1, 1) // the first minted key is the process instance
	var answer *model.VariableValue
	if err := store.VariablesOfScope(pi, func(v *model.VariableValue) error {
		if v.Name == "answer" {
			cp := *v
			answer = &cp
		}
		return nil
	}); err != nil {
		t.Fatalf("VariablesOfScope: %v", err)
	}
	if answer == nil || answer.Kind != model.VarNumber || answer.Text != "42" {
		t.Fatalf("answer variable = %+v, want number 42", answer)
	}
}

// TestRunnerHandleCompleting covers the third registration shape: a worker that
// returns a Completion carrying both output variables and the decision evaluation
// to retain. It is how the DMN worker records what a business rule task decided
// (ADR-0066) — the decision has to ride on the same CompleteJob command as the
// outputs, so the retained evaluation and the variables it produced can never be
// made durable apart from each other.
func TestRunnerHandleCompleting(t *testing.T) {
	p, store, jobType, defKey := setup(t)

	runner := job.NewRunner(store, p)
	runner.HandleCompleting(jobType, func(j job.Job) (job.Completion, error) {
		return job.Completion{
			Outputs: []model.VariableValue{{Name: "grade", Kind: model.VarString, Text: "A"}},
			Decision: &model.DecisionEvaluationValue{
				DecisionId:         "creditRating",
				ProcessInstanceKey: j.ProcessInstanceKey,
				ElementInstanceKey: j.ElementInstanceKey,
				InputsJSON:         `{"score":710}`,
				OutputsJSON:        `{"rating":"A"}`,
			},
		}, nil
	})

	p.CreateInstance(defKey)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if pi, ei := active(t, store); pi != 0 || ei != 0 {
		t.Fatalf("after Drive: process=%d element=%d, want the instance completed", pi, ei)
	}

	pi := model.NewKey(1, 1) // the first minted key is the process instance
	var grade *model.VariableValue
	if err := store.VariablesOfScope(pi, func(v *model.VariableValue) error {
		if v.Name == "grade" {
			cp := *v
			grade = &cp
		}
		return nil
	}); err != nil {
		t.Fatalf("VariablesOfScope: %v", err)
	}
	if grade == nil || grade.Text != "A" {
		t.Fatalf("grade = %+v, want the handler's output variable", grade)
	}

	var seen []model.DecisionEvaluationValue
	if err := store.DecisionEvaluationHistory(pi, func(_ int64, _ uint64, d *model.DecisionEvaluationValue) error {
		seen = append(seen, *d)
		return nil
	}); err != nil {
		t.Fatalf("DecisionEvaluationHistory: %v", err)
	}
	if len(seen) != 1 || seen[0].DecisionId != "creditRating" || seen[0].OutputsJSON != `{"rating":"A"}` {
		t.Fatalf("retained decisions = %+v, want one for creditRating with its outputs", seen)
	}
}
