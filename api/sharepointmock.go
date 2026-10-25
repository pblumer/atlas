package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/connector/sharepoint"
)

// The SharePoint mockup's server half (ADR-0484).
//
// A SharePoint Worker whose provider is the mockup reaches no tenant: its operations run
// against a site in this server's memory (s.sharePointMocks). The site lives here and
// not in the worker for the reason the preview outbox does (ADR-0150): a supervised
// worker is restarted whenever a Worker record changes, and a site that vanished with
// it would make the second run of a process find nothing the first one wrote. A worker
// in mockup mode forwards each operation to the route below; the engine's own handler
// calls the same site directly.
//
// Like the outbox it is **runtime state, not engine state** — memory, no event, nothing
// replayed (I4/I6), gone on restart — and it stays off the run loop: the sites hold
// their own lock, so neither a worker forwarding nor a Console polling can slow a
// running process down.

// mockReport is one mockup as Operations › Diagnostics › Mockups shows it — the envelope
// ADR-0216 settles: what kind it mocks, who holds it, which configured thing it stands
// in for, when it was read, the line a folded card shows, and the kind's own shape,
// which the server serves back without interpreting.
type mockReport struct {
	Kind    string `json:"kind"`
	Source  string `json:"source"`
	Target  string `json:"target"`
	At      int64  `json:"at"`
	Summary string `json:"summary"`
	Data    any    `json:"data"`
}

// handleMockReports serves every mockup this server can see, which is what Operations ›
// Diagnostics › Mockups renders (ADR-0216). Today that is the SharePoint mockups: they
// are in this server's memory, so they are collected at read time — "an in-process mock
// posts nothing" — and there is no report to go stale. The AD and SQL mockups still
// report to their own routes and views; moving them here is the rest of ADR-0216.
//
// Admin-gated, for the reason the mock directory and the SQL journal are
// (ADR-0213/0224): a mockup is invented, but what a process writes into it is shaped
// like the real thing — applicants, amounts, letters — and a view of it is no more public
// than the data it stands in for.
func (s *Server) handleMockReports(w http.ResponseWriter, _ *http.Request) {
	reports := []mockReport{}
	if s.sharePointMocks != nil {
		at := s.now()
		for _, snap := range s.sharePointMocks.Snapshot() {
			reports = append(reports, mockReport{
				Kind: connectorKindSharePoint, Source: "engine", Target: snap.Worker,
				At: at, Summary: sharePointMockSummary(snap), Data: snap,
			})
		}
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{"reports": reports})
}

// sharePointMockSummary is the one line a folded SharePoint card shows.
func sharePointMockSummary(snap sharepoint.MockSiteSnapshot) string {
	lists := 0
	for _, site := range snap.Sites {
		lists += len(site.Lists)
	}
	refused := 0
	for _, op := range snap.Journal {
		if op.Error != "" {
			refused++
		}
	}
	return fmt.Sprintf("%s, %s, %s", mockCount(len(snap.Sites), "site", "sites"),
		mockCount(lists, "list or library", "lists and libraries"), mockCount(refused, "refused operation", "refused operations"))
}

func mockCount(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// handleSharePointMockOperation performs one operation a worker in mockup mode
// forwarded, against the named Worker's site, and answers what the site answered — or,
// with 422, why it refused, worded as the site worded it, so the job fails with the
// message it would have failed with in the engine.
//
// It writes nothing durable and reaches nothing outside this process, so an
// authenticated caller posting here can change what a mockup holds and nothing else —
// the reasoning that lets a mail worker post into the preview outbox. A Worker that is
// not a mockup has no site here and is answered 404: this route never reaches a tenant.
func (s *Server) handleSharePointMockOperation(w http.ResponseWriter, r *http.Request) {
	worker := r.PathValue("worker")
	var site *sharepoint.MockSite
	if s.sharePointMocks != nil {
		site, _ = s.sharePointMocks.Lookup(worker)
	}
	if site == nil {
		httpapi.Error(w, http.StatusNotFound, "no SharePoint Worker named "+worker+" is a mockup on this server")
		return
	}
	var req sharepoint.Request
	dec := json.NewDecoder(io.LimitReader(r.Body, s.budgets().Payload))
	// Column values keep the JSON shape they were sent in: a number stays the number it
	// was written as, which is what the engine's own handler hands the site.
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid SharePoint operation: "+err.Error())
		return
	}
	answer, err := site.Do(r.Context(), req)
	if err != nil {
		httpapi.JSON(w, http.StatusUnprocessableEntity, sharepoint.MockAnswer{Error: err.Error()})
		return
	}
	httpapi.JSON(w, http.StatusOK, sharepoint.MockAnswer{Answer: answer})
}

// handleClearSharePointMockSite empties one Worker's mockup: the clean slate the next
// trial run wants. Nothing in it exists anywhere else, so clearing it destroys no record
// of anything that happened outside this server's memory.
func (s *Server) handleClearSharePointMockSite(w http.ResponseWriter, r *http.Request) {
	if s.sharePointMocks == nil || !s.sharePointMocks.Clear(r.PathValue("worker")) {
		httpapi.Error(w, http.StatusNotFound, "no SharePoint Worker named "+r.PathValue("worker")+" is a mockup on this server")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
