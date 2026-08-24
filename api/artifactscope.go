package api

import (
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
)

// This file enforces ADR-0071's "membership is inherited by the project's
// artifacts" on the design-time artifact handlers (drafts, DMN references,
// forms). An artifact filed into a project is governed by that project's sharing
// scope: reading it needs viewer, writing it needs editor. An ungrouped artifact
// — or one whose project no longer exists (the ADR-0034 degrade-to-Ungrouped
// case) — is open, the pre-scopes/legacy behavior, until per-artifact ownership
// gives Ungrouped a personal owner (a separate follow-up). With auth off the
// server is open and everything below is a no-op, so single-user mode is
// unchanged.
//
// Runtime-facing reads stay OUT of scope, per ADR-0071: fetching a form to render
// a running task (handleGetForm) is not gated here — that is execution, not
// authoring.

// projectsByID loads every project into an id→project map so a list handler can
// resolve many artifacts' scopes without a store read per artifact. It must be
// called on the run-loop goroutine (inside s.do), like the store it reads.
func (s *Server) projectsByID() (map[string]project, error) {
	all, err := s.projects.LoadAll()
	if err != nil {
		return nil, err
	}
	m := make(map[string]project, len(all))
	for _, p := range all {
		m[p.ID] = p
	}
	return m, nil
}

// artifactRole returns the caller's effective role over an artifact governed by
// projectID, using a preloaded projects map. An artifact in an existing project
// inherits that project's scope (ADR-0071); an ungrouped artifact, or one whose
// project is gone, is open (owner-equivalent).
func (s *Server) artifactRole(r *http.Request, projectID string, projs map[string]project) string {
	if projectID != "" {
		if p, ok := projs[projectID]; ok {
			return p.effectiveRole(httpapi.PrincipalFrom(r.Context()), s.authEnabled)
		}
	}
	return ScopeRoleOwner
}

// canViewArtifact reports whether the caller may see an artifact in a listing —
// the filter the list handlers apply.
func (s *Server) canViewArtifact(r *http.Request, projectID string, projs map[string]project) bool {
	return scopeRank(s.artifactRole(r, projectID, projs)) >= scopeRank(ScopeRoleViewer)
}

// authorizeArtifact authorizes the request against an artifact's governing scope
// at minRole, loading the one project on the run loop. An ungrouped artifact
// (projectID "") or a dangling projectID (project deleted) is open and returns
// status 0. Otherwise it defers to checkProjectRole, which returns 404 when the
// caller has no access (hiding the artifact's existence, just as the listing
// does) and 403 when they have some access but not enough.
func (s *Server) authorizeArtifact(r *http.Request, projectID, minRole string) (int, string) {
	if projectID == "" {
		return 0, ""
	}
	var (
		proj   project
		ok     bool
		getErr error
	)
	s.do(func() { proj, ok, getErr = s.projects.Get(projectID) })
	if getErr != nil {
		return http.StatusInternalServerError, "read project: " + getErr.Error()
	}
	if !ok {
		return 0, "" // dangling projectId → treated as ungrouped → open
	}
	return s.checkProjectRole(r, proj, minRole)
}

// authorizeTargetProject authorizes filing an artifact INTO a named project at
// minRole. Unlike authorizeArtifact it treats a missing project as the caller
// error it is for a create/move destination — a 400 "unknown project id",
// preserving the existing contract — rather than as Ungrouped. Call it only when
// projectID is non-empty (Ungrouped needs no target check).
func (s *Server) authorizeTargetProject(r *http.Request, projectID, minRole string) (int, string) {
	var (
		proj   project
		ok     bool
		getErr error
	)
	s.do(func() { proj, ok, getErr = s.projects.Get(projectID) })
	if getErr != nil {
		return http.StatusInternalServerError, "read project: " + getErr.Error()
	}
	if !ok {
		return http.StatusBadRequest, "unknown project id"
	}
	return s.checkProjectRole(r, proj, minRole)
}
