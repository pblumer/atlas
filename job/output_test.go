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
