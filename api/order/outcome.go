package order

import (
	"strconv"
	"strings"

	"github.com/pblumer/atlas/api/catalog"
)

// How an act on a position ended, as the engine records it beside the right it
// changed (ADR-0429 §3).
//
// The provision and the return are the order's own acts, and their statuses map
// onto the closed outcome vocabulary: done and returned are completed, failed and
// returnFailed are failed, and an approver's refusal is the rejected of the
// provision. A change or a service asked through the action act is reported by the
// process that carries it out.

// Outcome is one such ending. The zero value is no outcome.
type Outcome struct {
	CommandID   string
	Action      string
	Effect      string
	Outcome     string
	EventType   string
	OrderID     string
	Position    string
	Principal   string
	ItemID      string
	VariantID   string
	InstanceKey uint64
	At          int64
	// Result is a JSON object of scalars, bounded by whoever reports it.
	Result string
}

// Set reports whether o names an ending at all.
func (o Outcome) Set() bool { return o.CommandID != "" }

// AttemptID names one attempt at one operation of one position: the same attempt
// delivered twice is one trigger, and a deliberate retry is the next attempt. It is
// also the command id of the order's own acts, so the outcome of a provision names
// the attempt that provisioned.
func AttemptID(orderID, position, op string, attempt int) string {
	return "order:" + orderID + ":" + position + ":" + op + ":" + strconv.Itoa(attempt)
}

// EventTypeOf is what an outcome of action a is published as: what the product
// declared for it, or <message>.<outcome>; an action with no message — a product of
// two processes — is named after the item and the action.
func EventTypeOf(itemID string, a catalog.Action, outcome string) string {
	if t := strings.TrimSpace(a.Outcomes[outcome]); t != "" {
		return t
	}
	if a.Message != "" {
		return a.Message + "." + outcome
	}
	return itemID + "." + a.Key + "." + outcome
}

// ownOutcome is the outcome of one of the order's own acts on line key of o — the
// provision or the return — for the attempt the line last recorded.
func ownOutcome(o Order, key string, line Line, effect, outcome string) Outcome {
	a := catalog.Action{Key: effect, Effect: effect}
	for _, cand := range line.ActionList() {
		if cand.Effect == effect {
			a = cand
		}
	}
	attempt := line.StartsOf(effect)
	if attempt == 0 {
		// Nothing was started — an approver refused before the provision ran. The
		// attempt that would have run is the first.
		attempt = 1
	}
	var inst uint64
	for _, in := range line.Instances {
		if in.Operation == effect {
			inst = in.Key
		}
	}
	return Outcome{
		CommandID: AttemptID(o.ID, key, effect, attempt), Action: a.Key, Effect: effect,
		Outcome: outcome, EventType: EventTypeOf(line.ItemID, a, outcome),
		OrderID: o.ID, Position: key, Principal: o.Recipient, ItemID: line.ItemID,
		VariantID: line.VariantID, InstanceKey: inst, At: o.UpdatedAt,
	}
}
