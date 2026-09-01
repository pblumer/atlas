package webscrape

import (
	"context"
	"fmt"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// A web-scrape task resolved into plain values, and the function that runs one.
//
// The division follows ADR-0168: the engine owns the compiled process and scope
// chain, so it resolves FEEL-backed URL/selector values and carries the already
// compiled structural format/bound outward; the worker owns network reach and
// document parsing. ADR-0190 changes only the extraction strategy behind that seam.

// Job is a web-scrape task with everything already evaluated. It is what travels
// with a leased job. Format is always explicit for newly resolved work; an empty
// value remains HTML for backwards compatibility with pre-ADR-0190 payloads.
type Job struct {
	URL       string `json:"url"`
	Selector  string `json:"selector,omitempty"`
	Attribute string `json:"attribute,omitempty"`
	Format    string `json:"format,omitempty"`
	MaxItems  int32  `json:"maxItems,omitempty"`
	// Result names the process variable the scraped values are written to; empty
	// means the task writes nothing back.
	Result string `json:"resultVariable,omitempty"`
}

// Result is what running a Job produces. HTML keeps Values as the historical
// string array; RSS/Atom populate Entries instead. Format tells the handler which
// result shape to persist without inspecting the returned content.
type Result struct {
	ResultVariable string
	Format         string
	Values         []string
	Entries        []FeedEntry
}

// Items is what a run's result becomes as the value of the process variable: the
// scraped strings for an HTML scrape, or the feed's entries as
// {title, link, description, published} objects for RSS/Atom (ADR-0190).
//
// Both halves call it. The in-process worker renders it through expr and one leased by
// another process sends it as JSON on the wire, but *what a scrape means* is decided
// here once — which is the property that was missing: the offloaded path built its own
// list from Values alone, so a feed reached a model as an empty array even on the days
// the format survived the hand-over at all.
func Items(res Result) []any {
	if res.Format == formatRSS || res.Format == formatAtom {
		items := make([]any, len(res.Entries))
		for i, entry := range res.Entries {
			// Spelled out rather than marshalled from the struct, so the four keys a
			// model reads are a decision here and not a consequence of field tags.
			items[i] = map[string]any{
				"title":       entry.Title,
				"link":        entry.Link,
				"description": entry.Description,
				"published":   entry.Published,
			}
		}
		return items
	}
	items := make([]any, len(res.Values))
	for i, v := range res.Values {
		items[i] = v
	}
	return items
}

// Resolve turns a compiled web-scrape task into a [Job] by evaluating its authored
// values against the variables the task sees. Format and MaxItems are already
// compile-time structural data (ADR-0190), so resolution copies rather than
// interprets them.
func Resolve(store state.Reader, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, ei *model.ElementInstanceValue, elementInstanceKey uint64) (Job, error) {
	if detail == nil {
		return Job{}, fmt.Errorf("webscrape: connector task has no detail")
	}
	scopeVars, err := state.VisibleVariablesMap(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("webscrape: read variables for element %d: %w", elementInstanceKey, err)
	}
	piKey := ei.ProcessInstanceKey
	format := detail.ScrapeFormat.String()
	if format == "" {
		return Job{}, fmt.Errorf("webscrape: compiled task has unknown format %d", detail.ScrapeFormat)
	}
	return Job{
		URL:       resolveValue(detail.Url, piKey, scopeVars),
		Selector:  resolveValue(detail.ScrapeSelector, piKey, scopeVars),
		Attribute: cp.Intern(detail.ScrapeAttribute),
		Format:    format,
		MaxItems:  detail.ScrapeMaxItems,
		Result:    cp.Intern(detail.ResultVar),
	}, nil
}

// Run fetches and extracts. The in-process path calls it too, so there is one
// definition of what a resolved scrape means rather than two that drift.
func Run(ctx context.Context, j Job, client Client) (Result, error) {
	format := j.Format
	if format == "" {
		format = formatHTML
	}
	req := Request{
		URL:       j.URL,
		Selector:  j.Selector,
		Attribute: j.Attribute,
		Format:    format,
		MaxItems:  j.MaxItems,
	}
	switch format {
	case formatHTML:
		values, err := client.Scrape(ctx, req)
		if err != nil {
			return Result{}, err
		}
		return Result{ResultVariable: j.Result, Format: format, Values: values}, nil
	case formatRSS, formatAtom:
		feedClient, ok := client.(FeedClient)
		if !ok {
			return Result{}, fmt.Errorf("webscrape: client does not support %s feed extraction", format)
		}
		entries, err := feedClient.ScrapeFeed(ctx, req)
		if err != nil {
			return Result{}, err
		}
		return Result{ResultVariable: j.Result, Format: format, Entries: entries}, nil
	default:
		return Result{}, fmt.Errorf("webscrape: unsupported compiled format %q", format)
	}
}
