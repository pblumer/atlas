package engine

import (
	"strconv"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// bpmnBehavior is the per-element-type logic invoked at lifecycle transitions.
// The element instance lifecycle (Activating → Activated → Completing →
// Completed) is shared; the behavior fills in what each element type does.
type bpmnBehavior interface {
	// OnActivated runs after the element's Activated event. The element may do
	// work (create a job), finish immediately (events), or wait.
	OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue)
	// OnCompleting runs when the element is told to complete. By default it
	// emits Completed and takes the outgoing flows.
	OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue)
}

// handlerKey packs a (ValueType, Intent) pair into a dispatch key.
func handlerKey(vt model.ValueType, intent model.Intent) uint16 {
	return uint16(vt)<<8 | uint16(intent)
}

func (p *Processor) registerHandlers() {
	p.handlers = map[uint16]func(*ProcessingContext){
		handlerKey(model.VTProcessInstance, model.IntentActivating):  handleProcessInstanceActivating,
		handlerKey(model.VTProcessInstance, model.IntentTerminating): handleProcessInstanceTerminating,
		handlerKey(model.VTElementInstance, model.IntentActivating):  handleElementActivating,
		handlerKey(model.VTElementInstance, model.IntentCompleting):  handleElementCompleting,
		handlerKey(model.VTJob, model.IntentJobCompleted):            handleJobCompleted,
		handlerKey(model.VTTimer, model.IntentTimerTriggered):        handleTimerTriggered,
		handlerKey(model.VTMessage, model.IntentMessagePublished):    handleMessagePublished,
	}
}

func (p *Processor) registerBehaviors() {
	p.behaviors[compiler.TypeStartEvent] = startEventBehavior{}
	p.behaviors[compiler.TypeEndEvent] = endEventBehavior{}
	p.behaviors[compiler.TypeServiceTask] = serviceTaskBehavior{}
	p.behaviors[compiler.TypeScriptTask] = scriptTaskBehavior{}
	p.behaviors[compiler.TypeBusinessRuleTask] = businessRuleTaskBehavior{}
	p.behaviors[compiler.TypeConnectorTask] = connectorTaskBehavior{}
	p.behaviors[compiler.TypeUserTask] = userTaskBehavior{}
	p.behaviors[compiler.TypeBoundaryEvent] = boundaryEventBehavior{}
	p.behaviors[compiler.TypeExclusiveGateway] = exclusiveGatewayBehavior{}
	p.behaviors[compiler.TypeTimerCatchEvent] = timerCatchEventBehavior{}
	p.behaviors[compiler.TypeMessageCatchEvent] = messageCatchEventBehavior{}
	p.behaviors[compiler.TypeMessageThrowEvent] = messageThrowEventBehavior{}
	p.behaviors[compiler.TypeTask] = passThroughBehavior{}
	p.behaviors[compiler.TypeParallelGateway] = parallelGatewayBehavior{}
	p.behaviors[compiler.TypeInclusiveGateway] = inclusiveGatewayBehavior{}
	// A message start event is a plain entry point once instantiated: it flows
	// straight on like a none start (ADR-0035). What makes it a start is the
	// deploy-time subscription (see Deploy), not a distinct runtime behavior.
	p.behaviors[compiler.TypeMessageStartEvent] = startEventBehavior{}
}

// --- command handlers ---

// handleProcessInstanceActivating creates the process instance and activates
// each start event.
func handleProcessInstanceActivating(c *ProcessingContext) {
	defKey := c.cmd.Value.process.ProcessDefKey
	piKey := c.NewKey()
	c.AppendProcessInstanceEvent(piKey, model.IntentActivated, model.ProcessInstanceValue{ProcessDefKey: defKey})

	// Seed the instance's start variables under its scope before any element runs.
	for i := range c.cmd.StartVars {
		v := c.cmd.StartVars[i]
		v.ScopeKey = piKey
		c.AppendVariableEvent(model.IntentVariableCreated, v)
	}

	cp := c.process(defKey)
	for _, startID := range cp.StartEvents() {
		node := cp.Node(startID)
		c.AppendElementCommand(c.NewKey(), model.IntentActivating, model.ElementInstanceValue{
			ProcessInstanceKey: piKey,
			ProcessDefKey:      defKey,
			ElementId:          startID,
			FlowScopeKey:       piKey, // root elements are scoped by the instance
			BpmnElementType:    uint8(node.Type),
		})
	}
}

// handleProcessInstanceTerminating cancels a running instance: it terminates
// every active element instance (each Terminated event deletes the element and
// decrements its scope's child counter) and then records the instance itself as
// terminated, moving it to the history index. A waiting timer/subscription/job is
// left to self-retire when it next fires (it finds no element instance and does
// nothing). Terminating an instance that is already gone is a no-op.
func handleProcessInstanceTerminating(c *ProcessingContext) {
	piKey := c.cmd.Key
	pi := c.GetProcessInstance(piKey)
	if pi == nil {
		return
	}
	c.ForEachElementInstance(piKey, func(elKey uint64) {
		if ei := c.GetElementInstance(elKey); ei != nil {
			c.AppendElementEvent(elKey, model.IntentTerminated, *ei)
		}
	})
	c.AppendProcessInstanceEvent(piKey, model.IntentTerminated, *pi)
}

// handleElementActivating emits the Activated lifecycle event, runs the
// element-type behavior, then arms any boundary events attached to this element
// (a no-op for a node with none).
func handleElementActivating(c *ProcessingContext) {
	ei := &c.cmd.Value.element
	c.AppendElementEvent(c.cmd.Key, model.IntentActivated, *ei)
	c.p.behavior(ei.BpmnElementType).OnActivated(c, c.cmd.Key, ei)
	armBoundaryEvents(c, c.cmd.Key, ei)
}

// handleElementCompleting runs the element-type completion behavior, then — if
// this element hosts boundary events — disarms any still armed, now that it has
// completed normally (their timers/subscriptions self-retire).
func handleElementCompleting(c *ProcessingContext) {
	ei := &c.cmd.Value.element
	c.p.behavior(ei.BpmnElementType).OnCompleting(c, c.cmd.Key, ei)
	if c.process(ei.ProcessDefKey).Node(ei.ElementId).BoundaryCount > 0 {
		disarmBoundaryEvents(c, c.cmd.Key, ei.ProcessInstanceKey)
	}
}

// handleJobCompleted retires the job and tells its element to complete.
func handleJobCompleted(c *ProcessingContext) {
	job := c.GetJob(c.cmd.Key)
	if job == nil {
		return // already gone or never existed; nothing to do
	}
	c.AppendJobEvent(c.cmd.Key, model.IntentJobCompleted, *job)

	if ei := c.GetElementInstance(job.ElementInstanceKey); ei != nil {
		c.AppendElementCommand(job.ElementInstanceKey, model.IntentCompleting, *ei)
	}
}

// handleTimerTriggered fires a due timer: it retires the timer and tells its
// waiting element instance to complete. The command carries the timer value
// (supplied by TriggerDueTimers), so no extra read is needed.
func handleTimerTriggered(c *ProcessingContext) {
	timer := c.cmd.Value.timer
	c.AppendTimerEvent(c.cmd.Key, model.IntentTimerTriggered, timer)
	if ei := c.GetElementInstance(timer.ElementInstanceKey); ei != nil {
		c.AppendElementCommand(timer.ElementInstanceKey, model.IntentCompleting, *ei)
	}
}

// handleMessagePublished correlates an externally published message (from the
// HTTP API) against the open subscriptions. The command carries the message name
// and correlation key (in its subscription payload) and any payload variables (in
// StartVars); correlation is the same path a message throw event uses.
func handleMessagePublished(c *ProcessingContext) {
	pub := c.cmd.Value.subscription
	correlateMessage(c, pub.MessageName, pub.CorrelationKey, c.cmd.StartVars)
}

// behavior resolves the behavior for a BPMN element type.
func (p *Processor) behavior(bpmnType uint8) bpmnBehavior {
	return p.behaviors[bpmnType]
}

// --- element behaviors ---

// completeAndTakeFlows is the default OnCompleting: emit Completed, then
// activate the targets of every outgoing flow.
func completeAndTakeFlows(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	c.AppendElementEvent(key, model.IntentCompleted, *ei)
	takeOutgoingFlows(c, ei)
}

// activateElement schedules activation of a fresh element instance on targetId,
// scoped like ei. It is the single "take a flow" primitive the flow-taking
// behaviors share.
func activateElement(c *ProcessingContext, ei *model.ElementInstanceValue, targetId int32) {
	target := c.process(ei.ProcessDefKey).Node(targetId)
	c.AppendElementCommand(c.NewKey(), model.IntentActivating, model.ElementInstanceValue{
		ProcessInstanceKey: ei.ProcessInstanceKey,
		ProcessDefKey:      ei.ProcessDefKey,
		ElementId:          targetId,
		FlowScopeKey:       ei.FlowScopeKey,
		BpmnElementType:    uint8(target.Type),
	})
}

// armBoundaryEvents activates a waiting element instance for each boundary event
// attached to the host activity that just activated (ei/hostKey). Each armed
// instance carries AttachedToKey = hostKey so it can later interrupt its host, and
// its own OnActivated arms the timer/subscription it waits on. A no-op for a node
// with no attached boundary events (ADR-0038).
func armBoundaryEvents(c *ProcessingContext, hostKey uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	for _, beID := range cp.BoundaryEvents(ei.ElementId) {
		c.AppendElementCommand(c.NewKey(), model.IntentActivating, model.ElementInstanceValue{
			ProcessInstanceKey: ei.ProcessInstanceKey,
			ProcessDefKey:      ei.ProcessDefKey,
			ElementId:          beID,
			FlowScopeKey:       ei.FlowScopeKey,
			BpmnElementType:    uint8(compiler.TypeBoundaryEvent),
			AttachedToKey:      hostKey,
		})
	}
}

// disarmBoundaryEvents terminates every still-armed boundary event instance
// attached to hostKey, when the host completes normally. A Terminated event drops
// the instance and decrements the scope's child count; the boundary's timer or
// subscription is left to self-retire (it fires later, finds no element, and does
// nothing) — the same pattern instance cancellation uses.
func disarmBoundaryEvents(c *ProcessingContext, hostKey, procKey uint64) {
	var boundaries []uint64
	c.ForEachElementInstance(procKey, func(elKey uint64) {
		if b := c.GetElementInstance(elKey); b != nil && b.AttachedToKey == hostKey {
			boundaries = append(boundaries, elKey)
		}
	})
	for _, bk := range boundaries {
		if b := c.GetElementInstance(bk); b != nil {
			c.AppendElementEvent(bk, model.IntentTerminated, *b)
		}
	}
}

// interruptHost terminates the host activity an interrupting boundary event fired
// on: it cancels the host's job (if any), terminates the host element instance,
// and terminates the host's other boundary siblings (their timers/subscriptions
// self-retire). It is idempotent — if the host is already gone (it completed, or a
// sibling boundary already interrupted it), it does nothing.
func interruptHost(c *ProcessingContext, hostKey, selfKey uint64) {
	host := c.GetElementInstance(hostKey)
	if host == nil {
		return
	}
	if jobKey, ok := c.JobOfElement(hostKey); ok {
		if job := c.GetJob(jobKey); job != nil {
			c.AppendJobEvent(jobKey, model.IntentJobCanceled, *job)
		}
	}
	c.AppendElementEvent(hostKey, model.IntentTerminated, *host)
	// Terminate the host's other boundary events (not this one — it completes and
	// takes its outgoing flow).
	var siblings []uint64
	c.ForEachElementInstance(host.ProcessInstanceKey, func(elKey uint64) {
		if elKey == selfKey {
			return
		}
		if s := c.GetElementInstance(elKey); s != nil && s.AttachedToKey == hostKey {
			siblings = append(siblings, elKey)
		}
	})
	for _, sk := range siblings {
		if s := c.GetElementInstance(sk); s != nil {
			c.AppendElementEvent(sk, model.IntentTerminated, *s)
		}
	}
}

// takeOutgoingFlows activates a fresh element instance for each outgoing flow's
// target. (Sequence-flow-taken audit events are deferred; the lifecycle events
// are enough to drive and recover state.)
func takeOutgoingFlows(c *ProcessingContext, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	for _, flowID := range cp.Outgoing(ei.ElementId) {
		activateElement(c, ei, cp.Flow(flowID).Target)
	}
}

// takeInclusiveOutgoing takes every outgoing flow whose FEEL condition holds (an
// unconditional, non-default flow always holds), or the default flow if none do.
// This is the inclusive (OR) split: unlike the exclusive gateway, it may take
// more than one branch.
func takeInclusiveOutgoing(c *ProcessingContext, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	took := false
	defaultFlow := int32(-1)
	for _, flowID := range cp.Outgoing(ei.ElementId) {
		f := cp.Flow(flowID)
		if f.Default {
			defaultFlow = flowID
			continue
		}
		if f.Condition == nil {
			activateElement(c, ei, f.Target)
			took = true
			continue
		}
		v, err := f.Condition.Eval(bindInputs(c, f.Condition.Inputs(), ei.ProcessInstanceKey))
		if err == nil && expr.IsTrue(v) {
			activateElement(c, ei, f.Target)
			took = true
		}
	}
	if !took && defaultFlow >= 0 {
		activateElement(c, ei, cp.Flow(defaultFlow).Target)
	}
}

// builtinProcessInstanceKey is a reserved FEEL identifier that resolves to the
// evaluating instance's own process-instance key, as a string so the full 64-bit
// key survives exactly (a FEEL number is a float and would lose precision on
// large keys). It lets a model correlate a reply back to the requesting instance
// without a hand-authored business key (ADR-0035). A process variable of the same
// name is shadowed by the built-in.
const builtinProcessInstanceKey = "processInstanceKey"

// bindInputs reads the named variables from a scope into a FEEL binding map for
// evaluation. A name absent from the scope is simply left unbound (FEEL null).
// The reserved name processInstanceKey binds to the scope's own key (the built-in
// above); at every call site the scope is the process instance, so it is the
// instance's key.
func bindInputs(c *ProcessingContext, inputs []string, scope uint64) map[string]expr.Value {
	if len(inputs) == 0 {
		return nil
	}
	vars := make(map[string]expr.Value, len(inputs))
	for _, name := range inputs {
		if name == builtinProcessInstanceKey {
			vars[name] = expr.FromStored(expr.KindString, false, strconv.FormatUint(scope, 10))
			continue
		}
		if vv := c.GetVariable(scope, name); vv != nil {
			vars[name] = expr.FromStored(toExprKind(vv.Kind), vv.Bool, vv.Text)
		}
	}
	return vars
}

// startEventBehavior: a none start event has no work; it completes at once.
type startEventBehavior struct{}

func (startEventBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	c.AppendElementCommand(key, model.IntentCompleting, *ei)
}

func (startEventBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	completeAndTakeFlows(c, key, ei)
}

// passThroughBehavior: an undefined/manual task with no execution semantics. It
// has no work of its own, so it completes on activation and takes its outgoing
// flow — the token flows straight through, exactly like a none event. This makes
// a routing test (e.g. of a gateway) runnable before its tasks are implemented.
type passThroughBehavior struct{}

func (passThroughBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	c.AppendElementCommand(key, model.IntentCompleting, *ei)
}

func (passThroughBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	completeAndTakeFlows(c, key, ei)
}

// serviceTaskBehavior: create a job on activation and wait; complete when the
// job is completed by a worker.
type serviceTaskBehavior struct{}

func (serviceTaskBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	detail := cp.ServiceTask(cp.Node(ei.ElementId).Detail)
	jobKey := c.NewKey()
	c.AppendJobEvent(jobKey, model.IntentJobCreated, model.JobValue{
		ProcessInstanceKey: ei.ProcessInstanceKey,
		ElementInstanceKey: key,
		JobType:            detail.JobType,
		Retries:            detail.Retries,
	})
	c.NotifyJobAvailable(detail.JobType)
	// Stays Activated: no Completing until a worker completes the job.
}

func (serviceTaskBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	completeAndTakeFlows(c, key, ei)
}

// scriptTaskBehavior: on activation, evaluate the task's compiled FEEL
// expression, write the result to its result variable, and complete — the whole
// task runs inside the engine, so the instance advances without an external
// worker. FEEL evaluation happens here (command processing), and its result is
// written into the variable event; on replay applyToState re-applies that stored
// result rather than re-evaluating (invariant I6), so applyToState stays pure.
type scriptTaskBehavior struct{}

func (scriptTaskBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	detail := cp.ScriptTask(cp.Node(ei.ElementId).Detail)

	// Bind the process variables the expression reads (its inputs) from the
	// instance scope, then evaluate.
	result, err := detail.Expr.Eval(bindInputs(c, detail.Expr.Inputs(), ei.ProcessInstanceKey))
	if err != nil {
		// Incidents are not modeled yet (Milestone 2); FEEL is null-propagating,
		// so a failed evaluation yields null rather than halting the processor.
		result = expr.Null
	}

	kind, b, text := expr.Classify(result)
	c.AppendVariableEvent(model.IntentVariableCreated, model.VariableValue{
		ScopeKey: ei.ProcessInstanceKey,
		Name:     detail.ResultVar,
		Kind:     toVarKind(kind),
		Bool:     b,
		Text:     text,
	})
	c.AppendElementCommand(key, model.IntentCompleting, *ei)
}

func (scriptTaskBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	completeAndTakeFlows(c, key, ei)
}

// timerCatchEventBehavior: an intermediate timer catch event. On activation it
// creates a timer due after the configured duration and then waits (stays
// Activated). A due timer is fired by TriggerDueTimers, which drives
// handleTimerTriggered → the element completes and takes its outgoing flows.
type timerCatchEventBehavior struct{}

func (timerCatchEventBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	detail := cp.TimerCatch(cp.Node(ei.ElementId).Detail)
	// Now() is read here (command processing) and frozen into the event's due
	// date; applyToState never reads the clock (invariant I4).
	due := c.Now() + detail.DurationNanos
	c.AppendTimerEvent(c.NewKey(), model.IntentTimerCreated, model.TimerValue{
		ProcessInstanceKey: ei.ProcessInstanceKey,
		ElementInstanceKey: key,
		TargetElementId:    ei.ElementId,
		DueDate:            due,
	})
	// Stays Activated: no Completing until the timer fires.
}

func (timerCatchEventBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	completeAndTakeFlows(c, key, ei)
}

// messageCatchEventBehavior: an intermediate message catch event. On activation
// it evaluates its correlation-key expression over the instance's variables,
// opens a subscription on (message name, key), and waits (stays Activated). A
// later publish or throw that matches correlates the subscription, which drives
// the element to complete and take its outgoing flows (ADR-0020). The key is
// evaluated here (command processing) and frozen into the SubscriptionCreated
// event; applyToState never re-evaluates it (invariant I6).
type messageCatchEventBehavior struct{}

func (messageCatchEventBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	detail := cp.MessageCatch(cp.Node(ei.ElementId).Detail)
	c.AppendMessageSubscriptionEvent(key, model.IntentSubscriptionCreated, model.MessageSubscriptionValue{
		ProcessInstanceKey: ei.ProcessInstanceKey,
		ElementInstanceKey: key,
		MessageName:        detail.MessageName,
		CorrelationKey:     evalCorrelationKey(c, detail.CorrelationKey, ei.ProcessInstanceKey),
	})
	// Stays Activated: no Completing until a message correlates.
}

func (messageCatchEventBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	completeAndTakeFlows(c, key, ei)
}

// messageThrowEventBehavior: an intermediate message throw event. On activation
// it evaluates the referenced message's correlation-key expression over its own
// variables and correlates — waking any instance already waiting on that (name,
// key) — then completes and takes its outgoing flows. Producing and consuming a
// message share one path (correlateMessage), so a throw event and an API publish
// behave identically (ADR-0020).
type messageThrowEventBehavior struct{}

func (messageThrowEventBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	detail := cp.MessageThrow(cp.Node(ei.ElementId).Detail)
	// The throw carries the throwing instance's variables as the message payload,
	// so a correlated catch — or a message-start instance the throw creates — is
	// seeded with them (ADR-0035). Reading the payload here (command processing)
	// keeps applyToState pure (I4).
	payload := instanceVariables(c, ei.ProcessInstanceKey)
	correlateMessage(c, detail.MessageName, evalCorrelationKey(c, detail.CorrelationKey, ei.ProcessInstanceKey), payload)
	c.AppendElementCommand(key, model.IntentCompleting, *ei)
}

func (messageThrowEventBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	completeAndTakeFlows(c, key, ei)
}

// evalCorrelationKey evaluates a compiled correlation-key expression over a
// scope's variables and returns its FEEL canonical string form — the value a
// subscription is keyed by and a publish matches against. A nil expression (an
// empty correlationKey) or an evaluation error yields "".
func evalCorrelationKey(c *ProcessingContext, e *expr.Compiled, scope uint64) string {
	if e == nil {
		return ""
	}
	v, err := e.Eval(bindInputs(c, e.Inputs(), scope))
	if err != nil {
		return ""
	}
	return v.String()
}

// correlateMessage delivers a message with the given name and correlation key to
// every open subscription that matches. For each match it emits
// SubscriptionCorrelated (which retires the subscription), writes the message's
// payload variables into that instance's scope, and commands the waiting element
// instance to complete. Matches are collected before any mutation so retiring a
// subscription can't disturb the scan. A message that matches nothing is a no-op
// — there is no buffering yet (ADR-0020).
func correlateMessage(c *ProcessingContext, name, correlationKey string, vars []model.VariableValue) {
	type match struct {
		elKey uint64
		sub   model.MessageSubscriptionValue
	}
	var matches []match
	c.p.fail(c.tx.CorrelatableSubscriptions(name, correlationKey, func(elKey uint64, v *model.MessageSubscriptionValue) error {
		matches = append(matches, match{elKey: elKey, sub: *v})
		return nil
	}))
	for i := range matches {
		m := matches[i]
		c.AppendMessageSubscriptionEvent(m.elKey, model.IntentSubscriptionCorrelated, m.sub)
		for j := range vars {
			vv := vars[j]
			vv.ScopeKey = m.sub.ProcessInstanceKey
			c.AppendVariableEvent(model.IntentVariableCreated, vv)
		}
		if ei := c.GetElementInstance(m.elKey); ei != nil {
			c.AppendElementCommand(m.elKey, model.IntentCompleting, *ei)
		}
	}
	// A message also instantiates every deployed process with a matching message
	// start event, seeded with the payload (ADR-0035). Matching is by name today;
	// the message's correlation key is not evaluated for start events yet. This
	// runs after the subscription scan so a single message can both correlate a
	// waiting instance and start new ones, all recovered from the events the
	// created instances emit.
	for _, defKey := range c.p.messageStarts[name] {
		c.AppendCreateInstanceCommand(defKey, vars)
	}
}

// instanceVariables reads all of an instance's variables into a fresh slice, to
// carry as a message payload. Unlike hot-path token movement this deliberately
// allocates — a message payload is runtime data, not a per-command cost. Returns
// nil if the instance has no variables.
func instanceVariables(c *ProcessingContext, scope uint64) []model.VariableValue {
	var vars []model.VariableValue
	c.p.fail(c.tx.VariablesOfScope(scope, func(v *model.VariableValue) error {
		vars = append(vars, *v)
		return nil
	}))
	return vars
}

// exclusiveGatewayBehavior: a data-based XOR split. It takes exactly one outgoing
// flow — the first whose FEEL condition is true (in flow order), an unconditional
// flow, or the default flow if none match. Like any gateway it has no work of its
// own, so it decides and completes at once. The decision is captured by which
// target gets an Activating command (and thus an Activated event); on replay that
// event is re-applied, not re-evaluated (invariant I6), so the same branch runs.
type exclusiveGatewayBehavior struct{}

func (exclusiveGatewayBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	c.AppendElementCommand(key, model.IntentCompleting, *ei)
}

func (exclusiveGatewayBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	c.AppendElementEvent(key, model.IntentCompleted, *ei)
	cp := c.process(ei.ProcessDefKey)
	flowID := selectExclusiveFlow(c, cp, ei)
	if flowID < 0 {
		// No condition matched and there is no default flow: nothing is taken.
		// This is a modeling error that becomes an incident once incidents land
		// (Milestone 2); for now the branch simply ends here.
		return
	}
	target := cp.Node(cp.Flow(flowID).Target)
	c.AppendElementCommand(c.NewKey(), model.IntentActivating, model.ElementInstanceValue{
		ProcessInstanceKey: ei.ProcessInstanceKey,
		ProcessDefKey:      ei.ProcessDefKey,
		ElementId:          target.ElementId,
		FlowScopeKey:       ei.FlowScopeKey,
		BpmnElementType:    uint8(target.Type),
	})
}

// parallelGatewayBehavior: an AND gateway. As a fork (one incoming) it fires at
// once, producing a token on every outgoing flow. As a join (several incoming) it
// waits until a token has arrived on each incoming flow — every arrival parks as a
// live element instance on the gateway — then consumes them all and fires the
// outgoing flow(s) once. The synchronization is captured entirely by which
// element instances exist and by the Completed/Activating events emitted, so it
// replays deterministically without re-counting (invariants I4/I6).
type parallelGatewayBehavior struct{}

func (parallelGatewayBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	node := c.process(ei.ProcessDefKey).Node(ei.ElementId)
	if node.IncomingCount <= 1 {
		c.AppendElementCommand(key, model.IntentCompleting, *ei) // fork: fire now
		return
	}
	// Join: fire only when a token sits on every incoming flow. Until then this
	// arrival waits here (stays Activated).
	arrived := c.ElementInstancesOnNode(ei.ProcessInstanceKey, ei.ElementId)
	if int32(len(arrived)) < node.IncomingCount {
		return
	}
	// All arrived: consume every waiting token, then fire the outgoing flow(s) once.
	for _, k := range arrived {
		if a := c.GetElementInstance(k); a != nil {
			c.AppendElementEvent(k, model.IntentCompleted, *a)
		}
	}
	takeOutgoingFlows(c, ei)
}

func (parallelGatewayBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	completeAndTakeFlows(c, key, ei)
}

// inclusiveGatewayBehavior: an OR gateway. As a split (one incoming) it fires at
// once, taking every outgoing flow whose condition holds (or the default). As a
// join (several incoming) it waits until no token could still arrive — no active
// token upstream and none in flight toward it — then consumes every token parked
// on it and fires the outgoing flow(s) once. That "no more can arrive" test is
// what distinguishes it from a parallel join, which waits for a fixed count: an
// inclusive join waits only for the branches the split actually took.
type inclusiveGatewayBehavior struct{}

func (inclusiveGatewayBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	if cp.Node(ei.ElementId).IncomingCount <= 1 {
		c.AppendElementCommand(key, model.IntentCompleting, *ei) // split: fire now
		return
	}
	// Join: park until nothing more can arrive at this gateway.
	if c.TokenCanStillReach(ei.ProcessInstanceKey, ei.ElementId, cp.NodesReaching(ei.ElementId)) {
		return
	}
	for _, k := range c.ElementInstancesOnNode(ei.ProcessInstanceKey, ei.ElementId) {
		if a := c.GetElementInstance(k); a != nil {
			c.AppendElementEvent(k, model.IntentCompleted, *a)
		}
	}
	takeInclusiveOutgoing(c, ei)
}

func (inclusiveGatewayBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	c.AppendElementEvent(key, model.IntentCompleted, *ei)
	takeInclusiveOutgoing(c, ei)
}

// selectExclusiveFlow returns the outgoing flow an exclusive gateway takes: the
// first (in flow order) whose FEEL condition is true, an unconditional non-default
// flow, or the default flow; -1 if none apply.
func selectExclusiveFlow(c *ProcessingContext, cp *compiler.CompiledProcess, ei *model.ElementInstanceValue) int32 {
	defaultFlow := int32(-1)
	for _, flowID := range cp.Outgoing(ei.ElementId) {
		f := cp.Flow(flowID)
		if f.Default {
			defaultFlow = flowID
			continue
		}
		if f.Condition == nil {
			return flowID // an unconditional flow is taken whenever reached
		}
		v, err := f.Condition.Eval(bindInputs(c, f.Condition.Inputs(), ei.ProcessInstanceKey))
		if err == nil && expr.IsTrue(v) {
			return flowID
		}
	}
	return defaultFlow
}

// toVarKind maps the expr scalar kind to the model's stored kind (same order,
// mapped explicitly so the two enums can evolve independently).
func toVarKind(k expr.ValueKind) model.VarKind {
	switch k {
	case expr.KindBool:
		return model.VarBool
	case expr.KindNumber:
		return model.VarNumber
	case expr.KindString:
		return model.VarString
	case expr.KindJSON:
		return model.VarJSON
	default:
		return model.VarNull
	}
}

// toExprKind is the inverse of toVarKind, for binding a stored variable back into
// an evaluation.
func toExprKind(k model.VarKind) expr.ValueKind {
	switch k {
	case model.VarBool:
		return expr.KindBool
	case model.VarNumber:
		return expr.KindNumber
	case model.VarString:
		return expr.KindString
	case model.VarJSON:
		return expr.KindJSON
	default:
		return expr.KindNull
	}
}

// businessRuleTaskBehavior: delegate a DMN decision to the temis engine. The
// engine treats it exactly like a service task — create a job on activation and
// wait — but the job carries the reserved DMN job type, so the in-process DMN
// worker (package dmn) picks it up, evaluates the decision off the hot path, and
// completes it. Keeping DMN evaluation on the worker side, not in a behavior,
// keeps the processor allocation-free (I1) and the temis dependency out of the
// engine hot path (ADR-0014).
type businessRuleTaskBehavior struct{}

func (businessRuleTaskBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	detail := cp.BusinessRuleTask(cp.Node(ei.ElementId).Detail)
	jobKey := c.NewKey()
	c.AppendJobEvent(jobKey, model.IntentJobCreated, model.JobValue{
		ProcessInstanceKey: ei.ProcessInstanceKey,
		ElementInstanceKey: key,
		JobType:            detail.JobType,
		Retries:            detail.Retries,
	})
	c.NotifyJobAvailable(detail.JobType)
	// Stays Activated until the DMN worker completes the job.
}

func (businessRuleTaskBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	completeAndTakeFlows(c, key, ei)
}

// connectorTaskBehavior: delegate to a server-registered connector (e.g. a clio
// event store). The engine treats it exactly like a service task — create a job
// on activation and wait — but the job carries the connector's reserved job type,
// so the in-process connector worker (package clio) picks it up, performs the
// outbound call off the hot path after fsync, and completes it. Keeping the call
// on the worker side, not in a behavior, keeps the processor allocation-free (I1)
// and the connector's network I/O out of applyToState (I4). See ADR-0036.
type connectorTaskBehavior struct{}

func (connectorTaskBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	detail := cp.ConnectorTask(cp.Node(ei.ElementId).Detail)
	jobKey := c.NewKey()
	c.AppendJobEvent(jobKey, model.IntentJobCreated, model.JobValue{
		ProcessInstanceKey: ei.ProcessInstanceKey,
		ElementInstanceKey: key,
		JobType:            detail.JobType,
		Retries:            detail.Retries,
	})
	c.NotifyJobAvailable(detail.JobType)
	// Stays Activated until the connector worker completes the job.
}

func (connectorTaskBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	completeAndTakeFlows(c, key, ei)
}

// userTaskBehavior: a human task. On activation it creates a job (carrying the
// reserved user-task job type) and waits — the "worker" is a person using the
// Tasks app, not an external service (ADR-0028). Completing the job (via the
// task API) drives the token onward, exactly like a service task.
type userTaskBehavior struct{}

func (userTaskBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	detail := cp.UserTask(cp.Node(ei.ElementId).Detail)
	jobKey := c.NewKey()
	c.AppendJobEvent(jobKey, model.IntentJobCreated, model.JobValue{
		ProcessInstanceKey: ei.ProcessInstanceKey,
		ElementInstanceKey: key,
		JobType:            detail.JobType,
		Retries:            detail.Retries,
	})
	c.NotifyJobAvailable(detail.JobType)
}

func (userTaskBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	completeAndTakeFlows(c, key, ei)
}

// boundaryEventBehavior: a timer/message event attached to a host activity
// (ADR-0038). On activation it arms its trigger — a timer keyed to itself (like an
// intermediate timer catch) or a message subscription — and waits. When the
// trigger fires, the existing timer/message path drives it to Completing:
//   - interrupting: cancel the host (and its job and other boundary siblings),
//     then take the boundary's outgoing flow;
//   - non-interrupting: just take the outgoing flow, leaving the host running.
//
// A boundary instance that a sibling's interrupt already terminated is skipped
// (its Completing command was queued before the sibling fired).
type boundaryEventBehavior struct{}

func (boundaryEventBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	d := cp.BoundaryEvent(cp.Node(ei.ElementId).Detail)
	switch d.Kind {
	case compiler.BoundaryTimer:
		// Now() is read here (command processing) and frozen into the due date;
		// applyToState never reads the clock (invariant I4).
		c.AppendTimerEvent(c.NewKey(), model.IntentTimerCreated, model.TimerValue{
			ProcessInstanceKey: ei.ProcessInstanceKey,
			ElementInstanceKey: key,
			TargetElementId:    ei.ElementId,
			DueDate:            c.Now() + d.DurationNanos,
		})
	case compiler.BoundaryMessage:
		c.AppendMessageSubscriptionEvent(key, model.IntentSubscriptionCreated, model.MessageSubscriptionValue{
			ProcessInstanceKey: ei.ProcessInstanceKey,
			ElementInstanceKey: key,
			MessageName:        d.MessageName,
			CorrelationKey:     evalCorrelationKey(c, d.CorrelationKey, ei.ProcessInstanceKey),
		})
	}
	// Stays Activated: waits until the timer fires or the message correlates.
}

func (boundaryEventBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	// A sibling boundary's interrupt may have terminated this instance after its
	// Completing command was queued; if so, there is nothing to fire.
	if c.GetElementInstance(key) == nil {
		return
	}
	d := c.process(ei.ProcessDefKey).BoundaryEvent(c.process(ei.ProcessDefKey).Node(ei.ElementId).Detail)
	if d.Interrupting {
		interruptHost(c, ei.AttachedToKey, key)
	}
	completeAndTakeFlows(c, key, ei)
}

// endEventBehavior: a none end event completes and, if it was the last active
// element in its scope, completes the process instance.
type endEventBehavior struct{}

func (endEventBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	c.AppendElementCommand(key, model.IntentCompleting, *ei)
}

func (endEventBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	c.AppendElementEvent(key, model.IntentCompleted, *ei) // decrements scope's active children
	if c.ActiveChildren(ei.FlowScopeKey) == 0 {
		if pi := c.GetProcessInstance(ei.ProcessInstanceKey); pi != nil {
			c.AppendProcessInstanceEvent(ei.ProcessInstanceKey, model.IntentCompleted, *pi)
		}
	}
}
