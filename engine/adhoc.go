package engine

import (
	"fmt"
	"strings"

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
	if d := cp.AdHoc(cp.Node(ei.ElementId).Detail); d.AgentDriven {
		// An agent-driven container activates nothing on entry: which contained
		// activity runs is the model's choice, and it has not been asked yet
		// (ADR-0253). It arms its event subprocesses like any scope, then parks on
		// one job whose completion carries the first round's tool calls. The
		// compiler guarantees at least one tool, so there is no empty-scope case.
		armEventSubprocesses(c, ei.ProcessInstanceKey, ei.ProcessDefKey, key, cp.EventSubprocesses(ei.ElementId))
		createAgentRoundJob(c, key, ei)
		return
	}
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
		// Entering the ad-hoc activates a contained multi-instance activity as its body,
		// exactly as taking a flow into one does — miRoleOf is where that rule lives.
		c.AppendElementCommand(k, model.IntentActivating, model.ElementInstanceValue{
			ProcessInstanceKey: ei.ProcessInstanceKey,
			ProcessDefKey:      ei.ProcessDefKey,
			ElementId:          entryID,
			FlowScopeKey:       key, // contained activities are scoped by this ad-hoc instance
			BpmnElementType:    uint8(node.Type),
			MultiInstance:      miRoleOf(node),
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
	// A root-scope element's scope key is its process instance, which is not an element
	// instance, so the lookup below returns nil and this falls straight through.
	scope := c.GetElementInstance(scopeKey)
	if scope == nil || scope.BpmnElementType != uint8(compiler.TypeAdHocSubProcess) {
		return
	}
	cp := c.process(scope.ProcessDefKey)
	d := cp.AdHoc(cp.Node(scope.ElementId).Detail)
	if d.AgentDriven {
		// An agent-driven container's fate is decided in one place only — the drain
		// funnel, where startNextAgentRound weighs another round against the completion
		// condition (ADR-0253). Completing it here as well would complete it twice: unlike
		// a plain ad-hoc, this checkpoint's drain is not a no-op, because the round that
		// just ended may have had a single tool in it. What is left here is the other half
		// of the condition's job — cutting a round short while its other tools still run,
		// whose cancellation then drains the scope into that one funnel.
		if d.CompletionCondition != nil && c.ActiveChildren(scopeKey) != 0 &&
			adHocConditionHolds(c, d.CompletionCondition, scopeKey) {
			terminateScope(c, scope.ProcessInstanceKey, scopeKey)
		}
		return
	}
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

// --- Agent-driven rounds (ADR-0253) ---

// agentRoundRetries is how many attempts a round's job gets before its failure becomes an
// incident. A round is one model call: worth retrying a rate limit or a dropped connection,
// not worth retrying forever.
const agentRoundRetries int32 = 3

// AgentCallIdVariable is the variable an activated tool carries in its own scope naming the
// call it answers, so a result can be paired back to the call that asked for it.
const AgentCallIdVariable = "toolCallId"

// createAgentRoundJob parks the container on one job: the round's decision (ADR-0253). The
// job sits on the *container's* element instance, not on a contained activity, because what
// it decides is which contained activities there will be.
func createAgentRoundJob(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	jobKey := c.NewKey()
	c.AppendJobEvent(jobKey, model.IntentJobCreated, model.JobValue{
		ProcessInstanceKey: ei.ProcessInstanceKey,
		ElementInstanceKey: key,
		JobType:            compiler.AgentJobTypeIndex,
		Retries:            agentRoundRetries,
	})
	c.NotifyJobAvailable(compiler.AgentJobTypeIndex)
}

// driveAgentRound is the completion checkpoint of an agent-driven ad-hoc (ADR-0253): the
// round's job has come back, and what it carries decides whether the container runs another
// round or finishes. It reports whether it consumed the completion — true means the caller
// must not drive the container to Completing, because the round continues.
//
// Three outcomes, in the order they are checked. Not an agent-driven container: not ours.
// Tool calls: activate exactly those, and the container stays Activated. No tool calls: the
// model answered instead of choosing, so the loop ends and the ordinary completion runs.
func driveAgentRound(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) bool {
	if ei.BpmnElementType != uint8(compiler.TypeAdHocSubProcess) {
		return false
	}
	cp := c.process(ei.ProcessDefKey)
	d := cp.AdHoc(cp.Node(ei.ElementId).Detail)
	if !d.AgentDriven || len(c.cmd.ToolCalls) == 0 {
		return false
	}
	for _, call := range c.cmd.ToolCalls {
		tool, ok := agentToolByName(cp, d, call.Tool)
		if !ok {
			// The model asked for something the deploy-time index does not have, which
			// means the worker offered it something it should not have. Stopping visibly
			// beats silently skipping a step the agent believes it took.
			c.AppendIncidentEvent(model.IntentIncidentCreated, model.IncidentValue{
				ProcessInstanceKey: ei.ProcessInstanceKey,
				ElementInstanceKey: key,
				ElementId:          ei.ElementId,
				RaisedAt:           c.Now(),
				Message: fmt.Sprintf("the agent asked for the tool %q, which %q does not offer; its tools are %s",
					call.Tool, cp.ElementBpmnId(ei.ElementId), agentToolNames(cp, d)),
			})
			return true
		}
		activateAgentTool(c, key, ei, cp, tool, call)
	}
	return true
}

// agentToolByName resolves a tool name — the contained activity's BPMN id, which is what the
// model was told — against the container's deploy-time tool index.
func agentToolByName(cp *compiler.CompiledProcess, d *compiler.AdHocDetail, name string) (compiler.AgentTool, bool) {
	for _, tool := range d.Tools {
		if cp.ElementBpmnId(tool.Element) == name {
			return tool, true
		}
	}
	return compiler.AgentTool{}, false
}

// agentToolNames lists what the container does offer, for an incident that has to be
// actionable without opening the model.
func agentToolNames(cp *compiler.CompiledProcess, d *compiler.AdHocDetail) string {
	names := make([]string, 0, len(d.Tools))
	for _, tool := range d.Tools {
		names = append(names, cp.ElementBpmnId(tool.Element))
	}
	return strings.Join(names, ", ")
}

// activateAgentTool activates one chosen contained activity, scoped by the container, with
// the call's arguments bound in its own scope. It is the entry activation of ADR-0138
// narrowed to one named activity: same scoping, same miRoleOf rule, same "activated on
// entry, not by a flow" marker. Two calls naming the same activity therefore produce two
// independent instances — the ad-hoc's zero-or-more semantics doing its job.
//
// The arguments are written after the activation command exactly as a multi-instance
// iteration binds its loopCounter and item: the scope key is known before the element
// exists, and the variable events land in it.
func activateAgentTool(c *ProcessingContext, containerKey uint64, container *model.ElementInstanceValue,
	cp *compiler.CompiledProcess, tool compiler.AgentTool, call model.ToolCall) {
	node := cp.Node(tool.Element)
	k := c.NewKey()
	c.AppendElementCommand(k, model.IntentActivating, model.ElementInstanceValue{
		ProcessInstanceKey: container.ProcessInstanceKey,
		ProcessDefKey:      container.ProcessDefKey,
		ElementId:          tool.Element,
		FlowScopeKey:       containerKey,
		BpmnElementType:    uint8(node.Type),
		MultiInstance:      miRoleOf(node),
		TokenID:            k,
		ParentTokenID:      container.TokenID,
		SourceFlowId:       -1, // chosen by the agent, not reached by a sequence flow
	})
	if call.CallId != "" {
		c.AppendVariableEvent(model.IntentVariableCreated, model.VariableValue{
			ScopeKey: k, Name: AgentCallIdVariable, Kind: model.VarString, Text: call.CallId,
		})
	}
	for _, arg := range call.Arguments {
		arg.ScopeKey = k
		c.AppendVariableEvent(model.IntentVariableCreated, arg)
	}
}

// startNextAgentRound is the round boundary of an agent-driven ad-hoc (ADR-0253). It runs
// when the container's scope has drained — every tool the last round asked for has finished
// — and reports whether it took the container over. True means another round was asked for
// and the caller must not complete the container.
//
// The model's own bound wins first: a completion condition that holds ends the loop here,
// which is also what keeps checkAdHocCompletion's early exit working — it drives the same
// completeScope, and re-evaluating the condition is what stops this from undoing it.
func startNextAgentRound(c *ProcessingContext, scope uint64, ei *model.ElementInstanceValue) bool {
	if ei.BpmnElementType != uint8(compiler.TypeAdHocSubProcess) {
		return false
	}
	cp := c.process(ei.ProcessDefKey)
	d := cp.AdHoc(cp.Node(ei.ElementId).Detail)
	if !d.AgentDriven {
		return false
	}
	if d.CompletionCondition != nil && adHocConditionHolds(c, d.CompletionCondition, scope) {
		return false // the model said how far this goes, and it has gone that far
	}
	createAgentRoundJob(c, scope, ei)
	return true
}

// collectAgentToolResult appends one finished tool's result to its container's result
// collection (ADR-0253) — the multi-instance outputCollection/outputElement pair applied to
// a round rather than an iteration. It is evaluated over the tool's own scope, so the
// expression reads what that tool produced, and appended rather than indexed because a
// round's calls are not known in advance the way a loop's iterations are.
//
// The collection lives on the container's scope, which is where the next round's job reads
// it from and where the agent sees what its earlier calls returned.
func collectAgentToolResult(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	scope := c.GetElementInstance(ei.FlowScopeKey)
	if scope == nil || scope.BpmnElementType != uint8(compiler.TypeAdHocSubProcess) {
		return
	}
	cp := c.process(scope.ProcessDefKey)
	d := cp.AdHoc(cp.Node(scope.ElementId).Detail)
	if !d.AgentDriven || d.ResultCollection < 0 {
		return
	}
	val := expr.Null
	if d.ResultElement != nil {
		// An expression that cannot be evaluated contributes null rather than nothing: the
		// agent is told the call produced no usable result, which it can act on, instead of
		// silently seeing one fewer entry than calls it made.
		if v, err := d.ResultElement.Eval(bindInputsChain(c, d.ResultElement.Inputs(), key)); err == nil {
			val = v
		}
	}
	name := cp.Intern(d.ResultCollection)
	writeList(c, ei.FlowScopeKey, name, append(readList(c, ei.FlowScopeKey, name), val))
}
