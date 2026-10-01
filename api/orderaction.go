package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/model"
)

// Asking a held position for one of its product's actions (ADR-0429 §2).
//
// A product declares what can be asked of what somebody holds — a larger mailbox, a
// password reset, an inactivation — and this is the one act that asks it. It checks
// the order first, in this order: the order is the caller's to act on, the action is
// one the line froze, the caller is one of the action's triggers, and the position is
// held. Only then does it fire, in-process and never through the public trigger route
// (ADR-0425 §7): to the strand that carries the right for a per-position product
// (ADR-0428 §2), or at the action's start event for a per-operation one (ADR-0425 §2).
//
// The change route of ADR-0428 is this act for the action keyed `change`.
//
// What the act does not do is give a position back or provision it: those are the
// order's own acts, with a status the order keeps (returning, running), and they keep
// their routes. Asking for either here is refused with the route that does it.

// actionReq is the body of an action act.
type actionReq struct {
	// CommandID makes a retry answer with the first outcome instead of asking twice.
	// It becomes the trigger id (ADR-0425 §3), so it is required.
	CommandID string `json:"commandId"`
	// Reason is what the caller says the action is for; it reaches the process.
	Reason string `json:"reason"`
	// Variables are what the action needs — what its form asked.
	Variables map[string]any `json:"variables"`
	// Trigger, when set, is which of the action's triggers the caller asks as:
	// `customer`, `operator` or `system`. The action must declare it and the caller
	// must be one — an operator for the last two. An agent asks through MCP with
	// `operator` or `system` only, so a customer's action is never an agent's to ask
	// (ADR-0429 §2, maintainers' decision of 2026-10-01). Left out, the caller asks
	// as whichever trigger they are.
	Trigger string `json:"trigger"`
}

// actionResp names the instance that took the action.
type actionResp struct {
	Action      string `json:"action"`
	InstanceKey uint64 `json:"instanceKey"`
	Process     string `json:"process"`
}

// seededActionVars are the variables the act sets itself. A caller's variable under
// one of these names is refused rather than appended: the process reads the order
// and the position from them, and the server records the instance on the position
// they name — a caller choosing them would file its instance on somebody else's order.
var seededActionVars = map[string]bool{
	"itemId": true, progressPositionVar: true, progressOrderVar: true, "recipient": true, "reason": true,
	commandIDVar: true,
}

// handleLineAction asks one held position for one of its product's actions.
func (s *Server) handleLineAction(w http.ResponseWriter, r *http.Request) {
	var req actionReq
	if !readActionBody(w, r, s.budgets().ModelUpload, &req) {
		return
	}
	if strings.TrimSpace(req.CommandID) == "" {
		httpapi.Error(w, http.StatusBadRequest, "commandId is required: it is what makes a retry "+
			"answer with the first outcome instead of asking for the action twice")
		return
	}
	out, status, msg := s.actOnLine(httpapi.PrincipalFrom(r.Context()), r.PathValue("id"),
		r.PathValue("item"), r.PathValue("action"), req)
	if status != http.StatusOK {
		httpapi.Error(w, status, msg)
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// readActionBody decodes an optional JSON body into v, answering 400 itself when it
// cannot. Numbers stay json.Number so a variable keeps the digits it was sent with.
func readActionBody(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, limit))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return false
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return true
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

// heldLine is one position of one order, read on the loop for an act on it.
type heldLine struct {
	ord      order.Order
	line     order.Line
	position string
}

// lineFor reads the order and resolves the position, answering the status a caller
// is told: 404 for an order that is not there or not theirs to act on — the same
// answer, so whether somebody else's order exists is not confirmed — and 409 for a
// position the order does not name unambiguously.
func (s *Server) lineFor(p *httpapi.Principal, id, item string) (heldLine, int, string) {
	var (
		out     heldLine
		found   bool
		refusal string
		opErr   error
	)
	s.do(func() {
		o, ok, err := s.orderStore.Get(id)
		if err != nil {
			opErr = err
			return
		}
		if !ok || !s.mayAskOf(p, o) {
			return
		}
		found = true
		position, err := order.ResolveLine(o, item)
		if err != nil {
			refusal = err.Error()
			return
		}
		for _, l := range o.Lines {
			if l.Key() == position {
				out = heldLine{ord: o, line: l, position: position}
			}
		}
	})
	switch {
	case opErr != nil:
		return heldLine{}, http.StatusInternalServerError, "read order: " + opErr.Error()
	case !found:
		return heldLine{}, http.StatusNotFound, "no order " + id
	case refusal != "":
		return heldLine{}, http.StatusConflict, refusal
	}
	return out, http.StatusOK, ""
}

// mayAskOf is who may act on a held position of order o at all: whoever placed the
// order, whoever holds what it granted, and an operator for any order (ADR-0429 §10,
// decision 4). A right ordered for somebody else is changed by the person who uses it.
// Whether they may ask for one particular action is [Server.mayTrigger].
func (s *Server) mayAskOf(p *httpapi.Principal, o order.Order) bool {
	if s.actsAsOperator(p) {
		return true
	}
	if p == nil {
		return false
	}
	return (o.Orderer != "" && o.Orderer == p.UserID) || (o.Recipient != "" && o.Recipient == p.UserID)
}

// actsAsOperator reports whether p acts for whoever runs the service: an operator or
// an admin, or anybody on a server without authentication.
func (s *Server) actsAsOperator(p *httpapi.Principal) bool {
	if !s.authEnabled {
		return true
	}
	return p != nil && (p.HasRole(RoleOperator) || p.HasRole(RoleAdmin))
}

// mayTrigger reports whether p is one of action a's triggers on order o. An operator
// may ask for every action a person or an observer asks for: `customer` includes an
// operator for any order, and an observer reports a `system` crossing under an
// operator credential until scoped grants exist (ADR-0429 §1, ADR-0425 §8). The
// orderer and the recipient may ask for what names `customer`.
func (s *Server) mayTrigger(p *httpapi.Principal, o order.Order, a catalog.Action) bool {
	if s.actsAsOperator(p) {
		return true
	}
	return a.TriggeredBy(catalog.TriggerCustomer) && s.mayAskOf(p, o)
}

// mayActAs reports whether p may ask as trigger t on order o: as `customer` whoever
// may act on the order at all, as `operator` or `system` an operator.
func (s *Server) mayActAs(p *httpapi.Principal, o order.Order, t string) bool {
	if t == catalog.TriggerCustomer {
		return s.mayAskOf(p, o)
	}
	return s.actsAsOperator(p)
}

// actOnLine runs the act: check, then fire. It answers the response or the status
// and the words of a refusal.
func (s *Server) actOnLine(p *httpapi.Principal, id, item, key string, req actionReq) (actionResp, int, string) {
	extra, err := startVarsFromMap(req.Variables)
	if err != nil {
		return actionResp{}, http.StatusBadRequest, err.Error()
	}
	asked := strings.TrimSpace(req.Trigger)
	if asked != "" && !catalog.KnownTrigger(asked) {
		return actionResp{}, http.StatusBadRequest, "trigger must be one of customer, operator, system"
	}
	for _, v := range extra {
		if seededActionVars[v.Name] {
			return actionResp{}, http.StatusBadRequest, "variable " + v.Name + " is set by the order " +
				"and cannot be sent with an action"
		}
	}
	h, status, msg := s.lineFor(p, id, item)
	if status != http.StatusOK {
		return actionResp{}, status, msg
	}
	line, position := h.line, h.position
	a, ok := line.ActionNamed(key)
	switch {
	case !ok:
		return actionResp{}, http.StatusConflict, "product " + line.ItemID + " declares no action " + key +
			" for this position"
	case a.Effect == catalog.EffectProvision:
		return actionResp{}, http.StatusConflict, "the provision action is the order's own: it runs " +
			"when the position is fulfilled and is not asked for"
	case a.Effect == catalog.EffectDeprovision:
		return actionResp{}, http.StatusConflict, "a held position is given back through its return " +
			"(POST /api/v1/orders/" + id + "/lines/" + position + "/return), which the order records"
	case asked != "" && !a.TriggeredBy(asked):
		return actionResp{}, http.StatusForbidden, "action " + key + " of product " + line.ItemID +
			" is asked for by " + strings.Join(a.Triggers, ", ") + ", not by " + asked
	case asked != "" && !s.mayActAs(p, h.ord, asked):
		return actionResp{}, http.StatusForbidden, "only an operator may ask as " + asked
	case asked == "" && !s.mayTrigger(p, h.ord, a):
		return actionResp{}, http.StatusForbidden, "action " + key + " of product " + line.ItemID +
			" is asked for by " + strings.Join(a.Triggers, ", ") + ", not by the person it is held for"
	case line.Status != order.StatusDone:
		return actionResp{}, http.StatusConflict, "position " + position + " is " + string(line.Status) +
			"; an action is asked only of a held right"
	}
	b := line.BindingFor(key)
	if !b.Triggered() {
		return actionResp{}, http.StatusConflict, "product " + line.ItemID + " binds no message for action " + key
	}

	vars := []model.VariableValue{
		{Name: "itemId", Kind: model.VarString, Text: line.ItemID},
		{Name: progressPositionVar, Kind: model.VarString, Text: position},
		{Name: progressOrderVar, Kind: model.VarString, Text: id},
		{Name: "recipient", Kind: model.VarString, Text: h.ord.Recipient},
		// What the process reports the outcome against (ADR-0429 §4).
		{Name: commandIDVar, Kind: model.VarString, Text: req.CommandID},
	}
	if reason := strings.TrimSpace(req.Reason); reason != "" {
		vars = append(vars, model.VariableValue{Name: "reason", Kind: model.VarString, Text: reason})
	}
	vars = append(vars, extra...)
	triggerID := "order:" + id + ":" + position + ":" + key + ":" + req.CommandID

	var instKey uint64
	if line.PerPosition() {
		// No fallback: an action reaches the strand or nothing. Starting a strand to
		// change a right would run a second instance for one right.
		strand := line.StrandOf()
		if strand == 0 {
			return actionResp{}, http.StatusConflict, "no instance carries position " + position +
				"; there is nothing running to ask"
		}
		instKey, err = s.deliverChange(strand, b, positionCorrelationKey(id, position), triggerID, vars)
	} else {
		instKey, err = s.startBinding(b, triggerID, vars)
	}
	if err != nil {
		var refused errTriggerRefused
		if errors.As(err, &refused) {
			return actionResp{}, http.StatusConflict, err.Error()
		}
		return actionResp{}, http.StatusInternalServerError, "action " + key + ": " + err.Error()
	}
	s.notePositionInstanceOp(vars, instKey, b.Process, key)
	return actionResp{Action: key, InstanceKey: instKey, Process: b.Process}, http.StatusOK, ""
}
