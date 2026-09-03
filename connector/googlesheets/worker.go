package googlesheets

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// ProcessLookup resolves a process-definition key to its compiled process. The worker
// uses it to find the connector name, operation and authored values a Google Sheets
// job belongs to, so one handler serves every deployed process.
type ProcessLookup func(defKey uint64) *compiler.CompiledProcess

// Handler builds a job handler that performs a Google Sheets connector task. Register
// it with a [job.Runner] under the reserved [compiler.GoogleSheetsJobTypeIndex] via
// HandleWithOutput; the runner then pulls activatable jobs, and for each the handler
// resolves the task's connector and authored values from the compiled process —
// evaluating any FEEL value over the variables the task sees, up its scope chain (the
// fx toggle, ADR-0067/0068) — resolves the named connector's client from reg, performs
// the one operation, and (when the task names a result variable and Google returned
// something) returns what Google answered as that variable to be written back on
// completion.
//
// Returning an error leaves the job pending (retry, then an incident, ADR-0061); the
// runner completes it only on success.
func Handler(store state.Reader, lookup ProcessLookup, reg *Registry) job.OutputHandler {
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
			return nil, fmt.Errorf("googlesheets: no compiled process for def %d", ei.ProcessDefKey)
		}
		detail, err := cp.ConnectorTaskOf(ei.ElementId)
		if err != nil {
			return nil, fmt.Errorf("googlesheets: %w", err)
		}
		task, err := Resolve(store, cp, detail, ei, j.ElementInstanceKey, j.Key)
		if err != nil {
			return nil, err
		}
		result, err := Run(context.Background(), task, reg)
		if err != nil {
			return nil, err
		}
		if task.ResultVariable == "" || result == nil {
			// Either the model discards the answer, or the operation is one that
			// answers with nothing. Writing a null variable for the second would make
			// "deleted" indistinguishable from "read something that was empty".
			return nil, nil
		}
		return []model.VariableValue{resultVariable(task.ResultVariable, result)}, nil
	}
}

// builtinProcessInstanceKey is the reserved FEEL name that binds to the instance's own
// key (mirrors the engine/REST builtin), so an authored value can reference
// processInstanceKey — which is what puts a traceable back-reference into a row.
const builtinProcessInstanceKey = "processInstanceKey"

// resolveValue turns an authored connector value into a string: a literal verbatim, or
// a FEEL expression evaluated over the scope's variables and coerced to its string
// form. A FEEL null — an absent variable or a failed evaluation — becomes the empty
// string, matching the engine's null-propagating contract (as the REST, SharePoint and
// Jira workers' values do).
func resolveValue(rv compiler.RestExpr, piKey uint64, scopeVars map[string]model.VariableValue) string {
	if rv.Expr == nil {
		return rv.Literal
	}
	v, err := rv.Expr.Eval(bindVars(piKey, scopeVars, rv.Expr.Inputs()))
	if err != nil {
		return ""
	}
	_, _, text := expr.Classify(v)
	return text
}

// resolveField resolves the task's values to the JSON value they should be written as,
// keeping the FEEL value's *kind* rather than flattening everything to a string.
//
// The shape follows from the kind, not from what the text looks like. A list of lists
// stays rows, a list of contexts stays objects (which [Rows] projects through the
// task's columns), a number stays a number. The alternative — trying to parse each
// resolved string as JSON — would turn a cell that happened to begin with "{" into a
// different kind of value, which is a defect nobody would find twice.
func resolveField(rv compiler.RestExpr, piKey uint64, scopeVars map[string]model.VariableValue) any {
	if rv.Expr == nil {
		return rv.Literal
	}
	v, err := rv.Expr.Eval(bindVars(piKey, scopeVars, rv.Expr.Inputs()))
	if err != nil {
		return nil
	}
	kind, b, text := expr.Classify(v)
	switch kind {
	case expr.KindBool:
		return b
	case expr.KindNumber:
		return json.Number(text)
	case expr.KindJSON:
		var decoded any
		dec := json.NewDecoder(strings.NewReader(text))
		dec.UseNumber()
		if err := dec.Decode(&decoded); err != nil {
			// Classify said JSON, so this cannot normally happen; writing the text is
			// still better than writing nothing and calling the row written.
			return text
		}
		return decoded
	case expr.KindNull:
		return nil
	default:
		return text
	}
}

// bindVars turns the named variables the task sees into a FEEL binding. A name absent
// from the chain is left unbound (FEEL null); the reserved name processInstanceKey
// binds to the process instance's key as a string.
func bindVars(piKey uint64, scopeVars map[string]model.VariableValue, names []string) map[string]expr.Value {
	if len(names) == 0 {
		return nil
	}
	m := make(map[string]expr.Value, len(names))
	for _, n := range names {
		if n == builtinProcessInstanceKey {
			m[n] = expr.String(strconv.FormatUint(piKey, 10))
			continue
		}
		if v, ok := scopeVars[n]; ok {
			m[n] = expr.FromStored(toExprKind(v.Kind), v.Bool, v.Text)
		}
	}
	return m
}

// toExprKind maps a stored variable kind to the expr kind for binding it into an
// evaluation (mirrors the REST worker's mapping so the two enums evolve independently).
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

// resultVariable turns what Google returned into the process variable named by the
// task's result variable. The value is canonicalized through the same expr path as any
// other variable (a scalar stays a scalar, an object or array becomes a structured
// VarJSON), so it round-trips on replay exactly like a REST response.
func resultVariable(name string, body any) model.VariableValue {
	kind, b, text := expr.Classify(expr.FromJSON(body))
	return model.VariableValue{Name: name, Kind: toVarKind(kind), Bool: b, Text: text}
}

// toVarKind maps an expr value kind to the stored variable kind (mirrors the REST
// worker's mapping so the two enums evolve independently).
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
