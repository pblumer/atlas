package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/layout"
)

// A draft and a deployed definition are two records that only share a process id:
// saving writes the draft, deploying mints a version, and neither touches the other.
// So the diagram the Modeler opens from an application — the draft — need not be what
// runs. A change deployed straight from the canvas, from an opened version or by an
// agent is in the newest version and not in the draft, and the next Publish of the
// application deploys the draft over it.
//
// The comparison is how the Modeler says which of the two a person is looking at,
// and an agent can ask the same question (ADR-0452).

// draftComparisonResp says how a draft relates to one deployed version of its
// process: by default the newest, or the one ?key= names.
type draftComparisonResp struct {
	ProcessID string `json:"processId"`
	// OwnerID / SavedBy are who created the draft and whose save wrote it, so the
	// answer alone says who to ask (draftResp carries the same).
	OwnerID     string `json:"ownerId,omitempty"`
	OwnerName   string `json:"ownerName,omitempty"`
	SavedAt     int64  `json:"savedAt"`
	SavedBy     string `json:"savedBy,omitempty"`
	SavedByName string `json:"savedByName,omitempty"`
	// Deployed is false when no version of the process is deployed; the fields
	// describing the version are then absent and SameModel is false.
	Deployed bool `json:"deployed"`
	// Key and Version name the version compared against, and LatestVersion the
	// newest one deployed, so a reader can say "v7 of 9".
	Key            uint64 `json:"key,omitempty"`
	Version        int32  `json:"version,omitempty"`
	LatestVersion  int32  `json:"latestVersion,omitempty"`
	DeployedAt     int64  `json:"deployedAt,omitempty"`
	DeployedBy     string `json:"deployedBy,omitempty"`
	DeployedByName string `json:"deployedByName,omitempty"`
	// SameModel reports whether the two describe the same process everywhere the
	// engine looks. The diagram is not compared: a draft whose shapes moved since
	// the deploy is still that version.
	SameModel bool `json:"sameModel"`
	// Difference names where they first part, when they do.
	Difference string `json:"difference,omitempty"`
}

// handleDraftComparison compares a draft with a deployed version of its process.
// Reading it needs viewer on the draft's scope, like reading the draft's XML: the
// answer says what the draft contains.
func (s *Server) handleDraftComparison(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var key uint64
	if q := r.URL.Query().Get("key"); q != "" {
		k, err := strconv.ParseUint(q, 10, 64)
		if err != nil {
			httpapi.Error(w, http.StatusBadRequest, "invalid definition key")
			return
		}
		key = k
	}

	var (
		rec          draft
		found        bool
		readErr      error
		unknownKey   bool
		otherProcess string
		latest       int32
		// The compared version, copied out of the registry so it can be read after
		// the writer's turn. Its XML is replaced, never written into, by a layout
		// save (handleUpdateProcessDiagram), so holding the slice is safe.
		dep struct {
			ok      bool
			key     uint64
			version int32
			at      int64
			by      string
			xml     []byte
		}
	)
	s.do(func() {
		if rec, found, readErr = s.drafts.Get(id); readErr != nil || !found {
			return
		}
		d := s.latestDeploymentOf(rec.ProcessID)
		if d != nil {
			latest = d.Version
		}
		if key != 0 {
			if d = s.deployments[key]; d == nil {
				unknownKey = true
				return
			}
			if d.ProcessID != rec.ProcessID {
				otherProcess = d.ProcessID
				return
			}
		}
		if d != nil {
			dep.ok, dep.key, dep.version, dep.at, dep.by, dep.xml = true, d.Key, d.Version, d.DeployedAt, d.DeployedBy, d.xml
		}
	})
	switch {
	case readErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read draft: "+readErr.Error())
		return
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no draft with that process id")
		return
	}
	// The scope is checked before anything about the key is said, so a draft the
	// caller may not see stays a 404 whatever the query carries.
	if code, msg := s.authorizeArtifact(r, rec.ProjectID, rec.OwnerID, ScopeRoleViewer); code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	switch {
	case unknownKey:
		httpapi.Error(w, http.StatusNotFound, "no deployment with that key")
		return
	case otherProcess != "":
		httpapi.Error(w, http.StatusBadRequest, fmt.Sprintf(
			"definition %d is a version of %q, not of the draft's process %q", key, otherProcess, rec.ProcessID))
		return
	}

	name := s.principalNamer()
	resp := draftComparisonResp{
		ProcessID: rec.ProcessID, SavedAt: rec.SavedAt,
		OwnerID: rec.OwnerID, OwnerName: name(rec.OwnerID),
		SavedBy: rec.SavedBy, SavedByName: name(rec.SavedBy),
		LatestVersion: latest,
	}
	if dep.ok {
		// Off the loop: two parses of documents that grow with the model.
		diff, err := layout.Difference(dep.xml, []byte(rec.XML), "the deployed version", "the draft")
		if err != nil {
			httpapi.Error(w, http.StatusInternalServerError, fmt.Sprintf("compare the draft with version %d: %v", dep.version, err))
			return
		}
		resp.Deployed = true
		resp.Key, resp.Version, resp.DeployedAt = dep.key, dep.version, dep.at
		resp.DeployedBy, resp.DeployedByName = dep.by, name(dep.by)
		resp.SameModel, resp.Difference = diff == "", diff
	}
	httpapi.JSON(w, http.StatusOK, resp)
}
