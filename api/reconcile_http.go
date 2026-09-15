package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// The reconciliation routes (ADR-0334).
//
// One route reads a target system's answer and compares it; one lists what is
// still in disagreement; three act on a single finding, each requiring a person to
// have decided.
//
// The split between them is the posture of the whole slice. Comparing is something
// a scheduled process may do unattended — it writes nothing but a journal entry.
// Acting is not, ever: it either changes what Atlas asserts about somebody's access
// or reaches into a target system to take it away. So the comparison is reachable
// with a confined worker credential and the three actions are not.

// reconcileRun compares one reading of one target system against the inventory.
func (s *Server) handleReconcile(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().Reconcile))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var msg reconcileMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	msg.System = strings.TrimSpace(msg.System)
	if msg.System == "" {
		httpapi.Error(w, http.StatusBadRequest,
			"name the target system this reading came from (\"system\"); it is what a product's "+
				"target references are matched against")
		return
	}
	if len(msg.Refs) == 0 && len(msg.Subjects) == 0 {
		// The refusal that keeps the endpoint honest. Absence is a finding here, so
		// a run has to say what it read whole — and no default can supply that,
		// because the only candidates are "nothing" (useless) and "everything"
		// (a guess that turns a truncated read into a report that the estate has
		// lost its access).
		httpapi.Error(w, http.StatusBadRequest,
			"name what this run read completely: the references (\"refs\"), the subjects "+
				"(\"subjects\"), or both. This endpoint reads absence as a finding — a right "+
				"recorded here and not seen inside the scope is reported as missing — and that is "+
				"only sound within a scope the caller promises is complete. A run that read three "+
				"groups names those three; one verifying a leaver names that person")
		return
	}
	if n := len(msg.Observations); n > int(s.budgets().ReconcileObservations) {
		httpapi.Error(w, http.StatusRequestEntityTooLarge,
			reconcileTooManyObservations(n, int(s.budgets().ReconcileObservations)))
		return
	}

	now := time.Now().Unix()
	var (
		in     reconcileInput
		ran    bool
		runErr error
	)
	s.do(func() {
		ran = true
		if in.Users, runErr = s.users.LoadAll(); runErr != nil {
			return
		}
		in.Items, runErr = s.catalogStore.Items()
	})
	if !ran {
		httpapi.Error(w, http.StatusServiceUnavailable, "reconcile: this server is shutting down")
		return
	}
	if runErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "reconcile: "+runErr.Error())
		return
	}

	// The inventory, whole, off the loop.
	//
	// This is the population-sized read ADR-0312 asked about, and it is why it runs
	// here rather than inside a turn: the loop is Atlas's single writer, and a walk
	// of an inventory with a million rows inside a turn would stop process execution
	// for its whole duration. The view is a snapshot, so the comparison is against
	// one consistent moment — a finding is a statement about that moment and says so.
	if err := s.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		return rv.Entitlements(func(v *model.EntitlementValue) error {
			in.Held = append(in.Held, *v)
			return nil
		})
	}); err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "reconcile: read the inventory: "+err.Error())
		return
	}

	plan := decideReconcile(msg, in)

	var (
		opened, closed, refused int
		wrote                   bool
		writeErr                error
	)
	s.do(func() {
		wrote = true
		opened, closed, refused, writeErr = s.applyReconcilePlan(plan, msg.System, now)
	})
	if !wrote {
		httpapi.Error(w, http.StatusServiceUnavailable, "reconcile: this server is shutting down")
		return
	}
	if writeErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "reconcile: "+writeErr.Error())
		return
	}

	httpapi.JSON(w, http.StatusOK,
		reconcileReportOf(plan, msg, opened, closed, refused, len(in.Held), s.budgets()))
}

// handleListDiscrepancies answers what is still in disagreement.
//
// Open findings only. A closed one is history and the journal keeps it, but a list
// that mixed the two would be a list somebody has to filter before they can read
// it — and the question this route answers is "what is wrong now".
func (s *Server) handleListDiscrepancies(w http.ResponseWriter, r *http.Request) {
	var (
		out     []discrepancyRecord
		ran     bool
		loadErr error
	)
	s.do(func() {
		ran = true
		out, loadErr = s.discrepancies.open()
	})
	if !ran {
		httpapi.Error(w, http.StatusServiceUnavailable, "discrepancies: this server is shutting down")
		return
	}
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "discrepancies: "+loadErr.Error())
		return
	}
	if system := strings.TrimSpace(r.URL.Query().Get("system")); system != "" {
		kept := out[:0]
		for _, rec := range out {
			if rec.System == system {
				kept = append(kept, rec)
			}
		}
		out = kept
	}
	if out == nil {
		out = []discrepancyRecord{}
	}
	httpapi.JSON(w, http.StatusOK, out)
}
