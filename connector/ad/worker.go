package ad

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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
	attrObjectClass       = "objectClass"
	uacNormalAccount      = 0x200
	uacAccountDisableFlag = 0x2
)

// Default object classes for the two create operations. AD requires objectClass on
// every entry and rejects an add without it, so the connector supplies the standard
// chain when the authored attributes do not — a forgotten objectClass is the single
// most common way a first create-user fails, and it is not a business decision worth
// making the author repeat. An authored objectClass always wins.
var (
	userObjectClass  = []string{"top", "person", "organizationalPerson", "user"}
	groupObjectClass = []string{"top", "group"}
	// A contact carries no account: it is a directory entry that exists to hold an
	// address, which is what a cross-forest address-book entry is.
	contactObjectClass = []string{"top", "person", "organizationalPerson", "contact"}
)

// Handler builds a job handler that performs an Active Directory connector task.
// Register it with a [job.Runner] under the reserved [compiler.AdJobTypeIndex] via
// HandleWithOutput; the runner then pulls activatable AD jobs, and for each the handler
// resolves the task's url / bind DN / operation / DNs from the compiled process, dials
// and binds through dialer — evaluating any FEEL values over the instance's variables
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
		task, err := Resolve(store, cp, detail, ei, j.ElementInstanceKey)
		if err != nil {
			return nil, err
		}
		// The same Resolve/Run pair the worker uses (ADR-0168), so relocating the work
		// cannot change what a resolved AD task means — only which process holds the
		// bind password behind the reference.
		out, err := Run(context.Background(), task, dialer, secret)
		if err != nil {
			return nil, err
		}
		return variablesFrom(out), nil
	}
}

// dispatch performs the resolved AD operation over the bound connection.
func dispatch(j Job, conn Conn) error {
	dn, op := j.DN, j.Operation
	switch op {
	case "create-user", "create-group", "create-contact":
		attrs := j.Attributes
		// An empty entry object is refused rather than created. It is almost always a
		// FEEL variable that resolved to nothing — a misspelled entryVariable, or a
		// script task that did not run — and the alternatives are both worse than
		// saying so: creating an account carrying nothing but an objectClass leaves a
		// nameless entry in a real directory, and writing the default class into a nil
		// map panics the worker outright, which is what this used to do.
		if len(attrs) == 0 {
			return fmt.Errorf("ad: %s resolved no attributes; the entry object is empty or the variable naming it is not set", op)
		}
		defaultClass := userObjectClass
		switch op {
		case "create-group":
			defaultClass = groupObjectClass
		case "create-contact":
			defaultClass = contactObjectClass
		}
		if _, authored := attrs[attrObjectClass]; !authored {
			attrs[attrObjectClass] = defaultClass
		}
		return conn.Add(dn, attrs)
	case "update-attributes":
		attrs := j.Attributes
		if len(attrs) == 0 {
			return fmt.Errorf("ad: update-attributes resolved no attributes to change")
		}
		// Replace, not add: the authored object states what each named attribute
		// should now be. Attributes it does not mention are left alone, so an update
		// is a change to some of an entry rather than a redefinition of all of it.
		mods := make([]Mod, 0, len(attrs))
		for _, name := range sortedKeys(attrs) {
			mods = append(mods, Mod{Op: modReplace, Attr: name, Vals: attrs[name]})
		}
		return conn.Modify(dn, mods)
	case "move":
		newDN := j.NewDN
		if newDN == "" {
			return fmt.Errorf("ad: move resolved an empty newDN")
		}
		rdn, superior := splitDN(newDN)
		if rdn == "" {
			return fmt.Errorf("ad: move newDN %q has no relative name", newDN)
		}
		return conn.ModifyDN(dn, rdn, superior)
	case "delete":
		return conn.Delete(dn)
	case "set-password":
		newPassword := j.NewPassword
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
		return conn.Modify(dn, []Mod{{Op: modAdd, Attr: attrMember, Vals: []string{j.MemberDN}}})
	case "remove-group-member":
		return conn.Modify(dn, []Mod{{Op: modDelete, Attr: attrMember, Vals: []string{j.MemberDN}}})
	default:
		return fmt.Errorf("ad: unknown operation %q", op)
	}
}

// variablesFrom turns what an operation completed with into process variables. Only
// sync produces any: every other AD operation's effect is in the directory.
func variablesFrom(out map[string]any) []model.VariableValue {
	if len(out) == 0 {
		return nil
	}
	names := make([]string, 0, len(out))
	for name := range out {
		names = append(names, name)
	}
	sort.Strings(names) // a stable order, so a replay writes the same events
	vars := make([]model.VariableValue, 0, len(names))
	for _, name := range names {
		vars = append(vars, responseVariable(name, out[name]))
	}
	return vars
}

// responseVariable encodes a value as the process variable it becomes: a plain string
// stays a string (the cookie), and anything structured becomes JSON so the result
// nests real objects and arrays rather than a stringified blob.
func responseVariable(name string, body any) model.VariableValue {
	if s, ok := body.(string); ok {
		return model.VariableValue{Name: name, Kind: model.VarString, Text: s}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return model.VariableValue{Name: name, Kind: model.VarNull}
	}
	return model.VariableValue{Name: name, Kind: model.VarJSON, Text: string(raw)}
}

// sortedKeys returns a map's keys in a stable order, so an update sends its changes
// the same way every time — a replayed job (delivery is at-least-once) then produces
// an identical request rather than one that merely means the same thing.
func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
