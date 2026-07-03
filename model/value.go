package model

import "encoding/binary"

// Value is the typed payload of a record. Each implementation owns a fixed
// binary layout. encode appends to a caller-owned buffer (no allocation when
// the buffer has spare capacity, satisfying invariant I1); decode reads a
// payload back, returning ErrShortBuffer if src is truncated.
//
// The methods are unexported on purpose: the set of value types is closed to
// this package, which keeps encode/decode and the newValue dispatch in lockstep.
type Value interface {
	// ValueType reports the discriminator a header should carry for this payload.
	ValueType() ValueType
	encode(dst []byte) []byte
	decode(src []byte) error
}

// Strings carried by payloads (element ids, job types) are interned to int32
// indices at compile time — never stored as text on the log (invariant I5).
// Cross-referenced data (variables) lives in its own state, referenced by key,
// not copied into these payloads.

// ElementInstanceValue is the token-carrying state of one active BPMN element.
type ElementInstanceValue struct {
	ProcessInstanceKey uint64
	ProcessDefKey      uint64
	ElementId          int32  // INDEX into the compiled graph, not a string
	FlowScopeKey       uint64 // parent scope (subprocess instance), 0 = root
	BpmnElementType    uint8  // for fast dispatch
}

const elementInstanceSize = 8 + 8 + 4 + 8 + 1

func (*ElementInstanceValue) ValueType() ValueType { return VTElementInstance }

func (v *ElementInstanceValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessDefKey)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(v.ElementId))
	dst = binary.LittleEndian.AppendUint64(dst, v.FlowScopeKey)
	return append(dst, v.BpmnElementType)
}

func (v *ElementInstanceValue) decode(src []byte) error {
	if len(src) < elementInstanceSize {
		return ErrShortBuffer
	}
	v.ProcessInstanceKey = binary.LittleEndian.Uint64(src[0:])
	v.ProcessDefKey = binary.LittleEndian.Uint64(src[8:])
	v.ElementId = int32(binary.LittleEndian.Uint32(src[16:]))
	v.FlowScopeKey = binary.LittleEndian.Uint64(src[20:])
	v.BpmnElementType = src[28]
	return nil
}

// JobValue is service-task work waiting for an external worker. Variables are
// referenced via the element/instance scope, not embedded here.
type JobValue struct {
	ProcessInstanceKey uint64
	ElementInstanceKey uint64
	JobType            int32 // interned string → index
	Retries            int32
	Deadline           int64
}

const jobSize = 8 + 8 + 4 + 4 + 8

func (*JobValue) ValueType() ValueType { return VTJob }

func (v *JobValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.ElementInstanceKey)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(v.JobType))
	dst = binary.LittleEndian.AppendUint32(dst, uint32(v.Retries))
	return binary.LittleEndian.AppendUint64(dst, uint64(v.Deadline))
}

func (v *JobValue) decode(src []byte) error {
	if len(src) < jobSize {
		return ErrShortBuffer
	}
	v.ProcessInstanceKey = binary.LittleEndian.Uint64(src[0:])
	v.ElementInstanceKey = binary.LittleEndian.Uint64(src[8:])
	v.JobType = int32(binary.LittleEndian.Uint32(src[16:]))
	v.Retries = int32(binary.LittleEndian.Uint32(src[20:]))
	v.Deadline = int64(binary.LittleEndian.Uint64(src[24:]))
	return nil
}

// User-task lifecycle sub-states carried in UserTaskValue.State. The BPMN
// element lifecycle (Activating…Completed) still applies to the task's element
// instance; this sub-state tracks the human interaction the tasklist drives.
const (
	UserTaskCreated uint8 = iota // offered, not yet claimed
	UserTaskClaimed              // an assignee has taken it
)

// UserTaskValue is a human task awaiting completion via the tasklist (ADR-0013).
// Like a job it is work handed out and later completed by a command, but its
// consumer is a person: it is offered to a candidate group, claimed by an
// assignee, and never leased. Assignment and the form reference are resolved at
// compile/runtime and written into the creating event so replay is deterministic
// (invariant I6). Variables (the form output) live in their own state, not here.
type UserTaskValue struct {
	ProcessInstanceKey uint64
	ElementInstanceKey uint64
	CandidateGroup     int32 // interned group offered the task, -1 if none
	Assignee           int32 // interned assignee; -1 until claimed
	FormRef            int32 // interned form reference (ADR-0014), -1 if none
	State              uint8 // UserTaskCreated | UserTaskClaimed
}

const userTaskSize = 8 + 8 + 4 + 4 + 4 + 1

func (*UserTaskValue) ValueType() ValueType { return VTUserTask }

func (v *UserTaskValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.ElementInstanceKey)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(v.CandidateGroup))
	dst = binary.LittleEndian.AppendUint32(dst, uint32(v.Assignee))
	dst = binary.LittleEndian.AppendUint32(dst, uint32(v.FormRef))
	return append(dst, v.State)
}

func (v *UserTaskValue) decode(src []byte) error {
	if len(src) < userTaskSize {
		return ErrShortBuffer
	}
	v.ProcessInstanceKey = binary.LittleEndian.Uint64(src[0:])
	v.ElementInstanceKey = binary.LittleEndian.Uint64(src[8:])
	v.CandidateGroup = int32(binary.LittleEndian.Uint32(src[16:]))
	v.Assignee = int32(binary.LittleEndian.Uint32(src[20:]))
	v.FormRef = int32(binary.LittleEndian.Uint32(src[24:]))
	v.State = src[28]
	return nil
}

// TimerValue is a timer-event subscription. The due-date index makes "which
// timers are due now" a range scan; see data-model.md.
type TimerValue struct {
	ProcessInstanceKey uint64
	ElementInstanceKey uint64
	TargetElementId    int32
	DueDate            int64
	Repetitions        int32 // -1 = infinite (timer cycle)
}

const timerSize = 8 + 8 + 4 + 8 + 4

func (*TimerValue) ValueType() ValueType { return VTTimer }

func (v *TimerValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.ElementInstanceKey)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(v.TargetElementId))
	dst = binary.LittleEndian.AppendUint64(dst, uint64(v.DueDate))
	return binary.LittleEndian.AppendUint32(dst, uint32(v.Repetitions))
}

func (v *TimerValue) decode(src []byte) error {
	if len(src) < timerSize {
		return ErrShortBuffer
	}
	v.ProcessInstanceKey = binary.LittleEndian.Uint64(src[0:])
	v.ElementInstanceKey = binary.LittleEndian.Uint64(src[8:])
	v.TargetElementId = int32(binary.LittleEndian.Uint32(src[16:]))
	v.DueDate = int64(binary.LittleEndian.Uint64(src[20:]))
	v.Repetitions = int32(binary.LittleEndian.Uint32(src[28:]))
	return nil
}

// ProcessInstanceValue is the running instance as a whole — the root scope a
// process's element instances live under. Minimal for now; fields grow as
// features (parent/call-activity links, state flags) land.
type ProcessInstanceValue struct {
	ProcessDefKey uint64
}

const processInstanceSize = 8

func (*ProcessInstanceValue) ValueType() ValueType { return VTProcessInstance }

func (v *ProcessInstanceValue) encode(dst []byte) []byte {
	return binary.LittleEndian.AppendUint64(dst, v.ProcessDefKey)
}

func (v *ProcessInstanceValue) decode(src []byte) error {
	if len(src) < processInstanceSize {
		return ErrShortBuffer
	}
	v.ProcessDefKey = binary.LittleEndian.Uint64(src[0:])
	return nil
}

// newValue returns a zero payload for the value types that have one. Value
// types without a payload yet return nil; their records carry only a header.
func newValue(vt ValueType) Value {
	switch vt {
	case VTProcessInstance:
		return &ProcessInstanceValue{}
	case VTElementInstance:
		return &ElementInstanceValue{}
	case VTJob:
		return &JobValue{}
	case VTUserTask:
		return &UserTaskValue{}
	case VTTimer:
		return &TimerValue{}
	default:
		return nil
	}
}
