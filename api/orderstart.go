package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/model"
)

// The start act: the order starts its own positions (ADR-0425).
//
// The fulfilment process and the approval processes used to start a position by
// posting a process id to POST /api/v1/instances. That put the choice of where a
// position starts into four models, and with a product's lifecycle process — one
// process, entered at a start event per operation — it would have put a branch
// between two routes into each of them, plus a trigger id only the order can
// compute. So the models ask the order to start the position, and the order
// decides: which process, which start event, whether an approval is still owed,
// and whether this position was already started.
//
// Authority is the order's state, not the caller's identity (ADR-0425 §7): any
// operator token can call this, as it could call the create route, but only a
// position the order says may start is started, and only once.

type lineStartReq struct {
	// Operation is what to start. Only provision is started through this act: a
	// return has its own route, and change is not something an order does yet.
	Operation string `json:"operation"`
	// ApprovedBy records the approval an approval process reached, in the same act
	// that starts the provisioning it approved — the order's record of who agreed,
	// and the state that lets this act refuse a gated line nobody approved.
	ApprovedBy string `json:"approvedBy"`
}

type lineStartResp struct {
	InstanceKey uint64 `json:"instanceKey"`
	// Started is what was started: "approval" for a gated line nobody approved yet,
	// "provision" otherwise.
	Started string `json:"started"`
	Process string `json:"process"`
	// Already is true when the position was started before and this answered with
	// that instance instead of starting another.
	Already bool `json:"already,omitempty"`
}

// handleStartLine starts one position of an order.
func (s *Server) handleStartLine(w http.ResponseWriter, r *http.Request) {
	id, ref := r.PathValue("id"), r.PathValue("item")
	var req lineStartReq
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpapi.Error(w, http.StatusBadRequest, "malformed JSON body: "+err.Error())
			return
		}
	}
	if req.Operation == "" {
		req.Operation = catalog.OpProvision
	}
	if req.Operation != catalog.OpProvision {
		httpapi.Error(w, http.StatusBadRequest, "operation "+req.Operation+" is not started "+
			"through this act: a return has its own route, and a change of what is held is not "+
			"an order's to start")
		return
	}

	var (
		ord      order.Order
		line     order.Line
		position string
		found    bool
		refusal  string
		opErr    error
	)
	at := s.now()
	s.do(func() {
		var ok bool
		if ord, ok, opErr = s.orderStore.Get(id); opErr != nil || !ok {
			return
		}
		if position, opErr = order.ResolveLine(ord, ref); opErr != nil {
			opErr = nil
			return
		}
		found = true
		for i := range ord.Lines {
			if ord.Lines[i].Key() != position {
				continue
			}
			line = ord.Lines[i]
			// An approval reached by the process that asked for it is recorded here,
			// before anything reads whether the line is approved.
			if req.ApprovedBy != "" && line.NeedsApproval() && line.ApprovedBy == "" {
				next, err := order.Approve(line, req.ApprovedBy, at)
				if err != nil {
					refusal = err.Error()
					return
				}
				ord.Lines[i], line = next, next
				ord.UpdatedAt = at
				opErr = s.orderStore.Save(ord)
			}
			break
		}
		switch {
		case line.Status == order.StatusRunning && line.StartsOf(catalog.OpProvision) > 0:
			// Started before: answered below with that instance.
		case line.Status != order.StatusPending:
			refusal = "line " + position + " is " + string(line.Status) + "; only a waiting line starts"
		case !slices.Contains(order.Next(ord), position):
			refusal = "line " + position + " waits on a precondition that is not provisioned yet"
		}
	})
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "start line: "+opErr.Error())
		return
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no order "+id+" with a line "+ref)
		return
	case refusal != "":
		httpapi.Error(w, http.StatusConflict, refusal)
		return
	}

	// Started before — by this act, or by an earlier delivery of the same call.
	if prev, ok := lastInstanceOf(line, catalog.OpProvision); ok {
		httpapi.JSON(w, http.StatusOK, lineStartResp{InstanceKey: prev.Key,
			Started: catalog.OpProvision, Process: prev.ProcessID, Already: true})
		return
	}

	vars := s.positionStartVars(ord, line, position)

	// A gated line nobody approved starts its approval, not its provisioning. The
	// approval process calls this act again with approvedBy once somebody agreed.
	if approval := line.ApprovalProcess(); approval != "" && line.ApprovedBy == "" {
		if prev, ok := lastInstanceOf(line, "approval"); ok {
			httpapi.JSON(w, http.StatusOK, lineStartResp{InstanceKey: prev.Key,
				Started: "approval", Process: prev.ProcessID, Already: true})
			return
		}
		b := catalog.Binding{Process: approval}
		key, err := s.startBinding(b, "", vars)
		if err != nil {
			s.startLineFailed(w, err)
			return
		}
		s.notePositionInstanceOp(vars, key, approval, "approval")
		httpapi.JSON(w, http.StatusOK, lineStartResp{InstanceKey: key, Started: "approval", Process: approval})
		return
	}

	b := line.BindingFor(catalog.OpProvision)
	key, err := s.startBinding(b, positionTriggerID(id, position, catalog.OpProvision, 1), vars)
	if err != nil {
		s.startLineFailed(w, err)
		return
	}
	s.notePositionInstanceOp(vars, key, b.Process, catalog.OpProvision)
	// The line is with its provisioning process now, and says so, so the next ask
	// of which lines may start does not offer it again.
	s.do(func() {
		cur, ok, err := s.orderStore.Get(id)
		if err != nil || !ok {
			opErr = err
			return
		}
		next, err := order.Apply(cur, position, order.StatusRunning, s.now())
		if err != nil {
			return // settled meanwhile — the report outran this; nothing to mark
		}
		opErr = s.orderStore.Save(next)
	})
	if opErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "the provisioning was started as instance "+
			"but the line could not be marked running: "+opErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, lineStartResp{InstanceKey: key, Started: catalog.OpProvision, Process: b.Process})
}

func (s *Server) startLineFailed(w http.ResponseWriter, err error) {
	var refused errTriggerRefused
	if errors.As(err, &refused) || strings.HasPrefix(err.Error(), "no deployed process") {
		httpapi.Error(w, http.StatusConflict, err.Error())
		return
	}
	httpapi.Error(w, http.StatusInternalServerError, "start line: "+err.Error())
}

// lastInstanceOf is the newest instance started for an operation of the line.
func lastInstanceOf(l order.Line, op string) (order.LineInstance, bool) {
	for i := len(l.Instances) - 1; i >= 0; i-- {
		if l.Instances[i].Operation == op {
			return l.Instances[i], true
		}
	}
	return order.LineInstance{}, false
}

// positionStartVars are the variables a position's process is started with: the
// ones the fulfilment process handed every process it started, so a model written
// against that keeps working when the order starts it instead.
func (s *Server) positionStartVars(o order.Order, l order.Line, position string) []model.VariableValue {
	str := func(name, text string) model.VariableValue {
		return model.VariableValue{Name: name, Kind: model.VarString, Text: text}
	}
	vars := []model.VariableValue{
		str("orderId", o.ID),
		str("itemId", l.ItemID),
		str("positionId", position),
		str("recipient", o.Recipient),
		str("orderer", o.Orderer),
		str("provisionProcess", l.BindingFor(catalog.OpProvision).Process),
		str("approvalRef", l.Approval.Ref),
		str("atlasApiBase", s.selfURL),
		str("portalBaseUrl", s.externalURL),
		// Always present, empty where the product has no variants, because a model
		// comparing an absent variable is comparing against null.
		str("variantId", l.VariantID),
	}
	return vars
}
