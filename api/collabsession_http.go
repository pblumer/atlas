package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// HTTP surface for live collaborative modeling sessions (ADR-0103). One SSE
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

// handleDraftSession is the SSE endpoint a participant joins to co-edit a draft.
// It streams the initial sync snapshot, then every presence / lock / change
// frame until the client disconnects, at which point the participant leaves and
// its locks are released. Membership follows the same reachability as the draft
// itself: under --auth the /api/v1 gate already requires a principal (ADR-0044).
func (s *Server) handleDraftSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// A session only makes sense for a draft that exists; mirror the other draft
	// handlers' 404 rather than opening a phantom session.
	var (
		ok      bool
		readErr error
	)
	s.do(func() { _, ok, readErr = s.drafts.get(id) })
	switch {
	case readErr != nil:
		writeError(w, http.StatusInternalServerError, "read draft: "+readErr.Error())
		return
	case !ok:
		writeError(w, http.StatusNotFound, "no draft with that process id")
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

	participant, sync, leave := s.collab.join(id, userID, name)
	defer leave()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	sseFrame(w, collabEventSync, 0, sync)
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, open := <-participant.ch:
			if !open {
				return // torn down (e.g. server shutdown); end the stream
			}
			sseFrame(w, ev.Type, ev.Seq, ev.Data)
			flusher.Flush()
		}
	}
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
	if !s.collab.presence(id, body.ParticipantID, body.Selection) {
		writeError(w, http.StatusNotFound, "no such participant in this draft's session")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDraftSessionLock acquires or releases a per-element edit lock. Acquiring
// an element another participant holds is a 409 Conflict — the boundary the
// first-cut concurrency model enforces (ADR-0103).
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
	default:
		writeError(w, http.StatusBadRequest, `action must be "acquire" or "release"`)
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
	if !s.collab.change(id, body.ParticipantID, body.ElementID, body.XML) {
		writeError(w, http.StatusNotFound, "no such participant in this draft's session")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
