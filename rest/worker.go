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
// from the compiled process and calls the API through client — sending the
// instance's variables as the JSON request body for methods that carry one, keyed
// by the job key so an at-least-once retry de-duplicates (ADR-0067). Authentication
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
		headers, err := decodeStringMap(cp.Intern(detail.Headers))
		if err != nil {
			return nil, fmt.Errorf("rest: decode headers: %w", err)
		}
		headers, err = applyAuth(headers, cp.Intern(detail.Auth), secret)
		if err != nil {
			return nil, err
		}
		query, err := decodeStringMap(cp.Intern(detail.Query))
		if err != nil {
			return nil, fmt.Errorf("rest: decode query parameters: %w", err)
		}
		var body map[string]any
		if methodHasBody(method) {
			body, err = instanceData(store, ei.ProcessInstanceKey)
			if err != nil {
				return nil, fmt.Errorf("rest: read variables for element %d: %w", j.ElementInstanceKey, err)
			}
		}
		resp, err := client.Do(context.Background(), Request{
			Method:         method,
			URL:            cp.Intern(detail.Url),
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

// decodeStringMap decodes an interned JSON object of headers or query parameters
// into a map. An empty string (the compiler's -1 → "") is no map, not an error.
func decodeStringMap(encoded string) (map[string]string, error) {
	if encoded == "" {
		return nil, nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(encoded), &m); err != nil {
		return nil, err
	}
	return m, nil
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

// instanceData reads the instance's variables into a JSON-ready map — the request
// body a connector task sends. Until input mappings exist the whole variable scope
// is the payload, exactly as the clio connector sends the instance's variables as
// its event body (ADR-0036/0067).
func instanceData(store *state.Store, scope uint64) (map[string]any, error) {
	data := map[string]any{}
	err := store.VariablesOfScope(scope, func(v *model.VariableValue) error {
		data[v.Name] = varToAny(v)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return data, nil
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
