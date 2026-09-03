package googlesheets

import (
	"context"
	"fmt"
	"strconv"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// A Google Sheets task resolved into plain values, and the function that performs it.
//
// This is ADR-0168's split applied to a spreadsheet, and it is what makes the Google
// Sheets worker type an ordinary external worker rather than a kind the engine alone
// can run. Finding the task's detail in the compiled process and evaluating every
// authored value against the variables the task sees up its scope chain
// (ADR-0068/0174) needs the compiled process and the store, which only the engine has
// — so [Resolve] does it and produces plain values. The credential is never among
// them: what travels is the connector's *name*, and [Run] looks that name up in the
// registry the caller was built with.
//
// A Google Sheets worker can therefore hold a service-account key the engine has never
// seen, and act as whichever Google identity the operator configured it with, without
// that key ever reaching the engine's process.

// Job is a spreadsheet task with everything already evaluated: which instance, which
// operation, and the operation's values.
//
// Every field here is model-authored or instance-derived. There is nowhere in a Job to
// put a private key, and that is a property of the type rather than of the code that
// fills it in.
type Job struct {
	// Connector names the Google credential the *worker* is configured for. A name and
	// not a key, because a key is the whole credential.
	Connector string `json:"connector"`
	// Operation is one of [OpNames]; the compiler refused an unknown one at deploy.
	Operation string `json:"operation"`
	// Spreadsheet is the spreadsheet id, already reduced from whatever the model
	// authored — an id or the URL a person copied out of the browser.
	Spreadsheet string `json:"spreadsheet,omitempty"`
	// Sheet is a tab title and Range an A1 range.
	Sheet string `json:"sheet,omitempty"`
	Range string `json:"range,omitempty"`
	// Title and Folder create a spreadsheet and place it.
	Title  string `json:"title,omitempty"`
	Folder string `json:"folder,omitempty"`
	// Values are the rows to write, already projected out of whatever shape the model
	// held (see [Rows]) — so a worker never has to know what a FEEL context is.
	Values [][]any `json:"values,omitempty"`
	// Input is [InputUser] or [InputRaw] and Header asks a read to key its answer by
	// the range's first row. Both were decided at deploy.
	Input  string `json:"input,omitempty"`
	Header bool   `json:"header,omitempty"`
	// RequestID is the job key, sent as X-Request-ID. It buys tracing, not idempotency:
	// a spreadsheet has no key an append is deduplicated on (see the package doc).
	RequestID string `json:"requestId,omitempty"`
	// ResultVariable names the process variable Google's answer is written to; empty
	// means the model discards it.
	ResultVariable string `json:"resultVariable,omitempty"`
}

// Resolve turns a compiled Google Sheets connector task into a [Job]: the authored
// operation and every value it carries, evaluated against the variables the task sees.
// It is engine work by necessity — FEEL is compiled at deploy (ADR-0008/0015) and the
// scope lives in the store.
//
// The values a write sends are projected here rather than at call time, so a shape the
// connector cannot write — a list of objects with no columns to project it through —
// fails with a message naming the fix instead of as a Sheets 400.
//
// It does not re-validate the operation. The compiler refused an unknown one at deploy
// and [Run]'s client refuses one it does not implement with the list of the ones it
// does; a third check would only be a third message for the same fault.
func Resolve(store state.Reader, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, ei *model.ElementInstanceValue, elementInstanceKey, jobKey uint64) (Job, error) {
	if detail == nil {
		return Job{}, fmt.Errorf("googlesheets: connector task has no detail")
	}
	// Read the variables the task sees once — up its scope chain, so its own
	// input-mapped locals shadow what it inherits (ADR-0068) — and evaluate every
	// authored value against that one snapshot.
	scopeVars, err := state.VisibleVariablesMap(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("googlesheets: read variables for element %d: %w", elementInstanceKey, err)
	}
	piKey := ei.ProcessInstanceKey // binds the processInstanceKey builtin; not the read scope
	job := Job{
		Connector:      cp.Intern(detail.Connector),
		Operation:      cp.Intern(detail.SheetsOp),
		Spreadsheet:    SpreadsheetID(resolveValue(detail.SheetsID, piKey, scopeVars)),
		Sheet:          resolveValue(detail.SheetsTab, piKey, scopeVars),
		Range:          resolveValue(detail.SheetsRange, piKey, scopeVars),
		Title:          resolveValue(detail.SheetsTitle, piKey, scopeVars),
		Folder:         FolderID(resolveValue(detail.SheetsFolder, piKey, scopeVars)),
		Input:          cp.Intern(detail.SheetsInput),
		Header:         detail.SheetsHeader,
		RequestID:      strconv.FormatUint(jobKey, 10),
		ResultVariable: cp.Intern(detail.ResultVar),
	}
	if detail.SheetsValues.Expr != nil || detail.SheetsValues.Literal != "" {
		rows, err := Rows(resolveField(detail.SheetsValues, piKey, scopeVars), detail.SheetsColumns)
		if err != nil {
			return Job{}, err
		}
		job.Values = rows
	}
	return job, nil
}

// Run performs a resolved job through the caller's own registry and answers with what
// Google returned. It is the whole of the worker's half, and the in-process path calls
// it too, so there is one definition of what a resolved spreadsheet task means rather
// than two that drift.
//
// The connector lookup comes first: an unconfigured name is the more actionable of the
// failures a job can carry here, and reporting it ahead of anything the operation
// itself might be missing keeps the message an operator sees pointed at the fix
// (ADR-0158).
func Run(ctx context.Context, j Job, reg *Registry) (any, error) {
	client, ok := reg.Client(j.Connector)
	if !ok {
		return nil, reg.Unresolved("googlesheets", j.Connector)
	}
	return client.Do(ctx, Request{
		Operation:   j.Operation,
		Spreadsheet: j.Spreadsheet,
		Sheet:       j.Sheet,
		Range:       j.Range,
		Title:       j.Title,
		Folder:      j.Folder,
		Values:      j.Values,
		Input:       j.Input,
		Header:      j.Header,
		RequestID:   j.RequestID,
	})
}
