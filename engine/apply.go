package engine

import (
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// eventSubTrigger is the element type of an armed event-subprocess trigger (ADR-0082).
// It is excluded from a scope's active-child counter so an armed trigger never keeps
// the scope from completing; it is a real element instance in every other respect.
const eventSubTrigger = uint8(compiler.TypeEventSubProcessStart)

// firstErr returns the first non-nil error of a sequence, so a run of state
// mutations that each already ran can be checked once instead of after every call.
// The arguments are evaluated eagerly — correct in applyToState, where each step must
// be applied regardless of an earlier one's (in practice unreachable) failure.
func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

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
			// One process-instance activation touches several derived indices:
			// the instance record itself, the per-definition active counter and
			// last-activity for the O(1) runtime view / instances summary (ADR-0080/0083),
			// and — for a message-start instance — the per-key live counter a singleton
			// message start gates on (ADR-0094). All are event-driven, so replay rebuilds
			// them (I4/I6). firstErr applies them and reports the first failure.
			err := firstErr(
				tx.PutProcessInstance(h.Key, &v.process),
				tx.IncDefInstanceCount(v.process.ProcessDefKey),
				tx.SetDefLastActivity(v.process.ProcessDefKey, h.Timestamp),
			)
			// A child started by a call activity is indexed under the element instance
			// that started it, so tearing that element down finds its child by lookup
			// rather than by walking every live instance. The link comes off this
			// event's own record, so replay rebuilds the index identically (I4/I6).
			if err == nil && v.process.ParentElementInstanceKey != 0 {
				err = tx.PutChildByParent(v.process.ParentElementInstanceKey, h.Key)
			}
			if err == nil && v.process.CorrelationKey != "" {
				err = tx.IncrementActiveStartKey(v.process.ProcessDefKey, v.process.CorrelationKey)
			}
			return err
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
			// The terminal event is the instance's last, so its position is the
			// instance's highest. Recording it lets history retention prove every event
			// is exported before hard-deleting the instance (ADR-0115). It comes only
			// from the event header, so replay rebuilds it identically (I4/I6).
			hist.CompletedPosition = h.Position
			// A finishing instance moves to the history, drops the active record, and
			// updates the derived counters: the active count down, the finished count up,
			// and last-activity stamped, so the summary reads both in O(1) (ADR-0083).
			err := firstErr(
				tx.PutProcessInstanceHistory(h.Key, &hist),
				tx.DeleteProcessInstance(h.Key, v.process.ProcessDefKey),
				tx.DecDefInstanceCount(v.process.ProcessDefKey),
				tx.IncDefCompletedCount(v.process.ProcessDefKey),
				tx.SetDefLastActivity(v.process.ProcessDefKey, h.Timestamp),
			)
			// The child is finished, so its reverse link goes with its active record —
			// the index holds live children only, which is what the teardown asks it for.
			if err == nil && v.process.ParentElementInstanceKey != 0 {
				err = tx.DeleteChildByParent(v.process.ParentElementInstanceKey, h.Key)
			}
			// A definition-declared history TTL schedules the record's hard delete: index the
			// finished instance by the purge due date the terminal event carries, so retention
			// selects candidates by asking what is due instead of walking the whole history
			// (ADR-0146). Zero means the definition declares no history TTL — nothing to index.
			if err == nil && hist.PurgeDueDate > 0 {
				err = tx.PutHistoryExpiry(hist.PurgeDueDate, h.Key)
			}
			// Releasing a message-start instance re-opens its correlation key so a later
			// message can start a fresh one (ADR-0094).
			if err == nil && v.process.CorrelationKey != "" {
				err = tx.DecrementActiveStartKey(v.process.ProcessDefKey, v.process.CorrelationKey)
			}
			// The instance's root scope tears down: drop any compensable records still held
			// under it so they never leak past the instance (ADR-0103).
			if err == nil {
				err = tx.DeleteCompensablesOfScope(h.Key)
			}
			return err
		case model.IntentPurged:
			// History retention hard-deletes a finished instance: its terminal record and
			// every per-instance history/live family (ADR-0115). Derived only from the
			// event — the instance key and the definition key it carries — so replay
			// reproduces the identical deletion (I4/I6). Deleting an already-absent key is
			// a no-op, so a re-applied purge (replay, or a double-enqueue) is idempotent.
			// The carried value also supplies the purge due date, so the scheduled-expiry
			// entry that nominated this instance is dropped with it (ADR-0146).
			return tx.PurgeInstanceHistory(h.Key, v.process.ProcessDefKey, v.process.PurgeDueDate)
		}

	case model.VTElementInstance:
		switch h.Intent {
		case model.IntentActivated:
			if err := tx.PutElementInstance(h.Key, &v.element); err != nil {
				return err
			}
			if v.element.BpmnElementType != eventSubTrigger {
				if err := tx.IncrementActiveChildren(v.element.FlowScopeKey); err != nil {
					return err
				}
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
			if err := tx.RecordElementReplay(v.element.ProcessInstanceKey, h.Timestamp, h.Position, v.element.ElementId, h.Key, v.element.TokenID, v.element.ParentTokenID, v.element.SourceFlowId, state.ReplayActivated); err != nil {
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
			// Completion and termination are distinct token facts, not one "left the
			// element" fact: a completed element hands its token to a successor, a
			// terminated one (interrupted by a boundary event, torn down with its scope,
			// cancelled) does not — nothing downstream ever activates from it. The replay
			// fold needs to tell them apart, so each gets its own action code (ADR-0136).
			action := state.ReplayCompleted
			if h.Intent == model.IntentTerminated {
				action = state.ReplayTerminated
				// Retain the cancellation as its own counter, per instance and per
				// definition, so the overlay's history can say a token got here and
				// did not go on. Without it "passed through" is visits minus live
				// tokens, which counts a cancelled loser exactly like a winner — and
				// on an event-based gateway that is half of every decided race
				// (ADR-draft-overlay-cancelled-tokens). Derived from the event payload
				// alone, like the visit beside it, so replay rebuilds it (I4).
				if err := tx.RecordElementTermination(v.element.ProcessDefKey, v.element.ProcessInstanceKey, v.element.ElementId); err != nil {
					return err
				}
				if err := tx.IncElementTerminationAgg(v.element.ProcessDefKey, v.element.ElementId); err != nil {
					return err
				}
			}
			if err := tx.RecordElementReplay(v.element.ProcessInstanceKey, h.Timestamp, h.Position, v.element.ElementId, h.Key, v.element.TokenID, v.element.ParentTokenID, v.element.SourceFlowId, action); err != nil {
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
			if v.element.BpmnElementType != eventSubTrigger {
				if err := tx.DecrementActiveChildren(v.element.FlowScopeKey); err != nil {
					return err
				}
			}
			// A cancel end event completing marks its enclosing transaction scope cancelling, so
			// when that scope drains the transaction routes out its cancel boundary rather than
			// completing normally (ADR-0108). Derived from the committed Completed event, so replay
			// rebuilds it identically (I4/I6).
			if v.element.BpmnElementType == uint8(compiler.TypeCancelEndEvent) && h.Intent == model.IntentCompleted {
				if err := tx.SetCanceling(v.element.FlowScopeKey); err != nil {
					return err
				}
			}
			// A subprocess scope tearing down (normal completion or termination) drops any
			// compensable records still held under it — its element key is its children's
			// scope key — so they never leak past the scope (ADR-0103). A transaction is a
			// TypeSubProcess, so its cancelling marker is dropped on the same teardown (ADR-0108).
			if v.element.BpmnElementType == uint8(compiler.TypeSubProcess) {
				if err := tx.DeleteCompensablesOfScope(h.Key); err != nil {
					return err
				}
				if err := tx.DeleteCanceling(h.Key); err != nil {
					return err
				}
			}
			return tx.DecElementToken(v.element.ProcessDefKey, v.element.ElementId)
		}

	case model.VTJob:
		switch h.Intent {
		case model.IntentJobCreated, model.IntentJobAssigned, model.IntentJobFailed,
			model.IntentJobActivated, model.IntentJobTimedOut:
			// Assigning re-puts the job with its new assignee; failing re-puts it with
			// its new (decremented, worker-reported) retry count; activating and timing
			// out re-put it with its lease taken and released (ADR-0007). PutJob's
			// activatable-index write is idempotent and keyed on the job having retries
			// left and nothing holding it, so a still-retryable job stays open while an
			// exhausted, backing-off or leased one parks off the index (ADR-0042/0061).
			if err := tx.PutJob(h.Key, &v.job); err != nil {
				return err
			}
			// Only creation opens a job. A re-put of one already open moves the
			// engine-wide count by nothing (ADR-0142).
			if h.Intent == model.IntentJobCreated {
				return tx.IncOpenJobs()
			}
			return nil
		case model.IntentJobCompleted, model.IntentJobCanceled:
			if err := tx.DeleteJob(h.Key, &v.job); err != nil {
				return err
			}
			return tx.DecOpenJobs()
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
			if err := tx.PutTimer(h.Key, &v.timer); err != nil {
				return err
			}
			return tx.IncPendingTimers()
		case model.IntentTimerTriggered, model.IntentTimerCanceled:
			// Both remove the timer from the due-date index; they differ only in the
			// side effect the command handler runs (a trigger may fire; a cancel does
			// not), which lives outside applyToState (ADR-0051). Either way the timer
			// stops pending, so both move the engine-wide count down (ADR-0142).
			if err := tx.DeleteTimer(h.Key, &v.timer); err != nil {
				return err
			}
			return tx.DecPendingTimers()
		}

	case model.VTMessageSubscription:
		switch h.Intent {
		case model.IntentSubscriptionCreated:
			if err := tx.PutMessageSubscription(&v.subscription); err != nil {
				return err
			}
			return tx.IncMessageSubscriptions()
		case model.IntentSubscriptionCorrelated:
			if err := tx.DeleteMessageSubscription(&v.subscription); err != nil {
				return err
			}
			return tx.DecMessageSubscriptions()
		}

	case model.VTSignal:
		// A signal subscription's lifecycle mirrors a message subscription's: opened
		// when a signal catch activates, retired when a broadcast fires it. Same two
		// intents, a separate column family (ADR-0088).
		switch h.Intent {
		case model.IntentSubscriptionCreated:
			return tx.PutSignalSubscription(&v.signalSub)
		case model.IntentSubscriptionCorrelated:
			return tx.DeleteSignalSubscription(&v.signalSub)
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

	case model.VTVariableAudit:
		if h.Intent == model.IntentVariableAudited {
			// Retain who set a variable from outside the model — an operator override —
			// as append-only audit history (ADR-0098), the "who changed it" analogue of
			// the decision-evaluation record. The record carries everything (actor, scope,
			// name, value); the timestamp and position come from this event's header, so
			// replay rebuilds identical history without re-running the modify command
			// (invariants I4/I6).
			return tx.RecordVariableAudit(h.Timestamp, h.Position, &v.variableAudit)
		}

	case model.VTOperatorAction:
		if h.Intent == model.IntentOperatorActed {
			// Retain who forced a step from outside the model — an operator completing a
			// parked job by hand — as append-only audit history (ADR-0159), the "who did
			// it, and why" analogue of the variable-override record. The record carries
			// everything (actor, reason, element, job); the timestamp and position come
			// from this event's header, so replay rebuilds identical history without
			// re-running the command (invariants I4/I6).
			return tx.RecordOperatorAction(h.Timestamp, h.Position, &v.operatorAct)
		}

	case model.VTProcessMigration:
		if h.Intent == model.IntentMigrated {
			// Rebind a running instance to another deployed version of its process
			// (ADR-0162). Everything the rewrite needs — both definition keys and the
			// whole element mapping — is on the event, so this fold computes nothing and
			// replay reproduces it exactly (invariants I4/I6). It is idempotent: the
			// rewrite is skipped unless the instance is still on the source version, so a
			// re-applied event moves no counter twice.
			return tx.MigrateInstance(&v.migration)
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

	case model.VTCompensable:
		switch h.Intent {
		case model.IntentCompensableRecorded:
			// Retain a completed compensable activity under its scope, keyed by this
			// event's log position so a scope scan yields completion order (ADR-0103).
			// The record carries the scope, the activity, and its handler; the position
			// comes from the header, so replay rebuilds the identical index (I4/I6).
			return tx.RecordCompensable(h.Position, &v.compensable)
		case model.IntentCompensableConsumed:
			// The activity was compensated: drop its record so it compensates at most
			// once. The scope and sequence ride on the event, so replay deletes the
			// identical entry.
			return tx.DeleteCompensable(v.compensable.ScopeKey, v.compensable.Seq)
		}
	}
	return nil
}
