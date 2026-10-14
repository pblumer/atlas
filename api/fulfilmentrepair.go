package api

import (
	"errors"
	"net/http"
	"sort"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// Putting an order back in front of a working orchestration.
//
// An orchestration that does not know its order id can do nothing and will never
// stop trying: it builds every request from that variable, so with it absent the
// address is null, the REST worker is asked for nothing, and the instance sits on
// its first service task until somebody removes it. Every order it was supposed to
// work stays at "Wartet" with no incident to find, because nothing failed.
//
// One installation could reach that state through a defect in the wake that started
// it (fixed in api/order/service.go: orderId was passed as the message's correlation
// key, which a message *start* event evaluates from the payload). The instances it
// left behind are not repaired by deploying the fix — they are already running, and
// the variable they needed was never written. This is the route that clears them.
//
// It is deliberately not a startup sweep. Cancelling instances and starting
// processes is an act with consequences outside Atlas, and the person who runs it
// should be able to look first — which is what the dry run is for.

// orphanedOrchestration is one fulfilment instance that cannot do its work, and
// why.
type orphanedOrchestration struct {
	Instance uint64 `json:"instance"`
	// OrderID is what the instance says it is working on, empty where it says
	// nothing — which is the case this route was written for.
	OrderID string `json:"orderId,omitempty"`
	Reason  string `json:"reason"`
}

// fulfilmentRepairResp is what the repair did, or would do.
type fulfilmentRepairResp struct {
	// DryRun says whether the two lists below are acts or a proposal.
	DryRun bool `json:"dryRun"`
	// Cancelled are the orchestrations ended, newest state first-come.
	Cancelled []orphanedOrchestration `json:"cancelled"`
	// Restarted names the orders an orchestration was started for again.
	Restarted []string `json:"restarted"`
	// Healthy counts the orchestrations left alone: they name an order that exists,
	// so they are working or waiting, and neither is this route's business.
	Healthy int `json:"healthy"`
}

// handleRepairFulfilment ends the fulfilment orchestrations that cannot work and
// starts one again for every open order left without one.
//
// The two halves are one act on purpose. Cancelling alone leaves the order with
// nothing working it, which is the state it was already in and now looks
// deliberate; starting alone would put a second orchestration beside a healthy one,
// and two orchestrations on one order both ask what may start and both start it.
func (s *Server) handleRepairFulfilment(w http.ResponseWriter, r *http.Request) {
	dryRun := r.URL.Query().Get("dryRun") == "true"

	// What is running, off the loop: this walks the active instances, which is the
	// population that grows with the installation (I3, ADR-0239).
	type orchestration struct {
		key     uint64
		orderID string
	}
	var running []orchestration
	scanErr := s.readOffLoop(func(rv *state.ReadView, defs defIndex) error {
		return rv.ActiveProcessInstances(func(key uint64, v *model.ProcessInstanceValue) error {
			if d, ok := defs[v.ProcessDefKey]; !ok || d.ProcessID != order.FulfilmentProcess {
				return nil
			}
			found := orchestration{key: key}
			if err := rv.VariablesOfScope(key, func(vv *model.VariableValue) error {
				if vv.Name == progressOrderVar && vv.Kind == model.VarString {
					found.orderID = vv.Text
				}
				return nil
			}); err != nil {
				return err
			}
			running = append(running, found)
			return nil
		})
	})
	switch {
	case errors.Is(scanErr, errLoopClosing):
		httpapi.Error(w, http.StatusServiceUnavailable, scanErr.Error())
		return
	case scanErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read the running orchestrations: "+scanErr.Error())
		return
	}

	var (
		orders  []order.Order
		loadErr error
	)
	s.do(func() { orders, loadErr = s.orderStore.All() })
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read orders: "+loadErr.Error())
		return
	}
	known := make(map[string]order.Order, len(orders))
	for _, o := range orders {
		known[o.ID] = o
	}

	out := fulfilmentRepairResp{DryRun: dryRun, Cancelled: []orphanedOrchestration{}, Restarted: []string{}}
	worked := map[string]bool{}
	for _, inst := range running {
		switch {
		case inst.orderID == "":
			// The defect this route exists for: nothing names the order, and nothing
			// ever will — the variable is written when the instance is created.
			out.Cancelled = append(out.Cancelled, orphanedOrchestration{
				Instance: inst.key,
				Reason:   "carries no order id, so every request it builds addresses nothing",
			})
		case known[inst.orderID].ID == "":
			// The order is gone and the orchestration is asking about it. Retention
			// deletes an order long after it settles, so this is an instance that
			// outlived its work rather than one that failed at it.
			out.Cancelled = append(out.Cancelled, orphanedOrchestration{
				Instance: inst.key, OrderID: inst.orderID,
				Reason: "names an order this server no longer holds",
			})
		default:
			out.Healthy++
			worked[inst.orderID] = true
		}
	}
	// An open order with nothing working it. Settled orders are left alone: an
	// orchestration for one would ask what may start, be told nothing, and end —
	// which is work for no purpose and a process instance in somebody's list.
	for _, o := range orders {
		if worked[o.ID] || order.Derive(o.Lines) != order.OrderRunning {
			continue
		}
		out.Restarted = append(out.Restarted, o.ID)
	}

	if dryRun {
		httpapi.JSON(w, http.StatusOK, out)
		return
	}

	// Cancel first, then start. The other order would leave a moment in which two
	// orchestrations name one order — and the broken one cannot be told from the new
	// one by anything but its variables.
	for _, gone := range out.Cancelled {
		s.do(func() { s.proc.CancelInstance(gone.Instance) })
		if err := s.drive(); err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "end an orchestration: "+err.Error())
			return
		}
	}
	for _, id := range out.Restarted {
		o := known[id]
		start := order.PlacedVariables(o, s.externalURL, s.selfURL)
		vars := make([]model.VariableValue, 0, len(start))
		for name, value := range start {
			vars = append(vars, model.VariableValue{Name: name, Kind: model.VarString, Text: value})
		}
		// Sorted, for the reason the placement's own wake sorts: a map has no order,
		// start variables are written in the order given, and the same act must
		// produce the same log live and on replay (I1).
		sort.Slice(vars, func(i, j int) bool { return vars[i].Name < vars[j].Name })
		s.do(func() { s.proc.PublishMessage(order.PlacedMessage, o.ID, vars...) })
		if err := s.drive(); err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "start an orchestration: "+err.Error())
			return
		}
	}
	httpapi.JSON(w, http.StatusOK, out)
}
