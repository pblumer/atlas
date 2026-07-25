package engine_test

import (
	"reflect"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// collectDecisions folds a scope's decision-evaluation history into a slice, so a
// test can assert what was retained and in what order.
func collectDecisions(t *testing.T, s interface {
	DecisionEvaluationHistory(uint64, func(int64, uint64, *model.DecisionEvaluationValue) error) error
}, scope uint64) []model.DecisionEvaluationValue {
	t.Helper()
	var out []model.DecisionEvaluationValue
	if err := s.DecisionEvaluationHistory(scope, func(_ int64, _ uint64, v *model.DecisionEvaluationValue) error {
		out = append(out, *v)
		return nil
	}); err != nil {
		t.Fatalf("DecisionEvaluationHistory: %v", err)
	}
	return out
}

// TestJobCompletionRecordsDecisionEvaluation covers the debugging side of the job
// path (ADR-0064): a business rule task's worker completes its job carrying the
// decision evaluation it made off the processor goroutine, and handleJobCompleted
// freezes that evaluation — inputs, outputs, and trace — into a durable history
// record keyed under the process instance. The record's scope keys are stamped from
// the authoritative job even though the worker left them zero, and a replay of the
// log rebuilds the identical record without re-evaluating (invariant I6). This
// exercises CompleteJobWithDecision and the VTDecisionEvaluation apply path directly
// in the engine, without a real DMN worker.
func TestJobCompletionRecordsDecisionEvaluation(t *testing.T) {
	dir := t.TempDir()
	h := openHarness(t, dir)
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

	pi := model.NewKey(1, 1) // the first minted key is the process instance
	// Capture the job's element instance before completion retires the job, so the
	// expected record can name the same business rule task instance.
	jv, ok, err := h.store.GetJob(jobs[0])
	if err != nil || !ok {
		t.Fatalf("GetJob: ok=%v err=%v", ok, err)
	}
	elKey := jv.ElementInstanceKey

	// The worker leaves ProcessInstanceKey/ElementInstanceKey zero to prove the
	// completion re-stamps them from the job; it sets the debugging payload.
	decision := &model.DecisionEvaluationValue{
		ProcessDefKey: cp.Key,
		ElementId:     7,
		DecisionId:    "Dish",
		InputsJSON:    `{"Season":"Winter"}`,
		OutputsJSON:   `{"Dish":"Roastbeef"}`,
		TraceJSON:     `{"tables":[{"hitPolicy":"U","matched":[0]}]}`,
	}
	p.CompleteJobWithDecision(jobs[0], decision,
		model.VariableValue{Name: "dish", Kind: model.VarString, Text: "Roastbeef"},
	)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after complete): %v", err)
	}

	// The result variable is written as before, and the decision evaluation is now
	// retained under the instance.
	if got := readVar(t, h.store, pi, "dish"); got == nil || got.Text != "Roastbeef" {
		t.Fatalf("dish = %+v, want string Roastbeef", got)
	}
	live := collectDecisions(t, h.store, pi)
	want := []model.DecisionEvaluationValue{{
		ProcessInstanceKey: pi,
		ElementInstanceKey: elKey,
		ProcessDefKey:      cp.Key,
		ElementId:          7,
		DecisionId:         "Dish",
		InputsJSON:         `{"Season":"Winter"}`,
		OutputsJSON:        `{"Dish":"Roastbeef"}`,
		TraceJSON:          `{"tables":[{"hitPolicy":"U","matched":[0]}]}`,
	}}
	if !reflect.DeepEqual(live, want) {
		t.Fatalf("live decision history = %+v, want %+v", live, want)
	}

	// Recovery: state rebuilt from the log alone carries the identical record
	// (invariant I4/I6) — the evaluation is a frozen fact, never re-run on replay.
	replay := replayInto(t, dir, cp)
	if got := collectDecisions(t, replay, pi); !reflect.DeepEqual(got, want) {
		t.Fatalf("replayed decision history = %+v, want %+v", got, want)
	}
}

// TestCompleteJobWithNilDecisionRecordsNothing proves the decision channel is
// optional: completing a job with a nil decision behaves exactly like CompleteJob —
// output variables are written and no decision-evaluation record is retained.
func TestCompleteJobWithNilDecisionRecordsNothing(t *testing.T) {
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

	p.CompleteJobWithDecision(jobs[0], nil,
		model.VariableValue{Name: "dish", Kind: model.VarString, Text: "Salad"},
	)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after complete): %v", err)
	}

	pi := model.NewKey(1, 1)
	if got := readVar(t, h.store, pi, "dish"); got == nil || got.Text != "Salad" {
		t.Fatalf("dish = %+v, want string Salad", got)
	}
	if got := collectDecisions(t, h.store, pi); len(got) != 0 {
		t.Fatalf("decision history = %+v, want none", got)
	}
}
