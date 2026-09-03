package scim

import (
	"context"
	"fmt"
	"strconv"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// A resolved SCIM task, and the function that performs one.
//
// This is [ADR-0168]'s split applied to the last of the HTTP-shaped kinds, and it
// lands where REST's and SOAP's did (ADR-0233, slices 1 and 4): the whole call is
// model data and travels resolved, while the credential behind the task's
// `authSecret` is a vault *reference* (ADR-0041) that whoever runs the job resolves.
//
// One choice here is deliberate and worth stating, because the obvious alternative
// looks tidier. [Job] carries the *authored* operation, base URL, resource, id and
// filter — not the HTTP method and URL derived from them. Two reasons: a parked job's
// payload is something an operator reads, and "operation: create, resource: Users"
// answers what they came to ask where "POST .../Users" makes them work backwards; and
// the derivation has failure cases of its own — a get with no id, a resource with no
// base — which belong in [Run], where both halves reach them, rather than in the
// engine where only one does.
//
// [ADR-0168]: https://github.com/pblumer/atlas/blob/main/docs/adr/0168-connector-work-on-a-worker.md

// Job is a SCIM task with everything the engine can evaluate already evaluated.
type Job struct {
	Operation  string `json:"operation"`
	BaseURL    string `json:"baseUrl"`
	Resource   string `json:"resource"`
	ResourceID string `json:"resourceId,omitempty"`
	// Filter narrows a search; it is sent as the SCIM `filter` query parameter.
	Filter string `json:"filter,omitempty"`
	// Body is the create/replace/patch payload: the named body variable, or the
	// task's input mappings, or everything it sees. It is engine state — a worker has
	// no scope chain to read a process variable from — so it travels resolved.
	Body map[string]any `json:"body,omitempty"`
	// Auth is the authored auth configuration, encoded as it sits in the compiled
	// process. It names a secret; it never carries one (see [compiler.RestAuth]).
	Auth string `json:"auth,omitempty"`
	// IdempotencyKey is the job key, so a call retried after a lease elapsed is
	// recognizable to the provider as the same one.
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
	// Result names the process variable the response is written to; empty means the
	// model discards it.
	Result string `json:"resultVariable,omitempty"`
}

// Result is what calling a Job produces: the decoded response, and the variable it
// belongs in.
type Result struct {
	ResultVariable string
	Body           any
}

// Variables renders a call's result as the process variables the job completes with —
// none when the model named no result variable. Both halves call it, so an offloaded
// call and an in-engine one cannot disagree about what a SCIM task returns.
func (r Result) Variables() []model.VariableValue {
	if r.ResultVariable == "" {
		return nil
	}
	return []model.VariableValue{responseVariable(r.ResultVariable, r.Body)}
}

// Resolve turns a compiled SCIM task into a [Job]. Engine work by necessity: FEEL is
// compiled at deploy (ADR-0008/0015) and the scope lives in the store.
func Resolve(store state.Reader, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, ei *model.ElementInstanceValue, elementInstanceKey, jobKey uint64) (Job, error) {
	if detail == nil {
		return Job{}, fmt.Errorf("scim: task has no detail")
	}
	op := cp.Intern(detail.ScimOp)
	// Read the variables the task sees once — up its scope chain, so its own
	// input-mapped locals shadow what it inherits (ADR-0068). The base-url, resource,
	// id and filter FEEL values and the request body all evaluate against that one
	// snapshot.
	scopeVars, err := state.VisibleVariablesMap(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("scim: read variables for element %d: %w", elementInstanceKey, err)
	}
	piKey := ei.ProcessInstanceKey // binds the processInstanceKey builtin; not the read scope
	j := Job{
		Operation:      op,
		BaseURL:        resolveValue(detail.ScimBaseURL, piKey, scopeVars),
		Resource:       resolveValue(detail.ScimResource, piKey, scopeVars),
		ResourceID:     resolveValue(detail.ScimResourceID, piKey, scopeVars),
		Auth:           cp.Intern(detail.Auth),
		IdempotencyKey: strconv.FormatUint(jobKey, 10),
		Result:         cp.Intern(detail.ResultVar),
	}
	if op == "search" {
		j.Filter = resolveValue(detail.ScimFilter, piKey, scopeVars)
	}
	if _, hasBody := methodForOp(op); hasBody {
		bodyVar := cp.Intern(detail.ScimBody)
		bodyVars := scopeVars
		if bodyVar == "" && len(cp.IOInputs(ei.ElementId)) > 0 {
			// No body variable named, but the task maps its inputs: those mappings are
			// the body — exactly the activity-local scope they wrote, inheriting
			// nothing (ADR-0174).
			if bodyVars, err = state.LocalVariablesMap(store, elementInstanceKey); err != nil {
				return Job{}, fmt.Errorf("scim: read mapped inputs for element %d: %w", elementInstanceKey, err)
			}
		}
		if j.Body, err = requestBody(bodyVar, bodyVars); err != nil {
			return Job{}, err
		}
	}
	return j, nil
}

// Run derives the request from a resolved job, applies the caller's own credential,
// and makes the call. The in-process path calls it too, so there is one definition of
// what a resolved SCIM task means rather than two that drift — only whose secret
// store is in reach differs.
func Run(ctx context.Context, j Job, client Client, secret SecretResolver) (Result, error) {
	method, _ := methodForOp(j.Operation)
	endpoint, err := resourceURL(j.Operation, j.BaseURL, j.Resource, j.ResourceID)
	if err != nil {
		return Result{}, err
	}
	headers, err := applyAuth(nil, j.Auth, secret)
	if err != nil {
		return Result{}, err
	}
	var query map[string]string
	if j.Operation == "search" && j.Filter != "" {
		query = map[string]string{"filter": j.Filter}
	}
	resp, err := client.Do(ctx, Request{
		Method:         method,
		URL:            endpoint,
		Headers:        headers,
		Query:          query,
		Body:           j.Body,
		IdempotencyKey: j.IdempotencyKey,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{ResultVariable: j.Result, Body: resp.Body}, nil
}
