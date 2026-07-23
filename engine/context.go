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

// ForEachElementInstance calls fn with the key of every element instance
// belonging to a process instance, via the committed elByProc index. Keys are
// collected before fn runs so fn may mutate element-instance state (e.g. emit
// terminations) without disturbing the scan.
func (c *ProcessingContext) ForEachElementInstance(procKey uint64, fn func(elKey uint64)) {
	var keys []uint64
	if err := c.p.store.ElementInstancesOfProcess(procKey, func(k uint64) error {
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

// ActiveChildren returns the active-child count of a scope (e.g. to detect that
// a process instance has finished).
func (c *ProcessingContext) ActiveChildren(scope uint64) int32 {
	n, err := c.tx.ActiveChildren(scope)
	c.p.fail(err)
	return n
}

// AppendProcessInstanceEvent records a process-instance lifecycle fact.
func (c *ProcessingContext) AppendProcessInstanceEvent(key uint64, intent model.Intent, v model.ProcessInstanceValue) {
	c.appendEvent(key, model.VTProcessInstance, intent, inflightValue{process: v})
}

// AppendElementEvent records an element-instance lifecycle fact.
func (c *ProcessingContext) AppendElementEvent(key uint64, intent model.Intent, v model.ElementInstanceValue) {
	c.appendEvent(key, model.VTElementInstance, intent, inflightValue{element: v})
}

// AppendJobEvent records a job lifecycle fact.
func (c *ProcessingContext) AppendJobEvent(key uint64, intent model.Intent, v model.JobValue) {
	c.appendEvent(key, model.VTJob, intent, inflightValue{job: v})
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
}

// AppendMessageSubscriptionEvent records a message-subscription fact (created or
// correlated). The key is the waiting element instance's key, and the value
// carries the match pair, so applyToState can locate the index entry from the
// event alone (invariant I4).
func (c *ProcessingContext) AppendMessageSubscriptionEvent(key uint64, intent model.Intent, v model.MessageSubscriptionValue) {
	c.appendEvent(key, model.VTMessageSubscription, intent, inflightValue{subscription: v})
}

// AppendElementCommand schedules an element-instance command for a later batch.
func (c *ProcessingContext) AppendElementCommand(key uint64, intent model.Intent, v model.ElementInstanceValue) {
	c.appendCommand(key, model.VTElementInstance, intent, inflightValue{element: v})
}

// AppendCreateInstanceCommand schedules creation of a new instance of defKey for
// a later batch, seeded with vars (each re-scoped to the new instance when it is
// created). A correlating message uses it to instantiate a message-start process
// (ADR-0035). Deferring to a followup keeps instance creation on the same
// command path as an API-submitted create, so its events — and thus recovery —
// are identical however the create was triggered.
func (c *ProcessingContext) AppendCreateInstanceCommand(defKey uint64, vars []model.VariableValue) {
	c.p.followups = append(c.p.followups, Command{
		ValueType: model.VTProcessInstance,
		Intent:    model.IntentActivating,
		Value:     inflightValue{process: model.ProcessInstanceValue{ProcessDefKey: defKey}},
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
