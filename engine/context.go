package engine

import (
	"github.com/pblumer/chrampfer/compiler"
	"github.com/pblumer/chrampfer/model"
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

// AppendFollowupEvent records a fact: it appends an event to the batch AND
// mutates state from that same event via applyToState. Doing both from one
// record is what keeps the log and state in lockstep — recovery replays the
// exact same applyToState.
func (c *ProcessingContext) AppendFollowupEvent(key uint64, intent model.Intent, v model.Value) {
	c.p.position++
	rec := model.Record{
		Header: model.RecordHeader{
			Position:    c.p.position,
			SourcePos:   c.cmd.SourcePos,
			Key:         key,
			Timestamp:   c.p.clock.Now(),
			RecordType:  model.RecordEvent,
			ValueType:   v.ValueType(),
			Intent:      intent,
			PartitionId: c.p.partition,
		},
		Value: v,
	}
	c.p.batchRecords = append(c.p.batchRecords, rec)
	c.lastPos = rec.Header.Position
	c.p.fail(applyToState(c.tx, rec.Header, v))
}

// AppendFollowupCommand schedules an internal command for a later batch. Its
// SourcePos points at the most recent event written here, threading causality.
func (c *ProcessingContext) AppendFollowupCommand(key uint64, intent model.Intent, v model.Value) {
	c.p.followups = append(c.p.followups, Command{
		Key:       key,
		ValueType: v.ValueType(),
		Intent:    intent,
		Value:     v,
		SourcePos: c.lastPos,
	})
}

// SideEffect registers work to run after the batch's fsync — never before
// (invariant I2). Worker notifications and client responses go here.
func (c *ProcessingContext) SideEffect(fn func()) {
	c.p.sideEffects = append(c.p.sideEffects, fn)
}
