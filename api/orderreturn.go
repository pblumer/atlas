package api

import (
	"fmt"
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/model"
)

// Giving back what an order granted.
//
// The pure half is in api/order (return.go): what may be returned, in what order,
// and what a line then says. This is the half that needs an engine, because a
// return is a process — the one the order froze when it was placed, so that
// revoking what was granted uses the rules that were in force when it was granted.
//
// It is deliberately not something a cancellation does on its own. Withdrawing an
// order takes back what has not happened; revoking an account somebody has been
// using for three weeks is a different act with a different risk, and a single
// click that did both would delete accounts on a mis-click.

// returnResp says what was started.
type returnResp struct {
	Order order.Order `json:"order"`
	// Process names the deprovisioning that is now running, so a caller can find
	// its instance without guessing which model was chosen.
	Process string `json:"process"`
}

// handleReturnLine starts the revocation of one provisioned line.
//
// Who may: the person who placed the order, and an operator. Not the recipient,
// even though they are the one holding it — they cannot see the order at all, and
// "what you hold, and giving it back" is the inventory's question rather than an
// order's. That surface does not exist yet, and inventing half of it here would
// put the same act in two places.
func (s *Server) handleReturnLine(w http.ResponseWriter, r *http.Request) {
	id, item := r.PathValue("id"), r.PathValue("item")
	p := httpapi.PrincipalFrom(r.Context())

	var (
		out       order.Order
		process   string
		found     bool
		returnErr error
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
		if !s.mayCancelOrder(p, ord) {
			// The same right that withdraws an order gives it back: both are the
			// orderer saying what happens to what they asked for. And the same 404,
			// because whose orders exist is not something this endpoint answers.
			found = false
			return
		}
		process = order.ReturnProcessOf(ord, item)
		out, returnErr = order.Returning(ord, item, at)
		if returnErr != nil {
			return
		}
		opErr = s.orderStore.Save(out)
	})

	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "return line: "+opErr.Error())
		return
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no order "+id)
		return
	case returnErr != nil:
		httpapi.Error(w, http.StatusConflict, returnErr.Error())
		return
	}

	// Durable first, then the process (I2). The line says a return is under way
	// before anything runs, so a start that fails leaves a visible "returning" an
	// operator can act on rather than a silent nothing.
	if err := s.startReturn(process, id, item, out); err != nil {
		httpapi.Error(w, http.StatusInternalServerError,
			"the return was recorded, but its process could not be started: "+err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, returnResp{Order: out, Process: process})
}

// startReturn runs the line's deprovisioning, seeded the way its provisioning was:
// the order and the line it is about, the variant that was chosen, and who holds
// it. The process reports its outcome back through the same endpoint a
// provisioning does.
func (s *Server) startReturn(process, orderID, itemID string, o order.Order) error {
	variant := ""
	for _, l := range o.Lines {
		if l.ItemID == itemID {
			variant = l.VariantID
			break
		}
	}
	vars := []model.VariableValue{
		{Name: "itemId", Kind: model.VarString, Text: itemID},
		{Name: "orderId", Kind: model.VarString, Text: orderID},
		{Name: "recipient", Kind: model.VarString, Text: o.Recipient},
	}
	if variant != "" {
		vars = append(vars, model.VariableValue{Name: "variantId", Kind: model.VarString, Text: variant})
	}

	var key uint64
	var found bool
	s.do(func() {
		if d := s.latestDeploymentOf(process); d != nil {
			key, found = d.Key, true
		}
	})
	if !found {
		return fmt.Errorf("no deployed process with id %s", process)
	}
	s.do(func() { s.proc.CreateInstance(key, vars...) })
	return s.drive()
}
