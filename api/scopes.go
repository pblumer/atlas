package api

import (
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// This file implements ADR-0071 "sharing scopes": a private-vs-shared access
// boundary layered onto the project (ADR-0034). A project gains an owner, a
// visibility, and a role-bearing member list; the design-time HTTP handlers
// enforce those against the resolved *Principal (ADR-0044). Nothing here touches
// the engine, the WAL, or applyToState — a sharing scope is operator/config
// data, exactly like a project or a user, so it stays off the six invariants.
//
// Naming: "scope" is already used inside the engine for FEEL/activity-local
// *variable* scopes (ADR-0068). This concept is a *sharing* scope — an access
// boundary on authoring — and is deliberately kept distinct.

// Visibility values for a project scope. A private project is visible only to
// its owner (and admins); a shared project additionally grants each listed
// member their role. The set is intentionally small and open: an org/public
// value can join later without reshaping the field (ADR-0071).
const (
	VisibilityPrivate = "private"
	VisibilityShared  = "shared"
)

// Scope roles, lowest to highest privilege. viewer reads, editor reads+writes,
// owner additionally manages membership, visibility, transfer, and deletion.
// owner is implicit (it is the project's OwnerID), never stored in Members. The
// list mirrors ADR-0044's "roles are a list, only some values enforced now"
// discipline, so richer roles cost no reshaping.
const (
	ScopeRoleViewer = "viewer"
	ScopeRoleEditor = "editor"
	ScopeRoleOwner  = "owner"
)

// PrincipalTypeUser is the only member reference type Phase 1 implements. The
// type field exists so groups ("group") slot in later without a migration
// (ADR-0071/0044 follow-up).
const PrincipalTypeUser = "user"

// principalRef names who a grant is for. Type is "user" today; carrying it now
// is what lets a group grant ("group") be added later without rewriting stored
// members.
type principalRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// projectMember grants one principal a role on a shared project.
type projectMember struct {
	Ref  principalRef `json:"ref"`
	Role string       `json:"role"`
}

// scopeRank ranks the roles so "at least editor" is a simple integer compare.
// An unknown or empty role ranks 0 (no access), so a caller with no grant never
// clears any threshold.
func scopeRank(role string) int {
	switch role {
	case ScopeRoleOwner:
		return 3
	case ScopeRoleEditor:
		return 2
	case ScopeRoleViewer:
		return 1
	default:
		return 0
	}
}

// isScopeRole reports whether a role string is one an owner may grant to a
// member (viewer or editor). owner is not grantable — ownership is transferred,
// not assigned as a member role.
func isScopeRole(role string) bool {
	return role == ScopeRoleViewer || role == ScopeRoleEditor
}

// effectiveRole reports the role a principal holds on a project, and "" for no
// access. It is the single encoding of the ADR-0071 access rule, kept pure so it
// can be unit-tested directly and reused by every handler:
//
//   - Auth disabled: the server is open (single-user), so everyone is treated as
//     an owner — the same "off by default, unchanged" stance as ADR-0044.
//   - admin: owner-equivalent on every project.
//   - Ownerless (legacy) project — one that predates scopes: no non-admin access
//     under auth until an admin assigns an owner; open when auth is off.
//   - The owner is an implicit owner.
//   - A shared project grants each listed user member their stored role; a
//     private project grants nobody but owner/admin, so flipping to private
//     revokes members without clearing the list.
func (p project) effectiveRole(pr *Principal, authEnabled bool) string {
	if !authEnabled {
		return ScopeRoleOwner
	}
	if pr == nil {
		return ""
	}
	if pr.hasRole(RoleAdmin) {
		return ScopeRoleOwner
	}
	// A deploy agent (ADR-0129) is a peer Atlas that published here, not a person.
	// Sharing scopes cannot express it — there is no account to own or be a member
	// of anything — and what it may do is already bounded, tightly, by the
	// middleware allowlist: push a bundle, and read back what this server runs for
	// an application. Viewer is what those reads need.
	//
	// Trade-off, accepted and worth stating: this is viewer on *every* application,
	// not only the ones a given peer published, so a leaked token could read the
	// deployment counts of applications it never shipped. The allowlist keeps that
	// to exactly one read-only endpoint; narrowing it further would mean the
	// receiver tracking which peer owns which application, which is state ADR-0129
	// deliberately keeps on the publishing side.
	if pr.hasRole(RoleDeployAgent) {
		return ScopeRoleViewer
	}
	// A protected system project (ADR-0122) is visible to every authenticated
	// principal so its platform processes can be found and started, but it grants
	// no more than viewer here — mutation is separately refused for all callers by
	// the protected guard, so no member/owner role on it is meaningful.
	if p.Protected {
		return ScopeRoleViewer
	}
	if p.OwnerID == "" {
		return "" // legacy/ownerless: only admin manages it (handled above)
	}
	if pr.UserID != "" && pr.UserID == p.OwnerID {
		return ScopeRoleOwner
	}
	if p.Visibility == VisibilityShared && pr.UserID != "" {
		for _, m := range p.Members {
			if m.Ref.Type == PrincipalTypeUser && m.Ref.ID == pr.UserID {
				return m.Role
			}
		}
	}
	return ""
}

// checkProjectRole authorizes an already-loaded project for the request at a
// minimum role. It returns an HTTP status (0 = authorized) and a message. A
// caller with no access at all gets 404 — the project is hidden from them just
// as it is hidden from their listing, so its existence never leaks — while a
// caller who has some access but not enough gets 403. The check is pure (it only
// reads the request's Principal), so it is safe to call anywhere.
func (s *Server) checkProjectRole(r *http.Request, p project, minRole string) (int, string) {
	rank := scopeRank(p.effectiveRole(principalFrom(r.Context()), s.authEnabled))
	switch {
	case rank == 0:
		return http.StatusNotFound, "no project with that id"
	case rank < scopeRank(minRole):
		return http.StatusForbidden, "insufficient project access"
	default:
		return 0, ""
	}
}

// authorizeProject loads a project by id and authorizes the request against it
// at minRole, running the store read on the run loop. It returns the loaded
// project and status 0 on success; otherwise it returns a non-zero HTTP status
// and message and a zero project. Handlers that already load the project inside
// their own do() call checkProjectRole directly instead, to avoid a second
// round-trip.
func (s *Server) authorizeProject(r *http.Request, id, minRole string) (project, int, string) {
	var (
		proj   project
		ok     bool
		getErr error
	)
	s.do(func() { proj, ok, getErr = s.projects.get(id) })
	if getErr != nil {
		return project{}, http.StatusInternalServerError, "read project: " + getErr.Error()
	}
	if !ok {
		return project{}, http.StatusNotFound, "no project with that id"
	}
	if code, msg := s.checkProjectRole(r, proj, minRole); code != 0 {
		return project{}, code, msg
	}
	return proj, 0, ""
}

// handleSetProjectMember shares a project with a user, or changes their role
// (ADR-0071). PUT /api/v1/projects/{id}/members/{userId}, body {"role":"viewer"|
// "editor"}. Only the owner (or an admin) may share. Adding the first member
// flips the project to "shared" so the grant takes effect immediately rather
// than sitting inert behind a still-private visibility.
func (s *Server) handleSetProjectMember(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	userID := r.PathValue("userId")
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if !isScopeRole(payload.Role) {
		writeError(w, http.StatusBadRequest, `role must be "viewer" or "editor"`)
		return
	}

	proj, code, msg := s.authorizeProject(r, id, ScopeRoleOwner)
	if code != 0 {
		writeError(w, code, msg)
		return
	}
	if code, msg := protectedGuard(proj); code != 0 {
		writeError(w, code, msg)
		return
	}
	if userID == proj.OwnerID {
		writeError(w, http.StatusBadRequest, "the owner already has full access and cannot be added as a member")
		return
	}

	var (
		userMissing     bool
		lookupErr, sErr error
		view            projectView
	)
	s.do(func() {
		if s.authEnabled {
			// Only grant to a real identity. With auth off there is no user store
			// to consult (and sharing is moot), so the check is skipped.
			if _, ok, e := s.users.get(userID); e != nil {
				lookupErr = e
				return
			} else if !ok {
				userMissing = true
				return
			}
		}
		proj.Members = upsertMember(proj.Members, userID, payload.Role)
		proj.Visibility = VisibilityShared
		proj.UpdatedAt = time.Now().Unix()
		if sErr = s.projects.save(proj); sErr != nil {
			return
		}
		n, e := s.countArtifactsInProject(proj.ID)
		if e != nil {
			sErr = e
			return
		}
		view = s.projectViewFor(r, proj, n)
	})
	switch {
	case lookupErr != nil:
		writeError(w, http.StatusInternalServerError, "look up user: "+lookupErr.Error())
	case userMissing:
		writeError(w, http.StatusBadRequest, "no user with that id")
	case sErr != nil:
		writeError(w, http.StatusInternalServerError, "share project: "+sErr.Error())
	default:
		writeJSON(w, http.StatusOK, view)
	}
}

// handleRemoveProjectMember revokes a user's membership (ADR-0071).
// DELETE /api/v1/projects/{id}/members/{userId}. Owner (or admin) only. It is
// idempotent: removing a non-member succeeds. Visibility is left as-is — an
// owner who wants to seal the project sets it back to private explicitly.
func (s *Server) handleRemoveProjectMember(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	userID := r.PathValue("userId")

	proj, code, msg := s.authorizeProject(r, id, ScopeRoleOwner)
	if code != 0 {
		writeError(w, code, msg)
		return
	}
	if code, msg := protectedGuard(proj); code != 0 {
		writeError(w, code, msg)
		return
	}

	var (
		sErr error
		view projectView
	)
	s.do(func() {
		proj.Members = removeMember(proj.Members, userID)
		proj.UpdatedAt = time.Now().Unix()
		if sErr = s.projects.save(proj); sErr != nil {
			return
		}
		n, e := s.countArtifactsInProject(proj.ID)
		if e != nil {
			sErr = e
			return
		}
		view = s.projectViewFor(r, proj, n)
	})
	if sErr != nil {
		writeError(w, http.StatusInternalServerError, "unshare project: "+sErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// upsertMember adds a user member with the given role, or updates the role if
// the user is already a member. It never duplicates a user.
func upsertMember(members []projectMember, userID, role string) []projectMember {
	for i, m := range members {
		if m.Ref.Type == PrincipalTypeUser && m.Ref.ID == userID {
			members[i].Role = role
			return members
		}
	}
	return append(members, projectMember{Ref: principalRef{Type: PrincipalTypeUser, ID: userID}, Role: role})
}

// removeMember drops a user member if present, returning the list unchanged
// otherwise.
func removeMember(members []projectMember, userID string) []projectMember {
	out := members[:0]
	for _, m := range members {
		if m.Ref.Type == PrincipalTypeUser && m.Ref.ID == userID {
			continue
		}
		out = append(out, m)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
