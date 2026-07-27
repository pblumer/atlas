package rest

import (
	"context"
	"encoding/base64"
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
// worker uses it to find the method, URL, and result variable a REST job belongs
// to, so one handler serves every deployed process.
type ProcessLookup func(defKey uint64) *compiler.CompiledProcess

// SecretResolver returns the secret value for a reference name, or "" if unknown.
// The worker uses it to turn a REST task's authentication secret *reference* into
// the actual credential at call time (ADR-0041), so a token never lives in the
// model or the compiled process — only its reference does.
type SecretResolver func(ref string) string

// Handler builds a job handler that performs an HTTP-REST connector task. Register
// it with a [job.Runner] under the reserved [compiler.RestJobTypeIndex] via
// HandleWithOutput; the runner then pulls activatable REST jobs, and for each the
// handler resolves the connector task's method/url/headers/query/result-variable
// from the compiled process and calls the API through client — evaluating any FEEL
// url/header/query values over the instance's variables (the fx toggle, ADR-0067)
// and sending the instance's variables as the JSON request body for methods that
// carry one, keyed by the job key so an at-least-once retry de-duplicates.
// Authentication
// (basic/bearer/apiKey) is resolved through secret, which turns the model's secret
// *reference* into the credential at call time (ADR-0041); the token never lives in
// the model. When the task names a result variable, the JSON response is returned
// as that variable to be written back into the instance on completion. Returning an
// error fails the job (retry, then an incident, ADR-0061); the runner completes it
// only on success.
func Handler(store *state.Store, lookup ProcessLookup, client Client, secret SecretResolver) job.OutputHandler {
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
			return nil, fmt.Errorf("rest: no compiled process for def %d", ei.ProcessDefKey)
		}
		detail := cp.ConnectorTask(cp.Node(ei.ElementId).Detail)
		method := cp.Intern(detail.Method)
		scope := ei.ProcessInstanceKey
		// Read the instance's variables once: the url/header/query FEEL values and
		// the request body all evaluate against them, off the hot path.
		scopeVars, err := readScopeVars(store, scope)
		if err != nil {
			return nil, fmt.Errorf("rest: read variables for element %d: %w", j.ElementInstanceKey, err)
		}
		url := resolveValue(detail.Url, scope, scopeVars)
		headers, err := applyAuth(resolveKVs(detail.Headers, scope, scopeVars), cp.Intern(detail.Auth), secret)
		if err != nil {
			return nil, err
		}
		query := resolveKVs(detail.Query, scope, scopeVars)
		var body map[string]any
		if methodHasBody(method) {
			body = bodyFromVars(scopeVars)
		}
		resp, err := client.Do(context.Background(), Request{
			Method:         method,
			URL:            url,
			Headers:        headers,
			Query:          query,
			Body:           body,
			IdempotencyKey: strconv.FormatUint(j.Key, 10),
		})
		if err != nil {
			return nil, err
		}
		resultVar := cp.Intern(detail.ResultVar)
		if resultVar == "" {
			return nil, nil // the model discards the response
		}
		return []model.VariableValue{responseVariable(resultVar, resp.Body)}, nil
	}
}

// builtinProcessInstanceKey is the reserved FEEL name that binds to the instance's
// own key (mirrors the engine/DMN builtin), so a url/header/query expression can
// reference processInstanceKey.
const builtinProcessInstanceKey = "processInstanceKey"

// resolveValue turns a REST field value into a string: a literal verbatim, or a
// FEEL expression evaluated over the scope's variables and coerced to its string
// form (a string stays itself, a number its decimal). A FEEL null — an absent
// variable or a failed evaluation — becomes the empty string, matching the
// engine's null-propagating contract (as the DMN worker's mappings do); a genuinely
// broken URL then surfaces as a call error downstream.
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

// resolveKVs resolves a list of named REST values (headers or query parameters)
// into a map, evaluating any FEEL values. Returns nil for an empty list.
func resolveKVs(kvs []compiler.RestKV, scope uint64, scopeVars map[string]model.VariableValue) map[string]string {
	if len(kvs) == 0 {
		return nil
	}
	out := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		out[kv.Name] = resolveValue(kv.Val, scope, scopeVars)
	}
	return out
}

// readScopeVars reads all of a scope's variables into a map keyed by name, so the
// worker binds only the names each expression reads without a per-name store
// lookup (mirrors the DMN worker).
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

// bindVars turns the named variables from a scope into a FEEL binding. A name
// absent from the scope is left unbound (FEEL null); the reserved name
// processInstanceKey binds to the scope's own key as a string.
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
// evaluation (mirrors the DMN worker's mapping so the two enums evolve independently).
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

// applyAuth resolves a REST task's authentication and adds the resulting header to
// headers. It resolves the secret *reference* to a value through secret (ADR-0041)
// and refuses to proceed when a configured scheme's secret is missing — a
// misconfigured credential fails the job (incident) rather than silently calling
// the API unauthenticated. A no-auth task is a no-op.
func applyAuth(headers map[string]string, encoded string, secret SecretResolver) (map[string]string, error) {
	if encoded == "" {
		return headers, nil
	}
	var a compiler.RestAuth
	if err := json.Unmarshal([]byte(encoded), &a); err != nil {
		return headers, fmt.Errorf("rest: decode auth: %w", err)
	}
	if a.Type == "" {
		return headers, nil
	}
	value := ""
	if secret != nil {
		value = strings.TrimSpace(secret(a.SecretRef))
	}
	if value == "" {
		return headers, fmt.Errorf("rest: auth secret %q for %s auth is not configured (set ATLAS_CONNECTOR_<REF>_TOKEN)", a.SecretRef, a.Type)
	}
	name, headerValue := authHeader(a, value)
	if name == "" {
		return headers, fmt.Errorf("rest: unsupported auth type %q", a.Type)
	}
	if headers == nil {
		headers = map[string]string{}
	}
	if headers[name] == "" { // don't clobber an explicit model header of the same name
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

// responseVariable turns a decoded JSON response into the process variable named
// by the task's result variable. The value is canonicalized through the same expr
// path as any other variable (a scalar stays a scalar, an object/array becomes a
// structured VarJSON), so it round-trips on replay exactly like a DMN result
// (ADR-0014/0066).
func responseVariable(name string, body any) model.VariableValue {
	kind, b, text := expr.Classify(expr.FromJSON(body))
	return model.VariableValue{Name: name, Kind: toVarKind(kind), Bool: b, Text: text}
}

// toVarKind maps an expr value kind to the stored variable kind (mirrors the DMN
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

// methodHasBody reports whether an HTTP method conventionally carries a request
// body. The worker sends the instance's variables as the body only for these, so
// a GET/DELETE/HEAD stays body-free.
func methodHasBody(method string) bool {
	switch method {
	case "POST", "PUT", "PATCH":
		return true
	default:
		return false
	}
}

// bodyFromVars turns the already-read scope variables into a JSON-ready map — the
// request body a connector task sends. Until input mappings exist the whole
// variable scope is the payload, exactly as the clio connector sends the instance's
// variables as its event body (ADR-0036/0067).
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
