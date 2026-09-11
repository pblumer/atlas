package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/api/taskfolder"
	"github.com/pblumer/atlas/state"
)

// Moving an approval nobody answered.
//
// The model in api/order/assignment.go says what may happen to a waiting
// approval: a deadline reminds, and then moves it to the approver's superior, and
// it may never decide — silence is not a refusal. This is the half that makes any
// of that happen, and it sits here rather than in the order area because moving an
// approval means moving a *task*, and only this server can reach one.
//
// # Why the caller names the superior
//
// Who somebody reports to is a question for a directory, and Atlas asks a
// directory the way it asks anything outside: through a worker, from a model. The
// approval process already does that lookup for its own "superior" variant, so the
// escalation reuses it rather than giving the server a second, server-side path to
// the same directory — which would be a configuration nobody set up and a
// credential the server does not hold.
//
// So the model looks up the superior and names them here. Everything that decides
// whether the hop may happen — a loop in the directory, somebody who already held
// it, a chain that has run out — stays in [order.Escalate], where it is pure and
// tested. An empty superior is not an error: it is the answer "nobody", which is
// what a group approval always gives and what the top of a hierarchy gives once.

// escalateReq names whom the caller's directory says the current approver reports
// to. Empty means nobody does, which stalls the approval instead of moving it.
type escalateReq struct {
	Superior string `json:"superior"`
}

// reassignReq names whom a person is giving a stuck approval to.
type reassignReq struct {
	To string `json:"to"`
}

// approvalMoveResp is what both handlers answer with: where the approval sits now,
// and whether it can go any further.
type approvalMoveResp struct {
	Approver string `json:"approver"`
	// Stalled is true when the chain has nowhere left to go. The approval is still
	// with whoever last held it — stalling reports a fact, it does not take the
	// task away — but somebody now has to look at it, and this is what tells the
	// caller to say so.
	Stalled bool `json:"stalled"`
	// Reason carries why it stalled, for a model to put in the message it sends.
	Reason     string           `json:"reason,omitempty"`
	Assignment order.Assignment `json:"assignment"`
}

// handleEscalateApproval moves one line's approval to the superior the caller
// named, or stalls it when there is none.
//
// It is deliberately idempotent about nothing: each call is one hop, because each
// call is one deadline that elapsed. A model that fires its escalation timer three
// times escalates three times, and [order.Escalate]'s loop guard is what stops the
// third one going somewhere it has already been.
func (s *Server) handleEscalateApproval(w http.ResponseWriter, r *http.Request) {
	var req escalateReq
	if err := decodeApprovalMove(r, &req); err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	s.moveApproval(w, r, func(a order.Assignment, at int64) (order.Assignment, error) {
		// A caller that found no superior is answering the question, not failing to
		// answer it. Escalate reports that as ErrNoFurtherEscalation, which is the
		// same thing the top of a hierarchy gives, and the stall below is the same
		// answer either way.
		return order.Escalate(a, func(string) string { return req.Superior }, at)
	})
}

// handleReassignApproval gives a stuck approval to somebody a person chose.
//
// Where an escalation is a clock's hop, this is an intervention, and the record
// keeps the difference: [order.Reassign] stamps who did it. It also refuses to
// give the approval to the caller themselves — the reason is in the model, and it
// is that the escalation path exists so a stalled approval reaches somebody who
// will act, not so that whoever finds it can take it and approve it.
func (s *Server) handleReassignApproval(w http.ResponseWriter, r *http.Request) {
	var req reassignReq
	if err := decodeApprovalMove(r, &req); err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	// The caller's *username*, not their principal id: an assignment is written
	// back onto a task, a task is addressed by username (ADR-0042), and the guard
	// below compares the two. Comparing an id against a username would let
	// somebody give themselves an approval and record it as an intervention.
	by := ""
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		if by = p.Username; by == "" {
			by = p.UserID
		}
	}
	if by == "" {
		// With authentication off there is no identity, and Reassign needs one:
		// a hop somebody made is only worth recording with the somebody.
		httpapi.Error(w, http.StatusBadRequest,
			"reassigning an approval records who did it, and this request has no identity")
		return
	}
	s.moveApproval(w, r, func(a order.Assignment, at int64) (order.Assignment, error) {
		return order.Reassign(a, req.To, by, at)
	})
}

// decodeApprovalMove reads a small JSON body, tolerating an empty one.
func decodeApprovalMove(r *http.Request, into any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<10))
	if err != nil {
		return errors.New("read body: " + err.Error())
	}
	if len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, into); err != nil {
		return errors.New("malformed JSON body: " + err.Error())
	}
	return nil
}

// moveApproval is what both handlers share: find the approval, apply the move,
// write it down, and put the task where the move says it belongs.
//
// The order is not incidental. The assignment is written before the task is
// reassigned, because the record of where an approval went must not depend on the
// task still being there — durable before visible (I2). A reassignment that then
// fails leaves a recorded hop and a task that did not move, which an operator can
// see and repair; the other order would leave a task in somebody's inbox that no
// record explains.
func (s *Server) moveApproval(w http.ResponseWriter, r *http.Request,
	move func(order.Assignment, int64) (order.Assignment, error)) {
	id, item := r.PathValue("id"), r.PathValue("item")

	// The live task is what says who holds the approval now. Reading it first also
	// answers whether there is one at all: an approval that was already decided has
	// no task, and moving it would be moving nothing.
	task, found, err := s.approvalTaskOf(id, item)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "find the approval: "+err.Error())
		return
	}
	if !found {
		httpapi.Error(w, http.StatusNotFound,
			"no open approval for line "+item+" of order "+id)
		return
	}

	var (
		moved     order.Assignment
		moveErr   error
		saveErr   error
		orderGone bool
	)
	at := s.now()
	s.do(func() {
		ord, ok, err := s.orderStore.Get(id)
		if err != nil {
			saveErr = err
			return
		}
		if !ok {
			orderGone = true
			return
		}
		// The recorded assignment, or a first one made from the task's own
		// assignee. Lazily, because until something moves an approval the task is
		// the whole truth and a copy here would be a second one.
		a, have := ord.AssignmentFor(item)
		if !have {
			a = order.Assign(item, task.Assignee, at)
		}
		moved, moveErr = move(a, at)
		if moveErr != nil {
			if !errors.Is(moveErr, order.ErrNoFurtherEscalation) {
				return
			}
			// A chain with nowhere left to go is not a failure to record. It is the
			// fact somebody has to act on, so it is written down and answered with.
			if moved, moveErr = order.Stall(a, at); moveErr != nil {
				return
			}
		}
		ord = ord.WithAssignment(moved)
		ord.UpdatedAt = at
		saveErr = s.orderStore.Save(ord)
	})

	switch {
	case saveErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "move the approval: "+saveErr.Error())
		return
	case orderGone:
		httpapi.Error(w, http.StatusNotFound, "no order "+id)
		return
	case moveErr != nil:
		httpapi.Error(w, http.StatusConflict, moveErr.Error())
		return
	}

	out := approvalMoveResp{Approver: moved.Approver, Stalled: moved.Stalled(), Assignment: moved}
	if out.Stalled {
		// Nothing moved, so the task stays where it is. The caller is told why, so
		// the model that asked can say it to somebody.
		out.Reason = "the approval can escalate no further and is still with " + moved.Approver
		httpapi.JSON(w, http.StatusOK, out)
		return
	}
	if err := s.assignApprovalTask(task.Key, moved.Approver); err != nil {
		httpapi.Error(w, http.StatusInternalServerError,
			"the hop was recorded, but the task could not be moved: "+err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// approvalTaskOf finds the open approval task deciding one order line.
//
// It walks the open user tasks off the run loop, which is the same walk the
// approval listing makes and for the same reason (ADR-0239): the population it
// grows with is not this order's. An escalation is rare — one per deadline per
// pending approval — so paying a scan for it is the right trade against keeping an
// index of something that is almost always empty.
func (s *Server) approvalTaskOf(orderID, itemID string) (taskResp, bool, error) {
	var (
		found taskResp
		ok    bool
	)
	_, err := s.visitOpenTasks(0, false, func(_ uint64, tr taskResp, _ taskfolder.Task) bool {
		var stop bool
		readErr := s.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
			a, isApproval, err := s.approvalOf(rv, tr)
			if err != nil || !isApproval {
				return err
			}
			if a.OrderID == orderID && a.ItemID == itemID {
				found, ok, stop = tr, true, true
			}
			return nil
		})
		if readErr != nil {
			return false
		}
		return !stop
	})
	return found, ok, err
}

// assignApprovalTask puts the task in the new holder's inbox. It is the claim path
// the Tasks app uses, driven from here: the assignment lives on the job, so moving
// it is a command like any other and the processor writes it.
func (s *Server) assignApprovalTask(jobKey uint64, approver string) error {
	s.do(func() { s.proc.AssignJob(jobKey, approver) })
	return s.drive()
}

// stalledApproval is one approval whose chain has run out, with the order it
// belongs to.
type stalledApproval struct {
	OrderID string `json:"orderId"`
	// Orderer is whose order is waiting, so somebody reading this list knows who to
	// tell. A principal id, as everywhere in an order.
	Orderer    string           `json:"orderer"`
	Assignment order.Assignment `json:"assignment"`
}

// handleStalledApprovals lists every approval that can escalate no further.
//
// This is what "make a failed escalation visible" means in practice. A stall
// records a fact on the order, and a fact nobody queries is not visible — an
// approval nobody can escalate and nobody is looking at is exactly how an order
// waits forever, which is the failure the whole deadline mechanism exists to
// prevent. So there is one place to ask.
//
// It reads every order, which grows with the population, so it runs off the run
// loop (ADR-0239). The answer is small: an installation with many stalled
// approvals has a problem this list is the first sight of.
func (s *Server) handleStalledApprovals(w http.ResponseWriter, r *http.Request) {
	orders, err := s.orderStore.All()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read orders: "+err.Error())
		return
	}
	out := []stalledApproval{}
	for _, o := range orders {
		for _, a := range o.Assignments {
			if a.Stalled() {
				out = append(out, stalledApproval{OrderID: o.ID, Orderer: o.Orderer, Assignment: a})
			}
		}
	}
	httpapi.JSON(w, http.StatusOK, out)
}
