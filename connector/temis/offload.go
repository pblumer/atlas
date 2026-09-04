package temis

import (
	"context"
	"fmt"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/dmn"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// A resolved central decision, and the function that evaluates one.
//
// This is [ADR-0168]'s split applied to the last kind ADR-0233 owed a worker half,
// and it is the one that did not simply copy the five before it. Every other kind is
// a *connector task* whose worker completes with output variables. A central decision
// is a **business rule task**, and it completes with something no other job carries: a
// durable [model.DecisionEvaluationValue] — the inputs, the outputs and the engine's
// trace — retained so an operator can see how a decision was made (ADR-0066).
//
// So the seam falls in a different place. The engine keeps what it alone can do and
// what must stay a fact it stamps itself:
//
//   - resolving the decision's inputs, which is [dmn.BuildInputs] — the static inputs
//     overlaid by the mappings evaluated over the instance's live variables. The
//     worker gets the merged context, so a decision is asked the same question
//     wherever it runs.
//   - folding the evaluation record on completion, with the process and element keys
//     re-stamped from the authoritative job rather than taken from the report.
//
// The worker gets the part that was the reason to move at all: the round trip to a
// decision service somebody else operates.
//
// What this does *not* change is the record's provenance, and that is worth saying
// plainly because it looks like it should. A central decision's trace has always been
// the remote service's account of its own evaluation — temis computes it and returns
// it over HTTP. Offloading moves which process makes that call, not who authored the
// trace.
//
// [ADR-0168]: https://github.com/pblumer/atlas/blob/main/docs/adr/0168-connector-work-on-a-worker.md

// Job is a central decision with its input context already built.
type Job struct {
	// Connector names the temis service in the worker's registry — the endpoint and
	// token live there, never here.
	Connector  string `json:"connector"`
	DecisionID string `json:"decisionId"`
	// Inputs is the merged input context: static inputs overlaid by the input
	// mappings evaluated over the instance's variables (ADR-0039). It is engine
	// state, so it travels resolved — a worker has no scope chain to build it from.
	Inputs map[string]any `json:"inputs,omitempty"`
	// Result names the process variable the decision's outputs are written to; empty
	// means the model routes on the decision without keeping it.
	Result string `json:"resultVariable,omitempty"`
}

// Result is what evaluating a Job produces: the decision's outputs, and the trace
// explaining how it reached them when the service returned one.
type Result struct {
	ResultVariable string
	Outputs        map[string]any
	Trace          []byte
}

// Variables renders the outputs as the process variables the job completes with —
// none when the model named no result variable. Both halves call it, so an offloaded
// decision and an in-engine one cannot disagree about what a task writes back.
func (r Result) Variables() []model.VariableValue {
	if r.ResultVariable == "" {
		return nil
	}
	return []model.VariableValue{dmn.OutputVariable(r.ResultVariable, r.Outputs)}
}

// Resolve turns a compiled business rule task into a [Job]. Engine work by
// necessity: the input mappings are compiled FEEL (ADR-0008/0015) and the scope
// lives in the store.
func Resolve(store state.Reader, cp *compiler.CompiledProcess, detail *compiler.BusinessRuleTaskDetail, ei *model.ElementInstanceValue, elementInstanceKey uint64) (Job, error) {
	if detail == nil {
		return Job{}, fmt.Errorf("temis: business rule task has no detail")
	}
	inputs, err := dmn.BuildInputs(store, elementInstanceKey, ei.ProcessInstanceKey,
		cp.Intern(detail.Inputs), detail.InputMappings)
	if err != nil {
		return Job{}, fmt.Errorf("temis: build inputs for element %d: %w", elementInstanceKey, err)
	}
	return Job{
		Connector:  cp.Intern(detail.Connector),
		DecisionID: cp.Intern(detail.DecisionId),
		Inputs:     inputs,
		Result:     cp.Intern(detail.ResultVar),
	}, nil
}

// Run evaluates a resolved decision through a registry the caller owns. The
// in-process path calls it too, so there is one definition of what a central
// decision means rather than two that drift — only which services are in reach
// differs.
func Run(ctx context.Context, j Job, reg *Registry) (Result, error) {
	if reg == nil {
		return Result{}, fmt.Errorf("temis: this job names the decision service %q, but none are configured where it runs; is this server offloading the temis kind to a worker that holds them?", j.Connector)
	}
	client, ok := reg.Client(j.Connector)
	if !ok {
		return Result{}, reg.Unresolved("temis", j.Connector)
	}
	outputs, err := client.Evaluate(ctx, j.DecisionID, j.Inputs)
	if err != nil {
		return Result{}, err
	}
	return Result{ResultVariable: j.Result, Outputs: outputs}, nil
}
