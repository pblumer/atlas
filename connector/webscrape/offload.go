package webscrape

import (
	"context"
	"fmt"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/state"
)

// A web-scrape task resolved into plain values, and the function that runs one.
//
// The third shape of ADR-0168's split, and the one that shows what the rule is
// actually about. CSV import needed no credential and no network. Mail needed a
// credential the worker holds and wrote nothing back. This one needs the network but
// no credential, and it *does* write a result variable — so it proves the seam
// carries results outward as well as instructions inward.
//
// The division is the same as always: the engine has the compiled process and the
// scope chain, so it resolves the url, the selector and the attribute; the worker
// has the network reach, so it fetches. A scrape target inside a network the engine
// cannot see is exactly the case for moving this kind out.

// Job is a web-scrape task with everything already evaluated. It is what travels
// with a leased job.
type Job struct {
	URL       string `json:"url"`
	Selector  string `json:"selector"`
	Attribute string `json:"attribute,omitempty"`
	// Result names the process variable the scraped values are written to; empty
	// means the task writes nothing back.
	Result string `json:"resultVariable,omitempty"`
}

// Result is what running a Job produces: the scraped values and the variable to
// write them to.
type Result struct {
	ResultVariable string
	Values         []string
}

// Resolve turns a compiled web-scrape task into a [Job] by evaluating its authored
// fields against the scope's variables. Engine work by necessity — FEEL is compiled
// at deploy (ADR-0008/0015) and the scope lives in the store.
func Resolve(store state.Reader, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, scope uint64) (Job, error) {
	if detail == nil {
		return Job{}, fmt.Errorf("webscrape: connector task has no detail")
	}
	// Read the scope's variables once: both the url and the selector evaluate against
	// the same snapshot.
	scopeVars, err := readScopeVars(store, scope)
	if err != nil {
		return Job{}, fmt.Errorf("webscrape: read variables for scope %d: %w", scope, err)
	}
	return Job{
		URL:       resolveValue(detail.Url, scope, scopeVars),
		Selector:  resolveValue(detail.ScrapeSelector, scope, scopeVars),
		Attribute: cp.Intern(detail.ScrapeAttribute),
		Result:    cp.Intern(detail.ResultVar),
	}, nil
}

// Run fetches and extracts. The in-process path calls it too, so there is one
// definition of what a resolved scrape means rather than two that drift.
func Run(ctx context.Context, j Job, client Client) (Result, error) {
	values, err := client.Scrape(ctx, Request{
		URL:       j.URL,
		Selector:  j.Selector,
		Attribute: j.Attribute,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{ResultVariable: j.Result, Values: values}, nil
}
