package api

import (
	"net/http"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
)

// This file is the HTTP-side of ADR-draft-grant-audit-log: the helper the sharing
// handlers call to record an access-control change, and the owner-only endpoint that
// reads a project's history back. Recording happens on the mutation path, never on
// the access path, so effectiveRole stays pure. Every store access runs on the run
// loop via s.do — the record helper assumes it is already on that goroutine (its
// callers record inside their own do block, right after the project save).

// newGrantAuditID mints an opaque event id. The "gra_" prefix keeps ids
// self-describing in logs and on disk; the random suffix guarantees uniqueness.
func newGrantAuditID() (string, error) {
	suffix, err := randomHex(12)
	if err != nil {
		return "", err
	}
	return "gra_" + suffix, nil
}

// recordGrantAudit stamps and persists one grant-audit event. The caller sets
// ApplicationID, Action, and the action-specific fields (Subject*, Role, From, To);
// this fills the id, the timestamp, and the actor from the request principal
// (ADR-0044). It must be called on the run loop (inside s.do). With auth off there
// is no principal — the actor is left blank — but callers only reach this on a real
// mutation, and sharing is moot in single-user mode, so nothing is recorded there in
// practice.
func (s *Server) recordGrantAudit(r *http.Request, rec grantAudit) error {
	id, err := newGrantAuditID()
	if err != nil {
		return err
	}
	rec.ID = id
	rec.At = time.Now().Unix()
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		rec.ActorID = p.UserID
		rec.ActorName = p.Username
	}
	return s.grantAudit.Save(rec)
}

// handleListProjectAudit returns a project's access-control history, newest first
// (ADR-draft-grant-audit-log). The history names every member and every actor, so it
// is more sensitive than the member list itself: only the owner (and an admin, who
// resolves as owner) may read it. GET /api/v1/applications/{id}/audit.
func (s *Server) handleListProjectAudit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, code, msg := s.authorizeProject(r, id, ScopeRoleOwner); code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	var (
		out     []grantAudit
		loadErr error
	)
	s.do(func() { out, loadErr = s.grantAudit.forApplication(id) })
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list grant audit: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}
