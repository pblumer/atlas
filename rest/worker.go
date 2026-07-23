package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// Result is one REST connector task's outcome, delivered to the sink a [Handler]
// is built with. Until process variables exist to receive them (Milestone 1), the
// response is surfaced here rather than written back into the instance — the same
// stand-in the dmn worker uses for decision outputs.
type Result struct {
	ElementInstanceKey uint64
	ProcessDefKey      uint64
	Method             string
	Path               string
	Status             int
	Response           any
}

// ProcessLookup resolves a process-definition key to its compiled process. The
// worker uses it to find the connector, method, and path a REST job belongs to,
// so one handler serves every deployed process.
type ProcessLookup func(defKey uint64) *compiler.CompiledProcess

// Handler builds a job handler that performs an HTTP-REST connector task.
// Register it with a [job.Runner] for the reserved RestJobType index; the runner
// then pulls activatable REST jobs, and for each the handler resolves the
// connector task's connector/method/path from the compiled process, resolves the
// connector's client from reg, and calls the API — sending the instance's
// variables as the JSON request body for methods that carry one, keyed by the job
// key so an at-least-once retry de-duplicates (ADR-0036). The response is reported
// to sink (which may be nil). Returning an error leaves the job pending, exactly
// as for any worker; the runner completes it only on success.
func Handler(store *state.Store, lookup ProcessLookup, reg *Registry, sink func(Result)) job.Handler {
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
			return fmt.Errorf("rest: no compiled process for def %d", ei.ProcessDefKey)
		}
		detail := cp.ConnectorTask(cp.Node(ei.ElementId).Detail)
		name := cp.Intern(detail.Connector)
		client, ok := reg.Client(name)
		if !ok {
			return fmt.Errorf("rest: no connector registered as %q", name)
		}
		method := cp.Intern(detail.Method)
		var body map[string]any
		if methodHasBody(method) {
			body, err = instanceData(store, ei.ProcessInstanceKey)
			if err != nil {
				return fmt.Errorf("rest: read variables for element %d: %w", j.ElementInstanceKey, err)
			}
		}
		resp, err := client.Do(context.Background(), Request{
			Method:         method,
			Path:           cp.Intern(detail.Path),
			Body:           body,
			IdempotencyKey: strconv.FormatUint(j.Key, 10),
		})
		if err != nil {
			return err
		}
		if sink != nil {
			sink(Result{
				ElementInstanceKey: j.ElementInstanceKey,
				ProcessDefKey:      cp.Key,
				Method:             method,
				Path:               cp.Intern(detail.Path),
				Status:             resp.Status,
				Response:           resp.Body,
			})
		}
		return nil
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
// body a connector task sends. Until output mappings exist (Milestone 1) the whole
// variable scope is the payload, exactly as the clio connector sends the
// instance's variables as its event body (ADR-0035/0036).
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
