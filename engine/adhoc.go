package engine

import (
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// --- Ad-hoc subprocesses (ADR-0138) ---
//
// An ad-hoc subprocess is a scope whose contained activities are not sequenced from a start
// event: entering it activates every *entry activity* (a contained node with no incoming flow)
// at once, each an independent token scoped by the container. Contained activities may still be
// wired to each other, so a token flows on inside the scope like anywhere else.
//
// It completes in one of two ways. With no completion condition it is an ordinary subprocess
// scope: it completes when the scope drains (ADR-0074). With one, the condition is re-evaluated
// after each contained activity completes; the first time it holds, the still-running activities
// are cancelled (cancelRemainingInstances, the BPMN default) and the ad-hoc completes — the same
// completion-condition eval plus terminateScope cancel a multi-instance loop uses (ADR-0077).

// adHocSubProcessBehavior runs an ad-hoc subprocess container (ADR-0138). On activation it arms
// the scope's event subprocesses (as an embedded subprocess does) and activates every entry
// activity at once; an ad-hoc with no contained activity is an already-empty scope and completes
// immediately. On completion it drops its local scope and takes its outgoing flow, so to the
// flow containing it an ad-hoc is an ordinary activity.
type adHocSubProcessBehavior struct{}

func (adHocSubProcessBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	entries := cp.AdHocEntries(ei.ElementId)
	if len(entries) == 0 {
		// No contained activity: an already-empty scope. Complete it at once rather than
		// parking a token forever — the same guard a start-event-less subprocess uses.
		c.AppendElementCommand(key, model.IntentCompleting, *ei)
		return
	}
	// An ad-hoc is a scope like any other, so it may host event subprocesses; arm them
	// before its activities run (ADR-0082).
	armEventSubprocesses(c, ei.ProcessInstanceKey, ei.ProcessDefKey, key, cp.EventSubprocesses(ei.ElementId))
	for _, entryID := range entries {
		node := cp.Node(entryID)
		k := c.NewKey()
		c.AppendElementCommand(k, model.IntentActivating, model.ElementInstanceValue{
			ProcessInstanceKey: ei.ProcessInstanceKey,
			ProcessDefKey:      ei.ProcessDefKey,
			ElementId:          entryID,
			FlowScopeKey:       key, // contained activities are scoped by this ad-hoc instance
			BpmnElementType:    uint8(node.Type),
			TokenID:            k,
			SourceFlowId:       -1, // activated on entry, not by a sequence flow
		})
	}
}

func (adHocSubProcessBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	// A scope's locals are dropped when it completes, so only an explicit output mapping
	// escapes — the embedded-subprocess rule (ADR-0074).
	dropLocalScope(c, key)
	completeAndTakeFlows(c, key, ei)
}

// checkAdHocCompletion is the ad-hoc's completion checkpoint (ADR-0138): it runs after a
// contained activity completes, with scopeKey the activity's flow scope. If that scope is an
// ad-hoc carrying a completion condition, the condition is evaluated over the ad-hoc's scope
// chain; the first time it holds, the still-running contained activities are cancelled (unless
// cancelRemainingInstances is false, when they are left to finish) and the ad-hoc completes once
// its scope has drained. A no-op for every element whose scope is not a conditional ad-hoc, so a
// plain ad-hoc keeps the ordinary scope-drain completion.
func checkAdHocCompletion(c *ProcessingContext, scopeKey uint64) {
	if scopeKey == 0 {
		return
	}
	scope := c.GetElementInstance(scopeKey)
	if scope == nil || scope.BpmnElementType != uint8(compiler.TypeAdHocSubProcess) {
		return
	}
	cp := c.process(scope.ProcessDefKey)
	d := cp.AdHoc(cp.Node(scope.ElementId).Detail)
	if d.CompletionCondition == nil {
		return // no predicate: the ad-hoc completes when its scope drains
	}
	if !adHocConditionHolds(c, d.CompletionCondition, scopeKey) {
		return
	}
	if !d.CancelRemaining {
		// cancelRemainingInstances="false": the still-running activities are left to finish,
		// and their own completion drains the scope — the ordinary path completes the ad-hoc
		// then. Doing it here too would schedule a second Completing for the same container
		// (the drain and this checkpoint both fire on that last completion), so this case is
		// deliberately left entirely to the scope-drain.
		return
	}
	// Cancel the activities still running in the ad-hoc (and their jobs), exactly as a
	// multi-instance loop's early exit does, then complete the now-drained scope. The
	// completing activity's own drain already ran before this checkpoint and was a no-op
	// (its siblings were still active), so the ad-hoc completes exactly once.
	terminateScope(c, scope.ProcessInstanceKey, scopeKey)
	completeScope(c, scopeKey)
}

// adHocConditionHolds evaluates an ad-hoc's boolean FEEL completion condition over the scope
// chain starting at the ad-hoc's own scope (ADR-0138) — the same eval a multi-instance
// completion condition and a gateway condition use. An eval error is false (keep running),
// matching FEEL's null-propagating behaviour elsewhere.
func adHocConditionHolds(c *ProcessingContext, cond *expr.Compiled, scopeKey uint64) bool {
	v, err := cond.Eval(bindInputsChain(c, cond.Inputs(), scopeKey))
	return err == nil && expr.IsTrue(v)
}
