package mail

import (
	"context"
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
// worker uses it to find the connector name and message fields a mail job belongs
// to, so one handler serves every deployed process.
type ProcessLookup func(defKey uint64) *compiler.CompiledProcess

// Handler builds a job handler that performs an outbound mail connector task.
// Register it with a [job.Runner] for the reserved [compiler.MailJobTypeIndex]; the
// runner then pulls activatable mail jobs, and for each the handler resolves the
// task's connector/recipients/subject/body from the compiled process — evaluating
// any FEEL field over the instance's variables (the fx toggle, ADR-0067) — resolves
// the named connector's provider client from reg, and sends the message keyed by the
// job key so an at-least-once retry de-duplicates (ADR-0079). Returning an error
// leaves the job pending (retry, then an incident, ADR-0061); the runner completes it
// only on success.
func Handler(store *state.Store, lookup ProcessLookup, reg *Registry) job.Handler {
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
			return fmt.Errorf("mail: no compiled process for def %d", ei.ProcessDefKey)
		}
		detail := cp.ConnectorTask(cp.Node(ei.ElementId).Detail)
		name := cp.Intern(detail.Connector)
		client, ok := reg.Client(name)
		if !ok {
			return fmt.Errorf("mail: no connector registered as %q", name)
		}
		scope := ei.ProcessInstanceKey
		// Read the instance's variables once: every recipient/subject/body FEEL value
		// evaluates against them, off the hot path.
		scopeVars, err := readScopeVars(store, scope)
		if err != nil {
			return fmt.Errorf("mail: read variables for element %d: %w", j.ElementInstanceKey, err)
		}
		to := splitAddrs(resolveValue(detail.To, scope, scopeVars))
		if len(to) == 0 {
			return fmt.Errorf("mail: task resolved no recipient")
		}
		return client.Send(context.Background(), Message{
			From:      resolveValue(detail.From, scope, scopeVars),
			To:        to,
			Cc:        splitAddrs(resolveValue(detail.Cc, scope, scopeVars)),
			Bcc:       splitAddrs(resolveValue(detail.Bcc, scope, scopeVars)),
			Subject:   resolveValue(detail.MailSubject, scope, scopeVars),
			Body:      resolveValue(detail.Body, scope, scopeVars),
			MessageID: strconv.FormatUint(j.Key, 10),
		})
	}
}

// splitAddrs turns a comma-separated recipient field into a trimmed address list,
// dropping empty entries so a trailing comma or an unset optional field yields no
// phantom recipient.
func splitAddrs(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if a := strings.TrimSpace(p); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// builtinProcessInstanceKey is the reserved FEEL name that binds to the instance's
// own key (mirrors the engine/REST builtin), so a recipient/subject/body expression
// can reference processInstanceKey.
const builtinProcessInstanceKey = "processInstanceKey"

// resolveValue turns a mail field value into a string: a literal verbatim, or a FEEL
// expression evaluated over the scope's variables and coerced to its string form. A
// FEEL null — an absent variable or a failed evaluation — becomes the empty string,
// matching the engine's null-propagating contract (as the REST worker's fields do).
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
// (mirrors the REST worker).
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
