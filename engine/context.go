package engine

import (
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// ProcessingContext is the surface every behavior works through while a command
// is processed. A behavior may do three things: read state, write events (a
// fact that also mutates state), and schedule what comes next. It never touches
// the log or fsync directly — it only accumulates into the batch (invariant I2:
// nothing becomes visible before the batch is durable).
type ProcessingContext struct {
	cmd     Command
	tx      *stateTx
	p       *Processor
	lastPos uint64 // position of the most recent event written here (for causality)
}

// process returns the immutable compiled definition (invariant I5: read by
// index, never parsed).
// latestDefKey resolves a bpmn process id to the newest deployed definition key
// for a call activity's `latest` binding; ok is false if no such process is
// deployed (ADR-0076).
func (c *ProcessingContext) latestDefKey(processId string) (uint64, bool) {
	k, ok := c.p.latestProcess[processId]
	return k, ok
}

// resolveCallTarget resolves the definition a call activity starts as a child,
// consulting the per-server override first and falling back to the default `latest`
// resolution (ADR-0105). Precedence for an override: disabled parks; a pinned key
// wins if still deployed; a redirect resolves the target's latest (one hop, so no
// cycle). ok is false when nothing resolves (park), identical to an undeployed
// callee (ADR-0076). A pure read of run-loop-owned maps — no allocation (I1).
func (c *ProcessingContext) resolveCallTarget(processId string) (uint64, bool) {
	if ov, has := c.p.callOverrides[processId]; has {
		switch {
		case ov.Disabled:
			return 0, false
		case ov.PinnedDefKey != 0:
			_, deployed := c.p.processes[ov.PinnedDefKey]
			return ov.PinnedDefKey, deployed
		case ov.RedirectProcessId != "":
			return c.latestDefKey(ov.RedirectProcessId)
		}
	}
	return c.latestDefKey(processId)
}

func (c *ProcessingContext) process(defKey uint64) *compiler.CompiledProcess {
	return c.p.processes[defKey]
}

// NewKey mints a fresh entity key. The minted key is frozen into the event that
// uses it, so replay reproduces it without regeneration (invariant I6).
func (c *ProcessingContext) NewKey() uint64 { return c.p.keygen.next() }

// Now reads wall-clock time. It is captured into events here, never inside
// applyToState (invariant I4).
func (c *ProcessingContext) Now() int64 { return c.p.clock.Now() }

// GetElementInstance reads element-instance state through the in-flight
// transaction (sees this batch's uncommitted writes).
func (c *ProcessingContext) GetElementInstance(key uint64) *model.ElementInstanceValue {
	v, err := c.tx.GetElementInstance(key)
	c.p.fail(err)
	return v
}

// GetJob reads job state through the in-flight transaction.
func (c *ProcessingContext) GetJob(key uint64) *model.JobValue {
	v, err := c.tx.GetJob(key)
	c.p.fail(err)
	return v
}

// JobOfElement returns the key of the job held by an element instance and whether
// it holds one, through the in-flight transaction. An interrupting boundary event
// uses it to cancel the host activity's job when it terminates the host.
func (c *ProcessingContext) JobOfElement(elKey uint64) (uint64, bool) {
	jobKey, ok, err := c.tx.JobOfElement(elKey)
	c.p.fail(err)
	return jobKey, ok
}

// GetIncident reads the incident attached to an element instance through the
// in-flight transaction, or nil if there is none (ADR-0061).
func (c *ProcessingContext) GetIncident(elKey uint64) *model.IncidentValue {
	v, err := c.tx.GetIncident(elKey)
	c.p.fail(err)
	return v
}

// GetProcessInstance reads process-instance state through the in-flight transaction.
func (c *ProcessingContext) GetProcessInstance(key uint64) *model.ProcessInstanceValue {
	v, err := c.tx.GetProcessInstance(key)
	c.p.fail(err)
	return v
}

// GetVariable reads a scope's variable by name through the in-flight transaction
// (sees writes from earlier in this batch, e.g. seeded start variables).
func (c *ProcessingContext) GetVariable(scope uint64, name string) *model.VariableValue {
	v, err := c.tx.GetVariable(scope, name)
	c.p.fail(err)
	return v
}

// VariablesOfScope calls fn with each variable owned by scope, read through the
// in-flight transaction (so it sees this batch's writes). Values are collected
// into a fresh slice before fn runs, so fn may emit variable events (e.g. deleting
// the scope's locals) without disturbing the underlying scan. Used to drop an
// activity-local scope on completion (ADR-0068).
func (c *ProcessingContext) VariablesOfScope(scope uint64, fn func(v model.VariableValue)) {
	var vars []model.VariableValue
	if err := c.tx.VariablesOfScope(scope, func(v *model.VariableValue) error {
		vars = append(vars, *v)
		return nil
	}); err != nil {
		c.p.fail(err)
		return
	}
	for i := range vars {
		fn(vars[i])
	}
}

// ForEachElementInstance calls fn with the key of every element instance
// belonging to a process instance, through the in-flight transaction — so it sees
// element instances created earlier in the same batch, consistently with
// GetElementInstance, GetJob, and ActiveChildren (all tx-reads). This matters for a
// terminate end event reached in the same batch as a parallel sibling's activation
// (ADR-0116): the sibling is not yet committed, but it is in the tx, so the scope
// teardown finds it. Keys are collected before fn runs so fn may mutate
// element-instance state (e.g. emit terminations) without disturbing the scan.
func (c *ProcessingContext) ForEachElementInstance(procKey uint64, fn func(elKey uint64)) {
	var keys []uint64
	if err := c.tx.ElementInstancesOfProcess(procKey, func(k uint64, _ *model.ElementInstanceValue) error {
		keys = append(keys, k)
		return nil
	}); err != nil {
		c.p.fail(err)
		return
	}
	for _, k := range keys {
		fn(k)
	}
}

// ForEachActiveProcessInstance calls fn with the key and value of every live
// process instance, via the committed process-instance column family. Entries are
// collected before fn runs so fn may emit events/commands (e.g. terminate a child)
// without disturbing the scan. Used to find the child a call activity started, via
// its persisted parent link (ADR-0076).
func (c *ProcessingContext) ForEachActiveProcessInstance(fn func(piKey uint64, pi *model.ProcessInstanceValue)) {
	type entry struct {
		key uint64
		v   model.ProcessInstanceValue
	}
	var entries []entry
	if err := c.p.store.ActiveProcessInstances(func(k uint64, v *model.ProcessInstanceValue) error {
		entries = append(entries, entry{key: k, v: *v})
		return nil
	}); err != nil {
		c.p.fail(err)
		return
	}
	for i := range entries {
		fn(entries[i].key, &entries[i].v)
	}
}

// ForEachStartTimer calls fn with the key and value of every armed start timer,
// read from the committed timer index. Entries are collected before fn runs so fn
// may emit timer events (arming/retiring) without disturbing the scan. Used only
// when a definition is deployed (off the hot path), to arm and supersede start
// timers (ADR-0051).
func (c *ProcessingContext) ForEachStartTimer(fn func(key uint64, v model.TimerValue)) {
	type entry struct {
		key uint64
		v   model.TimerValue
	}
	var entries []entry
	if err := c.p.store.StartTimers(func(k uint64, v *model.TimerValue) error {
		entries = append(entries, entry{key: k, v: *v})
		return nil
	}); err != nil {
		c.p.fail(err)
		return
	}
	for _, e := range entries {
		fn(e.key, e.v)
	}
}

// ElementInstancesOnNode returns the keys of every live element instance sitting
// on the given BPMN node within a process instance, seen through the in-flight
// transaction (so it includes one activated earlier in this batch). A parallel
// join uses it to count how many tokens have arrived on its incoming flows.
func (c *ProcessingContext) ElementInstancesOnNode(procKey uint64, elementId int32) []uint64 {
	var keys []uint64
	err := c.tx.ElementInstancesOfProcess(procKey, func(elKey uint64, v *model.ElementInstanceValue) error {
		if v.ElementId == elementId {
			keys = append(keys, elKey)
		}
		return nil
	})
	c.p.fail(err)
	return keys
}

// TokenCanStillReach reports whether any live token could still arrive at nodeId:
// an active element instance sitting on a node from which nodeId is reachable
// (per reaches), or a token in flight as an element-activating command not yet
// processed — the rest of this batch's queue plus followups generated so far —
// targeting such a node or nodeId itself. Tokens already parked on nodeId are the
// join's own arrivals and are excluded. An inclusive join fires only when this is
// false. Considering in-flight commands is what keeps two pass-through branches
// from each firing the join separately.
func (c *ProcessingContext) TokenCanStillReach(procKey uint64, nodeId int32, reaches map[int32]bool) bool {
	upstream := false
	err := c.tx.ElementInstancesOfProcess(procKey, func(_ uint64, v *model.ElementInstanceValue) error {
		if v.ElementId != nodeId && reaches[v.ElementId] {
			upstream = true
		}
		return nil
	})
	c.p.fail(err)
	if upstream {
		return true
	}
	scan := func(cmds []Command) bool {
		for i := range cmds {
			cmd := &cmds[i]
			if cmd.ValueType != model.VTElementInstance || cmd.Intent != model.IntentActivating {
				continue
			}
			e := &cmd.Value.element
			if e.ProcessInstanceKey == procKey && (e.ElementId == nodeId || reaches[e.ElementId]) {
				return true
			}
		}
		return false
	}
	return scan(c.p.queue[c.p.batchPos+1:]) || scan(c.p.followups)
}

// ActiveChildren returns the active-child count of a scope (e.g. to detect that
// a process instance has finished).
func (c *ProcessingContext) ActiveChildren(scope uint64) int32 {
	n, err := c.tx.ActiveChildren(scope)
	c.p.fail(err)
	return n
}

// IsCanceling reports whether the transaction scope txKey was marked cancelling by a cancel
// end event (ADR-0108).
func (c *ProcessingContext) IsCanceling(txKey uint64) bool {
	ok, err := c.tx.IsCanceling(txKey)
	c.p.fail(err)
	return ok
}

// AppendProcessInstanceEvent records a process-instance lifecycle fact.
func (c *ProcessingContext) AppendProcessInstanceEvent(key uint64, intent model.Intent, v model.ProcessInstanceValue) {
	c.appendEvent(key, model.VTProcessInstance, intent, inflightValue{process: v})
}

// AppendProcessInstanceCommand schedules a process-instance command (e.g. the
// Terminating that cancels a call activity's child) for a later batch — the same
// command an API cancel enqueues, so the child tears down through the identical
// path however its termination was triggered (ADR-0076).
func (c *ProcessingContext) AppendProcessInstanceCommand(key uint64, intent model.Intent, v model.ProcessInstanceValue) {
	c.appendCommand(key, model.VTProcessInstance, intent, inflightValue{process: v})
}

// AppendElementEvent records an element-instance lifecycle fact.
func (c *ProcessingContext) AppendElementEvent(key uint64, intent model.Intent, v model.ElementInstanceValue) {
	c.appendEvent(key, model.VTElementInstance, intent, inflightValue{element: v})
}

// AppendJobEvent records a job lifecycle fact.
func (c *ProcessingContext) AppendJobEvent(key uint64, intent model.Intent, v model.JobValue) {
	c.appendEvent(key, model.VTJob, intent, inflightValue{job: v})
}

// AppendIncidentEvent records an incident lifecycle fact (created or resolved).
// The key is the element instance the incident is attached to, and the value
// carries that key too, so applyToState can locate the index entry from the event
// alone on either intent (ADR-0061).
func (c *ProcessingContext) AppendIncidentEvent(intent model.Intent, v model.IncidentValue) {
	c.appendEvent(v.ElementInstanceKey, model.VTIncident, intent, inflightValue{incident: v})
}

// AppendTimerEvent records a timer lifecycle fact (created or triggered).
func (c *ProcessingContext) AppendTimerEvent(key uint64, intent model.Intent, v model.TimerValue) {
	c.appendEvent(key, model.VTTimer, intent, inflightValue{timer: v})
}

// AppendVariableEvent records a variable write. The value is data (a name and
// contents), so unlike the graph-derived events this one does allocate for its
// strings — variables are runtime data, not hot-path token movement.
func (c *ProcessingContext) AppendVariableEvent(intent model.Intent, v model.VariableValue) {
	c.appendEvent(v.ScopeKey, model.VTVariable, intent, inflightValue{variable: v})
	c.markConditionDirty(v.ScopeKey)
}

// markConditionDirty notes that a variable changed in the given scope, so the batch loop will
// schedule a re-check of the instance's conditional events (ADR-0134). It resolves the scope to
// its process instance and records it (deduplicated); an instance whose process has no
// conditional event records nothing, so a conditional-free process pays nothing on a write.
func (c *ProcessingContext) markConditionDirty(scopeKey uint64) {
	piKey := scopeKey
	if ei := c.GetElementInstance(scopeKey); ei != nil {
		piKey = ei.ProcessInstanceKey
	}
	pi := c.GetProcessInstance(piKey)
	if pi == nil || !c.process(pi.ProcessDefKey).HasConditionalEvents() {
		return
	}
	for _, k := range c.p.condDirty {
		if k == piKey {
			return
		}
	}
	c.p.condDirty = append(c.p.condDirty, piKey)
}

// AppendDataObjectEvent records a data-object write (created or state-changed).
// Like a variable it carries genuine runtime data (a name, a data state, and a
// value), so it allocates for its strings — data objects are runtime data, not
// hot-path token movement (ADR-0053). The event is keyed by the owning scope.
func (c *ProcessingContext) AppendDataObjectEvent(intent model.Intent, v model.DataObjectValue) {
	c.appendEvent(v.ScopeKey, model.VTDataObject, intent, inflightValue{dataObject: v})
}

// AppendDecisionEvaluationEvent records how a business rule task's decision was
// made — its inputs, outputs, and trace — as append-only history (ADR-0066). The
// worker evaluated the decision off the processor goroutine and froze the result
// onto the completion command; this event carries genuine runtime data (JSON
// payloads), so it allocates for its strings, not hot-path token movement. It is
// keyed by the owning process instance, so a scope-wide scan yields every decision
// an instance evaluated in order.
func (c *ProcessingContext) AppendDecisionEvaluationEvent(v model.DecisionEvaluationValue) {
	c.appendEvent(v.ProcessInstanceKey, model.VTDecisionEvaluation, model.IntentDecisionEvaluated, inflightValue{decisionEval: v})
}

// AppendVariableAuditEvent records who set a variable from outside the model — an
// operator override — as append-only audit history (ADR-0098). Like a variable it
// carries genuine runtime data (an actor, a name, and contents), so it allocates for
// its strings; it rides only on the non-hot-path variable-modify command, never token
// movement. It is keyed by the owning process instance, so a scope-wide scan yields
// every override an instance received in order.
func (c *ProcessingContext) AppendVariableAuditEvent(v model.VariableAuditValue) {
	c.appendEvent(v.ProcessInstanceKey, model.VTVariableAudit, model.IntentVariableAudited, inflightValue{variableAudit: v})
}

// AppendCompensableEvent records a compensation-index change: IntentCompensableRecorded
// retains a completed compensable activity (keyed under its scope in completion order),
// and IntentCompensableConsumed drops one once it has been compensated (ADR-0103). Both
// ride only on the command path (a completion or a compensation throw), never token
// movement; applyToState folds them into the compensable index so recovery rebuilds it
// (invariant I6). Keyed by the owning process instance, like the other history events.
func (c *ProcessingContext) AppendCompensableEvent(intent model.Intent, v model.CompensableValue) {
	c.appendEvent(v.ProcessInstanceKey, model.VTCompensable, intent, inflightValue{compensable: v})
}

// GetDataObject reads a scope's data object by name through the in-flight
// transaction (sees writes from earlier in this batch). A data-output association
// uses it to keep the object's current value or state when the write changes only
// one of them (ADR-0058); nil if the object is absent.
func (c *ProcessingContext) GetDataObject(scope uint64, name string) *model.DataObjectValue {
	v, err := c.tx.GetDataObject(scope, name)
	c.p.fail(err)
	return v
}

// AppendMessageSubscriptionEvent records a message-subscription fact (created or
// correlated). The key is the waiting element instance's key, and the value
// carries the match pair, so applyToState can locate the index entry from the
// event alone (invariant I4).
func (c *ProcessingContext) AppendMessageSubscriptionEvent(key uint64, intent model.Intent, v model.MessageSubscriptionValue) {
	c.appendEvent(key, model.VTMessageSubscription, intent, inflightValue{subscription: v})
}

// AppendSignalSubscriptionEvent records a signal-subscription fact (created or
// correlated). The key is the waiting element instance's key, and the value
// carries the signal name, so applyToState can locate the index entry from the
// event alone (invariant I4). A signal reuses the message subscription intents
// (SubscriptionCreated / SubscriptionCorrelated) over a separate family (ADR-0088).
func (c *ProcessingContext) AppendSignalSubscriptionEvent(key uint64, intent model.Intent, v model.SignalSubscriptionValue) {
	c.appendEvent(key, model.VTSignal, intent, inflightValue{signalSub: v})
}

// AppendMessageFlowEvent retains one delivered message flow as history for the
// collaboration replay (ADR-0038). It is keyed by its receiving definition (the
// state index leads with it); the event's header timestamp and position order it
// on the replay timeline. Emitted once per correlated catch event and once per
// message-start instantiation, so both kinds of cross-pool delivery are recorded.
func (c *ProcessingContext) AppendMessageFlowEvent(v model.MessageFlowValue) {
	c.appendEvent(v.ReceiverProcessDefKey, model.VTMessageFlow, model.IntentMessagePublished, inflightValue{messageFlow: v})
}

// AppendInboundDeliveryEvent advances an external source's inbound high-water mark
// (ADR-0075), keyed on the receiving definition space as a neutral key (the record
// carries the source id and sequence it needs). Emitted in the same batch as the
// message publish it guards, so the dedup mark and the effects it authorizes commit
// atomically under one fsync (invariant I2).
func (c *ProcessingContext) AppendInboundDeliveryEvent(v model.InboundDeliveryValue) {
	c.appendEvent(0, model.VTInboundDelivery, model.IntentInboundDeliveryApplied, inflightValue{inbound: v})
}

// AppendElementCommand schedules an element-instance command for a later batch.
func (c *ProcessingContext) AppendElementCommand(key uint64, intent model.Intent, v model.ElementInstanceValue) {
	c.appendCommand(key, model.VTElementInstance, intent, inflightValue{element: v})
}

// AppendCreateInstanceCommand schedules creation of a new instance of defKey for
// a later batch, seeded with vars (each re-scoped to the new instance when it is
// created) and the correlationKey the created instance records (empty for a
// timer or API start). A correlating message uses it to instantiate a
// message-start process (ADR-0035). Deferring to a followup keeps instance
// creation on the same command path as an API-submitted create, so its events —
// and thus recovery — are identical however the create was triggered.
func (c *ProcessingContext) AppendCreateInstanceCommand(defKey uint64, vars []model.VariableValue, correlationKey string) {
	c.p.followups = append(c.p.followups, Command{
		ValueType: model.VTProcessInstance,
		Intent:    model.IntentActivating,
		Value:     inflightValue{process: model.ProcessInstanceValue{ProcessDefKey: defKey, CorrelationKey: correlationKey}},
		StartVars: vars,
		SourcePos: c.lastPos,
	})
}

// AppendCreateChildInstanceCommand is AppendCreateInstanceCommand for a call
// activity: the created instance records the caller's call-activity element
// instance as its parent, so on completion it resumes that element (ADR-0076).
func (c *ProcessingContext) AppendCreateChildInstanceCommand(defKey uint64, vars []model.VariableValue, parentElementKey uint64) {
	c.p.followups = append(c.p.followups, Command{
		ValueType: model.VTProcessInstance,
		Intent:    model.IntentActivating,
		Value:     inflightValue{process: model.ProcessInstanceValue{ProcessDefKey: defKey, ParentElementInstanceKey: parentElementKey}},
		StartVars: vars,
		SourcePos: c.lastPos,
	})
}

// NotifyJobAvailable registers a post-fsync notification that a job of the given
// type is available (invariant I2: runs after the batch is durable).
func (c *ProcessingContext) NotifyJobAvailable(jobType int32) {
	c.p.sideEffects = append(c.p.sideEffects, sideEffect{jobType: jobType})
}

// appendEvent writes an event into the batch AND mutates state from that same
// record via applyToState. Doing both from one record is what keeps the log and
// state in lockstep — recovery replays the exact same applyToState.
func (c *ProcessingContext) appendEvent(key uint64, vt model.ValueType, intent model.Intent, v inflightValue) {
	c.p.position++
	c.p.batchRecords = append(c.p.batchRecords, eventRecord{
		header: model.RecordHeader{
			Position:    c.p.position,
			SourcePos:   c.cmd.SourcePos,
			Key:         key,
			Timestamp:   c.p.clock.Now(),
			RecordType:  model.RecordEvent,
			ValueType:   vt,
			Intent:      intent,
			PartitionId: c.p.partition,
		},
		value: v,
	})
	er := &c.p.batchRecords[len(c.p.batchRecords)-1]
	c.lastPos = er.header.Position
	c.p.fail(applyToState(c.tx, er.header, &er.value))
}

// appendCommand schedules an internal command for a later batch. Its SourcePos
// points at the most recent event written here, threading causality.
func (c *ProcessingContext) appendCommand(key uint64, vt model.ValueType, intent model.Intent, v inflightValue) {
	c.p.followups = append(c.p.followups, Command{
		Key:       key,
		ValueType: vt,
		Intent:    intent,
		Value:     v,
		SourcePos: c.lastPos,
	})
}
