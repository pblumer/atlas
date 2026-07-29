package engine

import "github.com/pblumer/atlas/model"

// applyToState mutates state from a single event record. It is the one place
// state changes from a record, and it runs identically live (in the processor)
// and on recovery (replaying the log) — invariant I4. It must stay deterministic
// and side-effect-free: no time reads, no key generation, no I/O beyond the
// transaction. Timestamps and keys are read from the record, never produced here.
func applyToState(tx *stateTx, h model.RecordHeader, v *inflightValue) error {
	switch h.ValueType {
	case model.VTProcessInstance:
		switch h.Intent {
		case model.IntentActivated:
			// The creation time comes only from this event's header timestamp, so
			// replay rebuilds the identical value — invariants I4/I6. It rides on the
			// active record and is copied into the history record on completion below,
			// so a finished instance still reports when it started.
			v.process.CreatedAt = h.Timestamp
			if err := tx.PutProcessInstance(h.Key, &v.process); err != nil {
				return err
			}
			// Maintain the per-definition active-instance counter so the runtime view
			// reads it in O(1) rather than scanning every instance (ADR-0080).
			if err := tx.IncDefInstanceCount(v.process.ProcessDefKey); err != nil {
				return err
			}
			// A message-start instance carries the correlation key it began with; count
			// it as one live instance for that (definition, key) so a singleton message
			// start can gate on it (ADR-0082). Event-driven, so replay rebuilds it (I4).
			if v.process.CorrelationKey != "" {
				return tx.IncrementActiveStartKey(v.process.ProcessDefKey, v.process.CorrelationKey)
			}
			return nil
		case model.IntentCompleted, model.IntentTerminated:
			// Retain a history record so operators can inspect finished
			// instances, then drop the active record. The terminal state and
			// completion time come only from this event (its intent and header
			// timestamp), so replay rebuilds identical history — invariant I4,
			// ADR-0017.
			hist := v.process
			if h.Intent == model.IntentTerminated {
				hist.State = model.PITerminated
			} else {
				hist.State = model.PICompleted
			}
			hist.CompletedAt = h.Timestamp
			if err := tx.PutProcessInstanceHistory(h.Key, &hist); err != nil {
				return err
			}
			if err := tx.DeleteProcessInstance(h.Key); err != nil {
				return err
			}
			if err := tx.DecDefInstanceCount(v.process.ProcessDefKey); err != nil {
				return err
			}
			// Releasing a message-start instance re-opens its correlation key so a
			// later message can start a fresh one (ADR-0082). Mirrors the increment on
			// activation, keyed by the same fields the still-populated value carries.
			if v.process.CorrelationKey != "" {
				return tx.DecrementActiveStartKey(v.process.ProcessDefKey, v.process.CorrelationKey)
			}
			return nil
		}

	case model.VTElementInstance:
		switch h.Intent {
		case model.IntentActivated:
			if err := tx.PutElementInstance(h.Key, &v.element); err != nil {
				return err
			}
			if err := tx.IncrementActiveChildren(v.element.FlowScopeKey); err != nil {
				return err
			}
			// Retain a token-visit count per element so the Operations overlay can
			// show where tokens have flowed even after instances finish (ADR-0022).
			// Derived only from the event payload, so replay rebuilds it (I4).
			if err := tx.RecordElementVisit(v.element.ProcessDefKey, v.element.ProcessInstanceKey, v.element.ElementId); err != nil {
				return err
			}
			// Retain the same activation as an ordered, timestamped step so a single
			// instance can be replayed step by step (ADR-0046). The order comes from
			// the event header's timestamp and position, so replay rebuilds an
			// identically-ordered trail (I4).
			if err := tx.RecordElementStep(v.element.ProcessInstanceKey, h.Timestamp, h.Position, v.element.ElementId); err != nil {
				return err
			}
			if err := tx.RecordElementReplay(v.element.ProcessInstanceKey, h.Timestamp, h.Position, v.element.ElementId, h.Key, v.element.TokenID, v.element.ParentTokenID, v.element.SourceFlowId, 1); err != nil {
				return err
			}
			// Maintain the per-(definition, element) live-token counter and the
			// cumulative-visit counter so the runtime overlay reads them in
			// O(elements) rather than scanning every instance (ADR-0080).
			if err := tx.IncElementToken(v.element.ProcessDefKey, v.element.ElementId); err != nil {
				return err
			}
			return tx.IncElementVisitAgg(v.element.ProcessDefKey, v.element.ElementId)
		case model.IntentCompleted, model.IntentTerminated:
			if err := tx.RecordElementReplay(v.element.ProcessInstanceKey, h.Timestamp, h.Position, v.element.ElementId, h.Key, v.element.TokenID, v.element.ParentTokenID, v.element.SourceFlowId, 2); err != nil {
				return err
			}
			// Terminating an element clears any incident it carried (a stuck job's,
			// ADR-0061); the delete is idempotent, so it is a no-op for the common
			// element with none.
			if err := tx.DeleteIncident(h.Key); err != nil {
				return err
			}
			if err := tx.DeleteElementInstance(h.Key, &v.element); err != nil {
				return err
			}
			if err := tx.DecrementActiveChildren(v.element.FlowScopeKey); err != nil {
				return err
			}
			return tx.DecElementToken(v.element.ProcessDefKey, v.element.ElementId)
		}

	case model.VTJob:
		switch h.Intent {
		case model.IntentJobCreated, model.IntentJobAssigned, model.IntentJobFailed:
			// Assigning re-puts the job with its new assignee; failing re-puts it with
			// its new (decremented, worker-reported) retry count. PutJob's activatable-
			// index write is idempotent and keyed on Retries > 0, so a still-retryable
			// job stays open while an exhausted one parks off the index (ADR-0042/0061).
			return tx.PutJob(h.Key, &v.job)
		case model.IntentJobCompleted, model.IntentJobCanceled:
			return tx.DeleteJob(h.Key, &v.job)
		}

	case model.VTIncident:
		switch h.Intent {
		case model.IntentIncidentCreated:
			return tx.PutIncident(&v.incident)
		case model.IntentIncidentResolved:
			return tx.DeleteIncident(v.incident.ElementInstanceKey)
		}

	case model.VTVariable:
		switch h.Intent {
		case model.IntentVariableCreated, model.IntentVariableUpdated:
			if err := tx.PutVariable(&v.variable); err != nil {
				return err
			}
			// Retain the change as an ordered, timestamped snapshot so a single
			// instance's replay can show the variable values as of each step
			// (ADR-0048). Derived only from the event header (timestamp/position)
			// and the variable value, so replay rebuilds it identically (I4).
			return tx.RecordVariableSnapshot(h.Timestamp, h.Position, &v.variable)
		case model.IntentVariableDeleted:
			// Dropping an activity-local scope on completion (ADR-0068). The delete is
			// idempotent and carries no snapshot: the local was scratch state, so its
			// removal is not part of the instance's variable timeline.
			return tx.DeleteVariable(v.variable.ScopeKey, v.variable.Name)
		}

	case model.VTDataObject:
		switch h.Intent {
		case model.IntentDataObjectCreated, model.IntentDataObjectStateChanged:
			if err := tx.PutDataObject(&v.dataObject); err != nil {
				return err
			}
			// Retain the change as an ordered, timestamped snapshot so the data
			// object's state history and provenance rebuild on replay — the data
			// analogue of the variable snapshot (ADR-0053, mirroring ADR-0048).
			// Derived only from the event (header timestamp/position and the value),
			// so replay rebuilds it identically (invariant I4).
			return tx.RecordDataObjectSnapshot(h.Timestamp, h.Position, &v.dataObject)
		}

	case model.VTTimer:
		switch h.Intent {
		case model.IntentTimerCreated:
			return tx.PutTimer(h.Key, &v.timer)
		case model.IntentTimerTriggered, model.IntentTimerCanceled:
			// Both remove the timer from the due-date index; they differ only in the
			// side effect the command handler runs (a trigger may fire; a cancel does
			// not), which lives outside applyToState (ADR-0051).
			return tx.DeleteTimer(h.Key, &v.timer)
		}

	case model.VTMessageSubscription:
		switch h.Intent {
		case model.IntentSubscriptionCreated:
			return tx.PutMessageSubscription(&v.subscription)
		case model.IntentSubscriptionCorrelated:
			return tx.DeleteMessageSubscription(&v.subscription)
		}

	case model.VTMessageFlow:
		if h.Intent == model.IntentMessagePublished {
			// Retain the delivered message flow so the Operations collaboration view
			// can replay it. The record carries everything (receiver, message, key);
			// the timestamp and position come from this event's header, so replay
			// rebuilds identical history — invariant I4, ADR-0038.
			return tx.RecordMessageFlow(h.Timestamp, h.Position, &v.messageFlow)
		}

	case model.VTInboundDelivery:
		if h.Intent == model.IntentInboundDeliveryApplied {
			// Advance the external source's inbound high-water mark (ADR-0075). The
			// sequence comes only from the event payload, so replay rebuilds the
			// identical mark (invariant I4) and a duplicate publish that a replay
			// re-drives is skipped by handleMessagePublished's guard.
			return tx.PutInboundHighWater(v.inbound.SourceID, v.inbound.SourceSeq)
		}

	case model.VTDecisionEvaluation:
		if h.Intent == model.IntentDecisionEvaluated {
			// Retain how a business rule task's decision was made — its inputs, outputs
			// and trace — so an operator can inspect it live and after the fact
			// (ADR-0066). The worker already evaluated the decision off the processor
			// goroutine and froze the result onto the completion command; here we only
			// record what the event carries (header timestamp/position plus the frozen
			// evaluation), so replay rebuilds it without re-evaluating — invariants
			// I4/I6.
			return tx.RecordDecisionEvaluation(h.Timestamp, h.Position, &v.decisionEval)
		}
	}
	return nil
}
