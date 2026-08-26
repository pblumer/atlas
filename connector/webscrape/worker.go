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
// worker uses it to find the resolved web-scrape configuration a job belongs to, so
// one handler serves every deployed process.
type ProcessLookup func(defKey uint64) *compiler.CompiledProcess

// Handler builds a job handler that performs a web-scraping connector task. Register
// it under [compiler.WebScrapeJobTypeIndex] via HandleWithOutput. The handler resolves
// model data against the task's visible variables, runs the network/document work off
// the processor, and returns one durable result variable. HTML remains a JSON array
// of strings; RSS/Atom are arrays of stable feed-entry objects (ADR-0190).
func Handler(store state.Reader, lookup ProcessLookup, client Client) job.OutputHandler {
	return func(j job.Job) ([]model.VariableValue, error) {
		ei, ok, err := store.GetElementInstance(j.ElementInstanceKey)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil
		}
		cp := lookup(ei.ProcessDefKey)
		if cp == nil {
			return nil, fmt.Errorf("webscrape: no compiled process for def %d", ei.ProcessDefKey)
		}
		detail, err := cp.ConnectorTaskOf(ei.ElementId)
		if err != nil {
			return nil, fmt.Errorf("webscrape: %w", err)
		}
		resolved, err := Resolve(store, cp, detail, ei, j.ElementInstanceKey)
		if err != nil {
			return nil, err
		}
		res, err := Run(context.Background(), resolved, client)
		if err != nil {
			return nil, err
		}
		if res.ResultVariable == "" {
			return nil, nil
		}
		if res.Format == formatRSS || res.Format == formatAtom {
			return []model.VariableValue{feedResultVariable(res.ResultVariable, res.Entries)}, nil
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
// form. A FEEL null becomes the empty string, matching the engine's existing
// null-propagating connector contract.
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
// from the chain is left unbound (FEEL null); processInstanceKey binds to the process
// instance key as a string.
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

// toExprKind maps a stored variable kind to the expr kind for FEEL binding.
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

// resultVariable turns HTML scrape values into the historical JSON array of strings.
func resultVariable(name string, values []string) model.VariableValue {
	items := make([]any, len(values))
	for i, v := range values {
		items[i] = v
	}
	return jsonResultVariable(name, items)
}

// feedResultVariable turns structured entries into plain JSON-shaped maps before
// passing them through expr. This keeps the process-variable boundary independent of
// Go struct encoding and guarantees all four ADR-0190 keys are present.
func feedResultVariable(name string, entries []FeedEntry) model.VariableValue {
	items := make([]any, len(entries))
	for i, entry := range entries {
		items[i] = map[string]any{
			"title":       entry.Title,
			"link":        entry.Link,
			"description": entry.Description,
			"published":   entry.Published,
		}
	}
	return jsonResultVariable(name, items)
}

func jsonResultVariable(name string, value any) model.VariableValue {
	kind, b, text := expr.Classify(expr.FromJSON(value))
	return model.VariableValue{Name: name, Kind: toVarKind(kind), Bool: b, Text: text}
}

// toVarKind maps an expr value kind to the stored variable kind.
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
