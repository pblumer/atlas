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
			return tx.RecordElementVisit(v.element.ProcessDefKey, v.element.ProcessInstanceKey, v.element.ElementId)
		case model.IntentCompleted, model.IntentTerminated:
			if err := tx.DeleteElementInstance(h.Key, &v.element); err != nil {
				return err
			}
			return tx.DecrementActiveChildren(v.element.FlowScopeKey)
		}

	case model.VTJob:
		switch h.Intent {
		case model.IntentJobCreated:
			return tx.PutJob(h.Key, &v.job)
		case model.IntentJobCompleted, model.IntentJobFailed, model.IntentJobCanceled:
			return tx.DeleteJob(h.Key, &v.job)
		}

	case model.VTVariable:
		switch h.Intent {
		case model.IntentVariableCreated, model.IntentVariableUpdated:
			return tx.PutVariable(&v.variable)
		}

	case model.VTTimer:
		switch h.Intent {
		case model.IntentTimerCreated:
			return tx.PutTimer(h.Key, &v.timer)
		case model.IntentTimerTriggered:
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
