package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
)

// This file is the HTTP surface for user groups (ADR-0180).
// Managing a group is administration, so every handler is admin-gated exactly
// like user management (ADR-0044); a non-admin owner reaches groups only through
// the principals directory, to pick one to share with. Every store access runs on
// the run loop via s.do, the sidecar discipline.

// newGroupID mints a stable, opaque group id. The "grp_" prefix keeps ids
// self-describing in logs and URLs; the random suffix guarantees uniqueness.
func newGroupID() (string, error) {
	suffix, err := randomHex(12)
	if err != nil {
		return "", err
	}
	return "grp_" + suffix, nil
}

// groupView is the outward shape of a group: its id, name, and member ids
// (normalized to a non-nil array), plus timestamps. A group carries no secret, so
// this is the record itself with Members made JSON-stable.
type groupView struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Members   []string `json:"members"`
	CreatedAt int64    `json:"createdAt"`
	UpdatedAt int64    `json:"updatedAt"`
}

func toGroupView(g group) groupView {
	members := g.Members
	if members == nil {
		members = []string{}
	}
	return groupView{ID: g.ID, Name: g.Name, Members: members, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt}
}

// handleListGroups lists every group (admin-only). Oldest first.
func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	list := []groupView{}
	var loadErr error
	s.do(func() {
		var recs []group
		if recs, loadErr = s.groups.LoadAll(); loadErr != nil {
			return
		}
		for _, g := range recs {
			list = append(list, toGroupView(g))
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list groups: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, list)
}

// handleCreateGroup creates a named group (admin-only). Body: {"name":"..."}. The
// name must be non-empty and unique (case-insensitive). Members are added
// afterward via the member endpoints.
func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Name string `json:"name"`
	}
	if !decodeJSONBody(w, r, &payload) {
		return
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		httpapi.Error(w, http.StatusBadRequest, "group name is required")
		return
	}
	id, err := newGroupID()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "create group: "+err.Error())
		return
	}
	now := time.Now().Unix()
	rec := group{ID: id, Name: name, CreatedAt: now, UpdatedAt: now}
	// The uniqueness check and the write happen in one run-loop turn, so no
	// concurrent create can slip a duplicate name between them.
	var (
		conflict bool
		saveErr  error
	)
	s.do(func() {
		if _, ok, e := s.groups.byName(name, ""); e != nil {
			saveErr = e
			return
		} else if ok {
			conflict = true
			return
		}
		saveErr = s.groups.Save(rec)
	})
	switch {
	case saveErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "create group: "+saveErr.Error())
	case conflict:
		httpapi.Error(w, http.StatusConflict, "a group with that name already exists")
	default:
		httpapi.JSON(w, http.StatusCreated, toGroupView(rec))
	}
}

// handleRenameGroup renames a group (admin-only). Body: {"name":"..."}. The new
// name must be non-empty and unique (ignoring the group's own current name).
func (s *Server) handleRenameGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var payload struct {
		Name string `json:"name"`
	}
	if !decodeJSONBody(w, r, &payload) {
		return
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		httpapi.Error(w, http.StatusBadRequest, "group name is required")
		return
	}
	var (
		found, conflict bool
		getErr, saveErr error
		view            groupView
	)
	s.do(func() {
		g, ok, e := s.groups.Get(id)
		if e != nil {
			getErr = e
			return
		}
		if !ok {
			return
		}
		found = true
		if _, taken, e := s.groups.byName(name, id); e != nil {
			saveErr = e
			return
		} else if taken {
			conflict = true
			return
		}
		g.Name = name
		g.UpdatedAt = time.Now().Unix()
		if saveErr = s.groups.Save(g); saveErr != nil {
			return
		}
		view = toGroupView(g)
	})
	switch {
	case getErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read group: "+getErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no group with that id")
	case conflict:
		httpapi.Error(w, http.StatusConflict, "a group with that name already exists")
	case saveErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "rename group: "+saveErr.Error())
	default:
		httpapi.JSON(w, http.StatusOK, view)
	}
}

// handleDeleteGroup removes a group (admin-only). Idempotent: deleting an absent
// group succeeds. Any scope that named this group as a member keeps the stale
// ref, which grants nobody once the group is gone — the same graceful degrade a
// deleted user id gets. The deletion takes effect live: the group id is dropped from
// every live session, so its grants stop applying for everyone at once without a
// re-login (ADR-0185).
func (s *Server) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var delErr error
	s.do(func() { delErr = s.groups.Delete(id) })
	if delErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "delete group: "+delErr.Error())
		return
	}
	s.sessions.dropGroupFromSessions(id)
	s.dropGroupFromGrants(id)
	w.WriteHeader(http.StatusNoContent)
}

// handleAddGroupMember adds a user to a group (admin-only).
// PUT /api/v1/groups/{id}/members/{userId}. The user must exist. Adding an
// already-present member is idempotent. The grant takes effect live: it is pushed
// into the user's live sessions, so it applies on their next request without a
// re-login, and a user not signed in snapshots it at their next login
// (ADR-0185).
func (s *Server) handleAddGroupMember(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	userID := r.PathValue("userId")
	var (
		found, userMissing bool
		getErr, saveErr    error
		view               groupView
	)
	s.do(func() {
		g, ok, e := s.groups.Get(id)
		if e != nil {
			getErr = e
			return
		}
		if !ok {
			return
		}
		found = true
		if _, uok, ue := s.users.Get(userID); ue != nil {
			getErr = ue
			return
		} else if !uok {
			userMissing = true
			return
		}
		if !g.hasMember(userID) {
			g.Members = append(g.Members, userID)
			g.UpdatedAt = time.Now().Unix()
			if saveErr = s.groups.Save(g); saveErr != nil {
				return
			}
		}
		view = toGroupView(g)
	})
	switch {
	case getErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read group: "+getErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no group with that id")
	case userMissing:
		httpapi.Error(w, http.StatusBadRequest, "no user with that id")
	case saveErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "add group member: "+saveErr.Error())
	default:
		// Push the grant into the user's live sessions so it applies without a
		// re-login (ADR-0185). No-op when auth is off (no
		// sessions) or the user is not signed in.
		s.sessions.setUserGroupMembership(userID, id, true)
		s.setGrantGroupMembership(userID, id, true)
		httpapi.JSON(w, http.StatusOK, view)
	}
}

// handleRemoveGroupMember removes a user from a group (admin-only).
// DELETE /api/v1/groups/{id}/members/{userId}. Idempotent: removing a non-member
// succeeds. The revoke takes effect live — the group id is dropped from the user's
// live sessions, so access is gone on their next request without a re-login
// (ADR-0185).
func (s *Server) handleRemoveGroupMember(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	userID := r.PathValue("userId")
	var (
		found           bool
		getErr, saveErr error
		view            groupView
	)
	s.do(func() {
		g, ok, e := s.groups.Get(id)
		if e != nil {
			getErr = e
			return
		}
		if !ok {
			return
		}
		found = true
		out := g.Members[:0]
		for _, m := range g.Members {
			if m != userID {
				out = append(out, m)
			}
		}
		g.Members = out
		g.UpdatedAt = time.Now().Unix()
		if saveErr = s.groups.Save(g); saveErr != nil {
			return
		}
		view = toGroupView(g)
	})
	switch {
	case getErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read group: "+getErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no group with that id")
	case saveErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "remove group member: "+saveErr.Error())
	default:
		// Drop the grant from the user's live sessions so access is revoked without a
		// re-login (ADR-0185).
		s.sessions.setUserGroupMembership(userID, id, false)
		s.setGrantGroupMembership(userID, id, false)
		httpapi.JSON(w, http.StatusOK, view)
	}
}
