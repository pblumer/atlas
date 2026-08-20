// Package script is Atlas's in-process worker for polyglot script tasks
// (PowerShell, Python, JavaScript — ADR-0047). A script task compiles to a job
// carrying the language's reserved job type (compiler.PwshJobType /
// PythonJobType / JsJobType); this worker subscribes to a type via a [job.Runner]
// and for each job runs the script off the processor goroutine — after fsync —
// through an [Exec], then writes the script's result back into the instance as the
// task's result variable.
//
// Keeping the interpreter here, not in an engine behavior, is what lets a general
// -purpose language run without breaking the engine's invariants: the processor
// stays allocation-free (I1) and side-effect-free on the recovery path (I4), and
// the result is frozen into the job completion so replay re-applies it verbatim
// (I6). It mirrors the DMN worker (ADR-0014): one handler per language serves every
// deployed process, resolving each job's script from the compiled process. The
// handler itself is language-agnostic; only the [Exec] differs per language.
package script

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// ProcessLookup resolves a process-definition key to its compiled process. The
// worker uses it to find the script source and result variable a job belongs to,
// so one handler serves every deployed process.
type ProcessLookup func(defKey uint64) *compiler.CompiledProcess

// Exec runs a script off the hot path and returns its result. It is the seam
// between the worker and the real interpreter: [CmdExec] shells out to the
// language's interpreter, but tests inject a deterministic fake so they need no
// interpreter installed. input is the instance's variables keyed by name; the
// returned value is the script's result, a JSON-shaped Go value (nil, bool,
// json.Number, string, []any, or map[string]any) that becomes the result variable.
type Exec interface {
	Run(ctx context.Context, source string, input map[string]any) (any, error)
}

// Handler builds a job handler that runs a script task in one language. Register it
// with a [job.Runner] via HandleWithOutput for that language's reserved job type
// (e.g. [compiler.PwshJobTypeIndex]); the runner then pulls activatable script
// jobs, and for each the handler:
//
//   - resolves the script source and result variable from the compiled process,
//   - reads the instance's variables as the script's inputs,
//   - runs the script through exec (off the processor goroutine, after fsync), and
//   - returns the result as the process variable named by the result variable,
//     which the job completion writes back into the instance so a downstream
//     gateway can route on it.
//
// Returning an error leaves the job pending, exactly as for any worker; the runner
// completes it only on success.
func Handler(store state.Reader, lookup ProcessLookup, exec Exec) job.OutputHandler {
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
			return nil, fmt.Errorf("script: no compiled process for def %d", ei.ProcessDefKey)
		}
		detail := cp.ScriptJobTask(cp.Node(ei.ElementId).Detail)
		input, err := scopeVars(store, j.ElementInstanceKey)
		if err != nil {
			return nil, fmt.Errorf("script: read variables for element %d: %w", j.ElementInstanceKey, err)
		}
		result, err := exec.Run(context.Background(), cp.Intern(detail.Source), input)
		if err != nil {
			return nil, fmt.Errorf("script: run script for element %d: %w", j.ElementInstanceKey, err)
		}
		if resultVar := cp.Intern(detail.ResultVar); resultVar != "" {
			return []model.VariableValue{outputVariable(resultVar, result)}, nil
		}
		return nil, nil
	}
}

// maxScopeDepth bounds the scope-chain walk, a defensive guard against a cyclic or
// corrupt FlowScopeKey chain. Real nesting (activity-local scopes over the process
// scope) is far shallower. It mirrors the engine's own guard (engine/scope.go).
const maxScopeDepth = 64

// scopeVars reads the variables a script sees into a JSON-ready map, resolving up
// the element instance's scope chain: its activity-local scope first (any
// input-mapped locals), then the enclosing scope(s) up to the process scope, with
// the nearest scope winning on a name clash (ADR-0068). A script task with no input
// mappings has an empty local scope, so this degenerates to reading the process
// scope, exactly as before. The chain is walked via each scope's element instance's
// FlowScopeKey; the process-instance root has no element instance, which ends it.
func scopeVars(store state.Reader, elementInstanceKey uint64) (map[string]any, error) {
	vars := map[string]any{}
	scope := elementInstanceKey
	for depth := 0; depth <= maxScopeDepth; depth++ {
		if err := store.VariablesOfScope(scope, func(v *model.VariableValue) error {
			if _, seen := vars[v.Name]; !seen { // a nearer scope already bound this name; it wins
				vars[v.Name] = varToAny(v)
			}
			return nil
		}); err != nil {
			return nil, err
		}
		ei, ok, err := store.GetElementInstance(scope)
		if err != nil {
			return nil, err
		}
		if !ok || ei.FlowScopeKey == 0 || ei.FlowScopeKey == scope {
			break // reached the process-instance root (no element instance) or a chain end
		}
		scope = ei.FlowScopeKey
	}
	return vars, nil
}

// varToAny maps a stored variable to its JSON-ready Go value. A number keeps its
// exact canonical decimal text via json.Number rather than a float, so large or
// high-precision numbers survive intact; a structured value (VarJSON) is re-parsed
// so it nests as a real object/array rather than a JSON-in-a-string blob.
func varToAny(v *model.VariableValue) any {
	switch v.Kind {
	case model.VarBool:
		return v.Bool
	case model.VarNumber:
		return json.Number(v.Text)
	case model.VarString:
		return v.Text
	case model.VarJSON:
		dec := json.NewDecoder(strings.NewReader(v.Text))
		dec.UseNumber()
		var out any
		if err := dec.Decode(&out); err != nil {
			return nil
		}
		return out
	default:
		return nil
	}
}

// outputVariable turns a script's result into the process variable named by the
// task's result variable. The value is canonicalized through the same expr path as
// any other variable (Classify over FromJSON), so it round-trips on replay
// (invariant I6) exactly like a FEEL script task's or DMN decision's result.
func outputVariable(name string, result any) model.VariableValue {
	kind, b, text := expr.Classify(expr.FromJSON(result))
	return model.VariableValue{Name: name, Kind: toVarKind(kind), Bool: b, Text: text}
}

// toVarKind maps an expr kind to the model's stored variable kind (mirrors the
// mapping the engine and DMN worker keep, so the enums evolve independently).
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
