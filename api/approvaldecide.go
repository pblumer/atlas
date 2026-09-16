package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/state"
)

// Deciding a request rather than a line (ADR-draft-collective-approval).
//
// An approval in Atlas is one user task per order line, each in its own process
// instance: the approval process is started multi-instance from the order's ready
// lines, so a workplace ordered as twelve products is twelve tasks. That shape is
// right and this does not change it — a line is what gets provisioned, refused or
// escalated, and the process that decides it has to be per line or none of that
// works.
//
// What was wrong was the surface. The approver of a twelve-line workplace was
// asked to press Genehmigen twelve times, read the same recipient twelve times,
// and — on a refusal — type the same reason twelve times. That is not twelve
// decisions being examined. It is one decision being typed twelve times, and a
// person doing it for the fourth time is no longer reading.
//
// So this decides all of an order's open approvals the caller holds, in one
// action, with one reason. Each one is still completed as its own task, because
// each one is still its own process instance and each of those still has to act.
// The record shows twelve completions in the same second by the same person on the
// same order, which is the legible signature of one decision — not a claim that
// twelve examinations happened.
//
// # What it refuses to do
//
// Two orders in one call. The reason field is the thing that makes a refusal
// reviewable a year later, and one sentence covering two people's requests is a
// sentence about neither. A caller naming keys from more than one order gets a
// 400 and decides them one request at a time.
//
// # What it cannot promise
//
// Atomicity. There is no transaction spanning twelve process instances, and there
// is no honest way to invent one: a completion that has gone through has already
// handed its answer to its process, which may already have started provisioning.
// So the answer is per line — what was decided and what was not, with the reason
// for each that was not — and the page shows it. A partial result is a true
// statement about a world that has partly moved, which is better than a rollback
// that cannot happen.

// maxCollectiveDecision bounds one call. It is not a resource limit — the work is
// one store read and one completion per key — it is a statement about what one
// decision can plausibly be. Nobody examines two hundred lines at once, and a
// caller sending two hundred keys is automating something this surface is not.
const maxCollectiveDecision = 100

// decideRequest is one decision, and the lines it covers.
type decideRequest struct {
	// Approved is the decision itself, written into every named task under the same
	// variable name the single-approval page uses. The two paths must agree: a
	// process cannot be asked to read one variable from one surface and another
	// from the other.
	Approved bool `json:"approved"`
	// Reason is required on a refusal and optional on an approval, which is the rule
	// the single-approval page already enforces in the browser. It is enforced here
	// too, because a rule only the browser knows is not a rule.
	Reason string `json:"reason"`
	// TaskKeys names the approvals. Keys and not item ids: a key identifies one open
	// task exactly, where an item id identifies a line that may have been decided,
	// reassigned and re-raised. The page has them fresh from the listing it is
	// showing, and a key that has gone stale in between is reported as such rather
	// than guessed at.
	TaskKeys []uint64 `json:"taskKeys"`
}

// decisionOutcome is what became of one named key. Error is empty on the ones
// that went through.
type decisionOutcome struct {
	TaskKey uint64 `json:"taskKey"`
	OrderID string `json:"orderId,omitempty"`
	ItemID  string `json:"itemId,omitempty"`
	Error   string `json:"error,omitempty"`
}

// decideResponse answers per line, never in aggregate. "11 of 12" tells an
// approver that something is wrong and not which thing.
type decideResponse struct {
	Decided []decisionOutcome `json:"decided"`
	Skipped []decisionOutcome `json:"skipped"`
}

// handleDecideApprovals decides every named approval the caller holds, as one
// decision on one order.
func (s *Server) handleDecideApprovals(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var req decideRequest
	if err := json.Unmarshal(body, &req); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	reason := strings.TrimSpace(req.Reason)
	switch {
	case len(req.TaskKeys) == 0:
		httpapi.Error(w, http.StatusBadRequest, "taskKeys is required: name the approvals to decide")
		return
	case len(req.TaskKeys) > maxCollectiveDecision:
		httpapi.Error(w, http.StatusBadRequest,
			"one decision covers at most 100 approvals; this is a decision, not an import")
		return
	case !req.Approved && reason == "":
		// The same rule as the single approval, and for the same reason: a refusal
		// somebody has to explain to the orderer, months later, is a refusal
		// somebody has to have written down.
		httpapi.Error(w, http.StatusBadRequest, "a refusal needs a reason")
		return
	}

	pr := httpapi.PrincipalFrom(r.Context())
	held, skipped, err := s.approvalsByKey(pr, req.TaskKeys)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read approvals: "+err.Error())
		return
	}
	// Nothing left to decide is not an error the caller can fix by retrying, and it
	// is not a server fault either: the page it came from is simply out of date.
	if len(held) == 0 {
		httpapi.JSON(w, http.StatusOK, decideResponse{Decided: []decisionOutcome{}, Skipped: skipped})
		return
	}
	order := held[0].OrderID
	for _, a := range held {
		if a.OrderID != order {
			httpapi.Error(w, http.StatusBadRequest,
				"these approvals belong to different orders; one reason cannot cover two requests")
			return
		}
	}

	// Built through the same converter the single completion's JSON body goes
	// through, rather than by hand. The two surfaces write into the same process
	// variable, and a hand-built value that encoded a bool differently from the
	// parsed one would make a FEEL condition answer differently depending on which
	// button was pressed — a difference nothing on either page would show.
	vars, err := startVarsFromMap(map[string]any{"genehmigt": req.Approved, "begruendung": reason})
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "encode decision: "+err.Error())
		return
	}
	decided := []decisionOutcome{}
	for _, a := range held {
		out := decisionOutcome{TaskKey: a.Task.Key, OrderID: a.OrderID, ItemID: a.ItemID}
		var found bool
		s.do(func() {
			// Re-read rather than trust the pass above: between resolving the keys and
			// completing them the task may have been completed by an escalation or by
			// this same person in another tab. GetJob reports ok=false for absent,
			// completed and unreadable alike, which are one answer here — it is not
			// open, so it was not decided by this call.
			if _, ok, gErr := s.store.GetJob(a.Task.Key); gErr != nil || !ok {
				return
			}
			found = true
			s.proc.CompleteJob(a.Task.Key, vars...)
		})
		if !found {
			out.Error = "no longer an open task; it was decided or withdrawn while this decision was being taken"
			skipped = append(skipped, out)
			continue
		}
		decided = append(decided, out)
	}
	// Drive once, outside the run loop, for the same reason the single completion
	// does it there: the jobs this unblocked may call outward, and the single
	// writer must not be held for that. The instances are independent, so there is
	// nothing for an earlier completion's drive to unblock for a later one.
	if err := s.drive(); err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "complete approvals: "+err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, decideResponse{Decided: decided, Skipped: skipped})
}

// approvalsByKey resolves named keys into the approvals this caller holds, and
// says of each one it drops why.
//
// Named keys and not the open-task walk the listing does: the walk is bounded by a
// budget and reports truncation, and a decision silently dropping a line because
// the walk ran out of budget would be the worst failure this surface could have.
// Here the cost is one store read per named key and nothing is hidden.
func (s *Server) approvalsByKey(pr *httpapi.Principal, keys []uint64) ([]approvalResp, []decisionOutcome, error) {
	skipped := []decisionOutcome{}
	var tasks []taskResp
	var loopErr error
	seen := map[uint64]bool{}
	s.do(func() {
		for _, key := range keys {
			// A key named twice is one approval, not two completions of it.
			if seen[key] {
				continue
			}
			seen[key] = true
			jv, ok, err := s.store.GetJob(key)
			if err != nil {
				loopErr = err
				return
			}
			if !ok || jv.JobType != compiler.UserTaskJobTypeIndex {
				skipped = append(skipped, decisionOutcome{TaskKey: key, Error: "no open task with that key"})
				continue
			}
			tr := s.enrichTask(key, jv)
			// The listing's gate, not the task surface's: an operator holds every task
			// and this page is one person's approvals. An operator who has to step in
			// does it on the task itself, where the record says an operator did.
			if !s.holdsApproval(pr, tr) {
				skipped = append(skipped, decisionOutcome{TaskKey: key, Error: "this approval is not yours to decide"})
				continue
			}
			tasks = append(tasks, tr)
		}
	})
	if loopErr != nil {
		return nil, nil, loopErr
	}

	var held []approvalResp
	err := s.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		for _, tr := range tasks {
			a, ok, err := s.approvalOf(rv, tr)
			if err != nil {
				return err
			}
			if !ok {
				// A user task the caller holds that is not deciding an order line. It is
				// somebody's work, but it is not an approval and this is not its surface.
				skipped = append(skipped, decisionOutcome{TaskKey: tr.Key, Error: "this task is not an approval"})
				continue
			}
			held = append(held, a)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return held, skipped, nil
}
