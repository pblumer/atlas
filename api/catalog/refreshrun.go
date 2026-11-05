package catalog

import (
	"slices"
	"sync"

	"github.com/pblumer/atlas/api/httpapi"
)

// refreshRun is the progress of one catalogue's content refresh: what a client
// that could not wait for the answer reads to know whether the refresh is still
// running and how far it has come (HandleCatalogContentRefreshStatus).
type refreshRun struct {
	Catalog string `json:"catalog"`
	// State is "running" until the last product is done, then "done".
	State string `json:"state"`
	// StartedBy is the account that asked for it.
	StartedBy  string `json:"startedBy,omitempty"`
	StartedAt  int64  `json:"startedAt"`
	FinishedAt int64  `json:"finishedAt,omitempty"`
	// Total is how many bound products the refresh covers, Done how many it has
	// finished — refreshed, refused or skipped.
	Total int              `json:"total"`
	Done  int              `json:"done"`
	Lines []catalogRefresh `json:"lines"`
}

// refreshRuns holds the newest run per catalogue. It lives in memory and not in
// the store: progress is about the request in flight, and a restart ends every
// one of those. Its zero value is ready to use.
type refreshRuns struct {
	mu   sync.Mutex
	runs map[string]*refreshRun
}

// start begins a run of a catalogue unless one is running, and answers a copy of
// the run — the new one, or the running one when started is false.
func (r *refreshRuns) start(catalog string, p *httpapi.Principal, total int, at int64) (refreshRun, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if run, ok := r.runs[catalog]; ok && run.State == "running" {
		return run.copy(), false
	}
	if r.runs == nil {
		r.runs = map[string]*refreshRun{}
	}
	run := &refreshRun{Catalog: catalog, State: "running", StartedAt: at, Total: total, Lines: []catalogRefresh{}}
	if p != nil {
		run.StartedBy = p.UserID
	}
	r.runs[catalog] = run
	return run.copy(), true
}

// record adds one finished product to a catalogue's run.
func (r *refreshRuns) record(catalog string, line catalogRefresh) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if run, ok := r.runs[catalog]; ok {
		run.Lines = append(run.Lines, line)
		run.Done = len(run.Lines)
	}
}

// finish marks a catalogue's run done.
func (r *refreshRuns) finish(catalog string, at int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if run, ok := r.runs[catalog]; ok {
		run.State, run.FinishedAt = "done", at
	}
}

// get answers a copy of a catalogue's newest run.
func (r *refreshRuns) get(catalog string) (refreshRun, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[catalog]
	if !ok {
		return refreshRun{}, false
	}
	return run.copy(), true
}

func (run *refreshRun) copy() refreshRun {
	c := *run
	c.Lines = slices.Clone(run.Lines)
	return c
}
