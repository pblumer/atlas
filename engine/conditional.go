package engine

import (
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// --- Conditional events (ADR-0137) ---
//
// A conditional event fires when a boolean FEEL condition over the process's variables becomes
// true. It arms inert — opening no subscription — and is re-evaluated whenever a variable it
// reads changes: every committed variable write marks its instance dirty (AppendVariableEvent),
// and the batch loop schedules a transient ConditionRecheck follow-up that evaluates the
// instance's armed conditionals and drives those now true to Completing. This reuses the
// gateway-condition eval (bindInputsChain over the scope chain) and the inert-armed-catch fire
// pattern (AppendElementCommand(IntentCompleting)) from errors/escalations.

// conditionHolds reports whether a conditional event's compiled FEEL condition evaluates true
// over the scope chain starting at scopeKey (ADR-0137) — the same eval a gateway condition
// uses (selectExclusiveFlow). A nil condition or an eval error is false (never fires).
func conditionHolds(c *ProcessingContext, cond *expr.Compiled, scopeKey uint64) bool {
	if cond == nil {
		return false
	}
	v, err := cond.Eval(bindInputsChain(c, cond.Inputs(), scopeKey))
	return err == nil && expr.IsTrue(v)
}

// handleConditionRecheck re-evaluates a process instance's armed conditional events after a
// variable in the instance changed (ADR-0137). It is a transient command-only handler — never
// persisted; the fire is the Completing→Completed chain — scheduled as a follow-up by the batch
// loop when variables are written, so it runs live only and never on replay (I6).
func handleConditionRecheck(c *ProcessingContext) {
	recheckConditionals(c, c.cmd.Key)
}

// recheckConditionals scans an instance's live element instances and fires each armed
// conditional catch/boundary whose condition now holds (ADR-0137). A conditional evaluates over
// its own flow scope, so a boundary inside a subprocess reads that subprocess's variables.
func recheckConditionals(c *ProcessingContext, piKey uint64) {
	c.ForEachElementInstance(piKey, func(elKey uint64) {
		ei := c.GetElementInstance(elKey)
		if ei == nil {
			return
		}
		cond := armedConditionOf(c, ei)
		if cond != nil && conditionHolds(c, cond, ei.FlowScopeKey) {
			c.AppendElementCommand(elKey, model.IntentCompleting, *ei)
		}
	})
}

// armedConditionOf returns the compiled condition an element instance waits on if it is an
// armed conditional catch or a conditional boundary, or nil otherwise (ADR-0137).
func armedConditionOf(c *ProcessingContext, ei *model.ElementInstanceValue) *expr.Compiled {
	cp := c.process(ei.ProcessDefKey)
	node := cp.Node(ei.ElementId)
	switch compiler.BpmnType(ei.BpmnElementType) {
	case compiler.TypeConditionalCatchEvent:
		return cp.Conditional(node.Detail).Condition
	case compiler.TypeBoundaryEvent:
		if d := cp.BoundaryEvent(node.Detail); d.Kind == compiler.BoundaryConditional {
			return d.Condition
		}
	case compiler.TypeEventSubProcessStart:
		// A conditional event subprocess trigger arms inert and is found by the same
		// variable-change re-check as a conditional boundary (ADR-0137).
		if d := cp.EventSubProcess(node.EventSub); d.Kind == compiler.BoundaryConditional {
			return d.Condition
		}
	}
	return nil
}

// conditionalCatchBehavior: a conditional intermediate catch event (ADR-0137). It arms inert —
// opens no subscription — and waits until its FEEL condition becomes true. OnActivated
// self-evaluates: if the condition already holds when the token arrives, it fires at once
// (passing straight through); otherwise it stays Activated until a variable-change re-check
// drives it to Completing. OnCompleting flows on.
type conditionalCatchBehavior struct{}

func (conditionalCatchBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	cond := cp.Conditional(cp.Node(ei.ElementId).Detail).Condition
	if conditionHolds(c, cond, ei.FlowScopeKey) {
		c.AppendElementCommand(key, model.IntentCompleting, *ei)
	}
	// else stay Activated, waiting for a re-check to find the condition true.
}

func (conditionalCatchBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	completeAndTakeFlows(c, key, ei)
}
