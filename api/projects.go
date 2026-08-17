package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// projectView is the JSON shape of a project for the Modeler. Artifacts is the
// number of artifacts (BPMN drafts + DMN references) currently tagged with this
// project. OwnerID, Visibility, and Members are the ADR-0071 sharing scope;
// MyRole is the caller's own effective role, so the UI can gate its sharing
// controls (hide "Share" from a viewer, "Delete" from a non-owner, …) without
// re-deriving the rule client-side.
type projectView struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	OwnerID    string          `json:"ownerId,omitempty"`
	Visibility string          `json:"visibility"`
	Members    []projectMember `json:"members"`
	MyRole     string          `json:"myRole"`
	Protected  bool            `json:"protected"`
	CreatedAt  int64           `json:"createdAt"`
	UpdatedAt  int64           `json:"updatedAt"`
	Artifacts  int             `json:"artifacts"`
}

// projectViewFor renders a project for the requesting principal. It normalizes
// Members to a non-nil slice and defaults an empty (pre-scopes) Visibility to
// private, so the JSON shape is stable, and stamps MyRole from the same pure
// access rule the handlers enforce.
func (s *Server) projectViewFor(r *http.Request, p project, artifacts int) projectView {
	vis := p.Visibility
	if vis == "" {
		vis = VisibilityPrivate
	}
	members := p.Members
	if members == nil {
		members = []projectMember{}
	}
	return projectView{
		ID:         p.ID,
		Name:       p.Name,
		OwnerID:    p.OwnerID,
		Visibility: vis,
		Members:    members,
		MyRole:     p.effectiveRole(principalFrom(r.Context()), s.authEnabled),
		Protected:  p.Protected,
		CreatedAt:  p.CreatedAt,
		UpdatedAt:  p.UpdatedAt,
		Artifacts:  artifacts,
	}
}

// newID mints a random, URL-safe id for design-time artifacts (projects, DMN
// references). These are organizational state, not engine facts (ADR-0034), so a
// server-generated random id is fine — it never enters the event log and is not
// replayed.
func newID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// handleCreateProject creates a named project. Body: {"name": "..."}.
func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "project name is required")
		return
	}
	id, err := newID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generate id: "+err.Error())
		return
	}
	now := time.Now().Unix()
	// A new project is private to its creator (ADR-0071). Under auth the owner is
	// the signed-in principal; with auth off there is no principal, so the project
	// is ownerless and the open single-user server treats it as fully accessible.
	rec := project{ID: id, Name: name, Visibility: VisibilityPrivate, CreatedAt: now, UpdatedAt: now}
	if p := principalFrom(r.Context()); p != nil {
		rec.OwnerID = p.UserID
	}
	var saveErr error
	s.do(func() { saveErr = s.projects.save(rec) })
	if saveErr != nil {
		writeError(w, http.StatusInternalServerError, "create project: "+saveErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.projectViewFor(r, rec, 0))
}

// handleListProjects lists the projects the caller may see (oldest first) with a
// live count of the artifacts tagged into each. Under auth a project the caller
// has no role on is filtered out entirely — the same information hiding the
// per-project 404 gives — so a private project never appears in another user's
// list (ADR-0071). With auth off every project is listed (open, single-user).
func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	list := []projectView{}
	var loadErr error
	s.do(func() {
		var projs []project
		if projs, loadErr = s.projects.loadAll(); loadErr != nil {
			return
		}
		var drafts []draft
		if drafts, loadErr = s.drafts.loadAll(); loadErr != nil {
			return
		}
		var refs []dmnRef
		if refs, loadErr = s.dmnrefs.loadAll(); loadErr != nil {
			return
		}
		counts := map[string]int{}
		for _, d := range drafts {
			if d.ProjectID != "" {
				counts[d.ProjectID]++
			}
		}
		for _, rec := range refs {
			if rec.ProjectID != "" {
				counts[rec.ProjectID]++
			}
		}
		for _, p := range projs {
			if scopeRank(p.effectiveRole(principalFrom(r.Context()), s.authEnabled)) == 0 {
				continue
			}
			list = append(list, s.projectViewFor(r, p, counts[p.ID]))
		}
	})
	if loadErr != nil {
		writeError(w, http.StatusInternalServerError, "list projects: "+loadErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleUpdateProject updates a project's mutable fields. Body may carry any of
// {"name","visibility","ownerId"}; at least one is required. Renaming, changing
// visibility (private/shared), and transferring ownership all require the owner
// role (ADR-0071) — under auth off the open server treats every caller as owner,
// so a name-only PATCH keeps working exactly as before. A transfer must name an
// existing user.
func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		Name       *string `json:"name"`
		Visibility *string `json:"visibility"`
		OwnerID    *string `json:"ownerId"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if payload.Name == nil && payload.Visibility == nil && payload.OwnerID == nil {
		writeError(w, http.StatusBadRequest, "no fields to update")
		return
	}
	var name string
	if payload.Name != nil {
		if name = strings.TrimSpace(*payload.Name); name == "" {
			writeError(w, http.StatusBadRequest, "project name cannot be empty")
			return
		}
	}
	if payload.Visibility != nil && *payload.Visibility != VisibilityPrivate && *payload.Visibility != VisibilityShared {
		writeError(w, http.StatusBadRequest, `visibility must be "private" or "shared"`)
		return
	}
	var (
		notFound            bool
		forbidden           int
		fmsg                string
		ownerMissing        bool
		getErr, saveErr     error
		lookupErr, countErr error
		view                projectView
	)
	s.do(func() {
		rec, ok, e := s.projects.get(id)
		if e != nil {
			getErr = e
			return
		}
		if !ok {
			notFound = true
			return
		}
		if code, msg := s.checkProjectRole(r, rec, ScopeRoleOwner); code != 0 {
			forbidden, fmsg = code, msg
			return
		}
		if code, msg := protectedGuard(rec); code != 0 {
			forbidden, fmsg = code, msg
			return
		}
		if payload.OwnerID != nil && s.authEnabled {
			if _, ok, e := s.users.get(*payload.OwnerID); e != nil {
				lookupErr = e
				return
			} else if !ok {
				ownerMissing = true
				return
			}
		}
		if payload.Name != nil {
			rec.Name = name
		}
		if payload.Visibility != nil {
			rec.Visibility = *payload.Visibility
		}
		if payload.OwnerID != nil {
			rec.OwnerID = *payload.OwnerID
		}
		rec.UpdatedAt = time.Now().Unix()
		if saveErr = s.projects.save(rec); saveErr != nil {
			return
		}
		n, e := s.countArtifactsInProject(id)
		if e != nil {
			countErr = e
			return
		}
		view = s.projectViewFor(r, rec, n)
	})
	switch {
	case getErr != nil:
		writeError(w, http.StatusInternalServerError, "read project: "+getErr.Error())
	case notFound:
		writeError(w, http.StatusNotFound, "no project with that id")
	case forbidden != 0:
		writeError(w, forbidden, fmsg)
	case lookupErr != nil:
		writeError(w, http.StatusInternalServerError, "look up new owner: "+lookupErr.Error())
	case ownerMissing:
		writeError(w, http.StatusBadRequest, "no user with that id")
	case saveErr != nil:
		writeError(w, http.StatusInternalServerError, "update project: "+saveErr.Error())
	case countErr != nil:
		writeError(w, http.StatusInternalServerError, "count artifacts: "+countErr.Error())
	default:
		writeJSON(w, http.StatusOK, view)
	}
}

// handleDeleteProject removes a project. Deleting requires the owner role
// (ADR-0071); it stays idempotent, so deleting an absent project — one a caller
// cannot see is indistinguishable from one that is gone — still succeeds.
// Artifacts tagged with the id are intentionally left in place; they read as
// Ungrouped once the project is gone (ADR-0034).
func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var (
		notFound  bool
		forbidden int
		fmsg      string
		getErr    error
		delErr    error
	)
	s.do(func() {
		rec, ok, e := s.projects.get(id)
		if e != nil {
			getErr = e
			return
		}
		if !ok {
			notFound = true // absent: idempotent success below
			return
		}
		if code, msg := s.checkProjectRole(r, rec, ScopeRoleOwner); code != 0 {
			forbidden, fmsg = code, msg
			return
		}
		if code, msg := protectedGuard(rec); code != 0 {
			forbidden, fmsg = code, msg
			return
		}
		if delErr = s.projects.delete(id); delErr != nil {
			return
		}
		// Drop the application's release history with it (ADR-0127). Unlike the
		// artifacts — which deliberately survive and fall back to Ungrouped — a
		// release is metadata *about* this application, reachable only through its
		// id, so leaving the records behind would accumulate unreachable files. The
		// deployed definitions themselves are untouched, as they always were.
		if delErr = s.releases.deleteForApplication(id); delErr != nil {
			return
		}
		delete(s.appVersions, id)
	})
	switch {
	case getErr != nil:
		writeError(w, http.StatusInternalServerError, "read project: "+getErr.Error())
	case forbidden != 0:
		writeError(w, forbidden, fmsg)
	case delErr != nil:
		writeError(w, http.StatusInternalServerError, "delete project: "+delErr.Error())
	case notFound:
		w.WriteHeader(http.StatusNoContent) // idempotent: nothing to delete
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// countArtifactsInProject counts the artifacts (BPMN drafts + DMN references)
// tagged with a project id. It must be called on the run-loop goroutine (inside
// do).
func (s *Server) countArtifactsInProject(id string) (int, error) {
	drafts, err := s.drafts.loadAll()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range drafts {
		if d.ProjectID == id {
			n++
		}
	}
	refs, err := s.dmnrefs.loadAll()
	if err != nil {
		return 0, err
	}
	for _, r := range refs {
		if r.ProjectID == id {
			n++
		}
	}
	return n, nil
}
