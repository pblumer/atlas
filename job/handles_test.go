package job_test

import (
	"testing"

	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// Handles is what keeps a job type from being worked twice: an external worker
// must not be able to lease a type an in-process handler is already draining, so
// the pull endpoint asks the runner first (ADR-0157).
func TestHandlesReportsRegisteredJobTypes(t *testing.T) {
	r := job.NewRunner(nil, nil)
	r.Handle(7, func(state.Reader) job.Handler { return func(job.Job) error { return nil } })
	r.HandleWithOutput(8, func(state.Reader) job.OutputHandler {
		return func(job.Job) ([]model.VariableValue, error) { return nil, nil }
	})

	for _, jobType := range []int32{7, 8} {
		if !r.Handles(jobType) {
			t.Errorf("Handles(%d) = false after registering it, want true", jobType)
		}
	}
	if r.Handles(9) {
		t.Error("Handles(9) = true for a job type nothing registered, want false")
	}
}
