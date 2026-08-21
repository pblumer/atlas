package ad

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

// ProcessLookup resolves a process-definition key to its compiled process.
type ProcessLookup func(defKey uint64) *compiler.CompiledProcess

// SecretResolver returns the secret value for a reference name, or "" if unknown. The
// worker uses it to turn an AD task's bind-password *reference* into the credential at
// call time (ADR-0041), so the password never lives in the model.
type SecretResolver func(ref string) string

// AD attribute and userAccountControl constants. uacNormalAccount (512) is the base
// flag for an ordinary enabled user; uacAccountDisable (0x2) is the bit that disables
// an account (Microsoft's ADS_UF_* flags).
const (
	attrUnicodePwd        = "unicodePwd"
	attrUserAccountCtl    = "userAccountControl"
	attrMember            = "member"
	uacNormalAccount      = 0x200
	uacAccountDisableFlag = 0x2
)

// Handler builds a job handler that performs an Active Directory connector task.
// Register it with a [job.Runner] under the reserved [compiler.AdJobTypeIndex] via
// HandleWithOutput; the runner then pulls activatable AD jobs, and for each the handler
// resolves the task's url / bind DN / operation / DNs from the compiled process, dials
// and binds through dialer — evaluating any FEEL values over the variables the task
// sees, up its scope chain (ADR-0068)
// (ADR-0067) and resolving the bind password from a secret reference (ADR-0041) — and
// performs the operation. Returning an error fails the job (retry, then an incident,
// ADR-0061); the runner completes it only on success.
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
			return nil, fmt.Errorf("ad: no compiled process for def %d", ei.ProcessDefKey)
		}
		detail, err := cp.ConnectorTaskOf(ei.ElementId)
		if err != nil {
			return nil, fmt.Errorf("ad: %w", err)
		}
		op := cp.Intern(detail.AdOp)
		piKey := ei.ProcessInstanceKey // binds the processInstanceKey builtin; not the read scope
		scopeVars, err := state.VisibleVariablesMap(store, j.ElementInstanceKey)
		if err != nil {
			return nil, fmt.Errorf("ad: read variables for element %d: %w", j.ElementInstanceKey, err)
		}
		serverURL := resolveValue(detail.AdURL, piKey, scopeVars)
		if serverURL == "" {
			return nil, fmt.Errorf("ad: %s has an empty url", op)
		}
		bindDN := resolveValue(detail.AdBindDN, piKey, scopeVars)
		bindPassword := ""
		if ref := cp.Intern(detail.AdBindSecret); ref != "" {
			bindPassword = resolveSecret(secret, ref)
			if bindPassword == "" {
				return nil, fmt.Errorf("ad: bind secret %q is not configured (set ATLAS_CONNECTOR_<REF>_TOKEN)", ref)
			}
		}
		conn, err := dialer.Dial(serverURL, bindDN, bindPassword, detail.AdStartTLS)
		if err != nil {
			return nil, err
		}
		defer conn.Close()
		return nil, dispatch(cp, detail, op, piKey, scopeVars, conn)
	}
}

// dispatch performs the authored AD operation over the bound connection.
func dispatch(cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, op string, piKey uint64, scopeVars map[string]model.VariableValue, conn Conn) error {
	dn := resolveValue(detail.AdDN, piKey, scopeVars)
	switch op {
	case "create-user":
		attrs, err := attrsFromVar(cp.Intern(detail.AdEntryVar), scopeVars)
		if err != nil {
			return err
		}
		return conn.Add(dn, attrs)
	case "set-password":
		newPassword := resolveValue(detail.AdNewPassword, piKey, scopeVars)
		if newPassword == "" {
			return fmt.Errorf("ad: set-password resolved an empty newPassword")
		}
		return conn.Modify(dn, []Mod{{Op: modReplace, Attr: attrUnicodePwd, Vals: []string{encodePassword(newPassword)}}})
	case "enable", "disable":
		current, err := conn.ReadAttr(dn, attrUserAccountCtl)
		if err != nil {
			return err
		}
		return conn.Modify(dn, []Mod{{Op: modReplace, Attr: attrUserAccountCtl, Vals: []string{accountControl(current, op == "disable")}}})
	case "add-group-member":
		return conn.Modify(dn, []Mod{{Op: modAdd, Attr: attrMember, Vals: []string{resolveValue(detail.AdMemberDN, piKey, scopeVars)}}})
	case "remove-group-member":
		return conn.Modify(dn, []Mod{{Op: modDelete, Attr: attrMember, Vals: []string{resolveValue(detail.AdMemberDN, piKey, scopeVars)}}})
	default:
		return fmt.Errorf("ad: unknown operation %q", op)
	}
}

// accountControl computes the new userAccountControl value for an enable/disable: it
// starts from the entry's current value (or a normal-account baseline when absent or
// unparseable) and sets or clears the ACCOUNTDISABLE bit, leaving every other flag
// intact. Returned as its decimal string, the form AD stores.
func accountControl(current []string, disable bool) string {
	base := uacNormalAccount
	if len(current) > 0 {
		if n, err := strconv.Atoi(strings.TrimSpace(current[0])); err == nil {
			base = n
		}
	}
	if disable {
		base |= uacAccountDisableFlag
	} else {
		base &^= uacAccountDisableFlag
	}
	return strconv.Itoa(base)
}

// attrsFromVar reads the create-user attribute object from a named process variable (a
// JSON object whose values are strings, arrays of strings, numbers, or booleans) and
// coerces it into an LDAP multi-valued attribute map.
func attrsFromVar(varName string, scopeVars map[string]model.VariableValue) (map[string][]string, error) {
	v, ok := scopeVars[varName]
	if !ok {
		return nil, fmt.Errorf("ad: entry variable %q is not set on the instance", varName)
	}
	obj, ok := varToAny(&v).(map[string]any)
	if !ok {
		return nil, fmt.Errorf("ad: entry variable %q must be a JSON object of attributes", varName)
	}
	attrs := make(map[string][]string, len(obj))
	for name, raw := range obj {
		attrs[name] = toStrings(raw)
	}
	return attrs, nil
}

// toStrings coerces one attribute value into LDAP's string-valued form.
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

// resolveSecret turns a secret reference into its value through the resolver, trimming
// whitespace; a nil resolver or unknown reference yields "".
func resolveSecret(secret SecretResolver, ref string) string {
	if secret == nil {
		return ""
	}
	return strings.TrimSpace(secret(ref))
}

// builtinProcessInstanceKey is the reserved FEEL name that binds to the instance's own
// key, so a url/dn expression can reference processInstanceKey.
const builtinProcessInstanceKey = "processInstanceKey"

// resolveValue turns an AD field value into a string: a literal verbatim, or a FEEL
// expression evaluated over the scope's variables. A FEEL null becomes "".
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

// bindVars turns the named variables the task sees into a FEEL binding.
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
