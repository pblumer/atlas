package soap

import (
	"context"
	"fmt"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// A resolved SOAP task, and the function that performs one.
//
// This is [ADR-0168]'s split applied to the WSDL kind, and it lands exactly where
// REST's did (ADR-0233, slice 1) because the two share their shape: everything about
// the call is model data — the endpoint, the SOAPAction, the envelope body — and the
// only thing that is not is the credential behind the task's `authSecret`, which is a
// vault *reference* (ADR-0041).
//
// So [Job] carries the authored auth configuration exactly as the compiled process
// holds it, encoded and unapplied. Applying it during Resolve would mean resolving the
// secret in the engine, and then the credential would be in the payload — which is the
// one thing the split exists to prevent.
//
// [ADR-0168]: https://github.com/pblumer/atlas/blob/main/docs/adr/0168-connector-work-on-a-worker.md

// Job is a SOAP task with everything the engine can evaluate already evaluated.
type Job struct {
	Endpoint string `json:"endpoint"`
	// Operation is the interned operation name, used for diagnostics and as the
	// default SOAPAction.
	Operation string `json:"operation,omitempty"`
	Action    string `json:"action,omitempty"`
	Version   string `json:"version,omitempty"`
	// Body is the XML placed inside the envelope, already evaluated.
	Body string `json:"body,omitempty"`
	// Auth is the authored auth configuration, encoded as it sits in the compiled
	// process. It names a secret; it never carries one (see [compiler.RestAuth]).
	Auth string `json:"auth,omitempty"`
	// Result names the process variable the parsed response Body is written to;
	// empty means the model discards it.
	Result string `json:"resultVariable,omitempty"`
}

// Result is what calling a Job produces: the parsed SOAP Body, and the variable it
// belongs in.
type Result struct {
	ResultVariable string
	Body           any
}

// Variables renders a call's result as the process variables the job completes with —
// none when the model named no result variable. Both halves call it, so an offloaded
// call and an in-engine one cannot disagree about what a SOAP task returns.
func (r Result) Variables() []model.VariableValue {
	if r.ResultVariable == "" {
		return nil
	}
	return []model.VariableValue{responseVariable(r.ResultVariable, r.Body)}
}

// Resolve turns a compiled SOAP task into a [Job]. Engine work by necessity: FEEL is
// compiled at deploy (ADR-0008/0015) and the scope lives in the store.
func Resolve(store state.Reader, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, ei *model.ElementInstanceValue, elementInstanceKey uint64) (Job, error) {
	if detail == nil {
		return Job{}, fmt.Errorf("soap: task has no detail")
	}
	// Read the variables the task sees once — up its scope chain, so its own
	// input-mapped locals shadow what it inherits (ADR-0068). The endpoint, action
	// and body FEEL values all evaluate against that one snapshot.
	scopeVars, err := state.VisibleVariablesMap(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("soap: read variables for element %d: %w", elementInstanceKey, err)
	}
	piKey := ei.ProcessInstanceKey // binds the processInstanceKey builtin; not the read scope
	op := cp.Intern(detail.SoapOp)
	action := resolveValue(detail.SoapAction, piKey, scopeVars)
	if strings.TrimSpace(action) == "" {
		action = op // the operation name is the default SOAPAction
	}
	return Job{
		Endpoint:  resolveValue(detail.SoapEndpoint, piKey, scopeVars),
		Operation: op,
		Action:    action,
		Version:   cp.Intern(detail.SoapVersion),
		Body:      resolveValue(detail.SoapBody, piKey, scopeVars),
		Auth:      cp.Intern(detail.Auth),
		Result:    cp.Intern(detail.ResultVar),
	}, nil
}

// Run applies the caller's own credential to a resolved job and makes the call. The
// in-process path calls it too, so there is one definition of what a resolved SOAP
// task means rather than two that drift — only whose secret store is in reach differs.
func Run(ctx context.Context, j Job, client Client, secret SecretResolver) (Result, error) {
	if strings.TrimSpace(j.Endpoint) == "" {
		// Checked here rather than at Resolve, so an endpoint whose FEEL evaluated to
		// empty fails the job the same way whichever process runs it.
		return Result{}, fmt.Errorf("soap: task has no endpoint (its FEEL endpoint evaluated to empty)")
	}
	headers, err := applyAuth(nil, j.Auth, secret)
	if err != nil {
		return Result{}, err
	}
	resp, err := client.Do(ctx, Request{
		Endpoint:  j.Endpoint,
		Operation: j.Operation,
		Action:    j.Action,
		Version:   j.Version,
		Body:      j.Body,
		Headers:   headers,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{ResultVariable: j.Result, Body: resp.Body}, nil
}
