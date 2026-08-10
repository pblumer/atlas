package engine

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

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
		handlerKey(model.VTJob, model.IntentJobFailed):               handleJobFailed,
		handlerKey(model.VTJob, model.IntentJobErrorThrown):          handleJobError,
		handlerKey(model.VTJob, model.IntentJobAssigned):             handleJobAssigned,
		handlerKey(model.VTIncident, model.IntentIncidentResolved):   handleIncidentResolved,
		handlerKey(model.VTTimer, model.IntentTimerTriggered):        handleTimerTriggered,
		handlerKey(model.VTTimer, model.IntentTimerStartArm):         handleTimerStartArm,
		handlerKey(model.VTMessage, model.IntentMessagePublished):    handleMessagePublished,
		handlerKey(model.VTVariable, model.IntentVariableModify):     handleVariablesModify,
	}
}

func (p *Processor) registerBehaviors() {
	p.behaviors[compiler.TypeStartEvent] = startEventBehavior{}
	p.behaviors[compiler.TypeEndEvent] = endEventBehavior{}
	p.behaviors[compiler.TypeServiceTask] = serviceTaskBehavior{}
	p.behaviors[compiler.TypeScriptTask] = scriptTaskBehavior{}
	p.behaviors[compiler.TypeBusinessRuleTask] = businessRuleTaskBehavior{}
	p.behaviors[compiler.TypeConnectorTask] = connectorTaskBehavior{}
	p.behaviors[compiler.TypeScriptJobTask] = scriptJobTaskBehavior{}
	p.behaviors[compiler.TypeUserTask] = userTaskBehavior{}
	p.behaviors[compiler.TypeBoundaryEvent] = boundaryEventBehavior{}
	p.behaviors[compiler.TypeExclusiveGateway] = exclusiveGatewayBehavior{}
	p.behaviors[compiler.TypeTimerCatchEvent] = timerCatchEventBehavior{}
	p.behaviors[compiler.TypeMessageCatchEvent] = messageCatchEventBehavior{}
	p.behaviors[compiler.TypeMessageThrowEvent] = messageThrowEventBehavior{}
	p.behaviors[compiler.TypeReceiveTask] = receiveTaskBehavior{}
	p.behaviors[compiler.TypeSignalCatchEvent] = signalCatchEventBehavior{}
	p.behaviors[compiler.TypeSignalThrowEvent] = signalThrowEventBehavior{}
	p.behaviors[compiler.TypeSignalEndEvent] = signalEndEventBehavior{}
	p.behaviors[compiler.TypeErrorEndEvent] = errorEndEventBehavior{}
	p.behaviors[compiler.TypeCompensationThrowEvent] = compensationThrowEventBehavior{}
	p.behaviors[compiler.TypeCompensationEndEvent] = compensationEndEventBehavior{}
	p.behaviors[compiler.TypeCancelEndEvent] = cancelEndEventBehavior{}
	// A signal start event is a plain entry point once instantiated: a broadcast
	// signal creates the instance (see broadcastSignal/Deploy) and it then flows
	// straight on like a none start (ADR-0088), the same as a message start.
	p.behaviors[compiler.TypeSignalStartEvent] = startEventBehavior{}
	p.behaviors[compiler.TypeTask] = passThroughBehavior{}
	p.behaviors[compiler.TypeParallelGateway] = parallelGatewayBehavior{}
	p.behaviors[compiler.TypeInclusiveGateway] = inclusiveGatewayBehavior{}
	p.behaviors[compiler.TypeEventBasedGateway] = eventBasedGatewayBehavior{}
	// A message start event is a plain entry point once instantiated: it flows
	// straight on like a none start (ADR-0035). What makes it a start is the
	// deploy-time subscription (see Deploy), not a distinct runtime behavior.
	p.behaviors[compiler.TypeMessageStartEvent] = startEventBehavior{}
	// A timer start event is the same once instantiated: a due timer creates the
	// instance and it flows straight on (ADR-0051). What makes it a start is the
	// deploy-time timer the arm handler creates, not a distinct runtime behavior.
	p.behaviors[compiler.TypeTimerStartEvent] = startEventBehavior{}
	p.behaviors[compiler.TypeMessageEndEvent] = messageEndEventBehavior{}
	p.behaviors[compiler.TypeSubProcess] = subProcessBehavior{}
	p.behaviors[compiler.TypeCallActivity] = callActivityBehavior{}
	p.behaviors[compiler.TypeEventSubProcessStart] = eventSubProcessStartBehavior{}
}

// --- command handlers ---

// handleProcessInstanceActivating creates the process instance and activates
// each start event.
func handleProcessInstanceActivating(c *ProcessingContext) {
	defKey := c.cmd.Value.process.ProcessDefKey
	piKey := c.NewKey()
	cp := c.process(defKey)

	// If the definition carries an instance TTL, schedule its self-cleaning expiry
	// (ADR-0085): freeze a due date at command time and record it both on the instance
	// and on a durable timer, so both rebuild identically on replay (I4/I6). The timer
	// reuses the instance key — a globally unique id no other timer holds — so normal
	// completion or an explicit cancel can retire it by key without scanning the index.
	var expiryDue int64
	if ttl := cp.InstanceTtlNanos(); ttl > 0 {
		expiryDue = c.Now() + ttl
	}

	// The correlation key (empty for a timer or API start) rides in on the create
	// command so the instance records which message key it began with (ADR-0020);
	// CreatedAt is stamped from the event timestamp in applyToState.
	c.AppendProcessInstanceEvent(piKey, model.IntentActivated, model.ProcessInstanceValue{
		ProcessDefKey:            defKey,
		CorrelationKey:           c.cmd.Value.process.CorrelationKey,
		ParentElementInstanceKey: c.cmd.Value.process.ParentElementInstanceKey,
		ExpiryDueDate:            expiryDue,
	})
	if expiryDue > 0 {
		// An expiry timer carries no owning element instance (ElementInstanceKey 0) — the
		// shape handleTimerTriggered dispatches to instance termination (ADR-0085).
		c.AppendTimerEvent(piKey, model.IntentTimerCreated, model.TimerValue{
			ProcessInstanceKey: piKey,
			ElementInstanceKey: 0,
			TargetElementId:    -1,
			DueDate:            expiryDue,
		})
	}

	// Seed the instance's start variables under its scope before any element runs.
	for i := range c.cmd.StartVars {
		v := c.cmd.StartVars[i]
		v.ScopeKey = piKey
		c.AppendVariableEvent(model.IntentVariableCreated, v)
	}

	// Seed the process's declared data objects under the instance scope, each with
	// its declared initial data state (value unset for now; data associations write
	// values in a later slice). Like the start variables, this is deterministic
	// state mutation from already-decided data, so it replays identically (ADR-0053).
	for _, d := range cp.DataObjects() {
		c.AppendDataObjectEvent(model.IntentDataObjectCreated, model.DataObjectValue{
			ScopeKey: piKey,
			Name:     cp.Intern(d.Name),
			State:    cp.Intern(d.InitialState),
			Kind:     model.VarNull,
		})
	}

	for _, startID := range cp.StartEvents() {
		node := cp.Node(startID)
		key := c.NewKey()
		c.AppendElementCommand(key, model.IntentActivating, model.ElementInstanceValue{
			ProcessInstanceKey: piKey,
			ProcessDefKey:      defKey,
			ElementId:          startID,
			FlowScopeKey:       piKey, // root elements are scoped by the instance
			BpmnElementType:    uint8(node.Type),
			TokenID:            key,
			SourceFlowId:       -1,
		})
	}
	// Arm the root-scope event subprocesses: each opens its trigger (message/timer) and
	// waits, ready to run its handler while the instance runs (ADR-0082).
	armEventSubprocesses(c, piKey, defKey, piKey, cp.RootEventSubprocesses())
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
	cancelExpiryTimer(c, piKey, pi.ExpiryDueDate)
	c.ForEachElementInstance(piKey, func(elKey uint64) {
		if ei := c.GetElementInstance(elKey); ei != nil {
			terminateChildInstance(c, elKey, ei.BpmnElementType)
			// Cancel any job the element left waiting, so a terminated instance leaves no
			// orphan in the activatable index (the same teardown terminateScope does for a
			// subprocess/multi-instance scope).
			if jobKey, ok := c.JobOfElement(elKey); ok {
				if job := c.GetJob(jobKey); job != nil {
					c.AppendJobEvent(jobKey, model.IntentJobCanceled, *job)
				}
			}
			c.AppendElementEvent(elKey, model.IntentTerminated, *ei)
		}
	})
	c.AppendProcessInstanceEvent(piKey, model.IntentTerminated, *pi)
}

// cancelExpiryTimer retires an instance's TTL expiry timer (ADR-0085) when the instance
// ends before its TTL elapses. The timer key is the instance key and its due date is
// stored on the instance record, so it deletes by key without scanning the timer index.
// A zero due date means the instance had no TTL — nothing to retire. The delete is
// idempotent, so this is also harmless when the expiry timer itself just terminated the
// instance (it fired and deleted its own record first).
func cancelExpiryTimer(c *ProcessingContext, piKey uint64, expiryDue int64) {
	if expiryDue == 0 {
		return
	}
	c.AppendTimerEvent(piKey, model.IntentTimerCanceled, model.TimerValue{
		ProcessInstanceKey: piKey,
		DueDate:            expiryDue,
	})
}

// handleElementActivating emits the Activated lifecycle event, runs the
// element-type behavior, then arms any boundary events attached to this element
// (a no-op for a node with none).
func handleElementActivating(c *ProcessingContext) {
	ei := &c.cmd.Value.element
	c.AppendElementEvent(c.cmd.Key, model.IntentActivated, *ei)
	// A multi-instance body seeds its iterations rather than running the node's real
	// behavior; its per-iteration data-input associations and io-mappings apply on
	// each inner instance, not on the body (ADR-0077). An inner instance (role 2)
	// falls through to the normal path and runs the real behavior.
	if node := c.process(ei.ProcessDefKey).Node(ei.ElementId); node.MultiInstance >= 0 && ei.MultiInstance != miInner {
		seedMultiInstance(c, c.cmd.Key, ei)
		armBoundaryEvents(c, c.cmd.Key, ei)
		return
	}
	// Read the activity's data-input associations into process variables before its
	// behavior runs, so the activity's own FEEL (e.g. a script task's expression)
	// sees them (ADR-0059).
	applyDataInputAssociations(c, ei)
	// Then evaluate its zeebe:ioMapping inputs into its activity-local scope, so the
	// behavior (and any worker) reads the mapped locals resolving up the scope chain
	// (ADR-0068). Runs after data-input associations so a mapping can read them.
	if hasIOMappings(c.process(ei.ProcessDefKey), ei.ElementId) {
		applyInputMappings(c, c.cmd.Key, ei)
	}
	c.p.behavior(ei.BpmnElementType).OnActivated(c, c.cmd.Key, ei)
	armBoundaryEvents(c, c.cmd.Key, ei)
}

// Multi-instance element-instance roles (ADR-0077), mirrored on
// model.ElementInstanceValue.MultiInstance.
const (
	miNone  uint8 = 0 // not a multi-instance element instance
	miBody  uint8 = 1 // the body: the scope that seeds and joins the iterations
	miInner uint8 = 2 // one iteration: runs the node's real behavior, scoped under the body
)

// applyDataInputAssociations evaluates an activating activity's data-input
// associations (ADR-0059): for each, it reads the source data object and writes a
// value into the target process variable the activity reads. With an <assignment>
// <from> transform, it evaluates the FEEL over the instance's variables plus the
// source object bound under its name; with none, it copies the object's value
// verbatim. The value is written as a VariableCreated event, so replay re-applies it
// without re-reading the object or re-evaluating (invariants I4/I6).
func applyDataInputAssociations(c *ProcessingContext, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	assocs := cp.DataInputAssociations(ei.ElementId)
	for i := range assocs {
		a := &assocs[i]
		objName := cp.Intern(a.DataObject)
		obj := c.GetDataObject(ei.ProcessInstanceKey, objName)

		out := model.VariableValue{ScopeKey: ei.ProcessInstanceKey, Name: cp.Intern(a.Variable)}
		switch {
		case a.Value != nil:
			// Bind the instance variables the transform reads plus the source object
			// (under its own name) into the FEEL scope, then evaluate.
			scope := bindInputs(c, a.Value.Inputs(), ei.ProcessInstanceKey)
			if obj != nil {
				if scope == nil {
					scope = map[string]expr.Value{}
				}
				scope[objName] = expr.FromStored(toExprKind(obj.Kind), obj.Bool, obj.Text)
			}
			result, err := a.Value.Eval(scope)
			if err != nil {
				result = expr.Null
			}
			kind, b, text := expr.Classify(result)
			out.Kind, out.Bool, out.Text = toVarKind(kind), b, text
		case obj != nil:
			// No transform: copy the object's value into the variable verbatim.
			out.Kind, out.Bool, out.Text = obj.Kind, obj.Bool, obj.Text
		}

		c.AppendVariableEvent(model.IntentVariableCreated, out)
	}
}

// handleElementCompleting runs the element-type completion behavior, then — if
// this element hosts boundary events — disarms any still armed, now that it has
// completed normally (their timers/subscriptions self-retire).
func handleElementCompleting(c *ProcessingContext) {
	ei := &c.cmd.Value.element
	// Write the activity's data-output associations before it completes, so a data
	// object it produces is in state before any outgoing flow activates the next
	// element (ADR-0058). Any element type may carry associations, so this is a
	// single shared point rather than per-behavior logic.
	applyDataOutputAssociations(c, ei)
	// Promote the activity's zeebe:ioMapping outputs to the parent scope, then drop
	// its activity-local scope, before it completes and any outgoing flow activates
	// the next element — so downstream FEEL sees the promoted values and never the
	// dropped locals (ADR-0068). Gated so a mapping-free activity keeps the pre-0068
	// behaviour with no scope-drop scan.
	if hasIOMappings(c.process(ei.ProcessDefKey), ei.ElementId) {
		// A call activity's output mappings are applied against the child instance's
		// variables when the child completes (resumeCaller), not against this
		// element's local scope, so the generic promotion is skipped for it — but its
		// input-mapped local scope is still dropped (ADR-0076).
		if c.process(ei.ProcessDefKey).Node(ei.ElementId).Type != compiler.TypeCallActivity {
			applyOutputMappings(c, c.cmd.Key, ei)
		}
		dropLocalScope(c, c.cmd.Key)
	}
	// An inner multi-instance iteration does not take the node's outgoing flow (the
	// body owns it): it completes, decrementing the body's active-child counter, and
	// the body completes once the last iteration drains (ADR-0077).
	if ei.MultiInstance == miInner {
		finishMultiInstanceIteration(c, c.cmd.Key, ei)
		return
	}
	// A completing multi-instance body promotes its assembled output collection to the
	// enclosing scope and drops its own locals before it takes its outgoing flow, so
	// downstream FEEL sees the collected list (ADR-0077).
	if ei.MultiInstance == miBody {
		promoteMultiInstanceOutput(c, c.cmd.Key, ei)
	}
	c.p.behavior(ei.BpmnElementType).OnCompleting(c, c.cmd.Key, ei)
	if c.process(ei.ProcessDefKey).Node(ei.ElementId).BoundaryCount > 0 {
		disarmBoundaryEvents(c, c.cmd.Key, ei.ProcessInstanceKey)
	}
}

// applyDataOutputAssociations evaluates a completing activity's data-output
// associations (ADR-0058): for each, it computes the written value from the
// association's FEEL expression over the instance's variables (a nil expression is
// a state-only transition that keeps the object's current value), and emits a
// DataObjectStateChanged event moving the object into the association's target data
// state (an unset target keeps the object's current state). The value and state ride
// in the event, so applyToState re-applies them on replay without re-evaluating
// (invariants I4/I6) — exactly as a script task's result variable does.
func applyDataOutputAssociations(c *ProcessingContext, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	assocs := cp.DataOutputAssociations(ei.ElementId)
	for i := range assocs {
		a := &assocs[i]
		name := cp.Intern(a.DataObject)
		cur := c.GetDataObject(ei.ProcessInstanceKey, name)

		out := model.DataObjectValue{ScopeKey: ei.ProcessInstanceKey, Name: name}
		switch {
		case a.Value != nil:
			result, err := a.Value.Eval(bindInputs(c, a.Value.Inputs(), ei.ProcessInstanceKey))
			if err != nil {
				// Incidents are not modeled yet; FEEL is null-propagating, so a failed
				// evaluation writes null rather than halting the processor.
				result = expr.Null
			}
			if a.TargetPath >= 0 {
				// Write only one member of the (structured) object, keeping the rest
				// (ADR-0060): read the current JSON, set the member to the result, and
				// write the merged canonical value back.
				leaf, ok := expr.ToJSON(result)
				if !ok {
					leaf = "null"
				}
				current := ""
				if cur != nil && cur.Kind == model.VarJSON {
					current = cur.Text
				}
				if merged, err := setJSONMember(current, cp.Intern(a.TargetPath), leaf); err == nil {
					out.Kind, out.Text = model.VarJSON, merged
				} else if cur != nil {
					out.Kind, out.Bool, out.Text = cur.Kind, cur.Bool, cur.Text
				}
			} else {
				kind, b, text := expr.Classify(result)
				out.Kind, out.Bool, out.Text = toVarKind(kind), b, text
			}
		case cur != nil:
			// State-only transition: keep the object's current value.
			out.Kind, out.Bool, out.Text = cur.Kind, cur.Bool, cur.Text
		}

		if a.TargetState >= 0 {
			out.State = cp.Intern(a.TargetState)
		} else if cur != nil {
			out.State = cur.State // no target state: keep the current one
		}

		c.AppendDataObjectEvent(model.IntentDataObjectStateChanged, out)
	}
}

// setJSONMember returns the canonical JSON of the object `current` with the dotted
// member `path` set to the pre-encoded canonical JSON `leaf`, creating intermediate
// objects as needed (ADR-0060). A `current` that is empty or not a JSON object
// starts from an empty object — so writing a member into an unset data object creates
// it. Numbers are decoded as json.Number so decimals stay exact, and the result
// marshals with sorted keys, matching the canonical form (ADR-0037).
func setJSONMember(current, path, leaf string) (string, error) {
	root := map[string]any{}
	if strings.TrimSpace(current) != "" {
		dec := json.NewDecoder(strings.NewReader(current))
		dec.UseNumber()
		var v any
		if dec.Decode(&v) == nil {
			if m, ok := v.(map[string]any); ok {
				root = m
			}
		}
	}
	parts := strings.Split(path, ".")
	m := root
	for i, p := range parts {
		if i == len(parts)-1 {
			m[p] = json.RawMessage(leaf)
			break
		}
		child, ok := m[p].(map[string]any)
		if !ok {
			child = map[string]any{}
			m[p] = child
		}
		m = child
	}
	b, err := json.Marshal(root)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// handleJobCompleted retires the job, writes the worker's output variables into
// the job's process instance scope, and tells its element to complete. The
// outputs (carried on the completion command as StartVars) are written before the
// element completes so a downstream condition can read them; the values are frozen
// into VariableCreated events, so replay re-applies them rather than re-running
// the worker (invariant I6). Writing here mirrors how instance creation seeds its
// start variables — deterministic state mutation from already-decided data.
func handleJobCompleted(c *ProcessingContext) {
	job := c.GetJob(c.cmd.Key)
	if job == nil {
		return // already gone or never existed; nothing to do
	}
	c.AppendJobEvent(c.cmd.Key, model.IntentJobCompleted, *job)

	// The worker's output variables land in the job's element scope: its
	// activity-local scope when the task has output mappings (so those mappings can
	// read the raw result before it is dropped on completion), otherwise the process
	// scope — the pre-ADR-0068 merge behaviour. Frozen into VariableCreated events,
	// so replay re-applies them rather than re-running the worker (invariant I6).
	resultScope := job.ProcessInstanceKey
	if ei := c.GetElementInstance(job.ElementInstanceKey); ei != nil {
		resultScope = ioResultScope(c.process(ei.ProcessDefKey), job.ElementInstanceKey, ei)
	}
	for i := range c.cmd.StartVars {
		v := c.cmd.StartVars[i]
		v.ScopeKey = resultScope
		c.AppendVariableEvent(model.IntentVariableCreated, v)
	}

	// A business rule task's worker evaluates its decision off the processor
	// goroutine and rides the inputs/outputs/trace back on the completion. Freeze it
	// into a history event so an operator can inspect how the decision was made, live
	// and after the fact (ADR-0066). The worker sees the job, so its keys are set;
	// stamp the scope from the authoritative job in case the worker left it zero.
	if d := c.cmd.Decision; d != nil {
		dv := *d
		dv.ProcessInstanceKey = job.ProcessInstanceKey
		dv.ElementInstanceKey = job.ElementInstanceKey
		c.AppendDecisionEvaluationEvent(dv)
	}

	if ei := c.GetElementInstance(job.ElementInstanceKey); ei != nil {
		c.AppendElementCommand(job.ElementInstanceKey, model.IntentCompleting, *ei)
	}
}

// handleVariablesModify applies an operator's external variable write to a running
// instance (ADR-0095). It is the manual counterpart to the variable writes a model
// makes for itself — start variables, job outputs, io-mappings — reusing the same
// VariableCreated/VariableUpdated events over the same applyToState, so the change
// is durable, replayable (invariant I6), and recorded in the instance's variable
// timeline as the audit trail, never a raw store write.
//
// The command carries the process instance in Key and the target scope in
// Value.variable.ScopeKey (0 means the instance root). It writes only when the
// instance is live and the scope belongs to it, so a correction to a finished
// instance or a foreign scope is a safe no-op. It deliberately does not re-drive the
// token: a variable a gateway has already routed on is not re-evaluated, only its
// stored value changes.
func handleVariablesModify(c *ProcessingContext) {
	piKey := c.cmd.Key
	if c.GetProcessInstance(piKey) == nil {
		return // instance finished or never existed: nothing to modify
	}
	scope := c.cmd.Value.variable.ScopeKey
	if scope == 0 {
		scope = piKey
	}
	if scope != piKey {
		// A non-root target must be a live element instance of this instance (a
		// subprocess or multi-instance body local scope); anything else is rejected so
		// a stray key cannot orphan variables under a scope no FEEL lookup reaches.
		if ei := c.GetElementInstance(scope); ei == nil || ei.ProcessInstanceKey != piKey {
			return
		}
	}
	for i := range c.cmd.StartVars {
		v := c.cmd.StartVars[i]
		v.ScopeKey = scope
		// Created for a name new to the scope, Updated for one already present — the
		// honest intent for an operator override, though applyToState upserts either
		// way. GetVariable reads through the in-flight transaction, so it sees a write
		// made earlier in this same call.
		intent := model.IntentVariableCreated
		if c.GetVariable(scope, v.Name) != nil {
			intent = model.IntentVariableUpdated
		}
		c.AppendVariableEvent(intent, v)
		// Record who made this override, alongside the variable event, so the "who
		// changed it" trail is durable and replayable (ADR-0098). The actor rides in on
		// the command; the value mirrors what was written so the audit row is
		// self-contained.
		c.AppendVariableAuditEvent(model.VariableAuditValue{
			ProcessInstanceKey: piKey,
			ScopeKey:           scope,
			Actor:              c.cmd.Actor,
			Name:               v.Name,
			Kind:               v.Kind,
			Bool:               v.Bool,
			Text:               v.Text,
		})
	}
}

// handleJobFailed applies a worker's failure report for a job (ADR-0061). The
// command carries the remaining retry count (in the job payload) and a failure
// message (in the incident payload). The job's retries are set to that count and
// the job is re-emitted: with retries left it returns to the activatable index for
// another attempt; with none it parks off the index and an incident is raised on
// its element instance, holding the token there until an operator resolves it.
// Failing a job that is gone (already completed or canceled) is a no-op.
func handleJobFailed(c *ProcessingContext) {
	job := c.GetJob(c.cmd.Key)
	if job == nil {
		return
	}
	job.Retries = c.cmd.Value.job.Retries
	// A worker-requested retry backoff (ADR-0111): with retries left and a positive delay,
	// hold the job off the activatable index (RetryDueDate keeps PutJob from indexing it) and
	// arm a retry timer that re-activates it when the backoff elapses. The due date is read
	// from the clock here and frozen into the event (I6). A zero backoff is the pre-0111 path.
	if job.Retries > 0 && c.cmd.RetryBackoff > 0 {
		job.RetryDueDate = c.Now() + c.cmd.RetryBackoff
		c.AppendJobEvent(c.cmd.Key, model.IntentJobFailed, *job)
		c.AppendTimerEvent(c.NewKey(), model.IntentTimerCreated, model.TimerValue{
			ProcessInstanceKey: job.ProcessInstanceKey,
			JobKey:             c.cmd.Key,
			DueDate:            job.RetryDueDate,
		})
		return
	}
	c.AppendJobEvent(c.cmd.Key, model.IntentJobFailed, *job)
	if job.Retries <= 0 {
		var elementId int32
		if ei := c.GetElementInstance(job.ElementInstanceKey); ei != nil {
			elementId = ei.ElementId
		}
		c.AppendIncidentEvent(model.IntentIncidentCreated, model.IncidentValue{
			ProcessInstanceKey: job.ProcessInstanceKey,
			ElementInstanceKey: job.ElementInstanceKey,
			JobKey:             c.cmd.Key,
			ElementId:          elementId,
			RaisedAt:           c.Now(),
			Message:            c.cmd.Value.incident.Message,
		})
	}
}

// reactivateJobAfterBackoff re-activates a job whose retry-backoff timer just fired (ADR-0111):
// it clears the job's RetryDueDate and re-emits it, so PutJob returns it to the activatable
// index, then notifies workers. A job that is gone (its instance was cancelled during the
// backoff) is a no-op — the timer self-retires.
func reactivateJobAfterBackoff(c *ProcessingContext, jobKey uint64) {
	job := c.GetJob(jobKey)
	if job == nil {
		return
	}
	job.RetryDueDate = 0
	c.AppendJobEvent(jobKey, model.IntentJobCreated, *job)
	c.NotifyJobAvailable(job.JobType)
}

// handleJobError handles a worker throwing a BPMN error from its job (ADR-0089): it
// cancels the job (the token neither retries nor completes) and propagates the error from
// the job's element to the nearest matching error handler. A matching error boundary on the
// service task — or an enclosing scope — catches it; uncaught, it raises an incident. The
// thrown code rides in the command's incident.Message (a transient command carrier).
// Throwing on a job that is gone is a no-op.
func handleJobError(c *ProcessingContext) {
	job := c.GetJob(c.cmd.Key)
	if job == nil {
		return
	}
	c.AppendJobEvent(c.cmd.Key, model.IntentJobCanceled, *job)
	propagateError(c, job.ElementInstanceKey, c.cmd.Value.incident.Message)
}

// handleIncidentResolved clears an incident and resumes the work it blocked
// (ADR-0061). The command is keyed by the incident's element instance and carries
// a positive retry count: the incident is deleted and its job re-created with that
// many retries, so the job returns to the activatable index and a worker retries it
// — the same path a fresh job takes. If the cause is unfixed the job fails again
// and a new incident is raised. Resolving an incident that is gone is a no-op.
func handleIncidentResolved(c *ProcessingContext) {
	inc := c.GetIncident(c.cmd.Key)
	if inc == nil {
		return
	}
	c.AppendIncidentEvent(model.IntentIncidentResolved, *inc)
	if inc.JobKey == 0 {
		// A timer incident carries no job (ADR-0064): re-arm the parked catch/boundary
		// element instead of re-creating one.
		rearmTimerElement(c, inc.ElementInstanceKey)
		return
	}
	job := c.GetJob(inc.JobKey)
	if job == nil {
		return // the job vanished (e.g. its instance was canceled); nothing to resume
	}
	job.Retries = c.cmd.Value.job.Retries
	c.AppendJobEvent(inc.JobKey, model.IntentJobCreated, *job)
	c.NotifyJobAvailable(job.JobType)
}

// rearmTimerElement re-runs the timer arm for a parked catch/boundary element
// whose FEEL schedule failed and raised an incident (ADR-0064). Re-evaluated
// against the instance's current variables, the schedule either resolves — a
// timer is created and the token waits normally — or fails again, raising a fresh
// incident (resolve is a genuine retry, not a blind clear). A vanished element
// (its instance was canceled) is a harmless no-op.
func rearmTimerElement(c *ProcessingContext, elKey uint64) {
	ei := c.GetElementInstance(elKey)
	if ei == nil {
		return
	}
	cp := c.process(ei.ProcessDefKey)
	if cp == nil {
		return
	}
	node := cp.Node(ei.ElementId)
	switch node.Type {
	case compiler.TypeTimerCatchEvent:
		armOneShotTimer(c, elKey, ei, cp.TimerCatch(node.Detail).Schedule)
	case compiler.TypeBoundaryEvent:
		if d := cp.BoundaryEvent(node.Detail); d.Kind == compiler.BoundaryTimer {
			armOneShotTimer(c, elKey, ei, d.Schedule)
		}
	case compiler.TypeEventSubProcessStart:
		// A recurring event-subprocess timer trigger whose FEEL cycle failed to re-arm
		// (ADR-0111): re-run its arm, which re-resolves against current variables.
		if d := cp.EventSubProcess(node.EventSub); d.Kind == compiler.BoundaryTimer {
			armOneShotTimer(c, elKey, ei, d.Schedule)
		}
	}
}

// handleJobAssigned rewrites a job's user-task assignee (claim sets it, unclaim
// clears it) and re-emits the job so the change is persisted through the one
// applyToState (ADR-0042). Assigning a job that is gone — already completed or
// never existed — is a no-op. The command carries only the new assignee; every
// other job field is preserved from the stored job.
func handleJobAssigned(c *ProcessingContext) {
	job := c.GetJob(c.cmd.Key)
	if job == nil {
		return
	}
	job.Assignee = c.cmd.Value.job.Assignee
	c.AppendJobEvent(c.cmd.Key, model.IntentJobAssigned, *job)
}

// handleTimerTriggered fires a due timer. The command carries the timer value
// (supplied by TriggerDueTimers), so no extra read is needed. A start timer (no
// owning instance, ADR-0051) instantiates its definition and, if it recurs, arms
// its next occurrence; an instance timer tells its waiting element to complete.
func handleTimerTriggered(c *ProcessingContext) {
	timer := c.cmd.Value.timer
	c.AppendTimerEvent(c.cmd.Key, model.IntentTimerTriggered, timer)
	if timer.JobKey != 0 {
		// A retry-backoff timer (ADR-0111): its backoff has elapsed, so re-activate the failed
		// job rather than firing an element. Checked first — a retry timer carries no element.
		reactivateJobAfterBackoff(c, timer.JobKey)
		return
	}
	if timer.ProcessInstanceKey == 0 {
		fireStartTimer(c, timer)
		return
	}
	if timer.ElementInstanceKey == 0 {
		// An instance-expiry (TTL) timer owns no element — its firing terminates the whole
		// instance through the ordinary cancellation path (ADR-0085). Terminating an
		// instance already gone (completed or cancelled first) is a no-op, so a race with
		// normal completion is safe.
		if pi := c.GetProcessInstance(timer.ProcessInstanceKey); pi != nil {
			c.AppendProcessInstanceCommand(timer.ProcessInstanceKey, model.IntentTerminating, *pi)
		}
		return
	}
	ei := c.GetElementInstance(timer.ElementInstanceKey)
	if ei == nil {
		return // element gone (cancelled/completed/host-interrupted): self-retire
	}
	if raw, ok := recurringBoundarySchedule(c, ei); ok {
		fireRecurringOrIncident(c, timer, ei, raw, fireRecurringBoundary)
		return
	}
	if raw, ok := recurringEventSubSchedule(c, ei); ok {
		fireRecurringOrIncident(c, timer, ei, raw, fireRecurringEventSub)
		return
	}
	c.AppendElementCommand(timer.ElementInstanceKey, model.IntentCompleting, *ei)
}

// fireRecurringOrIncident resolves a recurring timer's (possibly FEEL) schedule on the
// occurrence that just fired and either fires it (arming the next occurrence) or, if the FEEL
// can't be re-evaluated against the instance's current variables, raises the ADR-0064 job-less
// incident and parks — instead of silently ceasing to recur (ADR-0111). Resolving the incident
// re-arms the element (rearmTimerElement).
func fireRecurringOrIncident(c *ProcessingContext, timer model.TimerValue, ei *model.ElementInstanceValue, raw compiler.TimerSchedule, fire func(*ProcessingContext, model.TimerValue, *model.ElementInstanceValue, compiler.TimerSchedule)) {
	sched, err := resolveScheduleErr(c, raw, ei.ProcessInstanceKey)
	if err != nil {
		raiseTimerScheduleIncident(c, timer.ElementInstanceKey, ei, err)
		return
	}
	fire(c, timer, ei, sched)
}

// recurringBoundarySchedule reports whether ei is a non-interrupting boundary timer whose
// schedule recurs, and returns its *unresolved* schedule to re-arm from (ADR-0054). This is a
// pure compiled-type check; the caller (fireRecurringOrIncident) resolves the schedule — a FEEL
// cycle is re-evaluated on each occurrence against the instance's current variables (ADR-0056),
// and a failure raises an incident rather than silently stopping (ADR-0111).
func recurringBoundarySchedule(c *ProcessingContext, ei *model.ElementInstanceValue) (compiler.TimerSchedule, bool) {
	cp := c.process(ei.ProcessDefKey)
	node := cp.Node(ei.ElementId)
	if node.Type != compiler.TypeBoundaryEvent {
		return compiler.TimerSchedule{}, false
	}
	d := cp.BoundaryEvent(node.Detail)
	if d.Kind != compiler.BoundaryTimer || d.Interrupting || !d.Schedule.Repeats() {
		return compiler.TimerSchedule{}, false
	}
	return d.Schedule, true
}

// fireRecurringBoundary spawns the boundary's outgoing (reminder) token without
// completing the boundary element instance — it stays armed — then arms the next
// occurrence, re-anchored to the clock and frozen into the event (I6), counting a
// finite Rn cycle down. When the count runs out it stops arming; the idle boundary
// is removed when its host completes (existing disarm). If its host is later
// interrupted or completes, the last re-armed timer finds no instance and does
// nothing (ADR-0054).
func fireRecurringBoundary(c *ProcessingContext, timer model.TimerValue, ei *model.ElementInstanceValue, sched compiler.TimerSchedule) {
	takeOutgoingFlows(c, ei)
	if timer.Repetitions == 0 {
		return // finite cycle exhausted: stop arming, leave the boundary idle
	}
	next, ok := sched.NextDue(c.Now())
	if !ok {
		return
	}
	reps := timer.Repetitions
	if reps > 0 {
		reps-- // count down a finite cycle; an infinite one (-1) stays -1
	}
	c.AppendTimerEvent(c.NewKey(), model.IntentTimerCreated, model.TimerValue{
		ProcessInstanceKey: ei.ProcessInstanceKey,
		ElementInstanceKey: timer.ElementInstanceKey,
		TargetElementId:    ei.ElementId,
		DueDate:            next,
		Repetitions:        reps,
	})
}

// recurringEventSubSchedule reports whether ei is a non-interrupting timer-triggered
// event-subprocess trigger whose schedule recurs (a cycle), and returns the concrete
// schedule to re-arm from — the event-subprocess analog of recurringBoundarySchedule
// (ADR-0082). A one-shot (duration/date) or interrupting trigger fires once through the
// generic Completing path instead; ok is false if the schedule can't resolve.
func recurringEventSubSchedule(c *ProcessingContext, ei *model.ElementInstanceValue) (compiler.TimerSchedule, bool) {
	if ei.BpmnElementType != uint8(compiler.TypeEventSubProcessStart) {
		return compiler.TimerSchedule{}, false
	}
	cp := c.process(ei.ProcessDefKey)
	d := cp.EventSubProcess(cp.Node(ei.ElementId).EventSub)
	if d.Kind != compiler.BoundaryTimer || d.Interrupting || !d.Schedule.Repeats() {
		return compiler.TimerSchedule{}, false
	}
	return d.Schedule, true
}

// fireRecurringEventSub fires a recurring non-interrupting event-subprocess timer: it
// activates a fresh handler run without completing the trigger (which stays armed), then
// arms the next occurrence — re-anchored to the clock, frozen into the event (I6), and
// counting a finite Rn cycle down. When the count runs out it stops arming; the idle
// trigger is disarmed when its scope completes. It mirrors fireRecurringBoundary (ADR-0054).
func fireRecurringEventSub(c *ProcessingContext, timer model.TimerValue, ei *model.ElementInstanceValue, sched compiler.TimerSchedule) {
	activateEventSubHandler(c, ei, ei.FlowScopeKey)
	if timer.Repetitions == 0 {
		return // finite cycle exhausted: stop arming, leave the trigger idle
	}
	next, ok := sched.NextDue(c.Now())
	if !ok {
		return
	}
	reps := timer.Repetitions
	if reps > 0 {
		reps-- // count down a finite cycle; an infinite one (-1) stays -1
	}
	c.AppendTimerEvent(c.NewKey(), model.IntentTimerCreated, model.TimerValue{
		ProcessInstanceKey: ei.ProcessInstanceKey,
		ElementInstanceKey: timer.ElementInstanceKey,
		TargetElementId:    ei.ElementId,
		DueDate:            next,
		Repetitions:        reps,
	})
}

// fireStartTimer instantiates the definition a due start timer names — through the
// same create-instance command an API create uses, so the instance's events and
// recovery are identical (ADR-0035) — then, if the timer recurs, arms its next
// occurrence with a due date re-anchored to the current clock (so a long outage
// does not replay a backlog) and frozen into the arming event (I6). A timer whose
// definition was undeployed or superseded finds no compiled process and stops.
func fireStartTimer(c *ProcessingContext, timer model.TimerValue) {
	cp := c.process(timer.ProcessDefKey)
	if cp == nil {
		return
	}
	c.AppendCreateInstanceCommand(timer.ProcessDefKey, nil, "") // a timer start has no correlation key
	if timer.Repetitions == 0 {
		return // one-shot (duration/date) or a finite cycle that has run out
	}
	node := cp.Node(timer.TargetElementId)
	if node.Type != compiler.TypeTimerStartEvent {
		return
	}
	// A start-event FEEL schedule is constant (the compiler enforces it), so it
	// resolves against an empty scope (ADR-0056).
	sched, ok := resolveSchedule(c, cp.TimerStart(node.Detail).Schedule, 0)
	if !ok {
		return
	}
	next, ok := sched.NextDue(c.Now())
	if !ok {
		return
	}
	reps := timer.Repetitions
	if reps > 0 {
		reps-- // count down a finite cycle; an infinite one (-1) stays -1
	}
	c.AppendTimerEvent(c.NewKey(), model.IntentTimerCreated, model.TimerValue{
		ProcessDefKey:   timer.ProcessDefKey,
		TargetElementId: timer.TargetElementId,
		DueDate:         next,
		Repetitions:     reps,
	})
}

// handleTimerStartArm arms a freshly deployed definition's timer start events and
// retires any that a prior version of the same process left armed, so only the
// latest version's schedule is active (ADR-0051). It runs once per fresh deploy,
// never on recovery — commands are not replayed (I6), and the restored
// TimerCreated events already hold the armed timers. The command carries the new
// definition key in cmd.Key.
func handleTimerStartArm(c *ProcessingContext) {
	defKey := c.cmd.Key
	cp := c.process(defKey)
	if cp == nil {
		return
	}
	// Retire a prior version's start timers for the same process id, and note which
	// of this definition's elements are already armed (idempotency if arm repeats).
	armed := map[int32]bool{}
	c.ForEachStartTimer(func(key uint64, v model.TimerValue) {
		if v.ProcessDefKey == defKey {
			armed[v.TargetElementId] = true
			return
		}
		if other := c.process(v.ProcessDefKey); other != nil && other.ProcessId() == cp.ProcessId() {
			c.AppendTimerEvent(key, model.IntentTimerCanceled, v)
		}
	})
	now := c.Now()
	for _, s := range cp.TimerStartEvents() {
		if armed[s.ElementId] {
			continue
		}
		// A start-event FEEL schedule is constant (compiler-enforced), so it
		// resolves against an empty scope; an unresolvable one is not armed (ADR-0056).
		sched, ok := resolveSchedule(c, s.Schedule, 0)
		if !ok {
			continue
		}
		c.AppendTimerEvent(c.NewKey(), model.IntentTimerCreated, model.TimerValue{
			ProcessDefKey:   defKey,
			TargetElementId: s.ElementId,
			DueDate:         sched.FirstDue(now),
			Repetitions:     sched.Repetitions,
		})
	}
}

// handleMessagePublished correlates an externally published message (from the
// HTTP API) against the open subscriptions. The command carries the message name
// and correlation key (in its subscription payload) and any payload variables (in
// StartVars); correlation is the same path a message throw event uses.
func handleMessagePublished(c *ProcessingContext) {
	pub := c.cmd.Value.subscription
	// A publish from an external event source (ADR-0075) carries a source id and
	// sequence; deduplicate it against the source's durable high-water mark so an
	// at-least-once replay is skipped rather than re-correlated (which would
	// double-start a message-start process). An API/throw publish has an empty
	// source id and is never deduplicated. The high-water advance rides this same
	// batch as the correlate/start effects, so one fsync commits them atomically.
	src := c.cmd.Value.inbound
	if src.SourceID != "" {
		hw, err := c.tx.InboundHighWater(src.SourceID)
		if err != nil {
			c.p.fail(err)
			return
		}
		if src.SourceSeq <= hw {
			return // already applied; a duplicate delivery is a no-op
		}
		c.AppendInboundDeliveryEvent(src)
	}
	// An API publish has no sending instance, so the recorded flow's sender is 0.
	correlateMessage(c, pub.MessageName, pub.CorrelationKey, c.cmd.StartVars, 0)
}

// behavior resolves the behavior for a BPMN element type.
func (p *Processor) behavior(bpmnType uint8) bpmnBehavior {
	return p.behaviors[bpmnType]
}

// --- element behaviors ---

// completeAndTakeFlows is the default OnCompleting: emit Completed, then
// activate the targets of every outgoing flow.
func completeAndTakeFlows(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	// A catch event armed by an event-based gateway wins the deferred choice when it fires:
	// before it continues, it cancels every other armed catch in its race group, so only this
	// branch proceeds (ADR-0110). A loser whose Completing was already queued when a sibling
	// cancelled it finds itself gone and drops out (a same-event double-fire). The guard is a
	// no-op for every element not armed by an event gateway (EventGatewayKey == 0).
	if ei.EventGatewayKey != 0 {
		if c.GetElementInstance(key) == nil {
			return
		}
		cancelEventGatewaySiblings(c, ei.ProcessInstanceKey, ei.EventGatewayKey, key)
	}
	c.AppendElementEvent(key, model.IntentCompleted, *ei)
	recordCompensable(c, key, ei)
	cp := c.process(ei.ProcessDefKey)
	if len(cp.Outgoing(ei.ElementId)) == 0 {
		// A node that completes with no outgoing flow is a dead end — the token has left
		// the scope like an implicit end, so check the scope for completion. Today this is
		// only a compensation handler (ADR-0103): an off-flow activity a compensation throw
		// activates, which must let its scope finish once it and its siblings drain.
		completeScope(c, ei.FlowScopeKey)
		return
	}
	takeOutgoingFlows(c, ei)
}

// completeScope finishes a scope whose last child just left. It is called after an
// end event's Completed decrements the scope's active-child counter: if the counter
// reached zero, the scope has drained and its owner completes. The owner is either
// the process-instance root (no element instance for the scope key) or an embedded
// subprocess element instance (ADR-0074). For a subprocess it schedules the
// container's Completing, so the subprocess behavior completes it and takes its
// outgoing flow — which may in turn drain the parent scope. The decision is a pure
// function of state (GetElementInstance / ActiveChildren), so recovery reconstructs
// it identically (I4/I6). A no-op while the scope still has active children.
func completeScope(c *ProcessingContext, scope uint64) {
	if c.ActiveChildren(scope) != 0 {
		return
	}
	if ei := c.GetElementInstance(scope); ei != nil {
		// A subprocess scope: disarm its event-subprocess triggers (they are uncounted, so
		// the scope drained with them still armed), then drive its container to Completing.
		disarmEventSubprocesses(c, ei.ProcessInstanceKey, scope)
		c.AppendElementCommand(scope, model.IntentCompleting, *ei)
		return
	}
	// The process-instance root scope: the instance itself completes. If it is a
	// child of a call activity, its completion resumes the caller (ADR-0076).
	if pi := c.GetProcessInstance(scope); pi != nil {
		disarmEventSubprocesses(c, scope, scope)
		// Finishing before the TTL: retire the instance's expiry timer in the same batch
		// so a completed instance leaves no armed timer behind (ADR-0085).
		cancelExpiryTimer(c, scope, pi.ExpiryDueDate)
		c.AppendProcessInstanceEvent(scope, model.IntentCompleted, *pi)
		if pi.ParentElementInstanceKey != 0 {
			resumeCaller(c, scope, pi.ParentElementInstanceKey)
		}
	}
}

// activateElement schedules activation of a fresh element instance on targetId,
// scoped like ei. It is the single "take a flow" primitive the flow-taking
// behaviors share.
func activateElement(c *ProcessingContext, ei *model.ElementInstanceValue, flowID int32, fork bool) {
	targetId := c.process(ei.ProcessDefKey).Flow(flowID).Target
	target := c.process(ei.ProcessDefKey).Node(targetId)
	key := c.NewKey()
	tokenID, parentID := ei.TokenID, uint64(0)
	if tokenID == 0 || fork {
		parentID, tokenID = ei.TokenID, key
	}
	// A flow into a multi-instance activity activates its body — the scope that seeds
	// the iterations (ADR-0077); an ordinary node activates with no role.
	miRole := miNone
	if target.MultiInstance >= 0 {
		miRole = miBody
	}
	c.AppendElementCommand(key, model.IntentActivating, model.ElementInstanceValue{
		ProcessInstanceKey: ei.ProcessInstanceKey,
		ProcessDefKey:      ei.ProcessDefKey,
		ElementId:          targetId,
		FlowScopeKey:       ei.FlowScopeKey,
		BpmnElementType:    uint8(target.Type),
		MultiInstance:      miRole,
		TokenID:            tokenID,
		ParentTokenID:      parentID,
		SourceFlowId:       flowID,
	})
}

// armBoundaryEvents activates a waiting element instance for each boundary event
// attached to the host activity that just activated (ei/hostKey). Each armed
// instance carries AttachedToKey = hostKey so it can later interrupt its host, and
// its own OnActivated arms the timer/subscription it waits on. A no-op for a node
// with no attached boundary events (ADR-0040).
func armBoundaryEvents(c *ProcessingContext, hostKey uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	for _, beID := range cp.BoundaryEvents(ei.ElementId) {
		// A compensation boundary is inert: it is compile-time metadata linking the host
		// to its compensation handler, never an armed waiting instance (ADR-0103). Arming
		// it would create an instance that disarmBoundaryEvents retires exactly when the
		// host completes — the moment compensation becomes available — so skip it.
		if cp.BoundaryEvent(cp.Node(beID).Detail).Kind == compiler.BoundaryCompensation {
			continue
		}
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

// scopeContains reports whether elKey lies within the scope rooted at ancestor —
// i.e. ancestor appears on elKey's flow-scope chain. It is how an interrupted
// subprocess finds the inner tokens it must terminate. Bounded by scope-chain
// depth (the same defensive bound variable resolution walks, ADR-0074).
func scopeContains(c *ProcessingContext, ancestor, elKey uint64) bool {
	for cur := c.GetElementInstance(elKey); cur != nil; cur = c.GetElementInstance(cur.FlowScopeKey) {
		if cur.FlowScopeKey == ancestor {
			return true
		}
	}
	return false
}

// terminateChildInstance tears down the child process instance a call-activity
// element started, when that element is terminated (a cancel of the caller, or an
// interrupting boundary event). The child is found by its persisted parent link, so
// this stays a pure function of committed state (I6); terminating it enqueues the
// same Terminating command an API cancel does, which recurses into the child's own
// element instances — and, through this helper, into any call activities the child
// itself is running. A no-op for an element with no live child (a plain activity, or
// a call activity whose child already completed).
func terminateChildInstance(c *ProcessingContext, callElKey uint64, elemType uint8) {
	if elemType != uint8(compiler.TypeCallActivity) {
		return
	}
	var children []uint64
	c.ForEachActiveProcessInstance(func(piKey uint64, pi *model.ProcessInstanceValue) {
		if pi.ParentElementInstanceKey == callElKey {
			children = append(children, piKey)
		}
	})
	for _, ck := range children {
		if pi := c.GetProcessInstance(ck); pi != nil {
			c.AppendProcessInstanceCommand(ck, model.IntentTerminating, *pi)
		}
	}
}

// terminateScope terminates every active element instance inside the scope rooted
// at scopeKey (a subprocess), recursively, cancelling any job each holds. Each
// Terminated event drops the element and decrements its own scope's child counter
// (apply.go). Victims are collected before any termination so the scope-chain walk
// sees an intact tree. A no-op for a plain activity — nothing is scoped under it.
func terminateScope(c *ProcessingContext, procKey, scopeKey uint64) {
	terminateScopeExcept(c, procKey, scopeKey, 0)
}

// terminateScopeExcept is terminateScope but spares one element instance (exceptKey). A cancel
// end event uses it to terminate the transaction's other live tokens while it is itself still
// being processed — so the scope drains to just the compensation handlers it then starts
// (ADR-0108). exceptKey == 0 spares nothing (the plain terminateScope). Completed compensable
// activities are not live instances, so their compensable records are untouched here; they are
// dropped only when the transaction element itself tears down (apply.go).
func terminateScopeExcept(c *ProcessingContext, procKey, scopeKey, exceptKey uint64) {
	var victims []uint64
	c.ForEachElementInstance(procKey, func(elKey uint64) {
		if elKey != exceptKey && scopeContains(c, scopeKey, elKey) {
			victims = append(victims, elKey)
		}
	})
	for _, k := range victims {
		ei := c.GetElementInstance(k)
		if ei == nil {
			continue
		}
		terminateChildInstance(c, k, ei.BpmnElementType)
		if jobKey, ok := c.JobOfElement(k); ok {
			if job := c.GetJob(jobKey); job != nil {
				c.AppendJobEvent(jobKey, model.IntentJobCanceled, *job)
			}
		}
		c.AppendElementEvent(k, model.IntentTerminated, *ei)
	}
}

// interruptHost terminates the host activity an interrupting boundary event fired
// on: it tears down the host's inner scope (for a subprocess), cancels the host's
// job (if any), terminates the host element instance, and terminates the host's
// other boundary siblings (their timers/subscriptions self-retire). It is
// idempotent — if the host is already gone (it completed, or a sibling boundary
// already interrupted it), it does nothing.
func interruptHost(c *ProcessingContext, hostKey, selfKey uint64) {
	host := c.GetElementInstance(hostKey)
	if host == nil {
		return
	}
	// If the host is a subprocess, its inner tokens must not outlive it: tear down
	// everything inside its scope (recursively) before the host itself. A no-op for
	// a plain activity, which is no one's flow scope (ADR-0074).
	terminateScope(c, host.ProcessInstanceKey, hostKey)
	// If the host is a call activity, its child instance must die with it (ADR-0076).
	terminateChildInstance(c, hostKey, host.BpmnElementType)
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
	flows := cp.Outgoing(ei.ElementId)
	for _, flowID := range flows {
		activateElement(c, ei, flowID, len(flows) > 1)
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
			activateElement(c, ei, flowID, true)
			took = true
			continue
		}
		// Over the gateway's scope chain, matching the exclusive gateway (ADR-0085).
		v, err := f.Condition.Eval(bindInputsChain(c, f.Condition.Inputs(), ei.FlowScopeKey))
		if err == nil && expr.IsTrue(v) {
			activateElement(c, ei, flowID, true)
			took = true
		}
	}
	if !took && defaultFlow >= 0 {
		activateElement(c, ei, defaultFlow, false)
	}
}

// builtinProcessInstanceKey is a reserved FEEL identifier that resolves to the
// evaluating instance's own process-instance key, as a string so the full 64-bit
// key survives exactly (a FEEL number is a float and would lose precision on
// large keys). It lets a model correlate a reply back to the requesting instance
// without a hand-authored business key (ADR-0035). A process variable of the same
// name is shadowed by the built-in.
const builtinProcessInstanceKey = "processInstanceKey"

// resolveSchedule returns the concrete schedule to compute a timer from: a fixed
// schedule as-is, or a FEEL schedule (ADR-0055/0056) evaluated against scope and
// reduced to the duration/date/cycle it names. ok is false if a FEEL
// expression can't be evaluated or its result isn't valid for the field. scope is
// the instance whose variables the expression reads; it is 0 for a start-event
// timer, whose FEEL must be constant (the compiler enforces this), so an empty
// scope still resolves it. Variables and the clock are read at command time and
// frozen into the event (invariants I4/I6).
func resolveSchedule(c *ProcessingContext, s compiler.TimerSchedule, scope uint64) (compiler.TimerSchedule, bool) {
	r, err := resolveScheduleErr(c, s, scope)
	return r, err == nil
}

// resolveScheduleErr is resolveSchedule but reports *why* a FEEL schedule could
// not be resolved — an evaluation error, or a result that isn't a usable temporal
// for the field — so a catch/boundary arm can put the reason in an incident's
// message (ADR-0064). A non-FEEL schedule always resolves.
func resolveScheduleErr(c *ProcessingContext, s compiler.TimerSchedule, scope uint64) (compiler.TimerSchedule, error) {
	if !s.IsFeel() {
		return s, nil
	}
	v, err := s.Expr.Eval(bindInputs(c, s.Expr.Inputs(), scope))
	if err != nil {
		return compiler.TimerSchedule{}, err
	}
	// Prefer a first-class FEEL temporal (exact nanoseconds/instant); fall back to
	// the canonical string form for a variable holding an ISO string, or a cycle
	// (ADR-0057).
	r, ok := s.ResolveFeelValue(v)
	if !ok {
		return compiler.TimerSchedule{}, errors.New("result is not a valid " + feelKindName(s.Kind))
	}
	return r, nil
}

// feelKindName names a FEEL schedule's field for an incident message.
func feelKindName(k compiler.TimerScheduleKind) string {
	switch k {
	case compiler.TimerFeelDate:
		return "date"
	case compiler.TimerFeelCycle:
		return "cycle"
	default:
		return "duration"
	}
}

// armOneShotTimer resolves a catch/boundary timer's schedule and appends its
// TimerCreated event. If the schedule is a FEEL expression that can't be
// evaluated, it raises an incident on the element instead of firing the timer
// immediately, so the token parks visibly and an operator can fix the data and
// resolve it (ADR-0064). Either way the element stays Activated; only a created
// timer will ever fire it. The clock and variables are read here (command time)
// and frozen into the event (I4/I6).
func armOneShotTimer(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue, s compiler.TimerSchedule) {
	sched, err := resolveScheduleErr(c, s, ei.ProcessInstanceKey)
	if err != nil {
		raiseTimerScheduleIncident(c, key, ei, err)
		return
	}
	now := c.Now()
	c.AppendTimerEvent(c.NewKey(), model.IntentTimerCreated, model.TimerValue{
		ProcessInstanceKey: ei.ProcessInstanceKey,
		ElementInstanceKey: key,
		TargetElementId:    ei.ElementId,
		DueDate:            sched.FirstDue(now),
		Repetitions:        sched.Repetitions,
	})
}

// raiseTimerScheduleIncident raises a job-less incident on a timer-bearing element whose FEEL
// schedule could not be evaluated (ADR-0064/0111): the initial arm (armOneShotTimer) and a
// recurring re-arm (handleTimerTriggered) share it, so a failure at either point parks the
// element visibly with the same operator-resolvable incident (JobKey stays zero — the marker
// that routes resolution back to rearmTimerElement).
func raiseTimerScheduleIncident(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue, err error) {
	c.AppendIncidentEvent(model.IntentIncidentCreated, model.IncidentValue{
		ProcessInstanceKey: ei.ProcessInstanceKey,
		ElementInstanceKey: key,
		ElementId:          ei.ElementId,
		RaisedAt:           c.Now(),
		Message:            "timer schedule: " + err.Error(),
	})
}

// bindInputs reads the named variables from a scope into a FEEL binding map for
// evaluation. A name absent from the scope is simply left unbound (FEEL null). The
// reserved name processInstanceKey binds to the scope's own key (the built-in
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

// bindInputsChain is bindInputs resolving each name up the scope chain from
// startScope (nearest scope wins), rather than from a single scope (ADR-0068). An
// activity with I/O mappings starts its own FEEL evaluation from its
// activity-local scope, so it sees its input-mapped locals shadowing anything
// inherited; without a local scope (no variables at startScope) it degenerates to
// reading the enclosing scope, exactly like bindInputs. The reserved
// processInstanceKey built-in still binds to startScope's key.
func bindInputsChain(c *ProcessingContext, inputs []string, startScope uint64) map[string]expr.Value {
	if len(inputs) == 0 {
		return nil
	}
	vars := make(map[string]expr.Value, len(inputs))
	var piKey uint64
	havePI := false
	for _, name := range inputs {
		if name == builtinProcessInstanceKey {
			// The built-in is the process-instance key, not the (possibly local)
			// start scope; resolve it lazily from the start scope's element instance.
			if !havePI {
				piKey, havePI = c.processInstanceKeyOfScope(startScope), true
			}
			vars[name] = expr.FromStored(expr.KindString, false, strconv.FormatUint(piKey, 10))
			continue
		}
		if vv := c.ResolveVariable(startScope, name); vv != nil {
			vars[name] = expr.FromStored(toExprKind(vv.Kind), vv.Bool, vv.Text)
		}
	}
	return vars
}

// processInstanceKeyOfScope returns the process-instance key that owns a scope: the
// scope's element instance's ProcessInstanceKey for an activity-local scope, or the
// scope itself when it is the process-instance root (which has no element instance).
func (c *ProcessingContext) processInstanceKeyOfScope(scope uint64) uint64 {
	if ei := c.GetElementInstance(scope); ei != nil {
		return ei.ProcessInstanceKey
	}
	return scope
}

// hasIOMappings reports whether an activity node carries any zeebe:ioMapping input
// or output mappings (ADR-0068). It gates the activity-local-scope machinery so the
// common mapping-free activity keeps reading and writing the process scope exactly
// as before — no local writes, no scope-drop scan.
func hasIOMappings(cp *compiler.CompiledProcess, elementId int32) bool {
	n := cp.Node(elementId)
	return n.IOInCount > 0 || n.IOOutCount > 0
}

// ioResultScope returns the scope an activity's result variables are written to: its
// activity-local scope (the element-instance key) when it has output mappings, so
// the mappings can read the raw result before it is dropped; otherwise the
// activity's enclosing (flow) scope — the process instance for a top-level activity,
// or the subprocess for one nested inside it, so an inner result stays local to its
// subprocess rather than leaking to the process root (ADR-0068/0074). For a
// top-level activity FlowScopeKey == ProcessInstanceKey, so this is unchanged there.
func ioResultScope(cp *compiler.CompiledProcess, elementKey uint64, ei *model.ElementInstanceValue) uint64 {
	// A multi-instance iteration's result is its own — inner-scoped, so each iteration's
	// output element reads this iteration's value and it is dropped with the iteration
	// rather than colliding at the shared body scope (ADR-0077). Otherwise the local
	// scope when the node has output mappings, else the enclosing scope (ADR-0068).
	if ei.MultiInstance == miInner || cp.Node(ei.ElementId).IOOutCount > 0 {
		return elementKey
	}
	return ei.FlowScopeKey
}

// applyInputMappings evaluates an activating activity's zeebe:ioMapping inputs and
// writes each into the activity-local scope (keyed by the element-instance key),
// so the activity's own FEEL and its worker see the mapped locals resolving up the
// scope chain (ADR-0068). Each source is evaluated over the chain from the local
// scope, so a later input can read an earlier one and unmapped names resolve to the
// enclosing scope. The value is frozen into a VariableCreated event, so replay
// re-applies it rather than re-evaluating (invariants I4/I6).
func applyInputMappings(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	for _, m := range cp.IOInputs(ei.ElementId) {
		c.AppendVariableEvent(model.IntentVariableCreated, evalMapping(c, cp, m, key, key))
	}
}

// evalMapping evaluates one I/O mapping's FEEL source over the scope chain from
// evalScope and returns the variable to write at destScope under the mapping's
// interned target name (ADR-0068). FEEL is null-propagating, so a failed evaluation
// yields null rather than halting the processor — the same rule the inline script
// task and data associations follow.
func evalMapping(c *ProcessingContext, cp *compiler.CompiledProcess, m compiler.IOMapping, evalScope, destScope uint64) model.VariableValue {
	result, err := m.Source.Eval(bindInputsChain(c, m.Source.Inputs(), evalScope))
	if err != nil {
		result = expr.Null
	}
	kind, b, text := expr.Classify(result)
	return model.VariableValue{
		ScopeKey: destScope,
		Name:     cp.Intern(m.Target),
		Kind:     toVarKind(kind),
		Bool:     b,
		Text:     text,
	}
}

// applyOutputMappings evaluates a completing activity's zeebe:ioMapping outputs over
// its activity-local scope and promotes each into the parent (flow) scope, so only
// the selected, possibly reshaped values escape the activity (ADR-0068). Sources are
// evaluated here (command processing) and frozen into VariableCreated events, so
// replay re-applies them without re-evaluating (I4/I6). The raw result and input
// locals are removed separately by dropLocalScope.
func applyOutputMappings(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	for _, m := range cp.IOOutputs(ei.ElementId) {
		// Evaluate over the local scope chain (key), promote to the parent (flow) scope.
		c.AppendVariableEvent(model.IntentVariableCreated, evalMapping(c, cp, m, key, ei.FlowScopeKey))
	}
}

// dropLocalScope deletes every variable in an activity's local scope when it
// completes, after output mappings have promoted what escapes (ADR-0068). Each
// deletion is a VariableDeleted event so replay reproduces the drop exactly (I6);
// the scope holds only the activity's input-mapped locals and (with output
// mappings) its raw result, so scratch data never accumulates in the instance.
func dropLocalScope(c *ProcessingContext, key uint64) {
	c.VariablesOfScope(key, func(v model.VariableValue) {
		c.AppendVariableEvent(model.IntentVariableDeleted, v)
	})
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

	// Bind the process variables the expression reads (its inputs) resolving up the
	// activity's scope chain, so it sees any input-mapped locals shadowing inherited
	// values (ADR-0068); with no I/O mappings this reads the instance scope exactly
	// as before. Then evaluate.
	result, err := detail.Expr.Eval(bindInputsChain(c, detail.Expr.Inputs(), key))
	if err != nil {
		// Incidents are not modeled yet (Milestone 2); FEEL is null-propagating,
		// so a failed evaluation yields null rather than halting the processor.
		result = expr.Null
	}

	kind, b, text := expr.Classify(result)
	c.AppendVariableEvent(model.IntentVariableCreated, model.VariableValue{
		ScopeKey: ioResultScope(cp, key, ei),
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
	// A catch schedule is a duration or date (a cycle is rejected at compile time),
	// so it fires once. armOneShotTimer computes the due date here (command
	// processing) and freezes it into the event (I4/I6), or — for an unresolvable
	// FEEL schedule — raises an incident and parks (ADR-0064).
	armOneShotTimer(c, key, ei, detail.Schedule)
	// Stays Activated: no Completing until the timer fires (or the incident resolves).
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
		ProcessDefKey:      ei.ProcessDefKey,
		ElementId:          ei.ElementId,
	})
	// Stays Activated: no Completing until a message correlates.
}

func (messageCatchEventBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	completeAndTakeFlows(c, key, ei)
}

// receiveTaskBehavior: a receive task waits for a correlating message, then continues —
// the message intermediate catch event's semantics in task form (ADR-0102). On activation
// it opens a subscription on the task's (message name, correlation key) and stays Activated;
// a correlating publish or throw drives it to Completing through the same correlateMessage
// path a catch event uses, and it then completes and takes its outgoing flows (running any
// I/O output mappings and data associations, since it is an activity). It reads the
// receive-task detail table rather than the message-catch table; otherwise it is
// messageCatchEventBehavior. An attached boundary event arms and fires unchanged.
type receiveTaskBehavior struct{}

func (receiveTaskBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	detail := cp.ReceiveTask(cp.Node(ei.ElementId).Detail)
	c.AppendMessageSubscriptionEvent(key, model.IntentSubscriptionCreated, model.MessageSubscriptionValue{
		ProcessInstanceKey: ei.ProcessInstanceKey,
		ElementInstanceKey: key,
		MessageName:        detail.MessageName,
		CorrelationKey:     evalCorrelationKey(c, detail.CorrelationKey, ei.ProcessInstanceKey),
		ProcessDefKey:      ei.ProcessDefKey,
		ElementId:          ei.ElementId,
	})
	// Stays Activated: no Completing until a message correlates.
}

func (receiveTaskBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
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
	correlateMessage(c, detail.MessageName, evalCorrelationKey(c, detail.CorrelationKey, ei.ProcessInstanceKey), payload, ei.ProcessInstanceKey)
	c.AppendElementCommand(key, model.IntentCompleting, *ei)
}

func (messageThrowEventBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	completeAndTakeFlows(c, key, ei)
}

// messageEndEventBehavior: an end event that publishes a message, then ends the
// instance (ADR-0052). It is the send-and-stop union of a message throw event and
// a none end event: OnActivated correlates exactly like a throw (reusing the
// throw detail table), and OnCompleting ends the instance exactly like a none end
// event rather than taking outgoing flows. Correlating on the command path keeps
// applyToState pure (I4/I6), the same reasoning as the throw event.
type messageEndEventBehavior struct{}

func (messageEndEventBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	detail := cp.MessageThrow(cp.Node(ei.ElementId).Detail)
	payload := instanceVariables(c, ei.ProcessInstanceKey)
	correlateMessage(c, detail.MessageName, evalCorrelationKey(c, detail.CorrelationKey, ei.ProcessInstanceKey), payload, ei.ProcessInstanceKey)
	c.AppendElementCommand(key, model.IntentCompleting, *ei)
}

func (messageEndEventBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	c.AppendElementEvent(key, model.IntentCompleted, *ei) // decrements scope's active children
	completeScope(c, ei.FlowScopeKey)                     // completes the scope owner if it drained
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

// evalStartCorrelationKey evaluates a message-start event's correlation-key
// expression over an incoming message's payload variables — the instance does
// not exist yet, so there is no scope to read from as evalCorrelationKey does.
// A nil expression (no correlation key declared) or an evaluation error yields
// "", so the started instance simply records no key.
func evalStartCorrelationKey(e *expr.Compiled, vars []model.VariableValue) string {
	if e == nil {
		return ""
	}
	inputs := e.Inputs()
	binding := make(map[string]expr.Value, len(inputs))
	for _, name := range inputs {
		for i := range vars {
			if vars[i].Name == name {
				binding[name] = expr.FromStored(toExprKind(vars[i].Kind), vars[i].Bool, vars[i].Text)
				break
			}
		}
	}
	v, err := e.Eval(binding)
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
func correlateMessage(c *ProcessingContext, name, correlationKey string, vars []model.VariableValue, senderPIKey uint64) {
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
		// Retain the delivery to the waiting catch event for the collaboration
		// replay (ADR-0038), before the subscription's fields are needed elsewhere.
		c.AppendMessageFlowEvent(model.MessageFlowValue{
			SenderProcessInstanceKey:   senderPIKey,
			ReceiverProcessInstanceKey: m.sub.ProcessInstanceKey,
			ReceiverProcessDefKey:      m.sub.ProcessDefKey,
			ReceiverElementId:          m.sub.ElementId,
			MessageName:                name,
			CorrelationKey:             correlationKey,
		})
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
	// start event, seeded with the payload (ADR-0035). Matching is by name; the
	// created instance additionally records the correlation key its start event
	// evaluates over the payload, so operators can see which key it began with
	// (ADR-0020). This runs after the subscription scan so a single message can
	// both correlate a waiting instance and start new ones, all recovered from the
	// events the created instances emit.
	for _, ref := range c.p.messageStarts[name] {
		startKey := evalStartCorrelationKey(ref.correlationKey, vars)
		// A singleton message start (ADR-0094) instantiates only if no instance of this
		// definition is already live for this correlation key. An empty key identifies
		// no entity, so it is never singleton (it always starts).
		if ref.singletonStart && startKey != "" {
			taken, err := c.singletonStartTaken(ref.defKey, startKey)
			if err != nil {
				c.p.fail(err)
				return
			}
			if taken {
				continue // a live instance for this key already exists (or is being started this batch)
			}
		}
		c.AppendCreateInstanceCommand(ref.defKey, vars, startKey)
		// Retain the delivery into the message-start event too, so the replay shows
		// the message that opened the receiving pool. The receiver instance does not
		// exist yet (the create is a followup), so its key is left 0 (ADR-0038).
		c.AppendMessageFlowEvent(model.MessageFlowValue{
			SenderProcessInstanceKey: senderPIKey,
			ReceiverProcessDefKey:    ref.defKey,
			ReceiverElementId:        ref.elementId,
			MessageName:              name,
			CorrelationKey:           correlationKey,
		})
	}
}

// signalCatchEventBehavior: an intermediate signal catch event. On activation it
// opens a name-only signal subscription and waits (stays Activated). A later
// signal broadcast of the same name fires the subscription, driving the element
// to complete and take its outgoing flows — the message-catch shape (ADR-0020)
// minus the correlation key (ADR-0088).
type signalCatchEventBehavior struct{}

func (signalCatchEventBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	detail := cp.SignalCatch(cp.Node(ei.ElementId).Detail)
	c.AppendSignalSubscriptionEvent(key, model.IntentSubscriptionCreated, model.SignalSubscriptionValue{
		ProcessInstanceKey: ei.ProcessInstanceKey,
		ElementInstanceKey: key,
		SignalName:         detail.SignalName,
		ProcessDefKey:      ei.ProcessDefKey,
		ElementId:          ei.ElementId,
	})
	// Stays Activated: no Completing until a signal broadcasts.
}

func (signalCatchEventBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	completeAndTakeFlows(c, key, ei)
}

// signalThrowEventBehavior: an intermediate signal throw event. On activation it
// broadcasts the signal to every waiting catch of the same name — across all
// instances — then completes and takes its outgoing flows. The throw carries the
// throwing instance's variables as the broadcast payload, so every catch it fires
// is seeded with them (ADR-0088), mirroring messageThrowEventBehavior. Reading the
// payload here (command processing) keeps applyToState pure (I4).
type signalThrowEventBehavior struct{}

func (signalThrowEventBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	detail := cp.SignalThrow(cp.Node(ei.ElementId).Detail)
	payload := instanceVariables(c, ei.ProcessInstanceKey)
	broadcastSignal(c, detail.SignalName, payload)
	c.AppendElementCommand(key, model.IntentCompleting, *ei)
}

func (signalThrowEventBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	completeAndTakeFlows(c, key, ei)
}

// signalEndEventBehavior: an end event that broadcasts a signal, then ends the
// instance (ADR-0088). It is the send-and-stop union of a signal throw event and
// a none end event: OnActivated broadcasts exactly like a throw (reusing the
// throw detail table), and OnCompleting ends the instance exactly like a none end
// event rather than taking outgoing flows. Broadcasting on the command path keeps
// applyToState pure (I4/I6), the same reasoning as the throw event — and mirrors
// messageEndEventBehavior.
type signalEndEventBehavior struct{}

func (signalEndEventBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	detail := cp.SignalThrow(cp.Node(ei.ElementId).Detail)
	payload := instanceVariables(c, ei.ProcessInstanceKey)
	broadcastSignal(c, detail.SignalName, payload)
	c.AppendElementCommand(key, model.IntentCompleting, *ei)
}

func (signalEndEventBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	c.AppendElementEvent(key, model.IntentCompleted, *ei) // decrements scope's active children
	completeScope(c, ei.FlowScopeKey)                     // completes the scope owner if it drained
}

// broadcastSignal delivers a signal with the given name to every open signal
// subscription — a 1:n broadcast, across all instances, matching by name alone
// (a signal has no correlation key). For each match it emits SubscriptionCorrelated
// (which retires the subscription — a catch fires once), writes the broadcast
// payload into that instance's scope, and commands the waiting element to
// complete. Matches are collected before any mutation so retiring one can't
// disturb the scan. A signal that matches nothing is a no-op — a broadcast is
// fire-and-forget, with no buffering (ADR-0088). It mirrors correlateMessage,
// and — like a correlating message — also instantiates every deployed process
// with a matching signal start event (ADR-0088).
func broadcastSignal(c *ProcessingContext, name string, vars []model.VariableValue) {
	type match struct {
		elKey uint64
		sub   model.SignalSubscriptionValue
	}
	var matches []match
	c.p.fail(c.tx.SubscribedSignals(name, func(elKey uint64, v *model.SignalSubscriptionValue) error {
		matches = append(matches, match{elKey: elKey, sub: *v})
		return nil
	}))
	for i := range matches {
		m := matches[i]
		c.AppendSignalSubscriptionEvent(m.elKey, model.IntentSubscriptionCorrelated, m.sub)
		for j := range vars {
			vv := vars[j]
			vv.ScopeKey = m.sub.ProcessInstanceKey
			c.AppendVariableEvent(model.IntentVariableCreated, vv)
		}
		if ei := c.GetElementInstance(m.elKey); ei != nil {
			c.AppendElementCommand(m.elKey, model.IntentCompleting, *ei)
		}
	}
	// A broadcast also instantiates every deployed process with a matching signal
	// start event, seeded with the payload (ADR-0088), mirroring message-start
	// instantiation. A signal carries no correlation key, so the created instance
	// records none. This runs after the subscription scan so one broadcast can both
	// fire waiting catches and start fresh instances, all recovered from the events
	// the created instances emit.
	for _, defKey := range c.p.signalStarts[name] {
		c.AppendCreateInstanceCommand(defKey, vars, "")
	}
}

// errorEndEventBehavior: an end event that throws a BPMN error (ADR-0089). Unlike a none
// end event it does not complete its scope; instead OnCompleting calls propagateError,
// which routes the error up the live scope chain to the nearest matching error boundary —
// aborting the scope below the catch and taking the boundary's recovery flow — or raises
// an incident if nothing catches it. Throwing on the command path keeps applyToState pure
// (I4/I6). OnActivated hops to Completing exactly like a none end.
type errorEndEventBehavior struct{}

func (errorEndEventBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	c.AppendElementCommand(key, model.IntentCompleting, *ei)
}

func (errorEndEventBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	code := cp.ErrorEnd(cp.Node(ei.ElementId).Detail).ErrorCode
	// The error end does NOT emit Completed: propagation tears its scope down, and the
	// throwing element instance is terminated by interruptHost's terminateScope (or, when
	// unhandled, parked under the incident). Emitting Completed here would double-count.
	propagateError(c, key, code)
}

// propagateError routes a thrown error up the live scope chain to the nearest matching
// error handler (ADR-0089). Starting from the throwing element, it walks up FlowScopeKey;
// at each enclosing scope it checks that scope's armed error event subprocesses and, for an
// activity scope, its armed error boundaries for a code match (equal code, or a code-less
// catch-all) and drives the first match to Completing — an error catch is always
// interrupting, so a boundary's interruptHost tears the activity down and it takes its
// recovery flow, while an event-subprocess trigger terminateScopes its scope and runs its
// handler. When the instance root is reached uncaught, the error crosses process boundaries:
// if the instance is a call-activity child, the child is terminated (the error aborts it)
// and propagation continues from the caller's call-activity element in the parent instance
// (ADR-0076); a top-level instance instead raises an incident on the throwing element and
// parks (ADR-0061). It reads only committed state and the compiled codes — a pure function
// of committed state (I6) — and runs only on the command path (a throw is a command), never
// on replay. Bounded by maxScopeDepth (both the scope-chain walk and the caller-hop count).
func propagateError(c *ProcessingContext, fromKey uint64, code string) {
	for hop := 0; hop <= maxScopeDepth; hop++ {
		ei := c.GetElementInstance(fromKey)
		if ei == nil {
			return // the throwing/caller element vanished (already torn down): nothing to do
		}
		if errorCaughtInInstance(c, ei.ProcessInstanceKey, fromKey, code) {
			return
		}
		// Uncaught in this instance. A call-activity child's error propagates to its caller:
		// terminate the child (the error aborts it), then continue from the caller's element.
		pi := c.GetProcessInstance(ei.ProcessInstanceKey)
		if pi != nil && pi.ParentElementInstanceKey != 0 {
			c.AppendProcessInstanceCommand(ei.ProcessInstanceKey, model.IntentTerminating, *pi)
			fromKey = pi.ParentElementInstanceKey
			continue
		}
		raiseErrorIncident(c, fromKey, code)
		return
	}
}

// errorCaughtInInstance walks the scope chain of one instance from fromKey up to its root,
// firing the nearest matching error catch (an event subprocess before a boundary at each
// scope; the root's event subprocesses last), and reports whether one fired (ADR-0089).
func errorCaughtInInstance(c *ProcessingContext, piKey, fromKey uint64, code string) bool {
	for depth, scope := 0, fromKey; depth <= maxScopeDepth; depth++ {
		ei := c.GetElementInstance(scope)
		if ei == nil {
			break // walked past the process root: no enclosing element scope left
		}
		// An error event subprocess declared in this scope catches errors thrown within it,
		// nearer than a boundary on the scope's activity (which catches the error leaving it).
		if fireErrorCatch(c, findErrorEventSub(c, piKey, scope, code)) {
			return true
		}
		if fireErrorCatch(c, findErrorBoundary(c, piKey, scope, code)) {
			return true
		}
		scope = ei.FlowScopeKey
	}
	// The process root's own error event subprocesses are keyed by the instance scope, which
	// is not an element instance — so they are checked here, after the element-scope walk.
	return fireErrorCatch(c, findErrorEventSub(c, piKey, piKey, code))
}

// fireErrorCatch drives a found error catch (a boundary or event-subprocess trigger element)
// to Completing and reports whether it fired. A zero or vanished key is not a catch.
func fireErrorCatch(c *ProcessingContext, catchKey uint64) bool {
	if catchKey == 0 {
		return false
	}
	catch := c.GetElementInstance(catchKey)
	if catch == nil {
		return false
	}
	c.AppendElementCommand(catchKey, model.IntentCompleting, *catch)
	return true
}

// findErrorBoundary returns the element-instance key of an armed error boundary attached to
// hostKey whose code catches the thrown code (equal, or a code-less catch-all), or 0 if the
// host has none (ADR-0089). It scans the instance's element instances for a boundary whose
// AttachedToKey is hostKey — the same lookup interruptHost/disarmBoundaryEvents use.
func findErrorBoundary(c *ProcessingContext, piKey, hostKey uint64, code string) uint64 {
	var found uint64
	c.ForEachElementInstance(piKey, func(elKey uint64) {
		if found != 0 {
			return
		}
		b := c.GetElementInstance(elKey)
		if b == nil || b.AttachedToKey != hostKey || b.BpmnElementType != uint8(compiler.TypeBoundaryEvent) {
			return
		}
		cp := c.process(b.ProcessDefKey)
		d := cp.BoundaryEvent(cp.Node(b.ElementId).Detail)
		if d.Kind == compiler.BoundaryError && errorCodeMatches(d.ErrorCode, code) {
			found = elKey
		}
	})
	return found
}

// findErrorEventSub returns the element-instance key of an armed error event-subprocess
// trigger in the scope scopeKey whose code catches the thrown code (equal, or a code-less
// catch-all), or 0 if the scope hosts none (ADR-0089, reusing ADR-0082). A trigger is a
// TypeEventSubProcessStart element scoped by scopeKey; driving it to Completing runs its
// handler (always interrupting for an error).
func findErrorEventSub(c *ProcessingContext, piKey, scopeKey uint64, code string) uint64 {
	var found uint64
	c.ForEachElementInstance(piKey, func(elKey uint64) {
		if found != 0 {
			return
		}
		t := c.GetElementInstance(elKey)
		if t == nil || t.FlowScopeKey != scopeKey || t.BpmnElementType != uint8(compiler.TypeEventSubProcessStart) {
			return
		}
		cp := c.process(t.ProcessDefKey)
		d := cp.EventSubProcess(cp.Node(t.ElementId).EventSub)
		if d.Kind == compiler.BoundaryError && errorCodeMatches(d.ErrorCode, code) {
			found = elKey
		}
	})
	return found
}

// errorCodeMatches reports whether a catch with catchCode catches a thrown code: an equal
// code, or a code-less catch-all (ADR-0089) — the runtime twin of the compiler's
// same-named check.
func errorCodeMatches(catchCode, code string) bool {
	return catchCode == "" || catchCode == code
}

// raiseErrorIncident records an incident on a throwing element whose error no enclosing
// handler caught, and parks (ADR-0089/ADR-0061). A vanished element is a harmless no-op.
func raiseErrorIncident(c *ProcessingContext, elKey uint64, code string) {
	ei := c.GetElementInstance(elKey)
	if ei == nil {
		return
	}
	msg := "unhandled error"
	if code != "" {
		msg = "unhandled error '" + code + "'"
	}
	c.AppendIncidentEvent(model.IntentIncidentCreated, model.IncidentValue{
		ProcessInstanceKey: ei.ProcessInstanceKey,
		ElementInstanceKey: elKey,
		ElementId:          ei.ElementId,
		RaisedAt:           c.Now(),
		Message:            msg,
	})
}

// --- Compensation (ADR-0103) ---

// compensationThrowEventBehavior triggers compensation, then flows on. OnActivated runs
// the handlers of completed compensable activities in the throw's scope (or of the one
// named by activityRef) via compensate — a command-path scope walk, the structural twin of
// propagateError — then hops to Completing exactly like a signal throw. Running compensation
// on the command path keeps applyToState pure (I4/I6).
type compensationThrowEventBehavior struct{}

func (compensationThrowEventBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	compensate(c, ei, cp.CompensationThrow(cp.Node(ei.ElementId).Detail).ActivityRef)
	c.AppendElementCommand(key, model.IntentCompleting, *ei)
}

func (compensationThrowEventBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	completeAndTakeFlows(c, key, ei)
}

// compensationEndEventBehavior triggers compensation, then ends its scope — the
// trigger-and-stop counterpart of a compensation throw, ending like a signal end rather
// than taking outgoing flows (ADR-0103).
type compensationEndEventBehavior struct{}

func (compensationEndEventBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	compensate(c, ei, cp.CompensationThrow(cp.Node(ei.ElementId).Detail).ActivityRef)
	c.AppendElementCommand(key, model.IntentCompleting, *ei)
}

func (compensationEndEventBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	c.AppendElementEvent(key, model.IntentCompleted, *ei) // decrements scope's active children
	completeScope(c, ei.FlowScopeKey)                     // completes the scope owner if it drained
}

// compensate runs the compensation handlers of the completed compensable activities in the
// throwing element's scope, newest first (reverse completion order). When activityRef >= 0
// it compensates only that activity's completed instances; otherwise the whole scope. For
// each matching record it activates the handler in the scope and consumes the record so it
// is compensated at most once. Records and handler links are read from committed state and
// the compiled graph — a pure function of committed state (I6) — and this runs only on the
// command path (a throw is a command). It collects matches before activating so consuming
// one cannot disturb the scan.
func compensate(c *ProcessingContext, ei *model.ElementInstanceValue, activityRef int32) {
	type match struct {
		seq uint64
		v   model.CompensableValue
	}
	var matches []match
	c.p.fail(c.tx.CompensablesOfScopeDesc(ei.FlowScopeKey, func(seq uint64, v *model.CompensableValue) error {
		if activityRef < 0 || v.ElementId == activityRef {
			matches = append(matches, match{seq: seq, v: *v})
		}
		return nil
	}))
	for i := range matches {
		m := matches[i]
		activateCompensationHandler(c, &m.v)
		consumed := m.v
		consumed.Seq = m.seq
		c.AppendCompensableEvent(model.IntentCompensableConsumed, consumed)
	}
}

// activateCompensationHandler activates a compensation handler activity in the scope of the
// activity it compensates. The handler runs its ordinary behavior (a service task creates a
// job, …) and, having no outgoing flow, completes without taking one; it is counted in the
// scope's active children, so a compensation throw's scope does not finish until its handlers
// do (ADR-0103).
func activateCompensationHandler(c *ProcessingContext, v *model.CompensableValue) {
	target := c.process(v.ProcessDefKey).Node(v.HandlerNode)
	key := c.NewKey()
	miRole := miNone
	if target.MultiInstance >= 0 {
		miRole = miBody
	}
	c.AppendElementCommand(key, model.IntentActivating, model.ElementInstanceValue{
		ProcessInstanceKey: v.ProcessInstanceKey,
		ProcessDefKey:      v.ProcessDefKey,
		ElementId:          v.HandlerNode,
		FlowScopeKey:       v.ScopeKey,
		BpmnElementType:    uint8(target.Type),
		MultiInstance:      miRole,
		TokenID:            key,
	})
}

// recordCompensable, when the just-completed element is a compensable activity (one bearing
// a compensation boundary), appends a durable record so a later compensation throw can run
// its handler (ADR-0103). It is called only on successful completion (completeAndTakeFlows),
// never on termination — a terminated activity did not succeed and is not compensable.
func recordCompensable(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	handler := compensationHandlerOf(c.process(ei.ProcessDefKey), ei.ElementId)
	if handler < 0 {
		return
	}
	c.AppendCompensableEvent(model.IntentCompensableRecorded, model.CompensableValue{
		ProcessInstanceKey: ei.ProcessInstanceKey,
		ProcessDefKey:      ei.ProcessDefKey,
		ScopeKey:           ei.FlowScopeKey,
		ElementInstanceKey: key,
		ElementId:          ei.ElementId,
		HandlerNode:        handler,
	})
}

// compensationHandlerOf returns the compensation handler node linked to elementId by a
// compensation boundary, or -1 if the element is not compensable. A bounded scan over the
// element's (usually zero or one) boundary events, so it is cheap on the completion path.
func compensationHandlerOf(cp *compiler.CompiledProcess, elementId int32) int32 {
	for _, beID := range cp.BoundaryEvents(elementId) {
		if d := cp.BoundaryEvent(cp.Node(beID).Detail); d.Kind == compiler.BoundaryCompensation {
			return d.CompensationHandler
		}
	}
	return -1
}

// cancelEndEventBehavior cancels the enclosing transaction (ADR-0108). Its FlowScopeKey is the
// transaction (the compiler requires a cancel end directly inside one). On activation it
// terminates the transaction's other live tokens — sparing itself — so the scope drains to just
// the compensation handlers it then starts, compensates every completed compensable activity in
// the transaction (newest first, reverse completion order) via the same command-path walk a
// compensation end event uses, and hops to Completing. Its Completed event marks the transaction
// scope cancelling (applyToState); completeScope then fires the transaction's Completing once
// the handlers drain, where subProcessBehavior's cancel branch routes out the cancel boundary.
type cancelEndEventBehavior struct{}

func (cancelEndEventBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	terminateScopeExcept(c, ei.ProcessInstanceKey, ei.FlowScopeKey, key)
	compensate(c, ei, -1) // compensate the whole transaction scope, reverse completion order
	c.AppendElementCommand(key, model.IntentCompleting, *ei)
}

func (cancelEndEventBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	c.AppendElementEvent(key, model.IntentCompleted, *ei) // decrements the transaction scope; applyToState marks it cancelling
	completeScope(c, ei.FlowScopeKey)                     // fires the transaction's Completing once compensation drains
}

// cancelTransaction completes a cancelled transaction whose scope has drained (compensation
// finished): it retires the transaction element and fires its inert cancel boundary to take the
// recovery flow (ADR-0108). Retiring the transaction first drops its compensable records and the
// cancelling marker (apply.go) and decrements the parent scope; completing the cancel boundary
// then takes the recovery flow, and the boundary being gone makes the subsequent
// disarmBoundaryEvents (handleElementCompleting) skip it while still disarming the transaction's
// other boundaries. With no cancel boundary (a compile warning), the transaction is simply torn
// down and the parent scope drained.
func cancelTransaction(c *ProcessingContext, txKey uint64, ei *model.ElementInstanceValue) {
	c.AppendElementEvent(txKey, model.IntentTerminated, *ei)
	if bKey := findCancelBoundary(c, ei.ProcessInstanceKey, txKey); bKey != 0 {
		if b := c.GetElementInstance(bKey); b != nil {
			completeAndTakeFlows(c, bKey, b)
			return
		}
	}
	completeScope(c, ei.FlowScopeKey)
}

// findCancelBoundary returns the armed cancel-boundary element instance attached to the
// transaction txKey, or 0 if none (ADR-0108).
func findCancelBoundary(c *ProcessingContext, procKey, txKey uint64) uint64 {
	var found uint64
	c.ForEachElementInstance(procKey, func(elKey uint64) {
		if found != 0 {
			return
		}
		b := c.GetElementInstance(elKey)
		if b == nil || b.AttachedToKey != txKey {
			return
		}
		cp := c.process(b.ProcessDefKey)
		if cp.BoundaryEvent(cp.Node(b.ElementId).Detail).Kind == compiler.BoundaryCancel {
			found = elKey
		}
	})
	return found
}

// singletonStartTaken reports whether a singleton message start for (defKey, key)
// should be skipped because a live instance already exists — either durably (the
// ActiveStartKey counter, maintained by applyToState) or already scheduled earlier in
// this batch (the per-batch set, which closes the window before the durable counter
// reflects a same-batch create). When it returns false it records the pair so a second
// message for the same key in the same batch is skipped (ADR-0094).
func (c *ProcessingContext) singletonStartTaken(defKey uint64, key string) (bool, error) {
	if c.p.startsThisBatch == nil {
		c.p.startsThisBatch = make(map[startKeyIdent]struct{})
	}
	ident := startKeyIdent{defKey: defKey, key: key}
	if _, ok := c.p.startsThisBatch[ident]; ok {
		return true, nil
	}
	n, err := c.tx.ActiveStartKeyCount(defKey, key)
	if err != nil {
		return false, err
	}
	if n > 0 {
		return true, nil
	}
	c.p.startsThisBatch[ident] = struct{}{}
	return false, nil
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
		TokenID:            ei.TokenID,
		SourceFlowId:       flowID,
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
	continuation := *ei
	continuation.ParentTokenID = ei.TokenID
	continuation.TokenID = 0
	takeOutgoingFlows(c, &continuation)
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

// eventBasedGatewayBehavior is a deferred choice (ADR-0110): on activation it arms every
// target catch event at once — each outgoing flow's target, a message/timer/signal
// intermediate catch — stamping each with this gateway's key as its race group, then
// completes. The armed catches open their own subscriptions/timers and wait unchanged;
// whichever fires first cancels the rest (the group prologue in completeAndTakeFlows). The
// gateway itself takes no outgoing flow — the targets were armed on activation.
type eventBasedGatewayBehavior struct{}

func (eventBasedGatewayBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	for _, flowID := range cp.Outgoing(ei.ElementId) {
		targetId := cp.Flow(flowID).Target
		target := cp.Node(targetId)
		k := c.NewKey()
		c.AppendElementCommand(k, model.IntentActivating, model.ElementInstanceValue{
			ProcessInstanceKey: ei.ProcessInstanceKey,
			ProcessDefKey:      ei.ProcessDefKey,
			ElementId:          targetId,
			FlowScopeKey:       ei.FlowScopeKey,
			BpmnElementType:    uint8(target.Type),
			TokenID:            k,
			ParentTokenID:      ei.TokenID,
			SourceFlowId:       flowID,
			EventGatewayKey:    key, // the race group: this gateway's element-instance key
		})
	}
	c.AppendElementCommand(key, model.IntentCompleting, *ei)
}

func (eventBasedGatewayBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	c.AppendElementEvent(key, model.IntentCompleted, *ei)
}

// cancelEventGatewaySiblings terminates every live element instance in the same event-gateway
// race group as the winner (groupKey), except the winner itself (selfKey) — the deferred
// choice's losing branches (ADR-0110). Each loser's message subscription or timer self-retires
// (a later correlate/fire finds no element and drops the stale entry), the same lazy cleanup a
// disarmed boundary uses. Mirrors interruptHost's sibling-terminate loop, keyed by the
// event-gateway group instead of a boundary host. Victims are collected before any termination
// so the scan sees an intact set.
func cancelEventGatewaySiblings(c *ProcessingContext, procKey, groupKey, selfKey uint64) {
	var losers []uint64
	c.ForEachElementInstance(procKey, func(elKey uint64) {
		if elKey == selfKey {
			return
		}
		if s := c.GetElementInstance(elKey); s != nil && s.EventGatewayKey == groupKey {
			losers = append(losers, elKey)
		}
	})
	for _, lk := range losers {
		if s := c.GetElementInstance(lk); s != nil {
			c.AppendElementEvent(lk, model.IntentTerminated, *s)
		}
	}
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
		// Resolve the condition over the gateway's scope chain, so a gateway inside a
		// subprocess or multi-instance body branches on that scope's variables, not
		// only the process root (ADR-0085). For a top-level gateway FlowScopeKey is
		// the process-instance key, so this reads exactly the root as before.
		v, err := f.Condition.Eval(bindInputsChain(c, f.Condition.Inputs(), ei.FlowScopeKey))
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

// scriptJobTaskBehavior: run a script authored in a general-purpose language
// (PowerShell, …) off the hot path. Unlike the inline FEEL script task, whose
// pure expression the engine evaluates on the processor goroutine, a job script
// is arbitrary side-effecting code: the engine treats it exactly like a service
// task — create a job on activation and wait — but the job carries the language's
// reserved job type, so the in-process script worker picks it up, runs the
// interpreter off the hot path after fsync, and completes the job with the
// script's result. Keeping the interpreter on the worker side, not in a behavior,
// keeps the processor allocation-free (I1) and the script's I/O out of
// applyToState (I4). See ADR-0047.
type scriptJobTaskBehavior struct{}

func (scriptJobTaskBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	detail := cp.ScriptJobTask(cp.Node(ei.ElementId).Detail)
	jobKey := c.NewKey()
	c.AppendJobEvent(jobKey, model.IntentJobCreated, model.JobValue{
		ProcessInstanceKey: ei.ProcessInstanceKey,
		ElementInstanceKey: key,
		JobType:            detail.JobType,
		Retries:            detail.Retries,
	})
	c.NotifyJobAvailable(detail.JobType)
	// Stays Activated until the script worker completes the job.
}

func (scriptJobTaskBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
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
	// A due date is authored as a duration relative to task creation, so the
	// absolute due instant is computed here and frozen into the job-created event
	// (ADR-0051). Now() is read at command processing and recorded, so recovery
	// replays the identical deadline — exactly like a timer's due date. 0 means
	// the task has no due date.
	var deadline int64
	if detail.DueDateNanos != 0 {
		deadline = c.Now() + detail.DueDateNanos
	}
	jobKey := c.NewKey()
	c.AppendJobEvent(jobKey, model.IntentJobCreated, model.JobValue{
		ProcessInstanceKey: ei.ProcessInstanceKey,
		ElementInstanceKey: key,
		JobType:            detail.JobType,
		Retries:            detail.Retries,
		Deadline:           deadline,
		// Seed the runtime assignee with the model's default; claim/unclaim
		// rewrites it through the job lifecycle (ADR-0042).
		Assignee: cp.Intern(detail.Assignee),
	})
	c.NotifyJobAvailable(detail.JobType)
}

func (userTaskBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	completeAndTakeFlows(c, key, ei)
}

// boundaryEventBehavior: a timer/message event attached to a host activity
// (ADR-0040). On activation it arms its trigger — a timer keyed to itself (like an
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
		// The schedule is resolved here (command processing) — reading the clock and,
		// for a FEEL schedule, the instance's variables — and the due date and cycle
		// count are frozen into the event; applyToState never reads either (I4/I6). A
		// non-interrupting cycle boundary seeds Repetitions so a finite Rn cycle counts
		// down as it recurs (ADR-0054). An unresolvable FEEL schedule raises an
		// incident and parks instead of firing immediately (ADR-0064).
		armOneShotTimer(c, key, ei, d.Schedule)
	case compiler.BoundaryMessage:
		c.AppendMessageSubscriptionEvent(key, model.IntentSubscriptionCreated, model.MessageSubscriptionValue{
			ProcessInstanceKey: ei.ProcessInstanceKey,
			ElementInstanceKey: key,
			MessageName:        d.MessageName,
			CorrelationKey:     evalCorrelationKey(c, d.CorrelationKey, ei.ProcessInstanceKey),
		})
	case compiler.BoundarySignal:
		// A name-only subscription (no correlation key). A later broadcast of the
		// name drives this boundary instance to Completing exactly as a correlating
		// message does — the fire path (interrupt-or-take-flow) is shared (ADR-0088).
		c.AppendSignalSubscriptionEvent(key, model.IntentSubscriptionCreated, model.SignalSubscriptionValue{
			ProcessInstanceKey: ei.ProcessInstanceKey,
			ElementInstanceKey: key,
			SignalName:         d.SignalName,
			ProcessDefKey:      ei.ProcessDefKey,
			ElementId:          ei.ElementId,
		})
	case compiler.BoundaryError:
		// An error boundary opens nothing — it is inert, armed only to be *found* by
		// propagateError walking the scope chain when an error is thrown, then driven to
		// Completing (ADR-0089). No subscription, timer, or record.
	case compiler.BoundaryCancel:
		// A cancel boundary opens nothing either — it is inert, armed only to be *found* when
		// its host transaction is cancelled (its scope has drained after compensation), then
		// driven to Completing to take the recovery flow (ADR-0108).
	}
	// Stays Activated: waits until the timer fires, the message correlates, the signal
	// broadcasts, or (for an error boundary) an error propagates up to it.
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
// subProcessBehavior runs an embedded subprocess: a container that is itself a
// scope. On activation it seeds the subprocess's inner start event(s) as element
// instances scoped by the subprocess (their FlowScopeKey is the container's key),
// so each increments the subprocess's active-child counter. The subprocess then
// waits — it is not driven to Completing by a token flowing through it but by its
// scope draining: when its last inner token leaves, an inner end event's
// completeScope schedules the container's Completing (ADR-0074). On completion it
// behaves like any activity: emit Completed (decrementing its parent scope) and
// take its outgoing flow.
type subProcessBehavior struct{}

func (subProcessBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	starts := cp.ScopeStartEvents(ei.ElementId)
	if len(starts) == 0 {
		// A subprocess with no start event has an already-empty scope; complete it
		// immediately rather than parking a token forever.
		c.AppendElementCommand(key, model.IntentCompleting, *ei)
		return
	}
	// Arm this subprocess scope's event subprocesses (ADR-0082 Phase 4) — the analog of
	// arming the root's at instance creation. They are disarmed when this scope completes
	// (completeScope) or is torn down by an interrupt (terminateScope terminates every
	// element scoped under it, triggers included).
	armEventSubprocesses(c, ei.ProcessInstanceKey, ei.ProcessDefKey, key, cp.EventSubprocesses(ei.ElementId))
	for _, startID := range starts {
		node := cp.Node(startID)
		k := c.NewKey()
		c.AppendElementCommand(k, model.IntentActivating, model.ElementInstanceValue{
			ProcessInstanceKey: ei.ProcessInstanceKey,
			ProcessDefKey:      ei.ProcessDefKey,
			ElementId:          startID,
			FlowScopeKey:       key, // inner elements are scoped by this subprocess instance
			BpmnElementType:    uint8(node.Type),
			TokenID:            k,
			SourceFlowId:       -1,
		})
	}
}

func (subProcessBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	// A subprocess is a scope: its locals (input mappings, and the results inner
	// activities wrote into it) are dropped when it completes, so only an explicit
	// output mapping escapes. A subprocess *with* output mappings was already
	// drained by handleElementCompleting (after promoting), so this is a no-op there;
	// one without mappings is drained here (ADR-0074).
	dropLocalScope(c, key)
	// A cancelled transaction: its scope drained after compensation, so route the
	// cancellation out its cancel boundary instead of taking the transaction's normal
	// outgoing flow (ADR-0108).
	if c.process(ei.ProcessDefKey).IsTransaction(ei.ElementId) && c.IsCanceling(key) {
		cancelTransaction(c, key, ei)
		return
	}
	// An event-subprocess handler has no outgoing flow; its completion instead drains
	// the parent scope it ran in, which may in turn complete that scope (ADR-0082).
	if c.process(ei.ProcessDefKey).IsEventSubProcess(ei.ElementId) {
		c.AppendElementEvent(key, model.IntentCompleted, *ei)
		completeScope(c, ei.FlowScopeKey)
		return
	}
	completeAndTakeFlows(c, key, ei)
}

// armEventSubprocesses activates one waiting trigger instance per event subprocess in a
// scope, scoped by it (ADR-0082). A trigger is a TypeEventSubProcessStart element whose
// ElementId is the handler container node — so its behavior can read the handler's
// trigger detail and, on firing, seed the handler — and is excluded from the scope's
// active-child counter (apply.go), so an armed trigger never blocks scope completion.
// Called when a scope is entered: the process root at instance creation (and, later,
// a subprocess on its activation).
func armEventSubprocesses(c *ProcessingContext, piKey, defKey, scopeKey uint64, handlers []int32) {
	for _, handlerNode := range handlers {
		armEventSubTrigger(c, piKey, defKey, scopeKey, handlerNode)
	}
}

// armEventSubTrigger activates one waiting event-subprocess trigger instance for a
// handler, scoped by its parent (ADR-0082). It is used both to arm at scope entry and
// to re-arm a non-interrupting message trigger after it fires, so a later message
// triggers it again.
func armEventSubTrigger(c *ProcessingContext, piKey, defKey, scopeKey uint64, handlerNode int32) {
	c.AppendElementCommand(c.NewKey(), model.IntentActivating, model.ElementInstanceValue{
		ProcessInstanceKey: piKey,
		ProcessDefKey:      defKey,
		ElementId:          handlerNode, // the handler container; its EventSub detail names the trigger
		FlowScopeKey:       scopeKey,
		BpmnElementType:    uint8(compiler.TypeEventSubProcessStart),
	})
}

// activateEventSubHandler activates the handler subprocess of a fired event-subprocess
// trigger, in the parent scope (ADR-0082). The handler seeds its inner start (which
// flows straight on) and runs to its end; having no outgoing flow, its completion
// drains the parent scope (subProcessBehavior.OnCompleting).
func activateEventSubHandler(c *ProcessingContext, ei *model.ElementInstanceValue, parentScope uint64) {
	k := c.NewKey()
	c.AppendElementCommand(k, model.IntentActivating, model.ElementInstanceValue{
		ProcessInstanceKey: ei.ProcessInstanceKey,
		ProcessDefKey:      ei.ProcessDefKey,
		ElementId:          ei.ElementId,
		FlowScopeKey:       parentScope,
		BpmnElementType:    uint8(c.process(ei.ProcessDefKey).Node(ei.ElementId).Type), // TypeSubProcess
		TokenID:            k,
		SourceFlowId:       -1,
	})
}

// disarmEventSubprocesses terminates every still-armed event-subprocess trigger in a
// scope, when the scope completes (ADR-0082). A trigger is uncounted, so the scope
// drained with it still armed; its Terminated event drops the element and its
// message subscription / timer self-retires (it fires later, finds no element, does
// nothing) — the same pattern disarmBoundaryEvents uses.
func disarmEventSubprocesses(c *ProcessingContext, procKey, scope uint64) {
	var triggers []uint64
	c.ForEachElementInstance(procKey, func(elKey uint64) {
		if t := c.GetElementInstance(elKey); t != nil &&
			t.BpmnElementType == uint8(compiler.TypeEventSubProcessStart) && t.FlowScopeKey == scope {
			triggers = append(triggers, elKey)
		}
	})
	for _, tk := range triggers {
		if t := c.GetElementInstance(tk); t != nil {
			c.AppendElementEvent(tk, model.IntentTerminated, *t)
		}
	}
}

// eventSubProcessStartBehavior is the armed trigger of an event subprocess (ADR-0082).
// On activation it opens its trigger — a message/signal subscription or a one-shot timer,
// exactly as a boundary event does — and waits. When the trigger fires, the existing
// timer/message/signal path drives it to Completing: it terminates the parent scope's
// other work if interrupting, then activates the handler subprocess. The handler runs as
// an ordinary subprocess scope; its inner start (a message/timer/signal start) flows
// straight on.
type eventSubProcessStartBehavior struct{}

func (eventSubProcessStartBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	d := cp.EventSubProcess(cp.Node(ei.ElementId).EventSub)
	switch d.Kind {
	case compiler.BoundaryTimer:
		armOneShotTimer(c, key, ei, d.Schedule)
	case compiler.BoundaryMessage:
		c.AppendMessageSubscriptionEvent(key, model.IntentSubscriptionCreated, model.MessageSubscriptionValue{
			ProcessInstanceKey: ei.ProcessInstanceKey,
			ElementInstanceKey: key,
			MessageName:        d.MessageName,
			CorrelationKey:     evalCorrelationKey(c, d.CorrelationKey, ei.ProcessInstanceKey),
		})
	case compiler.BoundarySignal:
		// A name-only subscription (no correlation key). A broadcast of the name drives
		// this trigger to Completing through the shared fire path (ADR-0088), exactly as
		// a boundary signal — then the handler runs and a non-interrupting trigger re-arms.
		c.AppendSignalSubscriptionEvent(key, model.IntentSubscriptionCreated, model.SignalSubscriptionValue{
			ProcessInstanceKey: ei.ProcessInstanceKey,
			ElementInstanceKey: key,
			SignalName:         d.SignalName,
			ProcessDefKey:      ei.ProcessDefKey,
			ElementId:          ei.ElementId,
		})
	case compiler.BoundaryError:
		// An error event subprocess arms inert — it opens nothing, waiting only to be found
		// by propagateError when an error is thrown in its scope, then driven to Completing
		// (ADR-0089). Always interrupting, so firing terminates the scope and runs the handler.
	}
	// Stays Activated: waits until the trigger fires.
}

func (eventSubProcessStartBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	// A parent-scope interrupt (or the scope completing) may have terminated this armed
	// trigger after its Completing command was queued; if so, there is nothing to fire.
	if c.GetElementInstance(key) == nil {
		return
	}
	cp := c.process(ei.ProcessDefKey)
	handlerNode := ei.ElementId
	d := cp.EventSubProcess(cp.Node(handlerNode).EventSub)
	parentScope := ei.FlowScopeKey
	// The trigger fired: it completes (uncounted, so no counter change).
	c.AppendElementEvent(key, model.IntentCompleted, *ei)
	if d.Interrupting {
		// Terminate the parent scope's other work — the main flow and, since event-sub
		// triggers live in the same scope, the sibling triggers — before the handler runs.
		terminateScope(c, ei.ProcessInstanceKey, parentScope)
	}
	activateEventSubHandler(c, ei, parentScope)
	// A non-interrupting message or signal trigger re-arms a fresh subscription so a
	// subsequent message/broadcast fires it again (ADR-0082/ADR-0088). A one-shot timer
	// fires once (no re-arm); a recurring timer re-arms through the timer path
	// (fireRecurringEventSub), not here.
	if !d.Interrupting && (d.Kind == compiler.BoundaryMessage || d.Kind == compiler.BoundarySignal) {
		armEventSubTrigger(c, ei.ProcessInstanceKey, ei.ProcessDefKey, parentScope, handlerNode)
	}
}

// loopCounterVar is the standard multi-instance per-iteration counter variable
// (1-based), bound into each inner iteration's scope (ADR-0077, matching Zeebe).
const loopCounterVar = "loopCounter"

// seedMultiInstance runs a multi-instance body's activation (ADR-0077): it evaluates
// the loop's input collection (or cardinality) over the body's scope chain to N items
// and seeds inner iterations of the same node scoped under the body — all N at once
// (parallel), or just the first (sequential; the rest follow one per completion). Each
// inner carries loopCounter (1-based) and, when the loop names an input element, its
// item, written into the inner's own scope so the iteration's behavior resolves them up
// the chain. When the loop has an output collection, a slot-per-iteration list is
// initialised on the body scope so each iteration writes its own index (order-
// preserving). An empty collection seeds nothing and completes the body at once. The
// collection is evaluated live; the inner Activated and variable events persist, so
// replay rebuilds the iterations without re-evaluating (I6).
func seedMultiInstance(c *ProcessingContext, bodyKey uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	d := cp.MultiInstance(cp.Node(ei.ElementId).MultiInstance)
	items := multiInstanceItems(c, d, bodyKey)
	if d.OutputCollection >= 0 {
		writeList(c, bodyKey, cp.Intern(d.OutputCollection), nullList(len(items)))
	}
	if len(items) == 0 {
		// No iterations: the body completes immediately, taking its outgoing flow.
		c.AppendElementCommand(bodyKey, model.IntentCompleting, *ei)
		return
	}
	if d.Sequential {
		seedMultiInstanceIteration(c, bodyKey, ei, cp, d, 0, items[0])
		return
	}
	for i := range items {
		seedMultiInstanceIteration(c, bodyKey, ei, cp, d, i, items[i])
	}
}

// seedMultiInstanceIteration activates one inner iteration i (0-based) of the body,
// scoped under it, binding its loopCounter (1-based) and — when the loop names an input
// element — its item, both as variable events in the inner's own scope (ADR-0077).
func seedMultiInstanceIteration(c *ProcessingContext, bodyKey uint64, body *model.ElementInstanceValue, cp *compiler.CompiledProcess, d *compiler.MultiInstanceDetail, i int, item expr.Value) {
	k := c.NewKey()
	c.AppendElementCommand(k, model.IntentActivating, model.ElementInstanceValue{
		ProcessInstanceKey: body.ProcessInstanceKey,
		ProcessDefKey:      body.ProcessDefKey,
		ElementId:          body.ElementId, // the same node, run again
		FlowScopeKey:       bodyKey,        // scoped under the body
		BpmnElementType:    body.BpmnElementType,
		MultiInstance:      miInner,
		TokenID:            k,
		ParentTokenID:      body.TokenID,
		SourceFlowId:       -1,
	})
	c.AppendVariableEvent(model.IntentVariableCreated, model.VariableValue{
		ScopeKey: k, Name: loopCounterVar, Kind: model.VarNumber, Text: strconv.Itoa(i + 1),
	})
	if inputElem := cp.Intern(d.InputElement); inputElem != "" {
		kind, b, text := expr.Classify(item)
		c.AppendVariableEvent(model.IntentVariableCreated, model.VariableValue{
			ScopeKey: k, Name: inputElem, Kind: toVarKind(kind), Bool: b, Text: text,
		})
	}
}

// multiInstanceItems evaluates a loop's iteration set over the body's scope chain to a
// concrete list of N items: the input collection's elements, or — for a cardinality —
// N nulls (there is no input element to bind). A non-list collection, a non-integer or
// negative cardinality, or an evaluation error yields no iterations rather than a panic.
func multiInstanceItems(c *ProcessingContext, d *compiler.MultiInstanceDetail, bodyKey uint64) []expr.Value {
	if d.InputCollection != nil {
		v, err := d.InputCollection.Eval(bindInputsChain(c, d.InputCollection.Inputs(), bodyKey))
		if err != nil {
			return nil
		}
		items, ok := expr.AsList(v)
		if !ok {
			return nil
		}
		return items
	}
	if d.Cardinality != nil {
		v, err := d.Cardinality.Eval(bindInputsChain(c, d.Cardinality.Inputs(), bodyKey))
		if err != nil {
			return nil
		}
		if n, ok := expr.AsInt(v); ok && n >= 0 {
			return nullList(n)
		}
	}
	return nil
}

// finishMultiInstanceIteration completes one inner iteration (ADR-0077): it collects the
// iteration's output element into the body's output collection (at its own index, so
// the list preserves iteration order regardless of completion order), drops the
// iteration's local scope, and emits Completed — which decrements the body's
// active-child counter. Then it decides how the loop proceeds: a satisfied completion
// condition ends the loop early (cancelling any still-running iterations); otherwise a
// sequential loop seeds the next iteration if one remains, and the body completes once
// the last iteration has drained. An inner never takes an outgoing flow; the body owns it.
func finishMultiInstanceIteration(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	d := cp.MultiInstance(cp.Node(ei.ElementId).MultiInstance)
	bodyKey := ei.FlowScopeKey
	idx := iterationIndex(c, key)
	if d.OutputCollection >= 0 && d.OutputElement != nil {
		val, err := d.OutputElement.Eval(bindInputsChain(c, d.OutputElement.Inputs(), key))
		if err != nil {
			val = expr.Null
		}
		setListElement(c, bodyKey, cp.Intern(d.OutputCollection), idx, val)
	}
	// The completion condition is evaluated over this iteration's scope chain (so it can
	// read loopCounter, the item, and the accumulating output collection at the body)
	// before the iteration's locals are dropped (ADR-0077).
	done := false
	if d.CompletionCondition != nil {
		v, err := d.CompletionCondition.Eval(bindInputsChain(c, d.CompletionCondition.Inputs(), key))
		done = err == nil && expr.IsTrue(v)
	}
	dropLocalScope(c, key)
	c.AppendElementEvent(key, model.IntentCompleted, *ei)
	if done {
		// Early exit: cancel any iterations still running (a no-op for a sequential loop,
		// where this was the only one live), then complete the body once its scope drains.
		terminateScope(c, ei.ProcessInstanceKey, bodyKey)
		completeScope(c, bodyKey)
		return
	}
	if d.Sequential {
		if items := multiInstanceItems(c, d, bodyKey); idx+1 < len(items) {
			if body := c.GetElementInstance(bodyKey); body != nil {
				seedMultiInstanceIteration(c, bodyKey, body, cp, d, idx+1, items[idx+1])
			}
			return // the body completes after this next iteration, not yet
		}
	}
	completeScope(c, bodyKey)
}

// promoteMultiInstanceOutput moves a completed body's output collection up to its
// enclosing scope and drops the body's own locals (the collection plus any per-
// iteration scratch), so only the assembled list escapes (ADR-0077). A no-op for a loop
// with no output collection beyond the scope drop.
func promoteMultiInstanceOutput(c *ProcessingContext, bodyKey uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	d := cp.MultiInstance(cp.Node(ei.ElementId).MultiInstance)
	if d.OutputCollection >= 0 {
		if v := c.GetVariable(bodyKey, cp.Intern(d.OutputCollection)); v != nil {
			out := *v
			out.ScopeKey = ei.FlowScopeKey // promote to the parent scope
			c.AppendVariableEvent(model.IntentVariableCreated, out)
		}
	}
	dropLocalScope(c, bodyKey)
}

// iterationIndex reads an inner iteration's 0-based index from its loopCounter (1-based).
func iterationIndex(c *ProcessingContext, key uint64) int {
	if v := c.GetVariable(key, loopCounterVar); v != nil {
		if n, err := strconv.Atoi(v.Text); err == nil && n > 0 {
			return n - 1
		}
	}
	return 0
}

// nullList returns a slice of n FEEL nulls — the initial output-collection slots and a
// cardinality loop's (bindingless) items.
func nullList(n int) []expr.Value {
	items := make([]expr.Value, n)
	for i := range items {
		items[i] = expr.Null
	}
	return items
}

// writeList writes a FEEL list value (canonical JSON) into scope under name (ADR-0077).
func writeList(c *ProcessingContext, scope uint64, name string, elems []expr.Value) {
	kind, b, text := expr.Classify(expr.ListOf(elems...))
	c.AppendVariableEvent(model.IntentVariableCreated, model.VariableValue{
		ScopeKey: scope, Name: name, Kind: toVarKind(kind), Bool: b, Text: text,
	})
}

// readList reads a stored JSON list variable back into FEEL values; nil if absent or
// not a list.
func readList(c *ProcessingContext, scope uint64, name string) []expr.Value {
	if v := c.GetVariable(scope, name); v != nil && v.Kind == model.VarJSON {
		if elems, ok := expr.AsList(expr.FromStored(expr.KindJSON, false, v.Text)); ok {
			return elems
		}
	}
	return nil
}

// setListElement sets index idx of a stored JSON list variable and writes it back; a
// no-op if idx is out of range (ADR-0077).
func setListElement(c *ProcessingContext, scope uint64, name string, idx int, val expr.Value) {
	elems := readList(c, scope, name)
	if idx < 0 || idx >= len(elems) {
		return
	}
	elems[idx] = val
	writeList(c, scope, name, elems)
}

// callActivityBehavior runs a call activity: on activation it starts a separate
// process as a child instance (in this partition) seeded with the variables to
// pass in, then parks — it does not complete until the child does. Variables in:
// the input mappings (evaluated generically into this element's local scope on
// activation) plus, when propagateAllParentVariables is on, all the caller's
// instance variables; the mapped locals win on name clash. The child records this
// element instance as its parent so its completion resumes the caller (see
// completeScope / resumeCaller). Variables out are applied there, not here, so the
// generic output-mapping path is skipped for a call activity in
// handleElementCompleting (ADR-0076).
type callActivityBehavior struct{}

func (callActivityBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	detail := cp.CallActivity(cp.Node(ei.ElementId).Detail)
	childDefKey, ok := c.resolveCallTarget(cp.Intern(detail.CalledProcessId))
	if !ok {
		// The called process is not deployed (yet), or a per-server override
		// disabled it or pinned/redirected to a target that is not deployed
		// (ADR-0105). Park — the token stays on the call activity; a
		// deploy-then-retry / incident is a follow-up (ADR-0076).
		return
	}
	var startVars []model.VariableValue
	if detail.PropagateAllParent {
		c.VariablesOfScope(ei.ProcessInstanceKey, func(v model.VariableValue) {
			startVars = append(startVars, v)
		})
	}
	// The input-mapped locals are appended last so they override same-named
	// propagated variables when re-applied to the child (last write wins).
	c.VariablesOfScope(key, func(v model.VariableValue) {
		startVars = append(startVars, v)
	})
	c.AppendCreateChildInstanceCommand(childDefKey, startVars, key)
	// Stays Activated: no Completing until the child instance completes.
}

func (callActivityBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	completeAndTakeFlows(c, key, ei)
}

// resumeCaller promotes a completed child instance's variables into its caller and
// resumes the caller's call-activity element (ADR-0076). It is called from
// completeScope when a completing instance has a parent element instance, and is a
// pure function of persisted state (the child's ParentElementInstanceKey, the
// child's variables, the caller's compiled output mappings), so recovery
// reconstructs it identically. A caller already gone (interrupted) is a no-op.
func resumeCaller(c *ProcessingContext, childScope, callerKey uint64) {
	caller := c.GetElementInstance(callerKey)
	if caller == nil {
		return
	}
	callerCp := c.process(caller.ProcessDefKey)
	detail := callerCp.CallActivity(callerCp.Node(caller.ElementId).Detail)
	if detail.PropagateAllChild {
		// All child variables merge into the caller's instance scope.
		c.VariablesOfScope(childScope, func(v model.VariableValue) {
			v.ScopeKey = caller.ProcessInstanceKey
			c.AppendVariableEvent(model.IntentVariableCreated, v)
		})
	} else {
		// Only the output mappings escape: each source is FEEL over the child's
		// variables, promoted to the caller's instance scope.
		for _, m := range callerCp.IOOutputs(caller.ElementId) {
			c.AppendVariableEvent(model.IntentVariableCreated, evalMapping(c, callerCp, m, childScope, caller.ProcessInstanceKey))
		}
	}
	c.AppendElementCommand(callerKey, model.IntentCompleting, *caller)
}

type endEventBehavior struct{}

func (endEventBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	c.AppendElementCommand(key, model.IntentCompleting, *ei)
}

func (endEventBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	c.AppendElementEvent(key, model.IntentCompleted, *ei) // decrements scope's active children
	completeScope(c, ei.FlowScopeKey)                     // completes the scope owner if it drained
}
