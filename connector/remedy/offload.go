package remedy

import (
	"context"
	"fmt"
	"strconv"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// A Remedy task resolved into plain values, and the function that creates the entry.
//
// This is ADR-0168's split applied to an ITSM connector. Finding the task's detail in
// the compiled process, evaluating its form and every field value against the
// variables the task sees up its scope chain (ADR-0068/0174) — all of that needs the
// compiled process and the store, which only the engine has, so [Resolve] does it and
// produces plain strings. The AR System base URL, the service account and its password
// are never among them: what travels is the connector's *name*, and [Run] looks that
// name up in the registry the caller was built with.
//
// A Remedy worker can therefore hold credentials the engine has never seen, which is
// what makes an offloaded Remedy connector deployable next to a Helix instance the
// engine cannot reach — the usual position of an ITSM system.
//
// The cost is the failure the engine used to catch at lease time: a connector name no
// worker holds. [Run] refuses it instead, naming the connector, and the Workers view
// is where an operator sees which names are configured nowhere.

// Job is a Remedy task with everything already evaluated: which Remedy instance,
// which form, and the entry's field values. It is what travels with a leased job.
//
// Every field here is model-authored or instance-derived. There is nowhere in a Job to
// put a base URL or a password, and that is a property of the type rather than of the
// code that fills it in.
type Job struct {
	// Connector names the Remedy instance the *worker* is configured for. A name and
	// not a URL, because a URL is half a credential.
	Connector string `json:"connector"`
	// Form is the Remedy form the entry is created in, e.g. HPD:IncidentInterface_Create.
	Form string `json:"form"`
	// Values are the entry's field values, keyed by Remedy field name. They are strings
	// because that is what the AR System is sent today; typed values are a follow-up
	// (ADR-0106), and keeping the wire type honest is what will make that change visible.
	Values map[string]string `json:"values,omitempty"`
	// RequestID is the job key, sent as X-Request-ID so an at-least-once retry carries
	// the same id and a downstream de-duplicator can recognize it.
	RequestID string `json:"requestId,omitempty"`
	// ResultVariable names the process variable the created entry's id is written to;
	// empty means the model discards it.
	ResultVariable string `json:"resultVariable,omitempty"`
}

// Resolve turns a compiled Remedy connector task into a [Job]: the authored form and
// field values evaluated against the variables the task sees. It is engine work by
// necessity — FEEL is compiled at deploy (ADR-0008/0015) and the scope lives in the
// store.
//
// It deliberately does not validate that a form resolved. That check belongs with the
// create, after the connector lookup, so an operator with both an unconfigured
// connector and an empty form hears about the configuration first — that being the one
// they can act on.
func Resolve(store state.Reader, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, ei *model.ElementInstanceValue, elementInstanceKey, jobKey uint64) (Job, error) {
	if detail == nil {
		return Job{}, fmt.Errorf("remedy: connector task has no detail")
	}
	// Read the variables the task sees once — up its scope chain, so its own
	// input-mapped locals shadow what it inherits (ADR-0068) — and evaluate the form
	// and every field against that one snapshot.
	scopeVars, err := state.VisibleVariablesMap(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("remedy: read variables for element %d: %w", elementInstanceKey, err)
	}
	piKey := ei.ProcessInstanceKey // binds the processInstanceKey builtin; not the read scope
	values := make(map[string]string, len(detail.RemedyFields))
	for _, f := range detail.RemedyFields {
		values[f.Name] = resolveValue(f.Val, piKey, scopeVars)
	}
	return Job{
		Connector:      cp.Intern(detail.Connector),
		Form:           resolveValue(detail.RemedyForm, piKey, scopeVars),
		Values:         values,
		RequestID:      strconv.FormatUint(jobKey, 10),
		ResultVariable: cp.Intern(detail.ResultVar),
	}, nil
}

// Run creates a resolved job's entry through the caller's own registry. It is the
// whole of the worker's half, and the in-process path calls it too, so there is one
// definition of what a resolved Remedy task means rather than two that drift.
//
// The connector lookup comes first: an unconfigured name is the more actionable of the
// two failures a job can carry here, and reporting it ahead of an unresolved form
// keeps the message an operator sees pointed at the fix.
func Run(ctx context.Context, j Job, reg *Registry) (Result, error) {
	client, ok := reg.Client(j.Connector)
	if !ok {
		return Result{}, reg.Unresolved("remedy", j.Connector)
	}
	if j.Form == "" {
		return Result{}, fmt.Errorf("remedy: task resolved no form")
	}
	values := make(map[string]any, len(j.Values))
	for name, v := range j.Values {
		values[name] = v
	}
	return client.CreateEntry(ctx, Entry{Form: j.Form, Values: values, RequestID: j.RequestID})
}
