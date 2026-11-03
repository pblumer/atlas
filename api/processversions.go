package api

import (
	"net/http"
	"sort"
	"strconv"

	"github.com/pblumer/atlas/api/httpapi"
)

// processVersionCounts is one deployed version of a process with what it still holds:
// the instances running on it, the ones that have finished, and the tokens parked
// behind an incident.
//
// Running is named for what it is rather than "active", because Active already means
// something on a definition — whether it may start instances by itself (ADR-0119) —
// and an inactive version that still runs fifty instances is exactly the case an
// operator needs to read without confusing the two.
type processVersionCounts struct {
	Key        uint64 `json:"key"`
	Version    int32  `json:"version"`
	VersionTag string `json:"versionTag,omitempty"`
	DeployedAt int64  `json:"deployedAt"`
	Active     bool   `json:"active"`
	Running    int    `json:"running"`
	Finished   int    `json:"finished"`
	Incidents  int    `json:"incidents"`
}

type processVersionsResp struct {
	ProcessID string `json:"processId"`
	// IncidentCountsExact reports that every version's Incidents is the whole truth.
	// False only when the incident reading could not be taken at all; the counts are
	// then zero and a reader must not take them for "nothing is stuck".
	IncidentCountsExact bool                   `json:"incidentCountsExact"`
	Versions            []processVersionCounts `json:"versions"`
}

// handleProcessVersions answers, for every deployed version of the process a
// definition belongs to, how many instances run on it, how many have finished and how
// many tokens are parked behind an incident — newest version first.
//
// It exists because the live view shows one version at a time, and the question an
// operator brings to an old process is "which of its versions still hold work": with
// the counts only on each version's own view, the answer was to open every version in
// turn. A version with nothing in it is listed with zeroes, because the reading is
// "every version, and what it holds", and a missing row would be read as a missing
// version.
//
// Its cost is the number of *versions*, not of instances: two O(1) counter reads per
// version on the loop, the same reads GET /api/v1/instances/summary makes (ADR-0083),
// and the incident counts from the overlay's reading of the incident family, which one
// walk answers for every definition at once and the live view's own poll keeps warm
// (ADR-0366). That walk runs off the loop and after it, never inside it.
func (s *Server) handleProcessVersions(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	var (
		found bool
		resp  = processVersionsResp{Versions: []processVersionCounts{}}
	)
	p := httpapi.PrincipalFrom(r.Context())
	s.do(func() {
		d, ok := s.deployments[key]
		if !ok || !definitionReadable(p, d) {
			return
		}
		found = true
		resp.ProcessID = d.ProcessID
		for _, k := range s.order {
			v := s.deployments[k]
			if v == nil || v.ProcessID != d.ProcessID || !definitionReadable(p, v) {
				continue
			}
			// A display aggregate, as on the summary: a counter that cannot be read is
			// shown as 0 rather than failing every other version's row.
			running, _ := s.store.DefInstanceCount(k)
			finished, _ := s.store.DefCompletedCount(k)
			resp.Versions = append(resp.Versions, processVersionCounts{
				Key:        k,
				Version:    v.Version,
				VersionTag: v.cp.VersionTag(),
				DeployedAt: v.DeployedAt,
				Active:     !v.inactive,
				Running:    running,
				Finished:   finished,
			})
		}
	})
	if !found {
		httpapi.Error(w, http.StatusNotFound, "no deployment with that key")
		return
	}
	sort.SliceStable(resp.Versions, func(i, j int) bool {
		return resp.Versions[i].Version > resp.Versions[j].Version
	})
	// After the loop turn, not inside it: the reading takes its snapshot through the
	// loop (ADR-0266), and dispatching onto the loop from a closure it runs deadlocks.
	// The first lookup may walk; the rest answer from the reading it left.
	resp.IncidentCountsExact = true
	for i := range resp.Versions {
		parked, ok := s.incidentOverlay(resp.Versions[i].Key)
		if !ok {
			resp.IncidentCountsExact = false
			for j := range resp.Versions {
				resp.Versions[j].Incidents = 0
			}
			break
		}
		resp.Versions[i].Incidents = parked.total
	}
	httpapi.JSON(w, http.StatusOK, resp)
}
