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
			return tx.PutProcessInstance(h.Key, &v.process)
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
			return tx.DeleteProcessInstance(h.Key)
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
			return tx.RecordElementStep(v.element.ProcessInstanceKey, h.Timestamp, h.Position, v.element.ElementId)
		case model.IntentCompleted, model.IntentTerminated:
			if err := tx.DeleteElementInstance(h.Key, &v.element); err != nil {
				return err
			}
			return tx.DecrementActiveChildren(v.element.FlowScopeKey)
		}

	case model.VTJob:
		switch h.Intent {
		case model.IntentJobCreated, model.IntentJobAssigned:
			// Assigning re-puts the whole job with its new assignee; the
			// activatable-index entry PutJob rewrites is idempotent, so the task
			// stays open (ADR-0042).
			return tx.PutJob(h.Key, &v.job)
		case model.IntentJobCompleted, model.IntentJobFailed, model.IntentJobCanceled:
			return tx.DeleteJob(h.Key, &v.job)
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
	}
	return nil
}
