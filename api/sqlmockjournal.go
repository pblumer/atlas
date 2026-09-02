package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/connector/sqldb"
)

// The mockup run, made visible (ADR-draft-sql-mock-journal).
//
// A mock database lives in a SQL worker's memory (ADR-0221), which is the right
// lifetime and, until this, an invisible one. The Console showed two things about it
// and neither was the run: the *prepared answers*, which are an input the worker reads
// once, and the worker's log — so "what did this process actually ask the database" was
// answered by scrolling a log past everything else that worker did.
//
// So a mock worker reports its journal and this is where it lands. The direction is the
// preview outbox's and for the same reason (ADR-0150/0168, and ADR-0213 for the mock
// directory that got there first): a worker may sit in a network this server cannot
// dial into, so what crosses is a report the worker sends.
//
// Like the outbox it is **runtime state, not engine state** — memory, no event, nothing
// replayed (I4/I6), gone on restart — which is the honest lifetime for a picture of a
// run whose database vanishes when its worker stops. And like the outbox it stays off
// the run loop: [sqldb.MockJournalView] holds its own lock, so a Console polling this
// cannot slow a running process down.

// maxSQLMockReport bounds one report. A journal is bounded in the worker already; this
// exists so a body is refused before being read into memory rather than after.
const maxSQLMockReport = 8 << 20

// handleSQLMockJournal serves the mockup journals this server's workers reported, which
// is what Operations › Mock database renders.
//
// Admin-gated, and for a stronger reason than the mock directory's. That view is
// admin-only because invented entries are still shaped like a staff list; this one
// carries **the values a process bound**, and a process under test binds whatever it
// binds — a password hash on its way into a table is a bound parameter like any other,
// and nothing here can tell it from an id. What the mockup switch is *set to* stays
// readable by everyone signed in: that is a different question, and one every operator
// watching a database task has.
func (s *Server) handleSQLMockJournal(w http.ResponseWriter, _ *http.Request) {
	workers := []sqldb.MockJournalSnapshot{}
	if s.sqlMockView != nil {
		workers = s.sqlMockView.Snapshots()
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{"workers": workers})
}

// handleReportSQLMockJournal accepts one worker's mockup journal.
//
// It writes nothing durable, so an authenticated caller posting here can change what a
// mockup view shows and can do nothing else — the same reasoning that lets a mail worker
// post into the preview outbox. The report replaces whatever that worker last said,
// because a worker sends its whole journal every time: a restarted worker's empty
// journal has to show as empty rather than as yesterday's run.
func (s *Server) handleReportSQLMockJournal(w http.ResponseWriter, r *http.Request) {
	var snap sqldb.MockJournalSnapshot
	if err := json.NewDecoder(io.LimitReader(r.Body, maxSQLMockReport)).Decode(&snap); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid mock journal report: "+err.Error())
		return
	}
	if s.sqlMockView == nil {
		httpapi.Error(w, http.StatusServiceUnavailable, "this server keeps no mock journal view")
		return
	}
	// At is the view's to assign: a reporter that could choose it could make its own
	// report look fresher than another's, and freshness is what decides which worker a
	// bounded view keeps.
	snap.At = 0
	s.sqlMockView.Put(snap)
	w.WriteHeader(http.StatusNoContent)
}
