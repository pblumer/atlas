package api

import (
	"fmt"
	"strings"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// The shop send task, served in-process (ADR-0429 §4).
//
// It states how the command its instance carries ended. Everything it needs is in
// the instance's scope — the order, the position and the command id, which every act
// seeds — and in the order the command was asked of; nothing is in the model but the
// action and the ending, and no credential is anywhere.
//
// The handler runs off the run loop (and on it, when a fork or a migration drives
// jobs), so it changes nothing itself: it reads the order — the order store is read
// off the loop by design — and hands the outcome back on the job's completion, which
// the engine appends in the batch that completes the job. A command that already
// ended differently keeps its first ending.

// outcomeSourceShop is the source an outcome stated by a shop send task is recorded
// under.
const outcomeSourceShop = "atlas:shop"

// shopTaskHandler builds the job handler for shop send tasks.
func (s *Server) shopTaskHandler(rd state.Reader) job.CompletingHandler {
	return func(j job.Job) (job.Completion, error) {
		ei, ok, err := rd.GetElementInstance(j.ElementInstanceKey)
		if err != nil {
			return job.Completion{}, err
		}
		if !ok {
			return job.Completion{}, nil // the task is gone; nothing to state
		}
		cp := s.processLookup(ei.ProcessDefKey)
		if cp == nil {
			return job.Completion{}, fmt.Errorf("shop task: no compiled process for def %d", ei.ProcessDefKey)
		}
		detail, err := cp.ConnectorTaskOf(ei.ElementId)
		if err != nil {
			return job.Completion{}, fmt.Errorf("shop task: %w", err)
		}
		scope, err := state.VisibleVariablesMap(rd, j.ElementInstanceKey)
		if err != nil {
			return job.Completion{}, fmt.Errorf("shop task: read variables: %w", err)
		}
		v, err := s.shopOutcome(rd, detail.ShopAction, detail.ShopOutcome, scope)
		if err != nil {
			return job.Completion{}, fmt.Errorf("shop task: %w", err)
		}
		return job.Completion{Outcome: &v}, nil
	}
}

// receiptReader is the read surface a store and a read view share for trigger
// receipts.
type receiptReader interface {
	TriggerReceipt(source, triggerID string) (uint64, bool, error)
}

// shopOutcome builds the outcome a shop task states, from what the instance carries
// and the order it carries it for. Each refusal names what is missing in words the
// operator reading the incident can act on.
func (s *Server) shopOutcome(rd state.Reader, actionKey, outcome string, scope map[string]model.VariableValue) (model.ActionOutcomeValue, error) {
	str := func(name string) string {
		if v, ok := scope[name]; ok && v.Kind == model.VarString {
			return strings.TrimSpace(v.Text)
		}
		return ""
	}
	orderID, ref, commandID := str(progressOrderVar), str(progressPositionVar), str(commandIDVar)
	if orderID == "" || ref == "" || commandID == "" {
		return model.ActionOutcomeValue{}, fmt.Errorf("the instance carries no %s, %s and %s as strings; "+
			"a shop task reports for an instance an order act started or delivered to",
			progressOrderVar, progressPositionVar, commandIDVar)
	}
	o, found, err := s.orderStore.Get(orderID)
	if err != nil {
		return model.ActionOutcomeValue{}, fmt.Errorf("read order %s: %w", orderID, err)
	}
	if !found {
		return model.ActionOutcomeValue{}, fmt.Errorf("no order %s", orderID)
	}
	position, err := order.ResolveLine(o, ref)
	if err != nil {
		return model.ActionOutcomeValue{}, err
	}
	var line order.Line
	for _, l := range o.Lines {
		if l.Key() == position {
			line = l
		}
	}
	// The command was asked for this action if the act's trigger for it was applied.
	// The receipt is written in the batch that delivered or started the command, so
	// it is visible to the task the command led to — unlike the order's own record of
	// the command, which is noted after the delivery returns and may lag the task.
	if !s.commandTook(rd, line, orderID, position, actionKey, commandID) {
		return model.ActionOutcomeValue{}, fmt.Errorf("position %s of order %s took no command %s for %s — "+
			"the model's path does not match the action it carries out", position, orderID, commandID, actionKey)
	}
	a, ok := line.ActionNamed(actionKey)
	if !ok {
		return model.ActionOutcomeValue{}, fmt.Errorf("product %s declares no action %s for this position", line.ItemID, actionKey)
	}
	if a.Effect == catalog.EffectProvision || a.Effect == catalog.EffectDeprovision {
		return model.ActionOutcomeValue{}, fmt.Errorf("the %s of a position is reported through the line's report "+
			"route, which records the right it changes", a.Effect)
	}
	return outcomeValue(order.Outcome{
		CommandID: commandID, Action: a.Key, Effect: a.Effect, Outcome: outcome,
		EventType: order.EventTypeOf(line.ItemID, a, outcome),
		OrderID:   o.ID, Position: position, Principal: o.Recipient,
		ItemID: line.ItemID, VariantID: line.VariantID, At: s.now(),
	}, outcomeSourceShop), nil
}

// commandTook reports whether position took command commandID for action key: the
// act's trigger for it was applied, or the order recorded it.
func (s *Server) commandTook(rd state.Reader, line order.Line, orderID, position, key, commandID string) bool {
	// A handler's reader is wrapped to decipher personal values; a receipt is none,
	// so it is read from the store beneath.
	if pr, ok := rd.(personalReader); ok {
		rd = pr.Reader
	}
	if rr, ok := rd.(receiptReader); ok {
		if _, found, err := rr.TriggerReceipt(orderTriggerSource,
			"order:"+orderID+":"+position+":"+key+":"+commandID); err == nil && found {
			return true
		}
	}
	inst, ok := line.Command(commandID)
	return ok && inst.Operation == key
}
