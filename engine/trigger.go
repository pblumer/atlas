package engine

import "github.com/pblumer/atlas/model"

// Directed triggers (ADR-0425).
//
// A message start event is a trigger, and a message reaches it by name through
// every deployed definition (ADR-0035). That is a broadcast, and every failure of a
// broadcast is silent: a name nobody listens to, a deactivated definition, a
// singleton key already live — each answers "published" and starts nothing. For a
// product's lifecycle process, started by an order or by an external system that
// retries, silence is the wrong answer: a line waits forever, or a retry revokes a
// right twice.
//
// A directed trigger addresses one definition and one of its message start events,
// starts it or says why not, and remembers the sender's trigger id so a second
// delivery answers with the first instance.

// TriggerOutcome is what a directed trigger did.
type TriggerOutcome uint8

const (
	// TriggerNotProcessed is the zero value: the command never ran.
	TriggerNotProcessed TriggerOutcome = iota
	// TriggerCreated: a new instance was started at the named start event.
	TriggerCreated
	// TriggerReplayed: this sender delivered this trigger id before; the answer is
	// the instance that answered then, and nothing was started now.
	TriggerReplayed
	// TriggerSingletonTaken: the start event is a singleton (ADR-0094) and an
	// instance for the same correlation key is still live.
	TriggerSingletonTaken
	// TriggerNoSuchStart: the definition is not the newest deployed version of its
	// process, or has no message start event of that name.
	TriggerNoSuchStart
	// TriggerInactive: the definition is deactivated (ADR-0119).
	TriggerInactive
)

// TriggerResult is written through Command.Triggered.
type TriggerResult struct {
	Outcome     TriggerOutcome
	InstanceKey uint64
}

// TriggerStart enqueues a directed trigger: start defKey at its message start event
// named messageName, seeded with vars. source and triggerID identify the delivery;
// a non-empty triggerID makes it idempotent per source. The answer is written to
// *result once RunUntilIdle returned without error.
func (p *Processor) TriggerStart(defKey uint64, messageName, source, triggerID string, result *TriggerResult, vars ...model.VariableValue) {
	p.queue = append(p.queue, Command{
		ValueType: model.VTTriggerReceipt,
		Intent:    model.IntentTriggering,
		Value: inflightValue{
			process:      model.ProcessInstanceValue{ProcessDefKey: defKey},
			subscription: model.MessageSubscriptionValue{MessageName: messageName},
			trigger:      model.TriggerReceiptValue{Source: source, TriggerID: triggerID},
		},
		StartVars: vars,
		Triggered: result,
	})
}

// PruneTriggerReceipts enqueues dropping every receipt received before cutoff (Unix
// nanoseconds). A retry that arrives after its receipt is gone is a new trigger.
func (p *Processor) PruneTriggerReceipts(cutoff int64) {
	p.queue = append(p.queue, Command{
		ValueType: model.VTTriggerReceipt,
		Intent:    model.IntentTriggerReceiptsPruning,
		Value:     inflightValue{trigger: model.TriggerReceiptValue{Cutoff: cutoff}},
	})
}

func handleTriggering(c *ProcessingContext) {
	res := c.cmd.Triggered
	report := func(o TriggerOutcome, key uint64) {
		if res != nil {
			res.Outcome, res.InstanceKey = o, key
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
			report(TriggerReplayed, key)
			return
		}
	}
	defKey := c.cmd.Value.process.ProcessDefKey
	// The index holds only the newest version of each process id (supersedeStarts),
	// so a definition key that is not the newest finds no entry here — the same
	// version a message would start.
	var ref *messageStartRef
	refs := c.p.messageStarts[c.cmd.Value.subscription.MessageName]
	for i := range refs {
		if refs[i].defKey == defKey {
			ref = &refs[i]
			break
		}
	}
	if ref == nil || c.process(defKey) == nil {
		report(TriggerNoSuchStart, 0)
		return
	}
	if !c.p.ProcessActive(defKey) {
		report(TriggerInactive, 0)
		return
	}
	startKey := evalStartCorrelationKey(ref.correlationKey, c.cmd.StartVars)
	if ref.singletonStart && startKey != "" {
		taken, err := c.singletonStartTaken(defKey, startKey)
		if err != nil {
			c.p.fail(err)
			return
		}
		if taken {
			report(TriggerSingletonTaken, 0)
			return
		}
	}
	// Created in this batch rather than as a followup, so the instance and the
	// receipt that names it commit with one fsync (I2): a crash leaves both or
	// neither, and a retry after it either replays or starts — never both.
	key := c.NewKey()
	activateInstance(c, key, instanceSeed{
		Instance:      model.ProcessInstanceValue{ProcessDefKey: defKey, CorrelationKey: startKey},
		Vars:          c.cmd.StartVars,
		StartElements: []int32{ref.elementId},
	})
	if rcpt.TriggerID != "" {
		rcpt.InstanceKey, rcpt.At = key, c.Now()
		c.AppendTriggerReceiptEvent(model.IntentTriggerReceived, rcpt)
	}
	report(TriggerCreated, key)
}

func handleTriggerReceiptsPruning(c *ProcessingContext) {
	c.AppendTriggerReceiptEvent(model.IntentTriggerReceiptsPruned, c.cmd.Value.trigger)
}
