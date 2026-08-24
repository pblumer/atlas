package soap

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

// ProcessLookup resolves a process-definition key to its compiled process. The worker
// uses it to find the endpoint, operation, body, and result variable a SOAP job belongs
// to, so one handler serves every deployed process.
type ProcessLookup func(defKey uint64) *compiler.CompiledProcess

// SecretResolver returns the secret value for a reference name, or "" if unknown. The
// worker uses it to turn a SOAP task's authentication secret *reference* into the actual
// credential at call time (ADR-0041), so a credential never lives in the model or the
// compiled process — only its reference does.
type SecretResolver func(ref string) string

// Handler builds a job handler that performs a SOAP / Web Services connector task.
// Register it with a [job.Runner] under the reserved [compiler.SoapJobTypeIndex] via
// HandleWithOutput; the runner then pulls activatable SOAP jobs, and for each the
// handler resolves the task's endpoint / operation / SOAPAction / body / version /
// result-variable from the compiled process and calls the web service through client —
// evaluating any FEEL values over the variables the task sees, up its scope chain (the
// fx toggle, ADR-0067/0068).
// Authentication (basic/bearer/apiKey) is resolved through secret, which turns the
// model's secret *reference* into the credential at call time (ADR-0041); the credential
// never lives in the model. When the task names a result variable, the parsed SOAP Body
// is returned as that variable to be written back into the instance on completion.
// Returning an error fails the job (retry, then an incident, ADR-0061); the runner
// completes it only on success.
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
			return nil, fmt.Errorf("soap: no compiled process for def %d", ei.ProcessDefKey)
		}
		detail, err := cp.ConnectorTaskOf(ei.ElementId)
		if err != nil {
			return nil, fmt.Errorf("soap: %w", err)
		}
		piKey := ei.ProcessInstanceKey // binds the processInstanceKey builtin; not the read scope
		// Read the variables the task sees once — up its scope chain, so its own
		// input-mapped locals shadow what it inherits (ADR-0068). The endpoint,
		// action and body FEEL values all evaluate against them, off the hot path.
		scopeVars, err := state.VisibleVariablesMap(store, j.ElementInstanceKey)
		if err != nil {
			return nil, fmt.Errorf("soap: read variables for element %d: %w", j.ElementInstanceKey, err)
		}
		endpoint := resolveValue(detail.SoapEndpoint, piKey, scopeVars)
		if strings.TrimSpace(endpoint) == "" {
			return nil, fmt.Errorf("soap: task has no endpoint (its FEEL endpoint evaluated to empty)")
		}
		op := cp.Intern(detail.SoapOp)
		action := resolveValue(detail.SoapAction, piKey, scopeVars)
		if strings.TrimSpace(action) == "" {
			action = op // the operation name is the default SOAPAction
		}
		headers, err := applyAuth(nil, cp.Intern(detail.Auth), secret)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(context.Background(), Request{
			Endpoint:  endpoint,
			Operation: op,
			Action:    action,
			Version:   cp.Intern(detail.SoapVersion),
			Body:      resolveValue(detail.SoapBody, piKey, scopeVars),
			Headers:   headers,
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

// builtinProcessInstanceKey is the reserved FEEL name that binds to the instance's own
// key (mirrors the engine/DMN builtin), so an endpoint/action/body expression can
// reference processInstanceKey.
const builtinProcessInstanceKey = "processInstanceKey"

// resolveValue turns a SOAP field value into a string: a literal verbatim, or a FEEL
// expression evaluated over the scope's variables and coerced to its string form. A FEEL
// null — an absent variable or a failed evaluation — becomes the empty string, matching
// the engine's null-propagating contract (as the REST worker does).
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
// from the chain is left unbound (FEEL null); the reserved name processInstanceKey binds
// to the process instance's key as a string.
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

// applyAuth resolves a SOAP task's authentication and adds the resulting header to
// headers. It resolves the secret *reference* to a value through secret (ADR-0041) and
// refuses to proceed when a configured scheme's secret is missing — a misconfigured
// credential fails the job (incident) rather than silently calling the service
// unauthenticated. A no-auth task is a no-op.
func applyAuth(headers map[string]string, encoded string, secret SecretResolver) (map[string]string, error) {
	if encoded == "" {
		return headers, nil
	}
	var a compiler.RestAuth
	if err := json.Unmarshal([]byte(encoded), &a); err != nil {
		return headers, fmt.Errorf("soap: decode auth: %w", err)
	}
	if a.Type == "" {
		return headers, nil
	}
	value := ""
	if secret != nil {
		value = strings.TrimSpace(secret(a.SecretRef))
	}
	if value == "" {
		return headers, fmt.Errorf("soap: auth secret %q for %s auth is not configured (set ATLAS_CONNECTOR_<REF>_TOKEN)", a.SecretRef, a.Type)
	}
	name, headerValue := authHeader(a, value)
	if name == "" {
		return headers, fmt.Errorf("soap: unsupported auth type %q", a.Type)
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
// (base64 of user:secret), a Bearer token, or a named api-key header. An unknown scheme
// yields an empty name.
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

// responseVariable turns a parsed SOAP Body into the process variable named by the
// task's result variable. The value is canonicalized through the same expr path as any
// other variable (a leaf stays a string, a nested body becomes a structured VarJSON), so
// it round-trips on replay exactly like a REST response (ADR-0014/0066/0067).
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
