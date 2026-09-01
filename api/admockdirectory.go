package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/connector/ad"
)

// The mock directory, made visible (ADR-draft-ad-mock-directory-in-the-console).
//
// A mock forest lives in the AD worker's memory (ADR-0181), which is the right
// lifetime and, until this, an invisible one. The Console showed two things about it
// and neither was the directory: the *seed*, which is where every forest starts and is
// never written back, and the worker's log, one line per operation — so "did that
// joiner create the account" was answered by reading a log, and "what is in the
// directory now" was not answered at all. The seed being the only directory-shaped
// thing on the screen made it the natural place to look for an account that was never
// going to be there.
//
// So a mock worker reports what it holds and this is where it lands. The direction is
// the preview outbox's and for the same reason (ADR-0150/0168): a worker may sit in a
// network this server cannot dial into, so what crosses is a report the worker sends.
//
// Like the outbox it is **runtime state, not engine state** — memory, no event,
// nothing replayed (I4/I6), gone on restart — which is the honest lifetime for a
// picture of a directory that itself vanishes when its worker stops. And like the
// outbox it stays off the run loop: [ad.MockView] holds its own lock, so a Console
// polling this cannot slow a running process down.

// maxADMockReport bounds one report. It is generous next to the outbox's message
// limit because a directory is not a message — [worker.maxReportedEntries] entries with
// their attributes is the shape of it — and it exists so a body is refused before
// being read into memory rather than after.
const maxADMockReport = 8 << 20

// handleADMockDirectory serves the mock directories this server's workers reported,
// which is what Operations › Mock directory renders.
//
// Admin-gated, for the reason the *seed's* content is (ADR-0202): a mock directory is
// invented, but it is shaped like a staff list, and a view of one is no more public
// than the file it started from. What the switch is *set to* stays readable by
// everyone signed in — that is a different question, and one every operator watching an
// AD task has.
func (s *Server) handleADMockDirectory(w http.ResponseWriter, _ *http.Request) {
	workers := []ad.MockSnapshot{}
	if s.adMockView != nil {
		workers = s.adMockView.Snapshots()
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{"workers": workers})
}

// handleReportADMockDirectory accepts one worker's mock directory.
//
// It writes nothing durable, so an authenticated caller posting here can change what a
// mockup view shows and can do nothing else — the same reasoning that lets a mail
// worker post into the preview outbox. The report replaces whatever that worker last
// said, because a worker sends its whole directory every time: an entry a leaver
// deleted has to leave the view with it.
func (s *Server) handleReportADMockDirectory(w http.ResponseWriter, r *http.Request) {
	var snap ad.MockSnapshot
	if err := json.NewDecoder(io.LimitReader(r.Body, maxADMockReport)).Decode(&snap); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid mock directory report: "+err.Error())
		return
	}
	if s.adMockView == nil {
		httpapi.Error(w, http.StatusServiceUnavailable, "this server keeps no mock directory view")
		return
	}
	// At is the view's to assign: a reporter that could choose it could make its own
	// report look fresher than another's, and freshness is what decides which worker a
	// bounded view keeps.
	snap.At = 0
	s.adMockView.Put(snap)
	w.WriteHeader(http.StatusNoContent)
}
