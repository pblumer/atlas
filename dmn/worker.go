package dmn

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/state"
)

// Result is one evaluated business rule task's outcome, delivered to the sink a
// [Handler] is built with. Until process variables exist to receive them, the
// outputs are surfaced here rather than written back into the instance.
type Result struct {
	ElementInstanceKey uint64
	ProcessDefKey      uint64
	DecisionId         string
	Outputs            map[string]any
}

// ProcessLookup resolves a process-definition key to its compiled process. The
// worker uses it to find the decision and static inputs a business-rule job
// belongs to, so one handler serves every deployed process.
type ProcessLookup func(defKey uint64) *compiler.CompiledProcess

// Handler builds a job handler that evaluates the DMN decision behind a business
// rule task. Register it with a [job.Runner] for the reserved DMN job type
// ([compiler.DMNJobType]'s interned index); the runner then pulls activatable
// business-rule jobs, and for each the handler resolves its decision and static
// inputs from the compiled process, evaluates them through reg, and reports the
// outputs to sink (which may be nil). Returning an error leaves the job pending,
// exactly as for any worker.
func Handler(store *state.Store, lookup ProcessLookup, reg *Registry, sink func(Result)) job.Handler {
	return func(j job.Job) error {
		ei, ok, err := store.GetElementInstance(j.ElementInstanceKey)
		if err != nil {
			return err
		}
		if !ok {
			return nil // element instance gone (e.g. already completed); nothing to do
		}
		cp := lookup(ei.ProcessDefKey)
		if cp == nil {
			return fmt.Errorf("dmn: no compiled process for def %d", ei.ProcessDefKey)
		}
		detail := cp.BusinessRuleTask(cp.Node(ei.ElementId).Detail)
		decisionId := cp.Intern(detail.DecisionId)
		inputs, err := decodeInputs(cp.Intern(detail.Inputs))
		if err != nil {
			return fmt.Errorf("dmn: decode inputs for element %d: %w", j.ElementInstanceKey, err)
		}
		outputs, err := reg.Evaluate(context.Background(), cp.Key, decisionId, inputs)
		if err != nil {
			return err
		}
		if sink != nil {
			sink(Result{
				ElementInstanceKey: j.ElementInstanceKey,
				ProcessDefKey:      cp.Key,
				DecisionId:         decisionId,
				Outputs:            outputs,
			})
		}
		return nil
	}
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
