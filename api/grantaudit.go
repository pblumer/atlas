package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
)

// This file is the HTTP-side of ADR-0184: the helper the sharing
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
// (ADR-0184). The history names every member and every actor, so it
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

// globalAuditView is one event in the cross-application admin view. It carries the
// raw event plus the owning application's current name, so the admin table reads
// without a second lookup per row. ApplicationName is empty only for an event whose
// application was deleted between load steps — the delete-on-project-delete cleanup
// (grantAuditStore.deleteForApplication) means that is a race, not a steady state.
type globalAuditView struct {
	grantAudit
	ApplicationName string `json:"applicationName,omitempty"`
}

// defaultAuditLimit and maxAuditLimit bound the global audit response: an admin view
// wants the most recent activity, not the entire history in one payload. The window
// is newest-first, so the default already shows what matters; a caller wanting more
// raises limit up to the cap.
const (
	defaultAuditLimit = 200
	maxAuditLimit     = 1000
)

// handleListAudit returns the access-control history across every application,
// newest first — the global admin audit view (ADR-0184). It is admin-only: unlike
// the per-application endpoint, which an owner may read for their own application,
// this spans applications the caller may not own, so it is gated like user and group
// administration. Optional query filters narrow it: applicationId to one application,
// action to one kind (share/unshare/visibility/transfer); limit caps the window
// (default 200, max 1000). GET /api/v1/audit.
func (s *Server) handleListAudit(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	q := r.URL.Query()
	appFilter := q.Get("applicationId")
	actionFilter := q.Get("action")
	limit := defaultAuditLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			httpapi.Error(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		if n > maxAuditLimit {
			n = maxAuditLimit
		}
		limit = n
	}

	var (
		out     []globalAuditView
		loadErr error
	)
	s.do(func() {
		events, e := s.grantAudit.LoadAll()
		if e != nil {
			loadErr = e
			return
		}
		projs, e := s.projects.LoadAll()
		if e != nil {
			loadErr = e
			return
		}
		names := make(map[string]string, len(projs))
		for _, p := range projs {
			names[p.ID] = p.Name
		}
		out = []globalAuditView{}
		for _, ev := range events {
			if appFilter != "" && ev.ApplicationID != appFilter {
				continue
			}
			if actionFilter != "" && ev.Action != actionFilter {
				continue
			}
			out = append(out, globalAuditView{grantAudit: ev, ApplicationName: names[ev.ApplicationID]})
			if len(out) >= limit {
				break
			}
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list audit: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}
