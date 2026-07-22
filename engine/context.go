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

// AppendVariableEvent records a process-variable fact. The key is the owning
// process-instance key, which both scopes the variable and routes it to the
// instance's partition.
func (c *ProcessingContext) AppendVariableEvent(intent model.Intent, v model.VariableValue) {
	c.appendEvent(v.ProcessInstanceKey, model.VTVariable, intent, inflightValue{variable: v})
}

// SetVariables writes each named variable into the given process instance,
// emitting a VariableCreated event for a new name and VariableUpdated for an
// existing one. Used to seed an instance's variables at creation and to write a
// job's output variables back.
func (c *ProcessingContext) SetVariables(piKey uint64, vars []model.NamedVariable) {
	for i := range vars {
		intent := model.IntentVariableCreated
		if has, err := c.tx.HasVariable(piKey, vars[i].Name); err != nil {
			c.p.fail(err)
			return
		} else if has {
			intent = model.IntentVariableUpdated
		}
		c.AppendVariableEvent(intent, model.VariableValue{
			ProcessInstanceKey: piKey,
			Name:               vars[i].Name,
			Value:              vars[i].Value,
		})
	}
}

// AppendElementCommand schedules an element-instance command for a later batch.
func (c *ProcessingContext) AppendElementCommand(key uint64, intent model.Intent, v model.ElementInstanceValue) {
	c.appendCommand(key, model.VTElementInstance, intent, inflightValue{element: v})
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
