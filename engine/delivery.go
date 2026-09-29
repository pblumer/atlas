package engine

import "github.com/pblumer/atlas/model"

// Directed delivery (ADR-0428).
//
// A product whose lifecycle runs as one instance per order position starts that
// instance once, at provisioning, and every later operation — a change, the return —
// is a message the same instance waits for. The name-correlated publish can reach it,
// but every failure of that path is silent: a message nobody waits for answers
// "published" and is gone. A return lost that way leaves a right that nobody revokes.
//
// A directed delivery names the instance by key, hands the message only to that
// instance's open subscriptions for it, and says what happened: delivered, already
// delivered before, the instance is not waiting for it right now, or the instance is
// gone. It shares ADR-0425's trigger receipts, so a sender that retries is answered
// from the first delivery instead of changing or returning the same right twice.

// DeliveryOutcome is what a directed delivery did.
type DeliveryOutcome uint8

const (
	// DeliveryNotProcessed is the zero value: the command never ran.
	DeliveryNotProcessed DeliveryOutcome = iota
	// DeliveryDelivered: the instance was waiting for this message and took it.
	DeliveryDelivered
	// DeliveryReplayed: this sender delivered this trigger id before; nothing was
	// delivered now, and the answer names the instance that took it then.
	DeliveryReplayed
	// DeliveryNotWaiting: the instance is running but holds no open subscription for
	// this message under this correlation key — it is still being provisioned, or busy
	// in a step that does not listen for it. Nothing was delivered or buffered.
	DeliveryNotWaiting
	// DeliveryGone: no running instance has this key — it finished, was cancelled, or
	// never existed.
	DeliveryGone
)

// DeliveryResult is written through Command.Delivered.
type DeliveryResult struct {
	Outcome     DeliveryOutcome
	InstanceKey uint64
	// Elements are the BPMN element indexes of the subscriptions that took the
	// message; empty unless Outcome is DeliveryDelivered.
	Elements []int32
}

// DeliverMessage enqueues a directed delivery: hand the message messageName, under
// correlationKey, to the running instance instanceKey only, writing vars into its
// scope. source and triggerID identify the delivery; a non-empty triggerID makes it
// idempotent per source. The answer is written to *result once RunUntilIdle returned
// without error.
func (p *Processor) DeliverMessage(instanceKey uint64, messageName, correlationKey, source, triggerID string, result *DeliveryResult, vars ...model.VariableValue) {
	p.queue = append(p.queue, Command{
		Key:       instanceKey,
		ValueType: model.VTTriggerReceipt,
		Intent:    model.IntentDelivering,
		Value: inflightValue{
			subscription: model.MessageSubscriptionValue{MessageName: messageName, CorrelationKey: correlationKey},
			trigger:      model.TriggerReceiptValue{Source: source, TriggerID: triggerID},
		},
		StartVars: vars,
		Delivered: result,
	})
}

func handleDelivering(c *ProcessingContext) {
	res := c.cmd.Delivered
	report := func(o DeliveryOutcome, key uint64, elements []int32) {
		if res != nil {
			res.Outcome, res.InstanceKey, res.Elements = o, key, elements
		}
	}
	rcpt := c.cmd.Value.trigger
	if rcpt.TriggerID != "" {
		key, seen, err := c.tx.TriggerReceipt(rcpt.Source, rcpt.TriggerID)
		if err != nil {
			c.p.fail(err)
			return
		}
		if seen {
			report(DeliveryReplayed, key, nil)
			return
		}
	}
	piKey := c.cmd.Key
	if c.GetProcessInstance(piKey) == nil {
		report(DeliveryGone, piKey, nil)
		return
	}
	pub := c.cmd.Value.subscription
	var matches []subscriptionMatch
	c.p.fail(c.tx.CorrelatableSubscriptions(pub.MessageName, pub.CorrelationKey, func(elKey uint64, v *model.MessageSubscriptionValue) error {
		if v.ProcessInstanceKey == piKey {
			matches = append(matches, subscriptionMatch{elKey: elKey, sub: *v})
		}
		return nil
	}))
	if len(matches) == 0 {
		report(DeliveryNotWaiting, piKey, nil)
		return
	}
	// An API delivery has no sending instance, so the recorded flow's sender is 0.
	deliverToSubscriptions(c, matches, pub.MessageName, pub.CorrelationKey, c.cmd.StartVars, 0)
	// In the same batch as the correlation, so the delivery and the receipt naming
	// it commit with one fsync (I2): a retry after a crash either replays or
	// delivers — never both.
	if rcpt.TriggerID != "" {
		rcpt.InstanceKey, rcpt.At = piKey, c.Now()
		c.AppendTriggerReceiptEvent(model.IntentTriggerReceived, rcpt)
	}
	elements := make([]int32, 0, len(matches))
	for i := range matches {
		elements = append(elements, matches[i].sub.ElementId)
	}
	report(DeliveryDelivered, piKey, elements)
}
