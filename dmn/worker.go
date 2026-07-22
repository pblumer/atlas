package dmn

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// Result is one evaluated business rule task's outcome, delivered to the sink a
// [Handler] is built with (which may be nil). It is an observability hook; the
// authoritative outputs are written back into the instance under the task's
// result variable.
type Result struct {
	ElementInstanceKey uint64
	ProcessDefKey      uint64
	DecisionId         string
	Outputs            map[string]any
}

// ProcessLookup resolves a process-definition key to its compiled process. The
// worker uses it to find the decision, input mappings, and result variable a
// business-rule job belongs to, so one handler serves every deployed process.
type ProcessLookup func(defKey uint64) *compiler.CompiledProcess

// Handler builds a job handler that evaluates the DMN decision behind a business
// rule task. Register it with a [job.Runner] for the reserved DMN job type
// ([compiler.DMNJobType]'s interned index). For each activatable business-rule
// job the handler:
//
//  1. resolves the decision, static inputs, input mappings, and result variable
//     from the compiled process;
//  2. builds the evaluation context from the static inputs overlaid with the
//     mapped process variables read from state;
//  3. evaluates the decision through reg; and
//  4. returns the outputs as the job's result variable (if the task names one),
//     which the processor writes back into the instance on completion.
//
// It also reports the outputs to sink for observability. Returning an error
// leaves the job pending, exactly as for any worker.
func Handler(store *state.Store, lookup ProcessLookup, reg *Registry, sink func(Result)) job.Handler {
	return func(j job.Job) ([]model.NamedVariable, error) {
		ei, ok, err := store.GetElementInstance(j.ElementInstanceKey)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil // element instance gone (e.g. already completed); nothing to do
		}
		cp := lookup(ei.ProcessDefKey)
		if cp == nil {
			return nil, fmt.Errorf("dmn: no compiled process for def %d", ei.ProcessDefKey)
		}
		detail := cp.BusinessRuleTask(cp.Node(ei.ElementId).Detail)
		decisionId := cp.Intern(detail.DecisionId)

		inputs, err := buildInputs(store, j.ProcessInstanceKey, cp, detail)
		if err != nil {
			return nil, fmt.Errorf("dmn: build inputs for element %d: %w", j.ElementInstanceKey, err)
		}
		outputs, err := reg.Evaluate(context.Background(), cp.Key, decisionId, inputs)
		if err != nil {
			return nil, err
		}
		if sink != nil {
			sink(Result{
				ElementInstanceKey: j.ElementInstanceKey,
				ProcessDefKey:      cp.Key,
				DecisionId:         decisionId,
				Outputs:            outputs,
			})
		}
		return outputVariables(cp.Intern(detail.ResultVariable), outputs)
	}
}

// buildInputs assembles a decision's evaluation context: the static inputs
// first, then the mapped process variables read from state, so a mapping wins
// over a static of the same name. A mapped variable that is absent contributes
// nothing (the decision sees it as null).
func buildInputs(store *state.Store, piKey uint64, cp *compiler.CompiledProcess, detail *compiler.BusinessRuleTaskDetail) (map[string]any, error) {
	inputs, err := decodeInputs(cp.Intern(detail.Inputs))
	if err != nil {
		return nil, err
	}
	for _, m := range detail.InputMappings {
		raw, ok, err := store.GetVariable(piKey, cp.Intern(m.Source))
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("variable %q: %w", cp.Intern(m.Source), err)
		}
		if inputs == nil {
			inputs = map[string]any{}
		}
		inputs[cp.Intern(m.Target)] = v
	}
	return inputs, nil
}

// outputVariables turns a decision's outputs into the job's result variables. If
// the task names no result variable, nothing is written back. The whole outputs
// map is stored as JSON under the result variable name.
func outputVariables(resultVar string, outputs map[string]any) ([]model.NamedVariable, error) {
	if resultVar == "" {
		return nil, nil
	}
	encoded, err := json.Marshal(outputs)
	if err != nil {
		return nil, fmt.Errorf("dmn: encode outputs for %q: %w", resultVar, err)
	}
	return []model.NamedVariable{{Name: resultVar, Value: encoded}}, nil
}

// decodeInputs turns the interned static-input JSON of a business rule task back
// into an evaluation context. An empty string (no inputs recorded) yields a nil
// map, which evaluates every referenced input to FEEL null.
func decodeInputs(encoded string) (map[string]any, error) {
	if encoded == "" {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(encoded), &m); err != nil {
		return nil, err
	}
	return m, nil
}
