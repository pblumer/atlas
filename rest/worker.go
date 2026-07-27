package rest

import (
	"context"
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

// Handler builds a job handler that performs an HTTP-REST connector task. Register
// it with a [job.Runner] under the reserved [compiler.RestJobTypeIndex] via
// HandleWithOutput; the runner then pulls activatable REST jobs, and for each the
// handler resolves the connector task's method/url/result-variable from the
// compiled process and calls the API through client — sending the instance's
// variables as the JSON request body for methods that carry one, keyed by the job
// key so an at-least-once retry de-duplicates (ADR-0067). When the task names a
// result variable, the JSON response is returned as that variable to be written
// back into the instance on completion. Returning an error fails the job (retry,
// then an incident, ADR-0061); the runner completes it only on success.
func Handler(store *state.Store, lookup ProcessLookup, client Client) job.OutputHandler {
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
