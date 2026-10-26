package api

import (
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
)

// runtimeCounts is a definition's runtime overlay with its counts and none of its
// details (ADR-0493): how many instances are live and finished, how many tokens
// sit, have passed or were cancelled on each element, and how many are parked
// behind an incident. It is what a system that shows the process elsewhere needs,
// and all a `diagrams` token reaches.
//
// What it leaves out is the reason it exists. The overlay's incident details carry
// a message, which can carry business data, and name the instance, the job and the
// Worker behind it; a credential held on another server, for as long as the
// integration lives, should not read them because a picture wants a red badge.
//
// The fields are copied one by one rather than the overlay being trimmed, so a
// field added to runtimeResp later reaches this answer only when somebody adds it
// here — a decision, not a side effect. runtimeElement is shared because it is
// counts and nothing else; TestRuntimeCountsCarryNoIncidentDetails holds it to that.
type runtimeCounts struct {
	Instances           int              `json:"instances"`
	Finished            int              `json:"finished"`
	Tokens              int              `json:"tokens"`
	Elements            []runtimeElement `json:"elements"`
	IncidentTotal       int              `json:"incidentTotal"`
	IncidentCountsExact bool             `json:"incidentCountsExact"`
}

// handleProcessRuntimeCounts answers the overlay's counts for the whole definition.
//
// It refuses ?instance= rather than ignoring it: isolating one instance is reading
// that instance, which this answer exists not to do, and a caller who asked for one
// instance must not be handed the whole definition's numbers as if they were its.
func (s *Server) handleProcessRuntimeCounts(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Has("instance") {
		httpapi.Error(w, http.StatusBadRequest, "the counts are the whole definition's; one instance is isolated on GET /api/v1/processes/{key}/runtime")
		return
	}
	s.serveProcessRuntime(w, r, func(resp runtimeResp) any {
		return runtimeCounts{
			Instances:           resp.Instances,
			Finished:            resp.Finished,
			Tokens:              resp.Tokens,
			Elements:            resp.Elements,
			IncidentTotal:       resp.IncidentTotal,
			IncidentCountsExact: resp.IncidentCountsExact,
		}
	})
}
