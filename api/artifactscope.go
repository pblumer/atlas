package api

import (
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
)

// This file enforces ADR-0071's "membership is inherited by the project's
// artifacts" on the design-time artifact handlers (drafts, DMN references,
// forms). An artifact filed into a project is governed by that project's sharing
// scope: reading it needs viewer, writing it needs editor.
//
// An ungrouped artifact — one with no project, or whose project no longer exists
// (the ADR-0034 degrade-to-Ungrouped case) — is a personal space governed by its
// own OwnerID: visible and writable only to its creator (and admins). An artifact
// that predates per-artifact ownership carries no OwnerID and stays open, the
// pre-scopes/legacy behavior, so nothing migrates. With auth off the server is
// open and everything below is a no-op, so single-user mode is unchanged.
//
// Runtime-facing reads stay OUT of scope, per ADR-0071: fetching a form to render
// a running task (handleGetForm) is not gated here — that is execution, not
// authoring.

// principalID is the acting account's id, or "" when there is none — an
// unauthenticated request on an open server, or a deploy the server makes for
// itself. Everything that records who did something reads it through here, so
// "nobody in particular" has one spelling.
func principalID(r *http.Request) string {
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		return p.UserID
	}
	return ""
}

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

// ungroupedRole reports the caller's role over an ungrouped artifact owned by
// ownerID (ADR-0071). Auth off is open; admin is owner-equivalent; an ownerless
// (legacy) artifact is open, as it was before per-artifact ownership; otherwise
// only the owner has access. A personal artifact is all-or-nothing — its owner is
// its owner — so this never returns viewer/editor.
func (s *Server) ungroupedRole(r *http.Request, ownerID string) string {
	if !s.authEnabled {
		return ScopeRoleOwner
	}
	pr := httpapi.PrincipalFrom(r.Context())
	if pr == nil {
		return ""
	}
	if pr.HasRole(RoleAdmin) {
		return ScopeRoleOwner
	}
	if ownerID == "" {
		return ScopeRoleOwner // legacy artifact with no recorded creator: open
	}
	if pr.UserID != "" && pr.UserID == ownerID {
		return ScopeRoleOwner
	}
	return ""
}

// artifactRole returns the caller's effective role over an artifact, using a
// preloaded projects map. An artifact filed into an existing project inherits
// that project's scope (ADR-0071); an ungrouped artifact, or one whose project is
// gone, is governed by its OwnerID via ungroupedRole.
func (s *Server) artifactRole(r *http.Request, projectID, ownerID string, projs map[string]project) string {
	if projectID != "" {
		if p, ok := projs[projectID]; ok {
			return p.effectiveRole(httpapi.PrincipalFrom(r.Context()), s.authEnabled)
		}
	}
	return s.ungroupedRole(r, ownerID)
}

// canViewArtifact reports whether the caller may see an artifact in a listing —
// the filter the list handlers apply.
func (s *Server) canViewArtifact(r *http.Request, projectID, ownerID string, projs map[string]project) bool {
	return scopeRank(s.artifactRole(r, projectID, ownerID, projs)) >= scopeRank(ScopeRoleViewer)
}

// authorizeArtifact authorizes the request against an artifact's governing scope
// at minRole. A project artifact loads its project and defers to checkProjectRole;
// an ungrouped one (or a dangling projectID — project deleted) is governed by its
// OwnerID. Either way a caller with no access gets 404 (hiding the artifact's
// existence, just as the listing does) and a caller with some but not enough gets
// 403.
func (s *Server) authorizeArtifact(r *http.Request, projectID, ownerID, minRole string) (int, string) {
	if projectID != "" {
		var (
			proj   project
			ok     bool
			getErr error
		)
		s.do(func() { proj, ok, getErr = s.projects.Get(projectID) })
		if getErr != nil {
			return http.StatusInternalServerError, "read project: " + getErr.Error()
		}
		if ok {
			return s.checkProjectRole(r, proj, minRole)
		}
		// dangling projectID → treated as ungrouped, governed by OwnerID below.
	}
	switch rank := scopeRank(s.ungroupedRole(r, ownerID)); {
	case rank == 0:
		return http.StatusNotFound, "no such artifact"
	case rank < scopeRank(minRole):
		return http.StatusForbidden, "insufficient access to this artifact"
	default:
		return 0, ""
	}
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

// artifactOwnerOnCreate returns the OwnerID to stamp on a newly created artifact:
// the signed-in principal, so an ungrouped artifact becomes that person's
// personal space (ADR-0071). Empty with auth off (no principal), which keeps the
// artifact ownerless and open — single-user mode is unchanged.
func (s *Server) artifactOwnerOnCreate(r *http.Request) string {
	if pr := httpapi.PrincipalFrom(r.Context()); pr != nil {
		return pr.UserID
	}
	return ""
}
