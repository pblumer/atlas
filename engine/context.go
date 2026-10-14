package engine

import (
	"encoding/json"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
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
	// producer is the element instance every variable written here is attributed to
	// (ADR-0219). It is derived from the command being
	// processed — an element command names its element instance — and overridden by the
	// handlers that write into an element other than the command's own: a job's outputs
	// belong to the job's task, a message payload to the catch event it arrived on, a
	// child instance's result to the call activity that started it. 0 means no element
	// wrote it (start variables, an operator override).
	producer uint64
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

// namespaceValue is the value of a namespace an instance of cp starts with
// (ADR-draft-constant-namespaces): a JSON object holding each member the process declares
// in it — for a constant, the value the installation holds on this server; for a fact,
// what Atlas knows — else the member's fallback, else nothing, so the member reads as
// null. The object is built once per definition and configuration change and cached on
// the processor, so the creations after the first allocate nothing (I1); the text is
// frozen into the creation event like any variable's, so replay never builds it (I6).
func (c *ProcessingContext) namespaceValue(defKey uint64, cp *compiler.CompiledProcess, namespace string) string {
	cache := c.p.serverNamespace
	if namespace == compiler.ApplicationNamespace {
		cache = c.p.appNamespace
	}
	if text, ok := cache[defKey]; ok {
		return text
	}
	app := c.p.defApplication[defKey]
	members := map[string]any{}
	for _, k := range cp.Constants() {
		if k.Namespace != namespace {
			continue
		}
		if v, ok := c.heldMember(namespace, app, &k); ok {
			members[k.Name] = v
		} else if k.HasFallback {
			members[k.Name] = namespaceMember(toVarKind(k.Kind), k.Bool, k.Text)
		}
	}
	_, _, text := expr.Classify(expr.FromJSON(members))
	cache[defKey] = text
	return text
}

// heldMember is what this installation holds for one declared member of a namespace: a
// constant's held value, or a fact Atlas knows. An empty fact is not held — a node with no
// environment set says nothing, and the member falls back like a missing constant.
func (c *ProcessingContext) heldMember(namespace, app string, k *compiler.Constant) (any, bool) {
	if k.Info {
		var fact string
		switch {
		case namespace == compiler.ApplicationNamespace && k.Name == "id":
			fact = app
		case namespace == compiler.ApplicationNamespace && k.Name == "key":
			fact = c.p.appInfo[app].Key
		case namespace == compiler.ApplicationNamespace && k.Name == "name":
			fact = c.p.appInfo[app].Name
		case k.Name == "id":
			fact = c.p.serverInfo.ID
		case k.Name == "name":
			fact = c.p.serverInfo.Name
		case k.Name == "environment":
			fact = c.p.serverInfo.Environment
		}
		return fact, fact != ""
	}
	held := c.p.serverConstants
	if namespace == compiler.ApplicationNamespace {
		held = c.p.appConstants[app]
	}
	v, ok := held[k.Name]
	if !ok {
		return nil, false
	}
	return namespaceMember(v.Kind, v.Bool, v.Text), true
}

// namespaceMember turns a stored value back into the JSON value it is a member of the
// namespace as. The text is canonical — the server parsed it when it was saved, the
// compiler when the model was deployed — so a JSON member that does not decode cannot
// occur, and would be null rather than a failed start.
func namespaceMember(kind model.VarKind, b bool, text string) any {
	switch kind {
	case model.VarString:
		return text
	case model.VarNumber:
		return json.Number(text)
	case model.VarBool:
		return b
	case model.VarJSON:
		dec := json.NewDecoder(strings.NewReader(text))
		dec.UseNumber()
		var v any
		if dec.Decode(&v) == nil {
			return v
		}
	}
	return nil
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

// ChildInstancesOf returns the live child process instances a call-activity element
// instance started, from the committed childByParent index (ADR-0076).
//
// It replaced a walk of every live process instance comparing each one's
// ParentElementInstanceKey. That walk was O(instances) *per call activity torn
// down*, so cancelling a parent holding many children cost the product of the two —
// on the single-writer loop, which is every other request's queue as well. The keys
// are collected before the caller acts on them, as the walk did, so the caller may
// emit events for each without disturbing the read.
//
// Read through the transaction, so a child activated earlier in this same batch is
// visible to the teardown. ADR-0238 originally read the committed store here and
// called that a determinism measure; it is not one. The records this transaction
// has already applied are a deterministic function of the commands processed so
// far — replay applies the same records in the same order — so seeing them is as
// reproducible as not seeing them, and strictly more correct. What the committed
// view actually produced was a child that outlived the caller cancelled in the
// batch that created it (ADR-0284).
func (c *ProcessingContext) ChildInstancesOf(callElKey uint64) []uint64 {
	var children []uint64
	if err := c.tx.ChildInstancesOfParent(callElKey, func(childPiKey uint64) error {
		children = append(children, childPiKey)
		return nil
	}); err != nil {
		c.p.fail(err)
		return nil
	}
	return children
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

// Arrival is one token waiting on a join: the element instance holding it and the
// incoming sequence flow it came in on.
//
// The flow is what a join has to count by. BPMN 2.0.2 §13.4 activates a parallel
// gateway when there is at least one token on *each* incoming sequence flow, and
// consumes exactly one from each — which is a different question from how many
// tokens are on the node, and gives a different answer whenever a flow carries two
// (ADR-0290).
type Arrival struct {
	Key  uint64
	Flow int32
}

// ArrivalsOnNode returns the tokens waiting on the given node in one execution
// scope, oldest first, each with the flow it arrived on. The order is the index's:
// element-instance keys ascend with their minting, so oldest-first is what the scan
// yields, and it is what makes a join's choice of which token to consume both
// first-in-first-out and a pure function of state.
//
// The result aliases a processor-owned buffer and is valid until the next call.
func (c *ProcessingContext) ArrivalsOnNode(procKey, scopeKey uint64, elementId int32) []Arrival {
	out := c.p.arrivalBuf[:0]
	err := c.tx.ElementInstancesOfProcess(procKey, func(elKey uint64, v *model.ElementInstanceValue) error {
		if v.ElementId == elementId && v.FlowScopeKey == scopeKey {
			out = append(out, Arrival{Key: elKey, Flow: v.SourceFlowId})
		}
		return nil
	})
	c.p.fail(err)
	c.p.arrivalBuf = out
	return out
}

// OldestPerFlow reduces arrivals to one token per distinct incoming flow — the
// oldest on each — which is the set a join firing consumes. Everything it leaves
// out is surplus: a second token on a flow that already has one, which belongs to
// the *next* firing and must not be swallowed by this one.
//
// The result aliases a second processor-owned buffer, so it stays valid across a
// re-scan of the node.
func (c *ProcessingContext) OldestPerFlow(arrivals []Arrival) []Arrival {
	set := c.p.joinSetBuf[:0]
	for _, a := range arrivals {
		seen := false
		for _, s := range set {
			if s.Flow == a.Flow {
				seen = true
				break
			}
		}
		if !seen {
			set = append(set, a)
		}
	}
	c.p.joinSetBuf = set
	return set
}

// TokenCanStillReach reports whether any live token in scopeKey could still arrive
// at nodeId: an active element instance sitting on a node from which nodeId is
// reachable (per reaches), or a token in flight as an element-activating command not
// yet processed — the rest of this batch's queue plus followups generated so far —
// targeting such a node or nodeId itself. Tokens already parked on nodeId are the
// join's own arrivals and are excluded. An inclusive join fires only when this is
// false. Considering in-flight commands is what keeps two pass-through branches from
// each firing the join separately.
//
// scopeKey scopes the question the same way it scopes a parallel join's count
// (ADR-0277): a token in a *sibling* iteration of a
// multi-instance subprocess is on the same node ids but can never arrive here, and
// letting it hold the join open made every iteration wait for the slowest one.
//
// Narrowing to one scope does not blind the join to a token running inside a nested
// subprocess on one of its branches: reaches follows sequence flows, which never
// cross a scope boundary, so an inner node is not in reaches at all — what keeps the
// join waiting is the subprocess's own element instance, which sits in *this* scope
// on a node that does reach the join.
func (c *ProcessingContext) TokenCanStillReach(procKey, scopeKey uint64, nodeId int32, reaches compiler.NodeSet) bool {
	upstream := false
	err := c.tx.ElementInstancesOfProcess(procKey, func(_ uint64, v *model.ElementInstanceValue) error {
		if v.FlowScopeKey == scopeKey && v.ElementId != nodeId && reaches.Has(v.ElementId) {
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
			if e.ProcessInstanceKey != procKey || e.FlowScopeKey != scopeKey {
				continue
			}
			if e.ElementId == nodeId || reaches.Has(e.ElementId) {
				return true
			}
		}
		return false
	}
	return scan(c.p.queue[c.p.batchPos+1:]) || scan(c.p.followups)
}

// TokenEntering reports whether a token is on its way into scopeKey over a sequence
// flow: an element that completed and took a flow has written its Completed already,
// and its successor's activation is still a queued command, so the token is in
// neither place the active-child counter sees (#1013). Only a flow's activation
// counts — one with SourceFlowId >= 0. An armed boundary, an event-subprocess
// trigger, a compensation handler or an ad-hoc activity is entered some other way,
// and none of them is a token a scope waits for in order to drain.
//
// It reads the queue the way [ProcessingContext.TokenCanStillReach] does, and for the
// same reason that is safe across a restart: the queue is the batch's continuation,
// restored on recovery (ADR-0271), so the commands seen here are the ones a resumed
// processor sees.
func (c *ProcessingContext) TokenEntering(scopeKey uint64) bool {
	return activationEntering(c.p.queue[c.p.batchPos+1:], scopeKey) ||
		activationEntering(c.p.followups, scopeKey)
}

// activationEntering is TokenEntering over one command list.
func activationEntering(cmds []Command, scopeKey uint64) bool {
	for i := range cmds {
		cmd := &cmds[i]
		if cmd.ValueType == model.VTElementInstance && cmd.Intent == model.IntentActivating &&
			cmd.Value.element.FlowScopeKey == scopeKey && cmd.Value.element.SourceFlowId >= 0 {
			return true
		}
	}
	return false
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

// AppendVariableEvent records a variable write and reports whether it happened: a
// value past the variable budget is refused, with an incident naming it
// (ADR-0294). Most callers write engine-derived values —
// a loop index, a counter — which cannot exceed a budget sized for a business record,
// and they ignore the result. A caller that writes something a model or a worker
// produced should not.
//
// The value is data (a name and
// contents), so unlike the graph-derived events this one does allocate for its
// strings — variables are runtime data, not hot-path token movement.
func (c *ProcessingContext) AppendVariableEvent(intent model.Intent, v model.VariableValue) bool {
	// Stamped here rather than at each call site so no path can forget it, and so a
	// value copied out of one scope and re-written into another (a call activity
	// promoting its child's result, a loop promoting its body's) can never carry the
	// producer of the write it was copied from (ADR-0219).
	v.ProducerKey = c.producer
	cp, atRoot := c.processOfScope(v.ScopeKey)
	// Whether this write belongs in the variable value index is stamped here for the
	// same reason, and for one more: applyToState holds the record and nothing else,
	// so it cannot ask a compiled process whether a name is searchable. Deciding it
	// here freezes the answer into the event, and replay indexes exactly what the live
	// write indexed (I6).
	v.Indexed = c.indexesVariable(cp, atRoot, v.Name)
	// A write of a constant's name into an instance that already holds the constant is
	// dropped, whatever wrote it (ADR-0454). The writer is told it
	// happened: nothing about it is wrong enough to park, and the constant is what the
	// instance was started with. A delete is exempt for the reason it is exempt below.
	if intent != model.IntentVariableDeleted && c.keepsConstant(cp, atRoot, &v) {
		return true
	}
	// A declared personal value that arrived readable is refused, whatever wrote it, and
	// so is a write that moves the instance's data subject out from under values already
	// sealed (ADR-0314). A delete is exempt for the reason it is exempt from the budget
	// below.
	if intent != model.IntentVariableDeleted && refusePersonalWrite(c, cp, &v) {
		return false
	}
	// A value past the budget is not written and not silently dropped: an incident
	// names the variable and both sizes (ADR-0294) — though resolving it does not
	// write the value again (#1123). Deleting is never refused — a delete carries
	// no value, and refusing to shrink an instance would be the wrong way round.
	return c.appendVariable(intent, v, c.p.variableCeiling())
}

// appendCollection is AppendVariableEvent measured against the *collection* budget
// rather than the variable one. A multi-instance activity's output collection is what
// a legitimate loop accumulates at the iteration ceiling, which is a different
// question from what one business record may weigh — and measuring it against the
// smaller number would refuse ordinary loops
// (ADR-0294).
func (c *ProcessingContext) appendCollection(intent model.Intent, v model.VariableValue) bool {
	v.ProducerKey = c.producer
	cp, atRoot := c.processOfScope(v.ScopeKey)
	v.Indexed = c.indexesVariable(cp, atRoot, v.Name)
	if intent != model.IntentVariableDeleted && c.keepsConstant(cp, atRoot, &v) {
		return true
	}
	if intent != model.IntentVariableDeleted && refusePersonalWrite(c, cp, &v) {
		return false
	}
	return c.appendVariable(intent, v, c.p.collectionCeiling())
}

// appendVariableElement records that one element of a list variable became this
// value, rather than recording the list. It is what a multi-instance activity emits
// per finished iteration, and it is the whole of the fix for a cost that grew with
// the square of the iteration count: the record is the size of one result, not of the
// collection so far (ADR-0296).
//
// The collection's own ceiling is not checked here, because this write does not carry
// the collection. Its caller checks it: setListElement measures the list this element
// produces before it records the element. ADR-0296 first left that out, trusting the
// variable budget times the iteration bound to cap the list; nothing then refused a
// list that outgrew its budget while it filled, and the promotion that did refuse it
// dropped the whole collection without an incident (amended 2026-09-28).
func (c *ProcessingContext) appendVariableElement(v model.VariableValue) bool {
	v.ProducerKey = c.producer
	cp, atRoot := c.processOfScope(v.ScopeKey)
	v.Indexed = c.indexesVariable(cp, atRoot, v.Name)
	if refusePersonalWrite(c, cp, &v) {
		return false
	}
	c.appendEvent(v.ScopeKey, model.VTVariable, model.IntentVariableElementSet, inflightValue{variable: v})
	c.markConditionDirty(v.ScopeKey)
	return true
}

// appendVariable is the shared tail of both: a value past ceiling is not written and
// not silently dropped — an incident names the variable and both sizes, and resolving
// retries. Deleting is never refused: a delete carries no value, and refusing to
// shrink an instance would be the budget working backwards.
func (c *ProcessingContext) appendVariable(intent model.Intent, v model.VariableValue, ceiling int64) bool {
	if intent != model.IntentVariableDeleted && int64(len(v.Text)) > ceiling {
		parkOversizedWrite(c, v.ScopeKey, v.Name, int64(len(v.Text)), ceiling)
		return false
	}
	c.appendEvent(v.ScopeKey, model.VTVariable, intent, inflightValue{variable: v})
	c.markConditionDirty(v.ScopeKey)
	return true
}

// processOfScope is the compiled process a write's scope belongs to, and whether that
// scope is the instance's own root rather than an activity-local one.
//
// Both per-write questions below need exactly this, so it is resolved once: the reads
// are the same one or two the index question already did, because an element instance
// names its definition itself.
func (c *ProcessingContext) processOfScope(scopeKey uint64) (cp *compiler.CompiledProcess, atRoot bool) {
	if ei := c.GetElementInstance(scopeKey); ei != nil {
		return c.process(ei.ProcessDefKey), false
	}
	if pi := c.GetProcessInstance(scopeKey); pi != nil {
		return c.process(pi.ProcessDefKey), true
	}
	return nil, false
}

// indexesVariable reports whether a write of name into scopeKey belongs in the
// variable value index: the scope has to be the instance's own root — an
// activity-local scope is scratch state that disappears when the activity completes
// (ADR-0068), and indexing it would fill the index with values no search should
// return — and the instance's process has to have declared the name searchable.
//
// A process that declares nothing costs one length check here, the same shape as the
// conditional-event question below it.
func (c *ProcessingContext) indexesVariable(cp *compiler.CompiledProcess, atRoot bool, name string) bool {
	return atRoot && cp != nil && cp.HasSearchableVariables() && cp.IsSearchableVariable(name)
}

// keepsConstant reports whether a write must be dropped because it names one of the
// process's constants in an instance root that already holds it
// (ADR-0454).
//
// Dropping rather than refusing is the decision, and it is decided by the writes that
// actually arrive here: a call activity handing its child's variables back to a caller
// that has a constant of the same name, a worker echoing its input, a message payload
// carrying a field the process fixes. In each the constant is what the instance must keep,
// and parking the token would stop a process for a value nobody meant to change. The
// model's own writes never reach this: the deploy refused them.
//
// Two writes go through. The constant's own seeding, which is the first write of the
// name and so finds nothing there yet — the reason activateInstance writes constants
// before start variables. And an operator's correction (ADR-0095), the escape hatch for
// a constant that is wrong in an instance already running, which the variable audit
// records.
//
// A process that declares no constants pays the length check in HasConstants and nothing
// else, the same shape as indexesVariable.
func (c *ProcessingContext) keepsConstant(cp *compiler.CompiledProcess, atRoot bool, v *model.VariableValue) bool {
	if !atRoot || cp == nil || !cp.HasConstants() || !cp.IsConstant(v.Name) {
		return false
	}
	if c.cmd.ValueType == model.VTVariable && c.cmd.Intent == model.IntentVariableModify {
		return false
	}
	return c.GetVariable(v.ScopeKey, v.Name) != nil
}

// clearPersonalWrite reports whether this write must be refused because it carries a
// value the process declared personal and carries it readable.
//
// It is the backstop that makes ADR-0314's rule hold on *every* path into an instance,
// not only the ones an edge could be put on. A form submission, a worker's result and an
// operator's write are all sealed before they become commands; a message payload is not,
// because the instance it correlates into is only known here, inside the engine, which
// holds no key. Without this check that one path would write personal data in the clear,
// permanently un-erasable, while the declaration said otherwise and nothing looked wrong.
//
// A delete carries no value and a null hides nothing, so neither is refused. The
// enciphered-or-not question is a prefix check on bytes the engine already holds, which
// is why it can be asked here at all (I4: no key, no I/O, deterministic).
func clearPersonalWrite(cp *compiler.CompiledProcess, v *model.VariableValue) bool {
	if cp == nil || !cp.HasPersonalVariables() || v.Kind == model.VarNull {
		return false
	}
	return cp.IsPersonal(v.Name) && !v.IsEnciphered()
}

// movedDataSubject reports whether this write would point the instance at a different
// data subject than the one its values are already sealed under.
//
// The hazard is quiet and total: sealing follows whatever the data-subject variable says
// at the moment of the write, so changing it halfway splits one person's values across
// two keys — and then erasing either subject leaves the other half readable while the
// request looks honoured. Nothing downstream could notice, because each value opens
// perfectly well under the key it names.
//
// The first write is how the subject arrives, and a change *before* anything has been
// sealed costs nothing — correcting a mistyped id then is ordinary, and refusing it would
// be pedantry. So the refusal asks the one question that matters: has anything already
// been sealed, anywhere in the instance? Every scope is scanned, not only the root: a
// form submitted or a worker completing inside an embedded subprocess — every screen of
// a wizard (ADR-0449) — writes into that subprocess's scope, not the root, so a root-only
// scan would let the subject move from under values sealed there. The scan runs only when
// the subject variable actually changes value, which is rare, so its cost is too.
func movedDataSubject(c *ProcessingContext, cp *compiler.CompiledProcess, v *model.VariableValue) bool {
	if cp == nil || !cp.HasPersonalVariables() || v.Name != cp.DataSubjectVariable() {
		return false
	}
	old := c.ResolveVariable(v.ScopeKey, v.Name)
	if old == nil || (old.Kind == v.Kind && old.Text == v.Text && old.Bool == v.Bool) {
		return false
	}
	piKey := v.ScopeKey
	if ei := c.GetElementInstance(v.ScopeKey); ei != nil {
		piKey = ei.ProcessInstanceKey
	}
	sealed := false
	scan := func(scope uint64) {
		if sealed {
			return
		}
		c.VariablesOfScope(scope, func(existing model.VariableValue) {
			if !sealed && cp.IsPersonal(existing.Name) && existing.IsEnciphered() {
				sealed = true
			}
		})
	}
	scan(piKey)
	c.ForEachElementInstance(piKey, scan)
	return sealed
}

// foreignSubject reports whether a sealed personal value is written into a scope whose
// data subject is somebody else.
//
// An edge seals under the subject the target scope's chain names, so a value written
// from outside always matches. A value only arrives sealed for someone else by being
// copied — an output mapping handing a value out of a subprocess that shadowed the
// subject (ADR-0314, amended). It would still open, under its own subject's key, so
// nothing downstream would notice; but an erasure of this instance's subject would leave
// it readable, which is the split movedDataSubject refuses from the other side. The
// envelope names its subject in the clear, so the question needs no key.
func foreignSubject(c *ProcessingContext, cp *compiler.CompiledProcess, v *model.VariableValue) bool {
	if cp == nil || !cp.HasPersonalVariables() || !cp.IsPersonal(v.Name) || !v.IsEnciphered() {
		return false
	}
	sealedFor, ok := model.EncipheredSubject(v.Text)
	if !ok {
		return false
	}
	current := c.ResolveVariable(v.ScopeKey, cp.DataSubjectVariable())
	if current == nil {
		return false // the subject arrives later, and subjectArrivesForAnother asks then
	}
	subject := subjectOf(current)
	return subject != "" && subject != sealedFor
}

// subjectArrivesForAnother is foreignSubject asked from the other side: the data subject
// arrives in a scope that already holds a value sealed for somebody else.
//
// A copy can arrive before the subject does. A call activity seeds its child with the
// caller's variables in name order and its input mappings after them, so the child's
// subject may be written after a value sealed for the caller's; an output mapping can hand
// a value out to a scope that never had the subject, which a later task then sets. The
// value's own write found no subject to disagree with, so the subject's first write is
// where the two meet. Only the written scope is read: that is where a copy lands, and a
// sibling scope naming a subject of its own — one iteration per person — is not this
// subject's business. The read happens only on a subject's first write into a scope of a
// process that declares personal data.
func subjectArrivesForAnother(c *ProcessingContext, cp *compiler.CompiledProcess, v *model.VariableValue) bool {
	if cp == nil || !cp.HasPersonalVariables() || v.Name != cp.DataSubjectVariable() {
		return false
	}
	if c.ResolveVariable(v.ScopeKey, v.Name) != nil {
		return false // a change, not an arrival: movedDataSubject's question
	}
	subject := subjectOf(v)
	if subject == "" {
		return false
	}
	foreign := false
	c.VariablesOfScope(v.ScopeKey, func(existing model.VariableValue) {
		if foreign || !cp.IsPersonal(existing.Name) || !existing.IsEnciphered() {
			return
		}
		if sealedFor, ok := model.EncipheredSubject(existing.Text); ok && sealedFor != subject {
			foreign = true
		}
	})
	return foreign
}

// subjectOf is the person a data-subject value names: its text, trimmed, when it is a
// string or a number, and nobody otherwise.
func subjectOf(v *model.VariableValue) string {
	if v.Kind == model.VarString || v.Kind == model.VarNumber {
		return strings.TrimSpace(v.Text)
	}
	return ""
}

// refusePersonalWrite parks an incident and reports true when ADR-0314 forbids this
// write. It is the one place the three appenders ask, so no write path can be the one
// that forgot.
func refusePersonalWrite(c *ProcessingContext, cp *compiler.CompiledProcess, v *model.VariableValue) bool {
	switch {
	case clearPersonalWrite(cp, v):
		parkClearPersonalWrite(c, v.ScopeKey, v.Name)
		return true
	case movedDataSubject(c, cp, v):
		parkMovedDataSubject(c, v.ScopeKey, v.Name)
		return true
	case foreignSubject(c, cp, v):
		parkPersonalOfAnotherSubject(c, v.ScopeKey, v.Name)
		return true
	case subjectArrivesForAnother(c, cp, v):
		parkSubjectOfAnotherPerson(c, v.ScopeKey, v.Name)
		return true
	}
	return false
}

// markConditionDirty notes that a variable changed in the given scope, so the batch loop will
// schedule a re-check of the instance's conditional events (ADR-0137). It resolves the scope to
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
	// Stamped here for the same reason a variable's producer is (ADR-0219): one funnel
	// no write path can forget, and a value read out of one object and written into
	// another can never inherit the producer of the write it came from. The seeding at
	// instance creation runs on the instance's own command, which names no element, so
	// it correctly attributes to nobody.
	v.ProducerKey = c.producer
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

// AppendOperatorActionEvent records that an operator intervened on a running instance
// — completing a parked job by hand, say (ADR-0159). Like AppendVariableAuditEvent it
// is pure history: emitted alongside the events the intervention produces, it freezes
// who acted and why into the log, so a forced step is never indistinguishable from one
// the engine drove and replay rebuilds the identical trail (invariant I6).
func (c *ProcessingContext) AppendOperatorActionEvent(v model.OperatorActionValue) {
	c.appendEvent(v.ProcessInstanceKey, model.VTOperatorAction, model.IntentOperatorActed, inflightValue{operatorAct: v})
}

// AppendMigrationEvent rebinds a running instance to another deployed version of its
// process (ADR-0162). Unlike every other event it describes no single entity's
// transition: it carries the whole element mapping the fold rewrites the instance's
// live records through, frozen at command time so replay reproduces the rebinding from
// the log rather than by re-deriving it (invariants I4/I6).
func (c *ProcessingContext) AppendMigrationEvent(v model.ProcessMigrationValue) {
	c.appendEvent(v.ProcessInstanceKey, model.VTProcessMigration, model.IntentMigrated, inflightValue{migration: v})
}

// AppendVariableIndexEvent corrects one variable's membership in the value index
// (ADR-0244). It carries no value: the variable holds that, and the whole point is that
// the value did not change. Emitted only where an instance's declaration can change
// under it — a migration, or an operator's reindex — one per variable whose membership
// actually differs, so an instance already in step produces no events at all.
func (c *ProcessingContext) AppendVariableIndexEvent(v model.VariableIndexValue) {
	c.appendEvent(v.ProcessInstanceKey, model.VTVariableIndex, model.IntentVariableIndexed, inflightValue{variableIndex: v})
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

// AppendTriggerReceiptEvent records a trigger receipt, or with
// IntentTriggerReceiptsPruned drops the receipts older than v.Cutoff (ADR-0425).
func (c *ProcessingContext) AppendTriggerReceiptEvent(intent model.Intent, v model.TriggerReceiptValue) {
	c.appendEvent(0, model.VTTriggerReceipt, intent, inflightValue{trigger: v})
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
// timer or signal start). A correlating message uses it to instantiate a
// message-start process (ADR-0035). Deferring to a followup keeps instance
// creation on the same command path as an API-submitted create, so its events —
// and thus recovery — are identical however the create was triggered.
//
// startElement is the root start event that fired, and every caller here has one: a
// trigger instantiates at itself, not at every entry the process happens to have
// (ADR-0226). It is a required argument rather than an
// optional one so that adding a fourth kind of trigger cannot quietly inherit the old
// seed-everything behaviour.
func (c *ProcessingContext) AppendCreateInstanceCommand(defKey uint64, vars []model.VariableValue, correlationKey string, startElement int32) {
	c.p.followups = append(c.p.followups, Command{
		ValueType:     model.VTProcessInstance,
		Intent:        model.IntentActivating,
		Value:         inflightValue{process: model.ProcessInstanceValue{ProcessDefKey: defKey, CorrelationKey: correlationKey}},
		StartVars:     vars,
		StartElements: []int32{startElement},
		SourcePos:     c.lastPos,
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
// AppendMessageEvent appends a message-buffer event (ADR-0370), keyed by the message's
// own key.
func (c *ProcessingContext) AppendMessageEvent(key uint64, intent model.Intent, v model.MessageValue) {
	c.appendEvent(key, model.VTMessage, intent, inflightValue{message: v})
}

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
