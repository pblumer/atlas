package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/model"
)

// The two routes a commissioning load uses
// (ADR-0333): one that says whether a system has ever
// been loaded, and one that reports what was found.
//
// # Why these are reachable with authentication off, when the directory ones are not
//
// The directory-synchronisation routes refuse outright in an open installation,
// because an unauthenticated caller there could create an account and then sign in
// as it the moment authentication was turned on. That argument does not transfer,
// and pretending it does would be cargo-culted caution rather than security.
//
// A load creates no identity and grants no access to anything. It records a
// statement *about* people who already exist — "this person already holds this" —
// in a store whose only readers are the portal's basket and a reconciliation that
// reports rather than acts. In an open installation every route is open, including
// publishing a catalogue and placing an order; singling this one out would be a
// rule that looks careful and protects nothing, while making the load unusable in
// exactly the single-user mode where somebody tries it first.
//
// What it *is* confined by is the ordinary pair: a scope that reaches these two
// patterns and nothing else, and a role declared in the route table.
//
// The honest cost, stated rather than left to be discovered: a caller who can reach
// this can write entitlements nobody holds, and an item the portal believes
// somebody holds is an item the basket will refuse to order for them. That is a
// denial, not an escalation, and it is repairable — but it is why the scope exists.

// inventoryLoadStateView is what the state route answers.
type inventoryLoadStateView struct {
	System string `json:"system"`
	// EverApplied is the question this route exists for. The inventory cannot answer
	// it: "loaded and found nothing" and "never loaded" leave it equally empty, and
	// those two call for opposite actions.
	EverApplied   bool  `json:"everApplied"`
	LastAppliedAt int64 `json:"lastAppliedAt,omitempty"`
	// LastReportedAt is when a load last previewed without writing. A system that
	// has been reporting for a week and never applied is the case this pair of
	// fields is here to make visible.
	LastReportedAt int64 `json:"lastReportedAt,omitempty"`
	Runs           int64 `json:"runs"`
	Granted        int64 `json:"granted"`
}

// inventorySystemOf reads and validates the system name a request names.
func inventorySystemOf(w http.ResponseWriter, raw string) (string, bool) {
	system := strings.TrimSpace(raw)
	if system == "" {
		httpapi.Error(w, http.StatusBadRequest,
			"name the target system this reading came from (\"system\"). It is what a "+
				"product's target references are matched against, so a load without one "+
				"matches nothing and would report every right in the batch as unmodelled")
		return "", false
	}
	return system, true
}

// handleInventoryLoadState answers whether a system has ever been loaded.
func (s *Server) handleInventoryLoadState(w http.ResponseWriter, r *http.Request) {
	system, ok := inventorySystemOf(w, r.URL.Query().Get("system"))
	if !ok {
		return
	}
	var (
		st      inventoryLoadState
		ran     bool
		loadErr error
	)
	s.do(func() {
		ran = true
		st, loadErr = s.inventoryLoads.current(system)
	})
	if !ran {
		httpapi.Error(w, http.StatusServiceUnavailable, "inventory load: this server is shutting down")
		return
	}
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "inventory load state: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, inventoryLoadStateView{
		System: system, EverApplied: st.AppliedAt != 0, LastAppliedAt: st.AppliedAt,
		LastReportedAt: st.ReportedAt, Runs: st.Runs, Granted: st.Granted,
	})
}

// handleInventoryLoad takes one reading of one target system and answers with what
// it decided.
//
// The decision and, where the message asked for it, the write happen in a single
// run-loop turn. That is a deliberate cost — the loop is Atlas's single writer, so
// a large batch stalls process execution for as long as it takes — and it is what
// the InventoryObservations budget bounds. It is also what makes the plan describe
// the inventory it is written against: reading the inventory in one turn and
// writing in another would leave a window in which an order settles, and the legacy
// grant decided against an empty slot would replace the ordered right that landed
// in it.
func (s *Server) handleInventoryLoad(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().InventoryLoad))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var msg inventoryLoadMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	system, ok := inventorySystemOf(w, msg.System)
	if !ok {
		return
	}
	msg.System = system
	if n := len(msg.Observations); n > int(s.budgets().InventoryObservations) {
		httpapi.Error(w, http.StatusRequestEntityTooLarge,
			inventoryTooManyObservations(n, int(s.budgets().InventoryObservations)))
		return
	}

	now := time.Now().Unix()
	var (
		plan     inventoryPlan
		st       inventoryLoadState
		applied  bool
		ran      bool
		runErr   error
		writeErr error
	)
	s.do(func() {
		ran = true
		users, err := s.users.LoadAll()
		if err != nil {
			runErr = err
			return
		}
		items, err := s.catalogStore.Items()
		if err != nil {
			runErr = err
			return
		}
		if st, runErr = s.inventoryLoads.current(system); runErr != nil {
			return
		}

		// The inventory is read inside this turn, through a snapshot taken here. It
		// is a point read per observation rather than a scan, and it is bounded by
		// the batch budget rather than by the population — which is the condition
		// ADR-0239 puts on anything that runs on the loop at all.
		view := s.store.ReadView()
		defer view.Close()
		// A failed read aborts the whole run rather than being absorbed into a
		// decision. Treating it as "nothing is held" would grant over an ordered
		// right; treating it as "something is held" would file a note saying a
		// record exists that nobody can see. Both put a sentence in the report that
		// is not true, and the report is the only thing anybody reads. There is no
		// third answer a decision can carry, so the run says it could not decide.
		var readErr error
		held := func(principal, itemID string) (*model.EntitlementValue, bool) {
			v, ok, err := view.Entitlement(principal, itemID)
			if err != nil && readErr == nil {
				readErr = fmt.Errorf("read what %s already holds of %s: %w", principal, itemID, err)
			}
			return v, ok
		}

		plan = decideInventoryLoad(msg, users, items, held, now)
		if readErr != nil {
			runErr = readErr
			return
		}
		applied = msg.Apply && len(plan.Grants) > 0
		if applied {
			s.applyInventoryPlan(plan)
		}
		writeErr = s.inventoryLoads.Save(
			advanceInventoryState(st, system, len(plan.Grants), applied, now))
	})
	if !ran {
		httpapi.Error(w, http.StatusServiceUnavailable, "inventory load: this server is shutting down")
		return
	}
	if runErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "inventory load: "+runErr.Error())
		return
	}
	if writeErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "inventory load: record the run: "+writeErr.Error())
		return
	}
	// The grants were enqueued on the loop; this is what makes them events. It runs
	// outside the turn above on purpose — driving dispatches onto the loop itself,
	// and doing that from inside a turn would be a nested rendezvous.
	if applied {
		if err := s.drive(); err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "inventory load: write the grants: "+err.Error())
			return
		}
	}
	httpapi.JSON(w, http.StatusOK, inventoryReportOf(plan, msg, st, applied, s.budgets()))
}
