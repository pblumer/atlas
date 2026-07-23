package dmn

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// Result is one evaluated business rule task's outcome, delivered to the optional
// sink a [Handler] is built with. The decision's outputs are written back into the
// instance as process variables (see Handler); the sink is an additional
// observation seam for tests and diagnostics, not the primary output path.
type Result struct {
	ElementInstanceKey uint64
	ProcessDefKey      uint64
	DecisionId         string
	Outputs            map[string]any
}

// ProcessLookup resolves a process-definition key to its compiled process. The
// worker uses it to find the decision, inputs, and result variable a
// business-rule job belongs to, so one handler serves every deployed process.
type ProcessLookup func(defKey uint64) *compiler.CompiledProcess

// builtinProcessInstanceKey mirrors the engine's reserved FEEL identifier: an
// input mapping may read processInstanceKey to feed the instance's own key (as a
// string, so the full 64-bit value survives) into a decision.
const builtinProcessInstanceKey = "processInstanceKey"

// Handler builds a job handler that evaluates the DMN decision behind a business
// rule task. Register it with a [job.Runner] via HandleWithOutput for the reserved
// DMN job type ([compiler.DMNJobTypeIndex]); the runner then pulls activatable
// business-rule jobs, and for each the handler:
//
//   - resolves the decision, static inputs, input mappings, and result variable
//     from the compiled process,
//   - builds the decision's input context by merging the static inputs with the
//     input mappings evaluated over the instance's live variables (a mapping wins
//     over a static input of the same name),
//   - evaluates the decision through reg, and
//   - returns the result as the process variable named by resultVariable, which
//     the job completion writes back into the instance so a downstream gateway can
//     route on it.
//
// Evaluation is a post-durability side effect off the processor goroutine
// (invariant I2/I4). Returning an error leaves the job pending, exactly as for any
// worker. sink, if non-nil, additionally observes each result.
func Handler(store *state.Store, lookup ProcessLookup, reg *Registry, sink func(Result)) job.OutputHandler {
	return func(j job.Job) ([]model.VariableValue, error) {
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

		inputs, err := buildInputs(store, ei.ProcessInstanceKey, cp.Intern(detail.Inputs), detail.InputMappings)
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
		if resultVar := cp.Intern(detail.ResultVar); resultVar != "" {
			return []model.VariableValue{outputVariable(resultVar, outputs)}, nil
		}
		return nil, nil
	}
}

// buildInputs assembles a decision's input context: the static constant inputs as
// a base, overlaid with the input mappings evaluated over the instance's live
// variables (a mapping overrides a static input of the same name). A nil result
// evaluates every referenced decision input to FEEL null.
func buildInputs(store *state.Store, scope uint64, staticJSON string, mappings []compiler.DecisionInputMapping) (map[string]any, error) {
	base, err := decodeInputs(staticJSON)
	if err != nil {
		return nil, err
	}
	if len(mappings) == 0 {
		return base, nil
	}
	scopeVars, err := readScopeVars(store, scope)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(base)+len(mappings))
	for k, v := range base {
		out[k] = v
	}
	for _, m := range mappings {
		out[m.Target] = evalMapping(scope, scopeVars, m.Source)
	}
	return out, nil
}

// evalMapping evaluates one input mapping's FEEL source over the scope's variables
// and returns its Go value for the decision context. A failed evaluation yields
// nil (FEEL null), matching the engine's null-propagating script-task behavior, so
// one bad mapping does not abort the decision.
func evalMapping(scope uint64, scopeVars map[string]model.VariableValue, source *expr.Compiled) any {
	v, err := source.Eval(bindStoredVars(scope, scopeVars, source.Inputs()))
	if err != nil {
		return nil
	}
	return feelToInput(v)
}

// readScopeVars reads all of a scope's variables into a map keyed by name, so the
// worker binds only the names each mapping reads without a per-name store lookup.
func readScopeVars(store *state.Store, scope uint64) (map[string]model.VariableValue, error) {
	vars := map[string]model.VariableValue{}
	err := store.VariablesOfScope(scope, func(v *model.VariableValue) error {
		vars[v.Name] = *v
		return nil
	})
	if err != nil {
		return nil, err
	}
	return vars, nil
}

// bindStoredVars turns the named variables from a scope into a FEEL binding. A
// name absent from the scope is left unbound (FEEL null); the reserved name
// processInstanceKey binds to the scope's own key as a string.
func bindStoredVars(scope uint64, scopeVars map[string]model.VariableValue, names []string) map[string]expr.Value {
	if len(names) == 0 {
		return nil
	}
	m := make(map[string]expr.Value, len(names))
	for _, n := range names {
		if n == builtinProcessInstanceKey {
			m[n] = expr.String(strconv.FormatUint(scope, 10))
			continue
		}
		if v, ok := scopeVars[n]; ok {
			m[n] = expr.FromStored(toExprKind(v.Kind), v.Bool, v.Text)
		}
	}
	return m
}

// feelToInput converts an evaluated FEEL value into the Go value a decision input
// context holds. It routes through canonical JSON so numbers arrive as float64 and
// structured values as map/slice — the same shape a static decisionInput produces,
// so mapped and static inputs are indistinguishable to temis. Any value with no
// JSON image degrades to nil (FEEL null), matching the null-propagating contract.
func feelToInput(v expr.Value) any {
	text, _ := expr.ToJSON(v)
	var out any
	_ = json.Unmarshal([]byte(text), &out)
	return out
}

// outputVariable turns a decision's outputs into the process variable named by the
// task's result variable. A single-output decision stores that value directly (so
// a condition reads it as a scalar); a multi-output decision stores the whole
// output map as a structured (JSON) context. The value is canonicalized through
// the same expr path as any other variable, so it round-trips on replay.
func outputVariable(name string, outputs map[string]any) model.VariableValue {
	var val any = outputs
	if len(outputs) == 1 {
		for _, v := range outputs {
			val = v
		}
	}
	kind, b, text := expr.Classify(expr.FromJSON(val))
	return model.VariableValue{Name: name, Kind: toVarKind(kind), Bool: b, Text: text}
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

// toExprKind maps a stored variable kind to the expr kind for binding it into an
// evaluation (mirrors the engine's mapping so the two enums evolve independently).
func toExprKind(k model.VarKind) expr.ValueKind {
	switch k {
	case model.VarBool:
		return expr.KindBool
	case model.VarNumber:
		return expr.KindNumber
	case model.VarString:
		return expr.KindString
	case model.VarJSON:
		return expr.KindJSON
	default:
		return expr.KindNull
	}
}

// toVarKind maps an expr kind to the model's stored variable kind (the inverse of
// toExprKind), for writing a decision result back as a variable.
func toVarKind(k expr.ValueKind) model.VarKind {
	switch k {
	case expr.KindBool:
		return model.VarBool
	case expr.KindNumber:
		return model.VarNumber
	case expr.KindString:
		return model.VarString
	case expr.KindJSON:
		return model.VarJSON
	default:
		return model.VarNull
	}
}
