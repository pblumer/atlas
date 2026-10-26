package sharepoint

import (
	"context"
	"fmt"
	"strconv"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// A resolved SharePoint task, and the function that performs one.
//
// This is [ADR-0168]'s split applied to a managed kind, so the line falls where
// Jira's and Remedy's do (ADR-0201/0192): the task names its instance and nothing
// more, because the site's address and its OAuth bundle are a *worker record* and a
// vault secret (ADR-0141) — a URL is half a credential, so neither travels. What
// travels is the name, the operation, and whichever site, list and values the model
// authored.
//
// [ADR-0168]: https://github.com/pblumer/atlas/blob/main/docs/adr/0168-connector-work-on-a-worker.md

// Job is a SharePoint task with everything the engine can evaluate already evaluated.
//
// A job from an engine built before operations existed carries no operation, and is a
// create-item — which is the only thing such an engine could have resolved.
type Job struct {
	// Connector names the SharePoint instance in the worker's registry — the Graph
	// endpoint and the OAuth bundle live there, never here.
	Connector string `json:"connector"`
	// Operation is one of [Ops]; empty is [OpCreateItem].
	Operation string `json:"operation,omitempty"`
	// Site addresses the site, List the list or document library. They are model data
	// (ADR-0141), evaluated against the instance's variables.
	Site string `json:"site,omitempty"`
	List string `json:"list,omitempty"`
	// ItemID addresses one list item, Path one file or folder in the library.
	ItemID string `json:"itemId,omitempty"`
	Path   string `json:"path,omitempty"`
	// Fields are column values, each keeping the JSON shape its FEEL value had.
	Fields map[string]any `json:"fields,omitempty"`
	// Content is what an upload writes, in the form Encoding names.
	Content  string `json:"content,omitempty"`
	Encoding string `json:"encoding,omitempty"`
	// Filter, Select, PageToken and MaxResults shape a listing; Select also a read.
	Filter     string `json:"filter,omitempty"`
	Select     string `json:"select,omitempty"`
	PageToken  string `json:"pageToken,omitempty"`
	MaxResults int32  `json:"maxResults,omitempty"`
	// RequestID is the job key, sent so an at-least-once retry is recognizable to
	// Graph as the same request. It is frozen at resolve time rather than recomputed
	// where the retry happens, so a retried job carries the key of the job it retries.
	RequestID string `json:"requestId,omitempty"`
	// Result names the process variable what Graph answered is written to; empty
	// discards it.
	Result string `json:"resultVariable,omitempty"`
}

// Result is what one operation produces: Graph's answer, and the variable it belongs
// in.
type Result struct {
	ResultVariable string
	Item           any
}

// Variables renders a run's result as the process variables the job completes with —
// none when the model named no result variable. Both halves call it, so an offloaded
// operation and an in-engine one cannot disagree about what a SharePoint task returns.
func (r Result) Variables() []model.VariableValue {
	if r.ResultVariable == "" {
		return nil
	}
	return []model.VariableValue{itemVariable(r.ResultVariable, r.Item)}
}

// Resolve turns a compiled SharePoint task into a [Job]. Engine work by necessity:
// FEEL is compiled at deploy (ADR-0008/0015) and the scope lives in the store.
func Resolve(store state.Reader, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, ei *model.ElementInstanceValue, elementInstanceKey, jobKey uint64) (Job, error) {
	if detail == nil {
		return Job{}, fmt.Errorf("sharepoint: task has no detail")
	}
	// Read the variables the task sees once — up its scope chain, so its own
	// input-mapped locals shadow what it inherits (ADR-0068). Every FEEL value
	// evaluates against that one snapshot.
	scopeVars, err := state.VisibleVariablesMap(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("sharepoint: read variables for element %d: %w", elementInstanceKey, err)
	}
	piKey := ei.ProcessInstanceKey // binds the processInstanceKey builtin; not the read scope
	return Job{
		Connector:  cp.Intern(detail.Connector),
		Operation:  cp.Intern(detail.SharePointOp),
		Site:       resolveValue(detail.Site, piKey, scopeVars),
		List:       resolveValue(detail.List, piKey, scopeVars),
		ItemID:     resolveValue(detail.SharePointItemID, piKey, scopeVars),
		Path:       resolveValue(detail.SharePointPath, piKey, scopeVars),
		Fields:     resolveFields(detail.Fields, piKey, scopeVars),
		Content:    resolveValue(detail.SharePointContent, piKey, scopeVars),
		Encoding:   cp.Intern(detail.SharePointEncoding),
		Filter:     resolveValue(detail.SharePointFilter, piKey, scopeVars),
		Select:     resolveValue(detail.SharePointSelect, piKey, scopeVars),
		PageToken:  resolveValue(detail.SharePointPageToken, piKey, scopeVars),
		MaxResults: detail.SharePointMaxResults,
		RequestID:  strconv.FormatUint(jobKey, 10),
		Result:     cp.Intern(detail.ResultVar),
	}, nil
}

// Run performs the operation through the caller's own registry. The in-process path
// calls it too, so there is one definition of what a resolved SharePoint task means
// rather than two that drift — only which instances are in reach differs.
func Run(ctx context.Context, j Job, reg *Registry) (Result, error) {
	if reg == nil {
		return Result{}, fmt.Errorf("sharepoint: this job names the instance %q, but no instances are configured where it runs; is this server offloading the sharepoint kind to a worker that holds them?", j.Connector)
	}
	client, ok := reg.Client(j.Connector)
	if !ok {
		return Result{}, reg.Unresolved("sharepoint", j.Connector)
	}
	item, err := client.Do(ctx, Request{
		Operation:  j.Operation,
		Site:       j.Site,
		List:       j.List,
		ItemID:     j.ItemID,
		Path:       j.Path,
		Fields:     j.Fields,
		Content:    j.Content,
		Encoding:   j.Encoding,
		Filter:     j.Filter,
		Select:     j.Select,
		PageToken:  j.PageToken,
		MaxResults: j.MaxResults,
		RequestID:  j.RequestID,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{ResultVariable: j.Result, Item: item}, nil
}
