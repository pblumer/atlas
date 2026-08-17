package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// Application releases (ADR-0127 Phase 2). Publishing an application runs the
// ADR-0034 bundle deploy and, only when the whole bundle registered, records an
// applicationRelease: the manifest of what shipped together at which artifact
// versions, numbered by a per-application counter layered above the ADR-0019
// per-processId version.
//
// This is all design-time state. Nothing here enters the event log or affects
// recovery of running instances; the counter is rebuilt from the release sidecars
// at startup (loadReleaseVersions), the same discipline loadDeployments uses.

// loadReleaseVersions rebuilds the per-application release counter from the durable
// records, so the publish after a restart continues the sequence rather than
// restarting it at v1. It runs before the loop serves traffic, so touching the map
// directly here respects the single-writer invariant.
func (s *Server) loadReleaseVersions() error {
	recs, err := s.releases.loadAll()
	if err != nil {
		return err
	}
	for _, rec := range recs {
		if rec.Version > s.appVersions[rec.ApplicationID] {
			s.appVersions[rec.ApplicationID] = rec.Version
		}
	}
	return nil
}

// publishResp is the publish outcome: the bundle-deploy report plus the release it
// minted. Release is nil when the bundle was refused — a refused publish deploys
// nothing, so there is nothing to record (all-or-nothing, ADR-0034).
type publishResp struct {
	projectDeployResp
	Release *applicationRelease `json:"release,omitempty"`
}

// handlePublishApplication is the headline ADR-0127 action: validate and deploy the
// application's artifacts as one bundle, then mint the next application release.
// Body (optional): {"note": "what changed"}.
func (s *Server) handlePublishApplication(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var payload struct {
		Note string `json:"note"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	// An absent or empty body is a plain publish with no note; only malformed JSON
	// is an error, so `POST .../publish` with no body keeps working.
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
	}

	out := s.deployApplicationBundle(r, id)
	if !out.deployed {
		// Refused or errored: render the bundle outcome unchanged, no release.
		out.write(w)
		return
	}

	rel, err := s.mintRelease(r, out, strings.TrimSpace(payload.Note))
	if err != nil {
		// The definitions are deployed and durable; only the release manifest failed.
		// Report it rather than pretending the publish did not happen.
		writeError(w, http.StatusInternalServerError, "record release: "+err.Error())
		return
	}
	writeJSON(w, out.status, publishResp{projectDeployResp: out.resp, Release: &rel})
}

// mintRelease records the release for a successful bundle deploy: the next version
// for this application, and a by-value snapshot of the definitions that shipped, so
// later edits cannot rewrite what was already published.
func (s *Server) mintRelease(r *http.Request, out bundleOutcome, note string) (applicationRelease, error) {
	relID, err := newID()
	if err != nil {
		return applicationRelease{}, err
	}
	members := make([]releaseMember, 0, len(out.resp.Definitions))
	for _, d := range out.resp.Definitions {
		members = append(members, releaseMember{
			Kind: "process", Ref: d.ProcessID, ArtifactVer: d.Version, Key: d.Key,
		})
	}
	rec := applicationRelease{
		ID:            relID,
		ApplicationID: out.proj.ID,
		PublishedAt:   time.Now().Unix(),
		Note:          note,
		Members:       members,
	}
	if p := principalFrom(r.Context()); p != nil {
		rec.PublishedBy = p.UserID
	}
	var saveErr error
	s.do(func() {
		rec.Version = s.appVersions[out.proj.ID] + 1
		if saveErr = s.releases.save(rec); saveErr != nil {
			return
		}
		s.appVersions[out.proj.ID] = rec.Version
	})
	return rec, saveErr
}

// handleListReleases returns one application's release history, newest first.
func (s *Server) handleListReleases(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, code, msg := s.authorizeProject(r, id, ScopeRoleViewer); code != 0 {
		writeError(w, code, msg)
		return
	}
	var (
		out     []applicationRelease
		loadErr error
	)
	s.do(func() { out, loadErr = s.releases.forApplication(id) })
	if loadErr != nil {
		writeError(w, http.StatusInternalServerError, "list releases: "+loadErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// applicationDeploymentsResp is the per-application live view: which of this
// application's definitions are deployed right now, at which version, and how many
// instances they are carrying. This is what ADR-0127 moves *into* the application
// from the global deployed list.
//
// Version is the latest published release version, or 0 for an application that has
// deployed definitions but no recorded release (e.g. deployed before Phase 2, or via
// the lower-level deploy route).
type applicationDeploymentsResp struct {
	ID          string                  `json:"id"`
	Name        string                  `json:"name"`
	Version     int32                   `json:"version"`
	PublishedAt int64                   `json:"publishedAt,omitempty"`
	Running     int                     `json:"running"`
	Finished    int                     `json:"finished"`
	Definitions []applicationDefinition `json:"definitions"`
}

// applicationDefinition is one deployed definition belonging to an application.
type applicationDefinition struct {
	Key          uint64 `json:"key"`
	ProcessID    string `json:"processId"`
	Name         string `json:"name"`
	Version      int32  `json:"version"`
	DeployedAt   int64  `json:"deployedAt"`
	Active       bool   `json:"active"`
	Running      int    `json:"running"`
	Finished     int    `json:"finished"`
	LastActivity int64  `json:"lastActivity,omitempty"`
}

// handleApplicationDeployments reports what this application currently has deployed
// on this server, with per-definition instance counts.
func (s *Server) handleApplicationDeployments(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	proj, code, msg := s.authorizeProject(r, id, ScopeRoleViewer)
	if code != 0 {
		writeError(w, code, msg)
		return
	}

	out := applicationDeploymentsResp{ID: proj.ID, Name: proj.Name, Definitions: []applicationDefinition{}}
	var loadErr error
	s.do(func() {
		for _, key := range s.order {
			d := s.deployments[key]
			if d == nil || d.ProjectID != id {
				continue
			}
			// Each is an O(1) point read of a maintained counter (ADR-0080/0083); this
			// is a display aggregate, so a counter that cannot be read shows as 0
			// rather than failing the whole view — the same choice the instances
			// summary makes.
			active, _ := s.store.DefInstanceCount(key)
			completed, _ := s.store.DefCompletedCount(key)
			lastAct, _ := s.store.DefLastActivity(key)
			out.Definitions = append(out.Definitions, applicationDefinition{
				Key: key, ProcessID: d.ProcessID, Name: d.Name, Version: d.Version,
				DeployedAt: d.DeployedAt, Active: !d.inactive,
				Running: active, Finished: completed, LastActivity: lastAct,
			})
			out.Running += active
			out.Finished += completed
		}
		// The application's current version is its latest release (Phase 2). An
		// application deployed without a release keeps version 0.
		rels, err := s.releases.forApplication(id)
		if err != nil {
			loadErr = err
			return
		}
		if len(rels) > 0 {
			out.Version = rels[0].Version // forApplication is newest-first
			out.PublishedAt = rels[0].PublishedAt
		}
	})
	if loadErr != nil {
		writeError(w, http.StatusInternalServerError, "read releases: "+loadErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}
