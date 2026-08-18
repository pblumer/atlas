package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTP surface for live collaborative modeling sessions (ADR-0139). One SSE
// stream (join) plus three POST endpoints (presence, lock, change) form the
// transport: server→client fan-out rides the one-directional event stream, and a
// client's own actions ride ordinary POSTs. All of it is design-time — it reads
// the resolved *Principal for identity and touches only the in-memory registry,
// never the engine.

// sseFrame writes one Server-Sent Events frame: an id (the session sequence, so
// a future client can resume), an event name, and a single-line JSON data field.
func sseFrame(w io.Writer, event string, seq uint64, data []byte) {
	fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", seq, event, data)
}

// draftSessionAccess authorizes a request to join a draft's live session and
// reports whether it may edit (ADR-0139/0071). A session inherits the sharing
// scope of the draft's project: at least viewer to watch, editor/owner to
// co-edit. A draft with no project (Ungrouped), or whose project no longer
// exists, stays open — exactly as the draft-content handlers are today, so
// session enforcement adds no new lock there. With --auth off, effectiveRole
// resolves to owner, so everything is open. It returns canEdit plus an HTTP
// (status, message); status 0 means access is granted. A principal with no
// access at all gets 404, so a scoped draft's existence never leaks.
func (s *Server) draftSessionAccess(r *http.Request, draftID string) (canEdit bool, status int, msg string) {
	var (
		rec     draft
		draftOK bool
		proj    project
		projOK  bool
		readErr error
	)
	s.do(func() {
		if rec, draftOK, readErr = s.drafts.get(draftID); readErr != nil || !draftOK {
			return
		}
		if rec.ProjectID != "" {
			proj, projOK, readErr = s.projects.get(rec.ProjectID)
		}
	})
	switch {
	case readErr != nil:
		return false, http.StatusInternalServerError, "read draft: " + readErr.Error()
	case !draftOK:
		return false, http.StatusNotFound, "no draft with that process id"
	}
	// Ungrouped or legacy draft (no project, or a project that no longer exists):
	// open, matching the draft-content handlers — no new lock introduced here.
	if rec.ProjectID == "" || !projOK {
		return true, 0, ""
	}
	rank := scopeRank(proj.effectiveRole(principalFrom(r.Context()), s.authEnabled))
	if rank == 0 {
		return false, http.StatusNotFound, "no draft with that process id" // hide existence
	}
	return rank >= scopeRank(ScopeRoleEditor), 0, ""
}

// requireSessionEditor gates a mutating session action (lock/change/presence) on
// the participant's edit capability, snapshotted at join from its project role
// (ADR-0071). It writes 404 for an unknown participant and 403 for a viewer, and
// returns false in both cases; true means the caller may proceed.
func (s *Server) requireSessionEditor(w http.ResponseWriter, draftID, participantID string) bool {
	canEdit, ok := s.collab.canEdit(draftID, participantID)
	if !ok {
		writeError(w, http.StatusNotFound, "no such participant in this draft's session")
		return false
	}
	if !canEdit {
		writeError(w, http.StatusForbidden, "viewer access is read-only; an editor role is required to change the model")
		return false
	}
	return true
}

// handleDraftSession is the SSE endpoint a participant joins to co-edit a draft.
// It streams the initial sync snapshot, then every presence / lock / change
// frame until the client disconnects, at which point the participant leaves and
// its locks are released. Membership follows the same reachability as the draft
// itself: under --auth the /api/v1 gate already requires a principal (ADR-0044).
func (s *Server) handleDraftSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// A session inherits the draft's project scope (ADR-0071): at least viewer to
	// watch, editor to co-edit. This also 404s a non-existent (or unreachable)
	// draft rather than opening a phantom session.
	canEdit, status, msg := s.draftSessionAccess(r, id)
	if status != 0 {
		writeError(w, status, msg)
		return
	}

	flusher, streamable := w.(http.Flusher)
	if !streamable {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	userID, name := "", "anonymous"
	if p := principalFrom(r.Context()); p != nil {
		userID, name = p.UserID, p.Username
	}
	// A client may label itself (useful with auth off, where there is no
	// principal) via ?name=; a resolved principal's name still wins when present.
	if p := principalFrom(r.Context()); p == nil {
		if n := strings.TrimSpace(r.URL.Query().Get("name")); n != "" {
			name = n
		}
	}

	participant, sync, leave := s.collab.joinStream(id, userID, name, canEdit)
	defer leave()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	sseFrame(w, collabEventSync, 0, sync)
	flusher.Flush()

	ctx := r.Context()
	keepalive := time.NewTicker(s.collabKeepalive)
	defer keepalive.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-keepalive.C:
			// net/http only learns a client vanished when a write fails, so an idle
			// stream would never notice a half-open (zombie) connection. This periodic
			// comment forces a write: if it errors the connection is dead — return so
			// the deferred leave() reaps the participant and releases its locks. The
			// ": " prefix is an SSE comment the browser's EventSource ignores.
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case ev, open := <-participant.ch:
			if !open {
				return // torn down (e.g. server shutdown); end the stream
			}
			sseFrame(w, ev.Type, ev.Seq, ev.Data)
			flusher.Flush()
		}
	}
}

// handleDraftSessionJoin is the non-streaming way to join a session, for a
// participant that cannot hold an SSE stream — an AI agent over MCP (ADR-0139
// M2). It registers the participant and returns the same sync snapshot the SSE
// stream sends first (self id, roster, locks); the agent then drives the session
// with the presence / lock / change POSTs and reads what others did by polling.
// The agent must call the leave endpoint when done — unlike an SSE client, there
// is no disconnect to reap it (a TTL reaper is an ADR-0139 follow-up).
func (s *Server) handleDraftSessionJoin(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Same scope gate as the SSE join: at least viewer to join, editor to co-edit
	// (ADR-0071); an unreachable or missing draft 404s.
	canEdit, status, msg := s.draftSessionAccess(r, id)
	if status != 0 {
		writeError(w, status, msg)
		return
	}

	// An optional display name lets an agent introduce itself (e.g. "Claude") so
	// humans see who joined; a resolved principal's name wins when present.
	var body struct {
		Name string `json:"name"`
	}
	if r.ContentLength != 0 {
		if !decodeSessionBody(w, r, &body) {
			return
		}
	}
	userID, name := "", strings.TrimSpace(body.Name)
	if name == "" {
		name = "agent"
	}
	if p := principalFrom(r.Context()); p != nil {
		userID = p.UserID
		if p.Username != "" {
			name = p.Username
		}
	}

	// A joined-over-MCP participant has no SSE stream to reap it on disconnect, so
	// it joins detached and is kept alive by polling; the reaper evicts it if it
	// falls silent (ADR-0139). Its project role decides whether it may edit.
	_, sync := s.collab.joinDetachedAs(id, userID, name, canEdit)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(sync)
}

// handleDraftSessionPoll drains a participant's buffered frames and returns them
// with the current roster and locks (ADR-0139 M2). It is the agent's read side —
// the request/response substitute for the SSE stream — and its liveness signal.
func (s *Server) handleDraftSessionPoll(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		ParticipantID string `json:"participantId"`
	}
	if !decodeSessionBody(w, r, &body) {
		return
	}
	if body.ParticipantID == "" {
		writeError(w, http.StatusBadRequest, "participantId is required")
		return
	}
	out, ok := s.collab.poll(id, body.ParticipantID)
	if !ok {
		writeError(w, http.StatusNotFound, "no such participant in this draft's session")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// handleDraftSessionLeave removes a participant and releases its locks (ADR-0139
// M2). It is idempotent: leaving a session you are not in still returns 204, so a
// retrying agent never wedges on cleanup.
func (s *Server) handleDraftSessionLeave(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		ParticipantID string `json:"participantId"`
	}
	if !decodeSessionBody(w, r, &body) {
		return
	}
	if body.ParticipantID == "" {
		writeError(w, http.StatusBadRequest, "participantId is required")
		return
	}
	s.collab.leave(id, body.ParticipantID)
	w.WriteHeader(http.StatusNoContent)
}

// decodeSessionBody reads a small JSON action body. It caps the read and returns
// false (writing 400) on a malformed body, so every POST handler shares one
// parse-and-validate path.
func decodeSessionBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return false
	}
	if err := json.Unmarshal(body, dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

// handleDraftSessionPresence updates a participant's selected element and
// broadcasts the roster. 404 when the participant is not in the session (a stale
// client that should rejoin).
func (s *Server) handleDraftSessionPresence(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		ParticipantID string `json:"participantId"`
		Selection     string `json:"selection"`
	}
	if !decodeSessionBody(w, r, &body) {
		return
	}
	if body.ParticipantID == "" {
		writeError(w, http.StatusBadRequest, "participantId is required")
		return
	}
	if !s.requireSessionEditor(w, id, body.ParticipantID) {
		return
	}
	if !s.collab.presence(id, body.ParticipantID, body.Selection) {
		writeError(w, http.StatusNotFound, "no such participant in this draft's session")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDraftSessionLock acquires or releases a per-element edit lock. Acquiring
// an element another participant holds is a 409 Conflict — the boundary the
// first-cut concurrency model enforces (ADR-0139).
func (s *Server) handleDraftSessionLock(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		ParticipantID string `json:"participantId"`
		ElementID     string `json:"elementId"`
		Action        string `json:"action"`
	}
	if !decodeSessionBody(w, r, &body) {
		return
	}
	if body.ParticipantID == "" || body.ElementID == "" {
		writeError(w, http.StatusBadRequest, "participantId and elementId are required")
		return
	}
	// Validate the action before the edit gate so a malformed request is a 400
	// regardless of who sends it.
	if body.Action != collabLockAcquire && body.Action != collabLockRelease {
		writeError(w, http.StatusBadRequest, `action must be "acquire" or "release"`)
		return
	}
	if !s.requireSessionEditor(w, id, body.ParticipantID) {
		return
	}
	switch body.Action {
	case collabLockAcquire:
		granted, ok := s.collab.acquireLock(id, body.ParticipantID, body.ElementID)
		switch {
		case !ok:
			writeError(w, http.StatusNotFound, "no such participant in this draft's session")
		case !granted:
			writeError(w, http.StatusConflict, "element is locked by another participant")
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	case collabLockRelease:
		if !s.collab.releaseLock(id, body.ParticipantID, body.ElementID) {
			writeError(w, http.StatusNotFound, "no such participant in this draft's session")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleDraftSessionChange relays a participant's element edit to everyone on the
// stream. It does not persist — the draft's own save path (ADR-0021) is the
// durable record — it only makes the change visible live.
func (s *Server) handleDraftSessionChange(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		ParticipantID string `json:"participantId"`
		ElementID     string `json:"elementId"`
		XML           string `json:"xml"`
	}
	if !decodeSessionBody(w, r, &body) {
		return
	}
	if body.ParticipantID == "" || body.ElementID == "" {
		writeError(w, http.StatusBadRequest, "participantId and elementId are required")
		return
	}
	if !s.requireSessionEditor(w, id, body.ParticipantID) {
		return
	}
	if !s.collab.change(id, body.ParticipantID, body.ElementID, body.XML) {
		writeError(w, http.StatusNotFound, "no such participant in this draft's session")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
