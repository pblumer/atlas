package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// TestJobCompletionWritesOutputVariables covers the output side of the job path:
// a worker that completes a job carrying output variables (as the DMN worker does
// with a decision result) has those variables written into the job's process
// instance scope before the element completes, so a downstream element can read
// them. It exercises handleJobCompleted's variable-write loop and CompleteJob's
// variadic outputs directly in the engine, without a worker.
func TestJobCompletionWritesOutputVariables(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType := businessRuleProcess(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	jobs := activatableJobs(t, h.store, jobType)
	if len(jobs) != 1 {
		t.Fatalf("activatable jobs = %d, want 1", len(jobs))
	}

	// Complete the job with two output variables of different kinds, as a worker
	// would after doing its work.
	p.CompleteJob(jobs[0],
		model.VariableValue{Name: "dish", Kind: model.VarString, Text: "Roastbeef"},
		model.VariableValue{Name: "score", Kind: model.VarNumber, Text: "42"},
	)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after complete): %v", err)
	}

	pi := model.NewKey(1, 1) // the first minted key is the process instance
	if got := readVar(t, h.store, pi, "dish"); got == nil || got.Kind != model.VarString || got.Text != "Roastbeef" {
		t.Fatalf("dish = %+v, want string Roastbeef", got)
	}
	if got := readVar(t, h.store, pi, "score"); got == nil || got.Kind != model.VarNumber || got.Text != "42" {
		t.Fatalf("score = %+v, want number 42", got)
	}
	if procs, elems := counts(t, h.store); procs != 0 || elems != 0 {
		t.Fatalf("after completion: process=%d element=%d, want 0 and 0", procs, elems)
	}
}
