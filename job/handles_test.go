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

// TestUnhandleParksAJobForAnExternalWorker is the mechanism behind
// --offload-connectors. The assertion that matters is not that Handles flips but
// that Claim stops collecting the type: a runner that still claimed it would work
// the job in the engine while an external worker was also allowed to lease it, which
// is the double-work the refusal exists to prevent.
func TestUnhandleParksAJobForAnExternalWorker(t *testing.T) {
	p, store, jobType, defKey := setup(t)
	r := job.NewRunner(store, p)
	r.Handle(jobType, func(state.Reader) job.Handler { return func(job.Job) error { return nil } })

	p.CreateInstance(defKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	claimed, err := r.Claim()
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d jobs while handling the type, want 1", len(claimed))
	}

	r.Unhandle(jobType)
	if r.Handles(jobType) {
		t.Error("Handles is still true after Unhandle, so the pull would refuse an external worker")
	}
	claimed, err = r.Claim()
	if err != nil {
		t.Fatalf("Claim after Unhandle: %v", err)
	}
	if len(claimed) != 0 {
		t.Errorf("claimed %d jobs after Unhandle, want none — the job must park for an external worker", len(claimed))
	}
}

// Unhandling a type nothing registered is a no-op rather than an error: the server
// applies the operator's list in one pass over every kind, and a kind whose handler
// was never registered (an optional one left off) must not fail that pass.
func TestUnhandleIsHarmlessForATypeNothingRegistered(t *testing.T) {
	r := job.NewRunner(nil, nil)
	r.Unhandle(11)
	if r.Handles(11) {
		t.Error("Handles(11) = true after unhandling a type nothing registered")
	}
}
