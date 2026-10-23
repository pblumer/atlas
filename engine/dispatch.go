package engine

import "github.com/pblumer/atlas/model"

// The record a send leaves on the log (ADR-0472 §1).
//
// A signal broadcast and a message publish each write one VTDispatch event after their
// deliveries, so it says how far the send reached: the subscriptions whose element
// still waited, and the instances it started. A send that reached nobody is written
// the same way, with both counts zero — that is the case the event log exists for.
// Everything in it is decided here, on the command path, and frozen into the event,
// so replay folds the identical row (I4, I6).

// sender is who sent a signal or a message.
type sender struct {
	source    model.DispatchSource
	piKey     uint64
	defKey    uint64
	elementId int32
}

// fromElement is a running instance's element sending: a throw, an end event, or a
// send task naming a message.
func fromElement(ei *model.ElementInstanceValue) sender {
	return sender{source: model.DispatchFromProcess, piKey: ei.ProcessInstanceKey, defKey: ei.ProcessDefKey, elementId: ei.ElementId}
}

// appendDispatch records one send. It is keyed by the sending instance, zero when
// none sent it.
func (c *ProcessingContext) appendDispatch(ch model.DispatchChannel, from sender, name, correlationKey string, reached, started int) {
	c.appendEvent(from.piKey, model.VTDispatch, model.IntentDispatched, inflightValue{dispatch: model.DispatchValue{
		Channel: ch, Source: from.source, Name: name, CorrelationKey: correlationKey,
		SenderProcessInstanceKey: from.piKey, SenderProcessDefKey: from.defKey, SenderElementId: from.elementId,
		Reached: uint32(reached), Started: uint32(started),
	}})
}

// appendDirectedDispatch records a directed delivery to one instance (ADR-0428): the
// instance it was addressed to and its definition, zero when the instance is gone.
func (c *ProcessingContext) appendDirectedDispatch(name, correlationKey string, targetPI, targetDef uint64, reached int) {
	c.appendEvent(targetPI, model.VTDispatch, model.IntentDispatched, inflightValue{dispatch: model.DispatchValue{
		Channel: model.DispatchMessage, Source: model.DispatchDirected, Name: name, CorrelationKey: correlationKey,
		TargetProcessInstanceKey: targetPI, TargetProcessDefKey: targetDef, Reached: uint32(reached),
	}})
}

// PruneEventLog enqueues dropping every event-log row of this partition at or before
// through, a log position (ADR-0472 §4). The cut is a fact, as the event
// feed's is (feed.go): the event carries it and applyToState drops the rows, live and
// on replay alike.
func (p *Processor) PruneEventLog(through uint64) {
	p.queue = append(p.queue, Command{
		ValueType: model.VTFeedRetention,
		Intent:    model.IntentEventLogPruning,
		Value:     inflightValue{feedRetention: model.FeedRetentionValue{Through: through}},
	})
}

func handleEventLogPruning(c *ProcessingContext) {
	c.appendEvent(0, model.VTFeedRetention, model.IntentEventLogPruned, c.cmd.Value)
}
