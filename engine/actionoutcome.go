package engine

import "github.com/pblumer/atlas/model"

// How an action asked of an order position ended (ADR-0429 §3).
//
// The process that carries out an action reports its ending at least once — a send
// task retried after a crash, a REST reporter that timed out waiting for the answer
// — so the report is idempotent per order, position and command id: a repeated
// identical report writes nothing and answers with the first, and a report of a
// different ending for the same command is refused. Both are decided here, on the
// command path, against state this same pipeline built, so the family never holds
// two endings of one command.

// OutcomeAnswer is what an outcome report did.
type OutcomeAnswer uint8

const (
	// OutcomeNotProcessed is the zero value: the command never ran.
	OutcomeNotProcessed OutcomeAnswer = iota
	// OutcomeRecorded: the outcome is now a fact.
	OutcomeRecorded
	// OutcomeReplayed: the same outcome was reported before; nothing was written.
	OutcomeReplayed
	// OutcomeConflict: the command already ended differently; nothing was written.
	OutcomeConflict
	// OutcomeInvalid: the report names no command or no ending.
	OutcomeInvalid
)

// OutcomeResult is written through Command.Reported. Recorded is the outcome as it
// stands after the command: the one written now, or the one already there.
type OutcomeResult struct {
	Answer   OutcomeAnswer
	Recorded model.ActionOutcomeValue
}

// ReportActionOutcome enqueues one report of how an action ended. The moment
// travels in the value, read by the caller at command time and frozen into the
// event (I4/I6). The answer is written to *result once RunUntilIdle returned.
func (p *Processor) ReportActionOutcome(v model.ActionOutcomeValue, result *OutcomeResult) {
	p.queue = append(p.queue, Command{
		ValueType: model.VTActionOutcome,
		Intent:    model.IntentActionReporting,
		Value:     inflightValue{actionOutcome: v},
		Reported:  result,
	})
}

// GrantEntitlementWithOutcome is GrantEntitlement for a grant that ends a
// provision: the grant and the provision's outcome are appended by one handler, so
// one fsync commits both or neither (I2).
func (p *Processor) GrantEntitlementWithOutcome(v model.EntitlementValue, outcome model.ActionOutcomeValue) {
	p.queue = append(p.queue, Command{
		ValueType: model.VTEntitlement,
		Intent:    model.IntentEntitlementGranted,
		Value:     inflightValue{entitlement: v, actionOutcome: outcome},
	})
}

// RevokeEntitlementWithOutcome is RevokeEntitlement for a revocation that ends a
// return, with the return's outcome in the same batch.
func (p *Processor) RevokeEntitlementWithOutcome(principal, itemID string, at int64,
	reason model.HoldEnd, by string, outcome model.ActionOutcomeValue) {

	p.queue = append(p.queue, Command{
		ValueType: model.VTEntitlementHistory,
		Intent:    model.IntentEntitlementRevoked,
		Value: inflightValue{entitlementEnd: model.EntitlementHistoryValue{
			Principal: principal, ItemID: itemID,
			EndedAt: at, EndedReason: reason, EndedBy: by,
		}, actionOutcome: outcome},
	})
}

// CompleteJobWithOutcome completes a shop send task's job carrying how the action
// its instance carries ended (ADR-0429 §4). The handler that produced it runs off the
// loop, so it does not write the outcome; handleJobCompleted appends it in the batch
// that completes the job, so the step and the fact commit together (I2). An outcome
// the command already has with a different ending is not written: the first stands.
func (p *Processor) CompleteJobWithOutcome(jobKey uint64, outcome model.ActionOutcomeValue, outputs ...model.VariableValue) {
	p.queue = append(p.queue, Command{
		Key:       jobKey,
		ValueType: model.VTJob,
		Intent:    model.IntentJobCompleted,
		StartVars: outputs,
		Value:     inflightValue{actionOutcome: outcome},
	})
}

func handleActionReporting(c *ProcessingContext) {
	res := c.cmd.Reported
	answer, recorded := appendActionOutcome(c, c.cmd.Value.actionOutcome)
	if res != nil {
		res.Answer, res.Recorded = answer, recorded
	}
}

// appendActionOutcome writes v unless its command already ended, and says which.
// Shared by the report and by the grant and revocation that carry an outcome, so an
// outcome reaches the log one way whoever states it.
func appendActionOutcome(c *ProcessingContext, v model.ActionOutcomeValue) (OutcomeAnswer, model.ActionOutcomeValue) {
	if !v.Valid() {
		return OutcomeInvalid, v
	}
	prior, found, err := c.tx.ActionOutcome(v.OrderID, v.Position, v.CommandID)
	if err != nil {
		c.p.fail(err)
		return OutcomeNotProcessed, v
	}
	if found {
		if prior.Outcome == v.Outcome {
			return OutcomeReplayed, *prior
		}
		return OutcomeConflict, *prior
	}
	c.appendEvent(0, model.VTActionOutcome, model.IntentActionCompleted, inflightValue{actionOutcome: v})
	return OutcomeRecorded, v
}
