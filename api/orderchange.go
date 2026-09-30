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

// Changing a held position (ADR-0428).
//
// A per-position lifecycle carries the right in one instance for as long as it is
// held, and a change — a larger mailbox, a second monitor cable, a new cost centre —
// is a message that instance waits for. This is the route that sends it: to the
// instance the line recorded, by key, and with an answer. A change has no fallback
// the way a return does: there is nothing to change on a right whose instance is
// gone, and starting one to change it would run a second strand for one right.
//
// The line does not change status: it is held before a change and held after it.
// What the change did is the instance's record, which is the point of the form.

// changeReq is the body of a change.
type changeReq struct {
	// ChangeID makes a retry answer with the first delivery instead of changing the
	// right twice. Required, as the trigger route's triggerId is.
	ChangeID string `json:"changeId"`
	// Reason is what the orderer says the change is for; it reaches the process.
	Reason string `json:"reason"`
	// Variables are what the change needs — the new size, the new cost centre.
	Variables map[string]any `json:"variables"`
}

// changeResp names the instance that took the change.
type changeResp struct {
	InstanceKey uint64 `json:"instanceKey"`
	Process     string `json:"process"`
}

func (s *Server) handleChangeLine(w http.ResponseWriter, r *http.Request) {
	id, item := r.PathValue("id"), r.PathValue("item")
	body, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().ModelUpload))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var req changeReq
	if len(bytes.TrimSpace(body)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		if err := dec.Decode(&req); err != nil {
			httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
	}
	if strings.TrimSpace(req.ChangeID) == "" {
		httpapi.Error(w, http.StatusBadRequest, "changeId is required: it is what makes a retry "+
			"answer with the first change instead of changing the right twice")
		return
	}
	extra, err := startVarsFromMap(req.Variables)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	p := httpapi.PrincipalFrom(r.Context())

	var (
		ord      order.Order
		line     order.Line
		position string
		found    bool
		refusal  string
		opErr    error
	)
	s.do(func() {
		o, ok, err := s.orderStore.Get(id)
		if err != nil {
			opErr = err
			return
		}
		// The same right that gives a line back changes it: both are the orderer
		// saying what happens to what they asked for, and the same 404 hides whose
		// orders exist.
		if !ok || !s.mayCancelOrder(p, o) {
			return
		}
		found = true
		if position, err = order.ResolveLine(o, item); err != nil {
			refusal = err.Error()
			return
		}
		for _, l := range o.Lines {
			if l.Key() == position {
				line = l
			}
		}
		ord = o
	})
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "change line: "+opErr.Error())
		return
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no order "+id)
		return
	case refusal != "":
		httpapi.Error(w, http.StatusConflict, refusal)
		return
	}
	b := line.BindingFor(catalog.OpChange)
	switch {
	case !line.PerPosition():
		httpapi.Error(w, http.StatusConflict, "position "+position+" does not run as one instance "+
			"per position; a change is delivered only to the instance that carries a right")
		return
	case !b.Triggered():
		httpapi.Error(w, http.StatusConflict, "product "+line.ItemID+" binds no change operation")
		return
	case line.Status != order.StatusDone:
		httpapi.Error(w, http.StatusConflict, "position "+position+" is "+string(line.Status)+
			"; only a held right can be changed")
		return
	}
	strand := line.StrandOf()
	if strand == 0 {
		httpapi.Error(w, http.StatusConflict, "no instance carries position "+position+"; "+
			"there is nothing running to change")
		return
	}

	vars := []model.VariableValue{
		{Name: "itemId", Kind: model.VarString, Text: line.ItemID},
		{Name: "positionId", Kind: model.VarString, Text: position},
		{Name: "orderId", Kind: model.VarString, Text: id},
		{Name: "recipient", Kind: model.VarString, Text: ord.Recipient},
	}
	if reason := strings.TrimSpace(req.Reason); reason != "" {
		vars = append(vars, model.VariableValue{Name: "reason", Kind: model.VarString, Text: reason})
	}
	vars = append(vars, extra...)
	triggerID := "order:" + id + ":" + position + ":" + catalog.OpChange + ":" + req.ChangeID
	// No fallback: a change reaches the strand or nothing. deliverOrStart falls back
	// only for a strand that is gone, so that case is answered here first.
	key, err := s.deliverChange(strand, b, positionCorrelationKey(id, position), triggerID, vars)
	if err != nil {
		var refused errTriggerRefused
		if errors.As(err, &refused) {
			httpapi.Error(w, http.StatusConflict, err.Error())
			return
		}
		httpapi.Error(w, http.StatusInternalServerError, "change line: "+err.Error())
		return
	}
	s.notePositionInstanceOp(vars, key, b.Process, catalog.OpChange)
	httpapi.JSON(w, http.StatusOK, changeResp{InstanceKey: key, Process: b.Process})
}
