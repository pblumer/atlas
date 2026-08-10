package remedy

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
// worker uses it to find the connector name, form, and field values a Remedy job
// belongs to, so one handler serves every deployed process.
type ProcessLookup func(defKey uint64) *compiler.CompiledProcess

// Handler builds a job handler that performs a BMC Remedy connector task. Register it
// with a [job.Runner] for the reserved [compiler.RemedyJobTypeIndex] via
// HandleWithOutput; the runner then pulls activatable Remedy jobs, and for each the
// handler resolves the task's connector/form/fields from the compiled process —
// evaluating any FEEL field over the instance's variables (the fx toggle, ADR-0067) —
// resolves the named connector's AR System client from reg, and creates the entry
// keyed by the job key so an at-least-once retry carries the same X-Request-ID
// (ADR-0106). When the task names a result variable, the created entry's id is
// returned as that variable to be written back into the instance on completion.
// Returning an error leaves the job pending (retry, then an incident, ADR-0061); the
// runner completes it only on success.
func Handler(store *state.Store, lookup ProcessLookup, reg *Registry) job.OutputHandler {
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
			return nil, fmt.Errorf("remedy: no compiled process for def %d", ei.ProcessDefKey)
		}
		detail := cp.ConnectorTask(cp.Node(ei.ElementId).Detail)
		name := cp.Intern(detail.Connector)
		client, ok := reg.Client(name)
		if !ok {
			return nil, fmt.Errorf("remedy: no connector registered as %q", name)
		}
		scope := ei.ProcessInstanceKey
		// Read the instance's variables once: the form and every field FEEL value
		// evaluate against them, off the hot path.
		scopeVars, err := readScopeVars(store, scope)
		if err != nil {
			return nil, fmt.Errorf("remedy: read variables for element %d: %w", j.ElementInstanceKey, err)
		}
		form := resolveValue(detail.RemedyForm, scope, scopeVars)
		if form == "" {
			return nil, fmt.Errorf("remedy: task resolved no form")
		}
		values := make(map[string]any, len(detail.RemedyFields))
		for _, f := range detail.RemedyFields {
			values[f.Name] = resolveValue(f.Val, scope, scopeVars)
		}
		res, err := client.CreateEntry(context.Background(), Entry{
			Form:      form,
			Values:    values,
			RequestID: strconv.FormatUint(j.Key, 10),
		})
		if err != nil {
			return nil, err
		}
		resultVar := cp.Intern(detail.ResultVar)
		if resultVar == "" {
			return nil, nil // the model discards the entry id
		}
		return []model.VariableValue{{Name: resultVar, Kind: model.VarString, Text: res.EntryID}}, nil
	}
}

// builtinProcessInstanceKey is the reserved FEEL name that binds to the instance's
// own key (mirrors the engine/mail/REST builtin), so a form/field expression can
// reference processInstanceKey.
const builtinProcessInstanceKey = "processInstanceKey"

// resolveValue turns a Remedy field value into a string: a literal verbatim, or a
// FEEL expression evaluated over the scope's variables and coerced to its string
// form. A FEEL null — an absent variable or a failed evaluation — becomes the empty
// string, matching the engine's null-propagating contract (as the mail worker's
// fields do).
func resolveValue(rv compiler.RestExpr, scope uint64, scopeVars map[string]model.VariableValue) string {
	if rv.Expr == nil {
		return rv.Literal
	}
	v, err := rv.Expr.Eval(bindVars(scope, scopeVars, rv.Expr.Inputs()))
	if err != nil {
		return ""
	}
	_, _, text := expr.Classify(v)
	return text
}

// readScopeVars reads all of a scope's variables into a map keyed by name, so the
// worker binds only the names each expression reads without a per-name store lookup
// (mirrors the mail worker).
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

// bindVars turns the named variables from a scope into a FEEL binding. A name absent
// from the scope is left unbound (FEEL null); the reserved name processInstanceKey
// binds to the scope's own key as a string.
func bindVars(scope uint64, scopeVars map[string]model.VariableValue, names []string) map[string]expr.Value {
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

// toExprKind maps a stored variable kind to the expr kind for binding it into an
// evaluation (mirrors the mail worker's mapping so the two enums evolve independently).
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
