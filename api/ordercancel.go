package api

import (
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
)

// Taking an order back.
//
// The pure transition is in api/order (cancel.go): what may be withdrawn, and
// what a withdrawn line then says. This is the part that needs the engine, and
// there are two reasons it does.
//
// A cancelled line's approval is a task in somebody's inbox asking them to decide
// something that no longer matters. Left standing, an approver eventually decides
// it — and a refusal recorded against a withdrawn line is a decision about a
// request that was taken back before it was ever considered. So the approval's
// instance is cancelled with the line.
//
// And the fulfilment orchestrator is parked waiting to hear that the order moved.
// It has to be told, or it waits for a line that will never start.

// cancelReq carries the optional reason.
type cancelReq struct {
	Reason string `json:"reason"`
}

// cancelResp says what was taken back and what was not.
type cancelResp struct {
	Order order.Order `json:"order"`
	// Cancelled and Kept name the lines by item id. Kept is the half that matters:
	// "your order is cancelled" when a laptop is already on its way is the sentence
	// that produces the second support call.
	Cancelled []string `json:"cancelled"`
	Kept      []string `json:"kept,omitempty"`
}

// handleCancelOrder withdraws everything in an order that has not happened yet.
//
// Who may: the person who placed it, because it is theirs, and an operator,
// because somebody has to be able to stop an order whose orderer has left. Not the
// recipient — an order placed *for* somebody is not theirs to withdraw, and the
// person who placed it is the one who will be asked why it vanished.
func (s *Server) handleCancelOrder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := httpapi.PrincipalFrom(r.Context())

	var req cancelReq
	if err := decodeApprovalMove(r, &req); err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	by := ""
	if p != nil {
		by = p.UserID
	}
	if by == "" && s.authEnabled {
		httpapi.Error(w, http.StatusBadRequest,
			"withdrawing an order records who did it, and this request has no identity")
		return
	}
	if by == "" {
		// Single-user mode is a single user, and the transition still needs a name
		// to put in the record.
		by = "single-user"
	}

	var (
		out       order.Order
		cancelled []string
		kept      []string
		found     bool
		allowed   bool
		cancelErr error
		opErr     error
	)
	at := s.now()
	s.do(func() {
		ord, ok, err := s.orderStore.Get(id)
		if err != nil {
			opErr = err
			return
		}
		if !ok {
			return
		}
		found = true
		// 404 and not 403 for somebody else's order, exactly as reading one does:
		// whose orders exist is not something this endpoint answers.
		if allowed = s.mayCancelOrder(p, ord); !allowed {
			found = false
			return
		}
		out, cancelled, kept, cancelErr = order.CancelOrder(ord, by, at, req.Reason)
		if cancelErr != nil {
			return
		}
		opErr = s.orderStore.Save(out)
	})

	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "cancel order: "+opErr.Error())
		return
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no order "+id)
		return
	case cancelErr != nil:
		// Everything is already under way or finished. A conflict rather than a
		// bad request: the order is real and the caller may act on it, there is
		// simply nothing left to take back.
		httpapi.Error(w, http.StatusConflict, cancelErr.Error())
		return
	}

	// Durable first, then the side effects (I2). Both of these can fail without
	// making the cancellation less true, and neither is worth refusing a caller
	// whose order is already withdrawn — so they are reported in the log and not
	// in the response.
	s.closeApprovalsOf(id, cancelled)
	s.wakeFulfilment(id)

	httpapi.JSON(w, http.StatusOK, cancelResp{Order: out, Cancelled: cancelled, Kept: kept})
}

// mayCancelOrder is the object question: an order belongs to whoever placed it.
// It reads nothing, so it runs inside the same run-loop turn as the write it
// guards.
func (s *Server) mayCancelOrder(p *httpapi.Principal, o order.Order) bool {
	if !s.authEnabled {
		return true
	}
	if p == nil {
		return false
	}
	if p.HasRole(RoleOperator) || p.HasRole(RoleAdmin) {
		return true
	}
	return o.Orderer != "" && o.Orderer == p.UserID
}

// closeApprovalsOf cancels the approval instance of every line that was
// withdrawn, so nobody is left holding a decision about a request that no longer
// exists.
//
// A line with no open approval is the ordinary case and costs one lookup. The
// lookup is a walk of the open tasks, which is why this happens after the
// response's work is done rather than inside the run-loop turn that wrote it.
func (s *Server) closeApprovalsOf(orderID string, items []string) {
	for _, item := range items {
		task, ok, err := s.approvalTaskOf(orderID, item)
		if err != nil || !ok {
			continue
		}
		s.do(func() { s.proc.CancelInstance(task.ProcessInstanceKey) })
		_ = s.drive()
	}
}

// wakeFulfilment tells the orchestrator the order moved, so it asks what may
// start now and finds that nothing may. Without it the process waits on a message
// about a line that will never run.
func (s *Server) wakeFulfilment(orderID string) {
	s.do(func() { s.proc.PublishMessage(order.AdvancedMessage, orderID) })
	_ = s.drive()
}
