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

// NamedVariable is a name/value pair carried on a command — the variables seeded
// when an instance is created, or produced when a job completes. It is not a
// persisted [Value]; the processor turns each into a scoped VariableValue event.
// The value bytes are the same opaque encoding VariableValue stores (JSON today).
type NamedVariable struct {
	Name  string
	Value []byte
}

// VariableValue is one process variable: a named value scoped to a process
// instance. The value is opaque encoded bytes (JSON today) so the engine can
// carry any shape without knowing its type; names are runtime data, not
// compile-time interned strings, because a variable's name may be produced at
// runtime (e.g. a decision output key). Variables are how a business rule task
// reads its inputs and writes its outputs (ADR-0014).
type VariableValue struct {
	ProcessInstanceKey uint64
	Name               string
	Value              []byte
}

func (*VariableValue) ValueType() ValueType { return VTVariable }

func (v *VariableValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessInstanceKey)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(len(v.Name)))
	dst = append(dst, v.Name...)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(len(v.Value)))
	return append(dst, v.Value...)
}

func (v *VariableValue) decode(src []byte) error {
	if len(src) < 8+4 {
		return ErrShortBuffer
	}
	v.ProcessInstanceKey = binary.LittleEndian.Uint64(src)
	pos := 8
	nameLen := int(binary.LittleEndian.Uint32(src[pos:]))
	pos += 4
	if len(src) < pos+nameLen+4 {
		return ErrShortBuffer
	}
	v.Name = string(src[pos : pos+nameLen]) // copies out of the transient buffer
	pos += nameLen
	valLen := int(binary.LittleEndian.Uint32(src[pos:]))
	pos += 4
	if len(src) < pos+valLen {
		return ErrShortBuffer
	}
	v.Value = append([]byte(nil), src[pos:pos+valLen]...) // owned copy
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
	case VTTimer:
		return &TimerValue{}
	case VTVariable:
		return &VariableValue{}
	default:
		return nil
	}
}
