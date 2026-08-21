package sharepoint

import (
	"context"
	"fmt"
	"strconv"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// ProcessLookup resolves a process-definition key to its compiled process. The
// worker uses it to find the connector name, site, list, and item fields a
// SharePoint job belongs to, so one handler serves every deployed process.
type ProcessLookup func(defKey uint64) *compiler.CompiledProcess

// Handler builds a job handler that performs a SharePoint connector task. Register it
// with a [job.Runner] under the reserved [compiler.SharePointJobTypeIndex] via
// HandleWithOutput; the runner then pulls activatable SharePoint jobs, and for each
// the handler resolves the task's connector/site/list/fields from the compiled
// process — evaluating any FEEL value over the variables the task sees, up its scope
// chain (the fx toggle,
// ADR-0067) — resolves the named connector's Graph client from reg, creates the list
// item, and (when the task names a result variable) returns the created item's JSON
// as that variable to be written back on completion. Returning an error leaves the
// job pending (retry, then an incident, ADR-0061); the runner completes it only on
// success.
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
		name := cp.Intern(detail.Connector)
		client, ok := reg.Client(name)
		if !ok {
			return nil, reg.Unresolved("sharepoint", name)
		}
		piKey := ei.ProcessInstanceKey // binds the processInstanceKey builtin; not the read scope
		// Read the variables the task sees once — up its scope chain, so its own
		// input-mapped locals shadow what it inherits (ADR-0068). Every site, list
		// and field FEEL value evaluates against them, off the hot path.
		scopeVars, err := state.VisibleVariablesMap(store, j.ElementInstanceKey)
		if err != nil {
			return nil, fmt.Errorf("sharepoint: read variables for element %d: %w", j.ElementInstanceKey, err)
		}
		item, err := client.CreateItem(context.Background(), ItemRequest{
			Site:      resolveValue(detail.Site, piKey, scopeVars),
			List:      resolveValue(detail.List, piKey, scopeVars),
			Fields:    resolveKVs(detail.Fields, piKey, scopeVars),
			RequestID: strconv.FormatUint(j.Key, 10),
		})
		if err != nil {
			return nil, err
		}
		resultVar := cp.Intern(detail.ResultVar)
		if resultVar == "" {
			return nil, nil // the model discards the created item
		}
		return []model.VariableValue{itemVariable(resultVar, item)}, nil
	}
}

// builtinProcessInstanceKey is the reserved FEEL name that binds to the instance's
// own key (mirrors the engine/REST builtin), so a site/list/field expression can
// reference processInstanceKey.
const builtinProcessInstanceKey = "processInstanceKey"

// resolveValue turns a connector field value into a string: a literal verbatim, or a
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

// resolveKVs resolves a list of named connector values (item fields) into a map,
// evaluating any FEEL values. Returns nil for an empty list.
func resolveKVs(kvs []compiler.RestKV, piKey uint64, scopeVars map[string]model.VariableValue) map[string]string {
	if len(kvs) == 0 {
		return nil
	}
	out := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		out[kv.Name] = resolveValue(kv.Val, piKey, scopeVars)
	}
	return out
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
