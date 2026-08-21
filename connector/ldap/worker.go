package ldap

import (
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
// worker uses it to find the server, bind DN, operation, and DNs an LDAP job belongs
// to, so one handler serves every deployed process.
type ProcessLookup func(defKey uint64) *compiler.CompiledProcess

// SecretResolver returns the secret value for a reference name, or "" if unknown.
// The worker uses it to turn an LDAP task's bind-password *reference* into the actual
// credential at call time (ADR-0041), so the password never lives in the model.
type SecretResolver func(ref string) string

// Handler builds a job handler that performs a generic LDAP connector task. Register
// it with a [job.Runner] under the reserved [compiler.LdapJobTypeIndex] via
// HandleWithOutput; the runner then pulls activatable LDAP jobs, and for each the
// handler resolves the task's url / bind DN / operation / DNs from the compiled
// process, dials and binds through dialer — evaluating any FEEL values over the
// variables the task sees, up its scope chain (ADR-0067/0068), and resolving the
// bind password from a secret reference (ADR-0041) — performs the operation, and for
// a search returns the entries
// as the task's result variable. Returning an error fails the job (retry, then an
// incident, ADR-0061); the runner completes it only on success.
func Handler(store state.Reader, lookup ProcessLookup, dialer Dialer, secret SecretResolver) job.OutputHandler {
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
			return nil, fmt.Errorf("ldap: no compiled process for def %d", ei.ProcessDefKey)
		}
		detail, err := cp.ConnectorTaskOf(ei.ElementId)
		if err != nil {
			return nil, fmt.Errorf("ldap: %w", err)
		}
		op := cp.Intern(detail.LdapOp)
		piKey := ei.ProcessInstanceKey // binds the processInstanceKey builtin; not the read scope
		scopeVars, err := state.VisibleVariablesMap(store, j.ElementInstanceKey)
		if err != nil {
			return nil, fmt.Errorf("ldap: read variables for element %d: %w", j.ElementInstanceKey, err)
		}
		serverURL := resolveValue(detail.LdapURL, piKey, scopeVars)
		if serverURL == "" {
			return nil, fmt.Errorf("ldap: %s has an empty url", op)
		}
		bindDN := resolveValue(detail.LdapBindDN, piKey, scopeVars)
		bindPassword := ""
		if ref := cp.Intern(detail.LdapBindSecret); ref != "" {
			bindPassword = resolveSecret(secret, ref)
			if bindPassword == "" {
				return nil, fmt.Errorf("ldap: bind secret %q is not configured (set ATLAS_CONNECTOR_<REF>_TOKEN)", ref)
			}
		}
		conn, err := dialer.Dial(serverURL, bindDN, bindPassword, detail.LdapStartTLS)
		if err != nil {
			return nil, err
		}
		defer conn.Close()
		return dispatch(cp, detail, op, piKey, scopeVars, conn)
	}
}

// dispatch performs the authored operation over the bound connection and returns a
// search's result variable (nil for the mutating operations, which write nothing
// back).
func dispatch(cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, op string, piKey uint64, scopeVars map[string]model.VariableValue, conn Conn) ([]model.VariableValue, error) {
	switch op {
	case "search":
		entries, err := conn.Search(SearchRequest{
			BaseDN: resolveValue(detail.LdapBaseDN, piKey, scopeVars),
			Scope:  cp.Intern(detail.LdapScope),
			Filter: resolveValue(detail.LdapFilter, piKey, scopeVars),
		})
		if err != nil {
			return nil, err
		}
		resultVar := cp.Intern(detail.ResultVar)
		if resultVar == "" {
			return nil, nil // the model discards the entries
		}
		return []model.VariableValue{responseVariable(resultVar, entriesToJSON(entries))}, nil
	case "add":
		attrs, err := attrsFromVar(cp.Intern(detail.LdapEntryVar), scopeVars)
		if err != nil {
			return nil, err
		}
		return nil, conn.Add(resolveValue(detail.LdapDN, piKey, scopeVars), attrs)
	case "modify":
		attrs, err := attrsFromVar(cp.Intern(detail.LdapEntryVar), scopeVars)
		if err != nil {
			return nil, err
		}
		return nil, conn.Modify(resolveValue(detail.LdapDN, piKey, scopeVars), attrs)
	case "delete":
		return nil, conn.Delete(resolveValue(detail.LdapDN, piKey, scopeVars))
	case "modify-password":
		newPassword := resolveValue(detail.LdapNewPassword, piKey, scopeVars)
		if newPassword == "" {
			return nil, fmt.Errorf("ldap: modify-password resolved an empty newPassword")
		}
		return nil, conn.SetPassword(resolveValue(detail.LdapDN, piKey, scopeVars), newPassword)
	default:
		return nil, fmt.Errorf("ldap: unknown operation %q", op)
	}
}

// entriesToJSON turns search entries into a JSON-ready slice: each entry is
// {"dn": …, "attributes": {name: [values]}}, so the result variable nests real
// objects/arrays rather than a stringified blob.
func entriesToJSON(entries []Entry) any {
	out := make([]any, 0, len(entries))
	for _, e := range entries {
		attrs := make(map[string]any, len(e.Attributes))
		for name, vals := range e.Attributes {
			vs := make([]any, len(vals))
			for i, v := range vals {
				vs[i] = v
			}
			attrs[name] = vs
		}
		out = append(out, map[string]any{"dn": e.DN, "attributes": attrs})
	}
	return out
}

// attrsFromVar reads the add/modify attribute object from a named process variable
// (a JSON object whose values are strings, arrays of strings, numbers, or booleans)
// and coerces it into LDAP's multi-valued attribute map. A missing variable or a
// non-object value is a modeling error that fails the job.
func attrsFromVar(varName string, scopeVars map[string]model.VariableValue) (map[string][]string, error) {
	v, ok := scopeVars[varName]
	if !ok {
		return nil, fmt.Errorf("ldap: entry variable %q is not set on the instance", varName)
	}
	obj, ok := varToAny(&v).(map[string]any)
	if !ok {
		return nil, fmt.Errorf("ldap: entry variable %q must be a JSON object of attributes", varName)
	}
	attrs := make(map[string][]string, len(obj))
	for name, raw := range obj {
		attrs[name] = toStrings(raw)
	}
	return attrs, nil
}

// toStrings coerces one attribute value into LDAP's string-valued form: a slice
// becomes its stringified elements, and a scalar (string/number/bool) a single value.
func toStrings(raw any) []string {
	switch t := raw.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			out = append(out, scalarString(e))
		}
		return out
	default:
		return []string{scalarString(raw)}
	}
}

// scalarString renders a scalar JSON value as an LDAP attribute string.
func scalarString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		return strconv.FormatBool(t)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}

// resolveSecret turns a secret reference into its value through the resolver,
// trimming surrounding whitespace; a nil resolver or unknown reference yields "".
func resolveSecret(secret SecretResolver, ref string) string {
	if secret == nil {
		return ""
	}
	return strings.TrimSpace(secret(ref))
}

// builtinProcessInstanceKey is the reserved FEEL name that binds to the instance's
// own key, so a url/dn/filter expression can reference processInstanceKey.
const builtinProcessInstanceKey = "processInstanceKey"

// resolveValue turns an LDAP field value into a string: a literal verbatim, or a FEEL
// expression evaluated over the scope's variables and coerced to its string form. A
// FEEL null (absent variable or failed evaluation) becomes "".
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

// bindVars turns the named variables the task sees into a FEEL binding. An absent name
// is left unbound (FEEL null); processInstanceKey binds to the process instance's key.
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

// toExprKind maps a stored variable kind to the expr kind for binding.
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

// responseVariable turns a decoded JSON value into the named process variable,
// canonicalized through the same expr path as any other variable so it round-trips on
// replay (ADR-0014/0066).
func responseVariable(name string, body any) model.VariableValue {
	kind, b, text := expr.Classify(expr.FromJSON(body))
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

// varToAny maps a stored variable to its JSON-ready Go value. A structured value
// (VarJSON) is re-parsed so an attribute object nests as a real map.
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
