package sharepoint

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

// ProcessLookup resolves a process-definition key to its compiled process. The
// worker uses it to find the worker name, operation and authored values a SharePoint
// job belongs to, so one handler serves every deployed process.
type ProcessLookup func(defKey uint64) *compiler.CompiledProcess

// Handler builds a job handler that performs a SharePoint worker task. Register it
// with a [job.Runner] under both reserved SharePoint job types —
// [compiler.SharePointJobTypeIndex] and [compiler.SharePointOpsJobTypeIndex] — via
// HandleWithOutput; the runner then pulls activatable SharePoint jobs, and for each
// the handler resolves the task's connector, operation and values from the compiled
// process — evaluating any FEEL value over the variables the task sees, up its scope
// chain (the fx toggle, ADR-0067) — resolves the named worker's Graph client from reg,
// performs the one operation, and (when the task names a result variable) returns what
// Graph answered as that variable to be written back on completion. Returning an error
// leaves the job pending (retry, then an incident, ADR-0061); the runner completes it
// only on success.
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
			return nil, fmt.Errorf("sharepoint: no compiled process for def %d", ei.ProcessDefKey)
		}
		detail, err := cp.ConnectorTaskOf(ei.ElementId)
		if err != nil {
			return nil, fmt.Errorf("sharepoint: %w", err)
		}
		task, err := Resolve(store, cp, detail, ei, j.ElementInstanceKey, j.Key)
		if err != nil {
			return nil, err
		}
		// The same Resolve/Run pair the worker uses (ADR-0168), so relocating the work
		// cannot change what a resolved SharePoint task means — only which instances
		// the running process holds.
		res, err := Run(context.Background(), task, reg)
		if err != nil {
			return nil, err
		}
		return res.Variables(), nil
	}
}

// builtinProcessInstanceKey is the reserved FEEL name that binds to the instance's
// own key (mirrors the engine/REST builtin), so any authored expression can reference
// processInstanceKey.
const builtinProcessInstanceKey = "processInstanceKey"

// resolveValue turns a worker field value into a string: a literal verbatim, or a
// FEEL expression evaluated over the scope's variables and coerced to its string
// form. A FEEL null — an absent variable or a failed evaluation — becomes the empty
// string, matching the engine's null-propagating contract (as the REST worker's
// fields do).
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

// resolveFields resolves the task's column values, keeping each value's JSON shape
// rather than flattening everything to a string.
//
// The shape is what Graph reads a column from: a Number column wants a number, a Yes/No
// column a boolean, a multi-choice column a list, and a lookup a number under its
// <Name>LookupId — none of which survives being turned into its text. The shape follows
// from the FEEL value's *kind*, not from what its text looks like, for the reason the
// Discord worker gives: parsing a resolved string as JSON would turn a title that
// happened to begin with "[" into a different kind of value. A literal stays the text
// it was written as, and a FEEL null — an absent variable or a failed evaluation — is
// sent as null, which clears the column rather than writing an empty string into it.
func resolveFields(kvs []compiler.RestKV, piKey uint64, scopeVars map[string]model.VariableValue) map[string]any {
	if len(kvs) == 0 {
		return nil
	}
	out := make(map[string]any, len(kvs))
	for _, kv := range kvs {
		out[kv.Name] = resolveField(kv.Val, piKey, scopeVars)
	}
	return out
}

// resolveField resolves one column value to the JSON value it is sent as.
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
			// Classify said JSON, so this cannot normally happen; sending the text is
			// still better than sending nothing and calling the column set.
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

// itemVariable turns the created item's decoded JSON into the process variable named
// by the task's result variable. The value is canonicalized through the same expr
// path as any other variable (a scalar stays a scalar, an object/array becomes a
// structured VarJSON), so it round-trips on replay exactly like a REST response
// (mirrors the REST worker's responseVariable).
func itemVariable(name string, body any) model.VariableValue {
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
