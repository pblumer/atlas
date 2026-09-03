package scim

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// ProcessLookup resolves a process-definition key to its compiled process. The
// worker uses it to find the base URL, resource, operation, and result variable a
// SCIM job belongs to, so one handler serves every deployed process.
type ProcessLookup func(defKey uint64) *compiler.CompiledProcess

// SecretResolver returns the secret value for a reference name, or "" if unknown.
// The worker uses it to turn a SCIM task's authentication secret *reference* into
// the actual credential at call time (ADR-0041), so a token never lives in the
// model or the compiled process — only its reference does.
type SecretResolver func(ref string) string

// Handler builds a job handler that performs a SCIM 2.0 worker task. Register it
// with a [job.Runner] under the reserved [compiler.ScimJobTypeIndex] via
// HandleWithOutput; the runner then pulls activatable SCIM jobs, and for each the
// handler resolves the task's base URL / resource / operation / resource-id /
// filter / result-variable from the compiled process and calls the provider through
// client — evaluating any FEEL values over the variables the task sees, up its scope
// chain (the fx toggle,
// ADR-0067) and sending the request payload for create/replace/patch, keyed by the
// job key so an at-least-once retry de-duplicates. Authentication
// (basic/bearer/apiKey) is resolved through secret, which turns the model's secret
// *reference* into the credential at call time (ADR-0041); the token never lives in
// the model. When the task names a result variable, the JSON response is returned as
// that variable to be written back into the instance on completion. Returning an
// error fails the job (retry, then an incident, ADR-0061); the runner completes it
// only on success.
func Handler(store state.Reader, lookup ProcessLookup, client Client, secret SecretResolver) job.OutputHandler {
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
			return nil, fmt.Errorf("scim: no compiled process for def %d", ei.ProcessDefKey)
		}
		detail, err := cp.ConnectorTaskOf(ei.ElementId)
		if err != nil {
			return nil, fmt.Errorf("scim: %w", err)
		}
		task, err := Resolve(store, cp, detail, ei, j.ElementInstanceKey, j.Key)
		if err != nil {
			return nil, err
		}
		// The same Resolve/Run pair the worker uses (ADR-0168), so relocating the work
		// cannot change what a resolved SCIM task means — only which process holds the
		// credential behind the reference.
		res, err := Run(context.Background(), task, client, secret)
		if err != nil {
			return nil, err
		}
		return res.Variables(), nil
	}
}

// methodForOp maps a SCIM operation to its HTTP method and whether it carries a
// request body. create/replace/patch send the resource payload; get/delete/search
// do not (a search authors its filter as a query parameter).
func methodForOp(op string) (method string, hasBody bool) {
	switch op {
	case "create":
		return "POST", true
	case "replace":
		return "PUT", true
	case "patch":
		return "PATCH", true
	case "delete":
		return "DELETE", false
	default: // "get", "search"
		return "GET", false
	}
}

// resourceURL assembles the SCIM endpoint from the base URL and resource type, and
// appends /{id} for a single-resource operation (get/replace/patch/delete). The id
// is path-escaped so a provider-assigned id with reserved characters addresses the
// right resource. A blank base URL or resource is a modeling error the compiler
// already rejects; guarding here keeps a stale job from building a nonsense URL.
func resourceURL(op, baseURL, resource, id string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	res := strings.Trim(strings.TrimSpace(resource), "/")
	if base == "" || res == "" {
		return "", fmt.Errorf("scim: %s needs a base url and resource (got base %q, resource %q)", op, baseURL, resource)
	}
	endpoint := base + "/" + res
	switch op {
	case "get", "replace", "patch", "delete":
		if strings.TrimSpace(id) == "" {
			return "", fmt.Errorf("scim: %s needs a resource id", op)
		}
		endpoint += "/" + url.PathEscape(strings.TrimSpace(id))
	}
	return endpoint, nil
}

// requestBody builds the create/replace/patch payload. When the task names a body
// variable, that variable must hold a JSON object (a SCIM resource or PatchOp) and
// is sent verbatim; otherwise the variables handed in are the payload — the task's
// input mappings when it has them, else everything it sees, mirroring the REST
// worker's body rule (ADR-0067/0152,
// ADR-0174).
func requestBody(bodyVar string, scopeVars map[string]model.VariableValue) (map[string]any, error) {
	if bodyVar == "" {
		return bodyFromVars(scopeVars), nil
	}
	v, ok := scopeVars[bodyVar]
	if !ok {
		return nil, fmt.Errorf("scim: body variable %q is not set on the instance", bodyVar)
	}
	obj, ok := varToAny(&v).(map[string]any)
	if !ok {
		return nil, fmt.Errorf("scim: body variable %q must be a JSON object", bodyVar)
	}
	return obj, nil
}

// builtinProcessInstanceKey is the reserved FEEL name that binds to the instance's
// own key (mirrors the engine/DMN builtin), so a base-url/resource/filter expression
// can reference processInstanceKey.
const builtinProcessInstanceKey = "processInstanceKey"

// resolveValue turns a SCIM field value into a string: a literal verbatim, or a FEEL
// expression evaluated over the scope's variables and coerced to its string form. A
// FEEL null — an absent variable or a failed evaluation — becomes the empty string,
// matching the engine's null-propagating contract (as the REST worker does).
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
// evaluation (mirrors the REST worker's mapping so the two enums evolve
// independently).
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

// applyAuth resolves a SCIM task's authentication and adds the resulting header to
// headers. It resolves the secret *reference* to a value through secret (ADR-0041)
// and refuses to proceed when a configured scheme's secret is missing — a
// misconfigured credential fails the job (incident) rather than silently calling the
// provider unauthenticated. A no-auth task is a no-op.
func applyAuth(headers map[string]string, encoded string, secret SecretResolver) (map[string]string, error) {
	if encoded == "" {
		return headers, nil
	}
	var a compiler.RestAuth
	if err := json.Unmarshal([]byte(encoded), &a); err != nil {
		return headers, fmt.Errorf("scim: decode auth: %w", err)
	}
	if a.Type == "" {
		return headers, nil
	}
	value := ""
	if secret != nil {
		value = strings.TrimSpace(secret(a.SecretRef))
	}
	if value == "" {
		return headers, fmt.Errorf("scim: auth secret %q for %s auth is not configured (set ATLAS_CONNECTOR_<REF>_TOKEN)", a.SecretRef, a.Type)
	}
	name, headerValue := authHeader(a, value)
	if name == "" {
		return headers, fmt.Errorf("scim: unsupported auth type %q", a.Type)
	}
	if headers == nil {
		headers = map[string]string{}
	}
	if headers[name] == "" { // don't clobber an explicit header of the same name
		headers[name] = headerValue
	}
	return headers, nil
}

// authHeader builds the header name and value for a resolved credential: HTTP Basic
// (base64 of user:secret), a Bearer token, or a named api-key header. An unknown
// scheme yields an empty name.
func authHeader(a compiler.RestAuth, secret string) (name, value string) {
	switch a.Type {
	case "basic":
		return "Authorization", "Basic " + base64.StdEncoding.EncodeToString([]byte(a.Username+":"+secret))
	case "bearer":
		return "Authorization", "Bearer " + secret
	case "apikey":
		return a.ApiKeyName, secret
	default:
		return "", ""
	}
}

// responseVariable turns a decoded JSON response into the process variable named by
// the task's result variable. The value is canonicalized through the same expr path
// as any other variable (a scalar stays a scalar, an object/array becomes a
// structured VarJSON), so it round-trips on replay exactly like a REST response
// (ADR-0014/0066/0067).
func responseVariable(name string, body any) model.VariableValue {
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

// bodyFromVars turns the already-read variables into a JSON-ready map — the request
// body a SCIM task sends when it names no body variable. Which variables those are is
// the handler's decision: the task's input mappings when it has them, else everything
// it sees — the same rule the REST worker's body follows (ADR-0067/0152,
// ADR-0174).
func bodyFromVars(scopeVars map[string]model.VariableValue) map[string]any {
	data := make(map[string]any, len(scopeVars))
	for name, v := range scopeVars {
		vv := v
		data[name] = varToAny(&vv)
	}
	return data
}

// varToAny maps a stored variable to its JSON-ready Go value. A number keeps its
// exact canonical decimal text via json.Number rather than being routed through a
// float, so large or high-precision numbers survive intact. A structured value
// (VarJSON) is re-parsed from its stored JSON so the request payload nests it as a
// real object/array rather than a JSON-in-a-string blob.
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
