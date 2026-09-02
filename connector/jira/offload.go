package jira

import (
	"context"
	"fmt"
	"strconv"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// A Jira task resolved into plain values, and the function that performs it.
//
// This is ADR-0168's split applied to an issue tracker, and it is what makes the Jira
// worker type an ordinary external worker rather than a kind the engine alone can run.
// Finding the task's detail in the compiled process and evaluating every authored value
// against the variables the task sees up its scope chain (ADR-0068/0174) needs the
// compiled process and the store, which only the engine has — so [Resolve] does it and
// produces plain values. The site URL and the credential are never among them: what
// travels is the connector's *name*, and [Run] looks that name up in the registry the
// caller was built with.
//
// A Jira worker can therefore hold a credential the engine has never seen, and operate
// as whichever Atlassian account the operator configured it with, without that account
// ever reaching the engine's process.

// Job is a Jira task with everything already evaluated: which instance, which
// operation, and the operation's values.
//
// Every field here is model-authored or instance-derived. There is nowhere in a Job to
// put a base URL, an email or an API token, and that is a property of the type rather
// than of the code that fills it in.
type Job struct {
	// Connector names the Jira instance the *worker* is configured for. A name and not
	// a URL, because a URL is half a credential.
	Connector string `json:"connector"`
	// Operation is one of [OpNames]; the compiler refused an unknown one at deploy.
	Operation string `json:"operation"`
	// Issue addresses one issue by key ("OPS-42") or numeric id.
	Issue string `json:"issue,omitempty"`
	// Project and IssueType create an issue.
	Project     string `json:"project,omitempty"`
	IssueType   string `json:"issueType,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Description string `json:"description,omitempty"`
	// Transition is a transition id or the name Jira shows for it.
	Transition string `json:"transition,omitempty"`
	Comment    string `json:"comment,omitempty"`
	Assignee   string `json:"assignee,omitempty"`
	// JQL is an issue search's query and Query an account search's term; MaxResults caps
	// what either may return, 0 reads every match. The compiler has already applied the
	// default.
	JQL        string `json:"jql,omitempty"`
	Query      string `json:"query,omitempty"`
	MaxResults int32  `json:"maxResults,omitempty"`
	// Fields are extra issue fields, each keeping the JSON shape its FEEL value had.
	Fields map[string]any `json:"fields,omitempty"`
	// RequestID is the job key, sent as X-Request-ID so an at-least-once retry carries
	// the same id and a downstream de-duplicator can recognize it.
	RequestID string `json:"requestId,omitempty"`
	// ResultVariable names the process variable Jira's answer is written to; empty
	// means the model discards it.
	ResultVariable string `json:"resultVariable,omitempty"`
}

// Resolve turns a compiled Jira connector task into a [Job]: the authored operation and
// every value it carries, evaluated against the variables the task sees. It is engine
// work by necessity — FEEL is compiled at deploy (ADR-0008/0015) and the scope lives in
// the store.
//
// It does not re-validate the operation. The compiler refused an unknown one at deploy
// and [Run]'s client refuses one it does not implement with the list of the ones it
// does; a third check would only be a third message for the same fault.
func Resolve(store state.Reader, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, ei *model.ElementInstanceValue, elementInstanceKey, jobKey uint64) (Job, error) {
	if detail == nil {
		return Job{}, fmt.Errorf("jira: connector task has no detail")
	}
	// Read the variables the task sees once — up its scope chain, so its own
	// input-mapped locals shadow what it inherits (ADR-0068) — and evaluate every
	// authored value against that one snapshot.
	scopeVars, err := state.VisibleVariablesMap(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("jira: read variables for element %d: %w", elementInstanceKey, err)
	}
	piKey := ei.ProcessInstanceKey // binds the processInstanceKey builtin; not the read scope
	return Job{
		Connector:      cp.Intern(detail.Connector),
		Operation:      cp.Intern(detail.JiraOp),
		Issue:          resolveValue(detail.JiraIssue, piKey, scopeVars),
		Project:        resolveValue(detail.JiraProject, piKey, scopeVars),
		IssueType:      resolveValue(detail.JiraIssueType, piKey, scopeVars),
		Summary:        resolveValue(detail.JiraSummary, piKey, scopeVars),
		Description:    resolveValue(detail.JiraDescription, piKey, scopeVars),
		Transition:     resolveValue(detail.JiraTransition, piKey, scopeVars),
		Comment:        resolveValue(detail.JiraComment, piKey, scopeVars),
		Assignee:       resolveValue(detail.JiraAssignee, piKey, scopeVars),
		JQL:            resolveValue(detail.JiraJQL, piKey, scopeVars),
		Query:          resolveValue(detail.JiraQuery, piKey, scopeVars),
		MaxResults:     detail.JiraMaxResults,
		Fields:         resolveFields(detail.JiraFields, piKey, scopeVars),
		RequestID:      strconv.FormatUint(jobKey, 10),
		ResultVariable: cp.Intern(detail.ResultVar),
	}, nil
}

// Run performs a resolved job through the caller's own registry and answers with what
// Jira returned (nil for an operation Jira answers with no content). It is the whole of
// the worker's half, and the in-process path calls it too, so there is one definition of
// what a resolved Jira task means rather than two that drift.
//
// The connector lookup comes first: an unconfigured name is the more actionable of the
// failures a job can carry here, and reporting it ahead of anything the operation itself
// might be missing keeps the message an operator sees pointed at the fix (ADR-0158).
func Run(ctx context.Context, j Job, reg *Registry) (any, error) {
	client, ok := reg.Client(j.Connector)
	if !ok {
		return nil, reg.Unresolved("jira", j.Connector)
	}
	return client.Do(ctx, Request{
		Operation:   j.Operation,
		Issue:       j.Issue,
		Project:     j.Project,
		IssueType:   j.IssueType,
		Summary:     j.Summary,
		Description: j.Description,
		Transition:  j.Transition,
		Comment:     j.Comment,
		Assignee:    j.Assignee,
		JQL:         j.JQL,
		Query:       j.Query,
		MaxResults:  j.MaxResults,
		Fields:      j.Fields,
		RequestID:   j.RequestID,
	})
}
