package webscrape

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
// worker uses it to find the URL, selector, attribute, and result variable a scrape
// job belongs to, so one handler serves every deployed process.
type ProcessLookup func(defKey uint64) *compiler.CompiledProcess

// Handler builds a job handler that performs a web-scraping connector task. Register
// it with a [job.Runner] under the reserved [compiler.WebScrapeJobTypeIndex] via
// HandleWithOutput; the runner then pulls activatable scrape jobs, and for each the
// handler resolves the connector task's url/selector/attribute/result-variable from
// the compiled process — evaluating any FEEL url/selector values over the instance's
// variables (the fx toggle, ADR-0067) — fetches the page through client, and returns
// the extracted values as the result variable (a JSON array of strings) to be written
// back into the instance on completion. Returning an error fails the job (retry, then
// an incident, ADR-0061); the runner completes it only on success.
func Handler(store state.Reader, lookup ProcessLookup, client Client) job.OutputHandler {
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
			return nil, fmt.Errorf("webscrape: no compiled process for def %d", ei.ProcessDefKey)
		}
		detail, err := cp.ConnectorTaskOf(ei.ElementId)
		if err != nil {
			return nil, fmt.Errorf("webscrape: %w", err)
		}
		// The same Resolve/Run pair a worker uses (ADR-0168). Running in the engine
		// changes only where the network reach comes from, never what a resolved
		// scrape means.
		resolved, err := Resolve(store, cp, detail, ei, j.ElementInstanceKey)
		if err != nil {
			return nil, err
		}
		res, err := Run(context.Background(), resolved, client)
		if err != nil {
			return nil, err
		}
		if res.ResultVariable == "" {
			return nil, nil // no result variable authored; nothing to write back
		}
		return []model.VariableValue{resultVariable(res.ResultVariable, res.Values)}, nil
	}
}

// builtinProcessInstanceKey is the reserved FEEL name that binds to the instance's
// own key (mirrors the engine/REST builtin), so a url/selector expression can
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

// resultVariable turns the extracted scrape values into the process variable named by
// the task's result variable: a JSON array of strings. The value is canonicalized
// through the same expr path as any other structured variable, so it round-trips on
// replay exactly like a REST response (mirrors the REST worker's responseVariable).
func resultVariable(name string, values []string) model.VariableValue {
	items := make([]any, len(values))
	for i, v := range values {
		items[i] = v
	}
	kind, b, text := expr.Classify(expr.FromJSON(items))
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
