package api

import (
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
)

// Changing one position rather than a whole order
// (ADR-draft-amending-an-order-line).
//
// The pure transitions are in api/order (amend.go): what may be withdrawn on its
// own, what may be corrected, and what a correction then says. This is the part
// that needs the engine, and it needs it for the same reason the whole-order
// withdrawal does — a withdrawn line's approval is a task in somebody's inbox
// asking them to decide something that no longer matters, and the fulfilment
// orchestrator is parked waiting to hear that the order moved.
//
// The correction needs neither. Nothing is withdrawn, so no approval goes stale;
// and a line still waiting its turn is read from the order when its turn comes, so
// the process sees the corrected answers without being told.

// amendReq carries the corrected answers and an optional reason.
type amendReq struct {
	Config map[string]string `json:"config"`
	Reason string            `json:"reason"`
}

// handleCancelLine withdraws one position.
//
// Who may is exactly who may withdraw the whole order: the person who placed it,
// and an operator. Removing one position is a smaller act than removing all of
// them and there is no case for it being a wider permission.
func (s *Server) handleCancelLine(w http.ResponseWriter, r *http.Request) {
	id, item := r.PathValue("id"), r.PathValue("item")
	p := httpapi.PrincipalFrom(r.Context())

	var req cancelReq
	if err := s.decodeApprovalMove(r, &req); err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	by, ok := s.actorFor(w, p, "withdrawing a position")
	if !ok {
		return
	}

	var (
		out       order.Order
		found     bool
		cancelErr error
		opErr     error
	)
	at := s.now()
	s.do(func() {
		ord, got, err := s.orderStore.Get(id)
		if err != nil {
			opErr = err
			return
		}
		if !got {
			return
		}
		found = true
		// 404 and not 403 for somebody else's order, exactly as reading one does:
		// whose orders exist is not something this endpoint answers.
		if !s.mayCancelOrder(p, ord) {
			found = false
			return
		}
		out, cancelErr = order.CancelLine(ord, item, by, at, req.Reason)
		if cancelErr != nil {
			return
		}
		opErr = s.orderStore.Save(out)
	})

	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "withdraw position: "+opErr.Error())
		return
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no order "+id)
		return
	case cancelErr != nil:
		// A conflict rather than a bad request: the order is real and the caller
		// may act on it, this one position simply cannot be taken back — because it
		// is already under way, or because its whole always carries it.
		httpapi.Error(w, http.StatusConflict, cancelErr.Error())
		return
	}

	// Durable first, then the side effects (I2), and neither is worth refusing a
	// caller whose position is already withdrawn.
	s.closeApprovalsOf(id, []string{item})
	s.wakeFulfilment(id)

	httpapi.JSON(w, http.StatusOK, out)
}

// handleAmendLine corrects the details somebody gave when they ordered.
func (s *Server) handleAmendLine(w http.ResponseWriter, r *http.Request) {
	id, item := r.PathValue("id"), r.PathValue("item")
	p := httpapi.PrincipalFrom(r.Context())

	var req amendReq
	if err := s.decodeApprovalMove(r, &req); err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	by, ok := s.actorFor(w, p, "correcting a position's details")
	if !ok {
		return
	}
	if int32(len(req.Config)) > s.budgets().OrderLineAnswers {
		httpapi.Error(w, http.StatusBadRequest,
			"more details than one position keeps; the ceiling is the same one the order was placed under")
		return
	}

	var (
		out      order.Order
		found    bool
		amendErr error
		opErr    error
	)
	at := s.now()
	s.do(func() {
		ord, got, err := s.orderStore.Get(id)
		if err != nil {
			opErr = err
			return
		}
		if !got {
			return
		}
		found = true
		if !s.mayCancelOrder(p, ord) {
			found = false
			return
		}
		lines := make([]order.Line, len(ord.Lines))
		copy(lines, ord.Lines)
		idx := -1
		for i := range lines {
			if lines[i].ItemID == item {
				idx = i
				break
			}
		}
		if idx < 0 {
			found = false
			return
		}
		var next order.Line
		if next, amendErr = order.AmendAnswers(lines[idx], req.Config, by, at, req.Reason); amendErr != nil {
			return
		}
		lines[idx] = next
		ord.Lines = lines
		ord.UpdatedAt = at
		out = ord
		opErr = s.orderStore.Save(out)
	})

	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "correct details: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no order "+id+" with a position for "+item)
	case amendErr != nil:
		httpapi.Error(w, http.StatusConflict, amendErr.Error())
	default:
		httpapi.JSON(w, http.StatusOK, out)
	}
}

// actorFor names who is acting, or writes the refusal and reports that it did.
//
// Every transition kept forever in an order needs one: a status that says somebody
// decided, without saying who, is a decision nobody made. With enforcement off
// there is a single user and the record still needs a name to hold.
func (s *Server) actorFor(w http.ResponseWriter, p *httpapi.Principal, what string) (string, bool) {
	if p != nil && p.UserID != "" {
		return p.UserID, true
	}
	if s.authEnabled {
		httpapi.Error(w, http.StatusBadRequest,
			what+" records who did it, and this request has no identity")
		return "", false
	}
	return "single-user", true
}
