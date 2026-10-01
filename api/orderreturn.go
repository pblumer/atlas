package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/catalog"
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
// Who may: the triggers of the line's deprovision action (ADR-0429). `customer` is
// the person who placed the order, the recipient who holds the right (§10, decision
// 4) and an operator; a product whose return names only `operator` is given back by
// an operator. A line that froze no actions — a product of two processes — is given
// back by its customer, as it always was.
func (s *Server) handleReturnLine(w http.ResponseWriter, r *http.Request) {
	id, item := r.PathValue("id"), r.PathValue("item")
	p := httpapi.PrincipalFrom(r.Context())

	var (
		out       order.Order
		binding   catalog.Binding
		found     bool
		returnErr error
		opErr     error
		forbidden string
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
		if !s.mayAskOf(p, ord) {
			// The same 404 as an order that is not there, because whose orders exist
			// is not something this endpoint answers.
			return
		}
		found = true
		if a, ok := frozenAction(ord, item, catalog.ActionDeprovision); ok && !s.mayTrigger(p, ord, a) {
			forbidden = "the return of this product is asked for by " + strings.Join(a.Triggers, ", ") +
				", not by the person it is held for"
			return
		}
		binding = order.ReturnBindingOf(ord, item)
		out, returnErr = order.Returning(ord, item, at, principalID(r))
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
	case forbidden != "":
		httpapi.Error(w, http.StatusForbidden, forbidden)
		return
	case returnErr != nil:
		httpapi.Error(w, http.StatusConflict, returnErr.Error())
		return
	}

	// Durable first, then the process (I2). The line says a return is under way
	// before anything runs, so a start that fails leaves a visible "returning" an
	// operator can act on rather than a silent nothing.
	if err := s.startReturn(binding, id, item, out, ""); err != nil {
		// A refusal is the engine's answer, not a fault: the instance that carries a
		// per-position line is not waiting for its return right now
		// (ADR-0428).
		status := http.StatusInternalServerError
		var refused errTriggerRefused
		if errors.As(err, &refused) {
			status = http.StatusConflict
		}
		httpapi.Error(w, status, "the return was recorded, but its process could not be started: "+err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, returnResp{Order: out, Process: binding.Process})
}

// startReturn runs the line's deprovisioning, seeded the way its provisioning was:
// the order and the line it is about, the variant that was chosen, and who holds
// it. The process reports its outcome back through the same endpoint a
// provisioning does.
//
// reason is set when something other than the orderer asked for the return — a
// recertification — and says what, in words for the person the process shows it
// to. The orderer's own return carries none.
func (s *Server) startReturn(b catalog.Binding, orderID, ref string, o order.Order, reason string) error {
	position, err := order.ResolveLine(o, ref)
	if err != nil {
		return err
	}
	var line order.Line
	for _, l := range o.Lines {
		if l.Key() == position {
			line = l
			break
		}
	}
	variant := line.VariantID
	// itemId names the product, because that is what a deprovisioning process
	// revokes. positionId names the position, because that is what it reports the
	// outcome against — and where one product was ordered in two shapes, only the
	// second can say which of them came back (ADR-0384).
	vars := []model.VariableValue{
		{Name: "itemId", Kind: model.VarString, Text: line.ItemID},
		{Name: "positionId", Kind: model.VarString, Text: position},
		{Name: "orderId", Kind: model.VarString, Text: orderID},
		{Name: "recipient", Kind: model.VarString, Text: o.Recipient},
	}
	if variant != "" {
		vars = append(vars, model.VariableValue{Name: "variantId", Kind: model.VarString, Text: variant})
	}
	if reason != "" {
		vars = append(vars, model.VariableValue{Name: "reason", Kind: model.VarString, Text: reason})
	}

	// The attempt is counted before this start, so a return asked for again after
	// it failed is a new trigger rather than a replay of the one that failed.
	triggerID := positionTriggerID(orderID, position, catalog.OpDeprovision,
		line.StartsOf(catalog.OpDeprovision)+1)
	vars = append(vars, model.VariableValue{Name: commandIDVar, Kind: model.VarString, Text: triggerID})
	var instKey uint64
	if line.PerPosition() {
		// A per-position line's return is delivered to the instance that carries it,
		// and starts the process only where that instance is gone
		// (ADR-0428).
		instKey, err = s.deliverOrStart(line.StrandOf(), b, positionCorrelationKey(orderID, position), triggerID, vars)
	} else {
		instKey, err = s.startBinding(b, triggerID, vars)
	}
	if err != nil {
		return err
	}
	// The return is a process working this position like any other, and its tasks
	// belong beside it in the shop (ADR-0416).
	s.notePositionInstanceOp(vars, instKey, b.Process, catalog.OpDeprovision)
	return nil
}

// frozenAction is the action key that the line ref names froze, when the reference
// resolves to one line and that line froze it. A reference that does not resolve is
// answered by whoever acts on it, with the words the order gives.
func frozenAction(o order.Order, ref, key string) (catalog.Action, bool) {
	position, err := order.ResolveLine(o, ref)
	if err != nil {
		return catalog.Action{}, false
	}
	for _, l := range o.Lines {
		if l.Key() == position {
			return l.ActionNamed(key)
		}
	}
	return catalog.Action{}, false
}
