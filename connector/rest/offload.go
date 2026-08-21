package rest

import (
	"context"
	"fmt"
	"strconv"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// A REST task resolved into plain values, and the function that calls one.
//
// The fifth shape of ADR-0168's split, and the sharpest test of its rule, because a
// REST task's credential is the one thing here that is *not* model-authored.
//
// Everything the model says travels: the method, the url, the headers and query the
// author wrote, the body, and the auth *configuration* — its type, the username, the
// api-key header name, the OAuth token url, client id and scope, and the
// **reference** naming the secret. None of that is a credential; it is all authored
// in the diagram and visible to anyone who can open it.
//
// The secret behind that reference never travels. [Run] resolves it through the
// caller's own resolver, so a worker reaches an API with a token the engine has
// never held — which is the shape ADR-0168 argues for, and the reason an offloaded
// REST connector can live in a network segment the engine is kept out of.

// Job is a REST task with everything model-authored already evaluated. It is what
// travels with a leased job, and it has nowhere to put a secret.
type Job struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Query   map[string]string `json:"query,omitempty"`
	Body    map[string]any    `json:"body,omitempty"`
	// Auth is the authored auth configuration, encoded as it sits in the compiled
	// process. It names a secret; it never carries one (see [compiler.RestAuth]).
	Auth string `json:"auth,omitempty"`
	// IdempotencyKey is the job key, so a call retried after a lease elapsed is
	// recognizable to the far end as the same one.
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
	// Result names the process variable the response is written to; empty means the
	// model discards it.
	Result string `json:"resultVariable,omitempty"`
}

// Result is what calling a Job produces.
type Result struct {
	ResultVariable string
	Body           any
}

// Resolve turns a compiled REST task into a [Job]. Engine work by necessity: FEEL is
// compiled at deploy (ADR-0008/0015) and the scope lives in the store.
//
// Auth is deliberately left unapplied. Applying it here would mean resolving the
// secret here, and then the credential would be in the job — which is the one thing
// the split exists to prevent.
func Resolve(store state.Reader, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, ei *model.ElementInstanceValue, elementInstanceKey, jobKey uint64) (Job, error) {
	if detail == nil {
		return Job{}, fmt.Errorf("rest: connector task has no detail")
	}
	// Read the variables the task sees once — up its scope chain, so its own
	// input-mapped locals shadow what it inherits (ADR-0068) — and evaluate the url,
	// headers and query against that one snapshot.
	scopeVars, err := state.VisibleVariablesMap(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("rest: read variables for element %d: %w", elementInstanceKey, err)
	}
	piKey := ei.ProcessInstanceKey // the processInstanceKey builtin, not the read scope
	method := cp.Intern(detail.Method)
	j := Job{
		Method:         method,
		URL:            resolveValue(detail.Url, piKey, scopeVars),
		Headers:        resolveKVs(detail.Headers, piKey, scopeVars),
		Query:          resolveKVs(detail.Query, piKey, scopeVars),
		Auth:           cp.Intern(detail.Auth),
		IdempotencyKey: strconv.FormatUint(jobKey, 10),
		Result:         cp.Intern(detail.ResultVar),
	}
	if methodHasBody(method) {
		bodyVars := scopeVars
		if len(cp.IOInputs(ei.ElementId)) > 0 {
			// The mappings are the body: exactly the activity-local scope they wrote,
			// inheriting nothing (ADR-draft-connector-payloads-are-the-input-mapping).
			if bodyVars, err = state.LocalVariablesMap(store, elementInstanceKey); err != nil {
				return Job{}, fmt.Errorf("rest: read mapped inputs for element %d: %w", elementInstanceKey, err)
			}
		}
		j.Body = bodyFromVars(bodyVars)
	}
	return j, nil
}

// Run applies the caller's own credential to a resolved job and makes the call. The
// in-process path calls it too, so there is one definition of what a resolved REST
// task means rather than two that drift — only whose secret store is in reach
// differs.
func Run(ctx context.Context, j Job, client Client, secret SecretResolver, tokens TokenProvider) (Result, error) {
	headers, err := applyAuth(ctx, j.Headers, j.Auth, secret, tokens)
	if err != nil {
		return Result{}, err
	}
	resp, err := client.Do(ctx, Request{
		Method:         j.Method,
		URL:            j.URL,
		Headers:        headers,
		Query:          j.Query,
		Body:           j.Body,
		IdempotencyKey: j.IdempotencyKey,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{ResultVariable: j.Result, Body: resp.Body}, nil
}
