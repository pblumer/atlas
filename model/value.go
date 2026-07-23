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

// ProcessInstanceState marks where an instance is in its lifecycle. The zero
// value is Active; the terminal states are only ever stored in the history
// index (an active instance's record always carries Active). See ADR-0017.
type ProcessInstanceState uint8

const (
	PIActive     ProcessInstanceState = iota // running
	PICompleted                              // reached its end normally
	PITerminated                             // ended by termination
)

func (s ProcessInstanceState) String() string {
	switch s {
	case PICompleted:
		return "completed"
	case PITerminated:
		return "terminated"
	default:
		return "active"
	}
}

// ProcessInstanceValue is the running instance as a whole — the root scope a
// process's element instances live under. State and CompletedAt are set only
// on the history record written when an instance ends (ADR-0017); while live,
// they carry their zero values (Active, 0).
type ProcessInstanceValue struct {
	ProcessDefKey uint64
	State         ProcessInstanceState
	CompletedAt   int64 // unix nano when it reached a terminal state; 0 while active
}

const processInstanceSize = 8 + 1 + 8

func (*ProcessInstanceValue) ValueType() ValueType { return VTProcessInstance }

func (v *ProcessInstanceValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessDefKey)
	dst = append(dst, byte(v.State))
	return binary.LittleEndian.AppendUint64(dst, uint64(v.CompletedAt))
}

func (v *ProcessInstanceValue) decode(src []byte) error {
	if len(src) < processInstanceSize {
		return ErrShortBuffer
	}
	v.ProcessDefKey = binary.LittleEndian.Uint64(src[0:])
	v.State = ProcessInstanceState(src[8])
	v.CompletedAt = int64(binary.LittleEndian.Uint64(src[9:]))
	return nil
}

// VarKind tags the FEEL values Atlas persists for a variable: the scalars, plus
// VarJSON for the structured values (objects and arrays) an author or a script
// produces.
type VarKind uint8

const (
	VarNull   VarKind = iota // no value
	VarBool                  // Bool is meaningful
	VarNumber                // Text is the canonical decimal string
	VarString                // Text is the string contents
	// VarJSON is a structured value (object or array). Text is its canonical
	// JSON encoding; it is re-parsed into a FEEL context/list when bound into an
	// evaluation. Kept as text so the durable record format is unchanged — a new
	// kind byte over the same length-prefixed Text (ADR-0009, ADR-0037).
	VarJSON
)

// VariableValue is a process variable: a named value owned by a scope (the
// process instance root for now). Unlike the graph-derived payloads, a variable
// carries genuine runtime data (its name and contents), so its encoding is
// length-prefixed rather than fixed-size.
type VariableValue struct {
	ScopeKey uint64 // owning scope (process instance key today)
	Name     string
	Kind     VarKind
	Bool     bool
	Text     string // number canonical string or string contents; empty otherwise
}

func (*VariableValue) ValueType() ValueType { return VTVariable }

func (v *VariableValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, v.ScopeKey)
	dst = appendString(dst, v.Name)
	dst = append(dst, byte(v.Kind))
	if v.Bool {
		dst = append(dst, 1)
	} else {
		dst = append(dst, 0)
	}
	return appendString(dst, v.Text)
}

func (v *VariableValue) decode(src []byte) error {
	if len(src) < 8 {
		return ErrShortBuffer
	}
	v.ScopeKey = binary.LittleEndian.Uint64(src)
	rest := src[8:]
	name, rest, err := readString(rest)
	if err != nil {
		return err
	}
	v.Name = name
	if len(rest) < 2 {
		return ErrShortBuffer
	}
	v.Kind = VarKind(rest[0])
	v.Bool = rest[1] != 0
	text, _, err := readString(rest[2:])
	if err != nil {
		return err
	}
	v.Text = text
	return nil
}

// MessageSubscriptionValue is an open subscription: an element instance (a
// message intermediate catch event) waiting for a named message whose
// correlation key matches. Like a variable it carries genuine runtime data (the
// message name and the evaluated correlation key), so its encoding is
// length-prefixed rather than fixed-size. The (MessageName, CorrelationKey) pair
// is the match key a publish scans for; see ADR-0020.
type MessageSubscriptionValue struct {
	ProcessInstanceKey uint64
	ElementInstanceKey uint64
	MessageName        string
	CorrelationKey     string // FEEL correlation key, evaluated at subscribe time
	// ProcessDefKey and ElementId identify the waiting catch event on its diagram.
	// They are carried so that when the subscription correlates, the retained
	// message-flow history record can name the receiving element without a lookup
	// (ADR-0038); they are set at subscribe time from the element instance.
	ProcessDefKey uint64
	ElementId     int32
}

func (*MessageSubscriptionValue) ValueType() ValueType { return VTMessageSubscription }

func (v *MessageSubscriptionValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.ElementInstanceKey)
	dst = appendString(dst, v.MessageName)
	dst = appendString(dst, v.CorrelationKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessDefKey)
	return binary.LittleEndian.AppendUint32(dst, uint32(v.ElementId))
}

func (v *MessageSubscriptionValue) decode(src []byte) error {
	if len(src) < 16 {
		return ErrShortBuffer
	}
	v.ProcessInstanceKey = binary.LittleEndian.Uint64(src[0:])
	v.ElementInstanceKey = binary.LittleEndian.Uint64(src[8:])
	rest := src[16:]
	name, rest, err := readString(rest)
	if err != nil {
		return err
	}
	v.MessageName = name
	key, rest, err := readString(rest)
	if err != nil {
		return err
	}
	v.CorrelationKey = key
	if len(rest) < 12 {
		return ErrShortBuffer
	}
	v.ProcessDefKey = binary.LittleEndian.Uint64(rest[0:])
	v.ElementId = int32(binary.LittleEndian.Uint32(rest[8:]))
	return nil
}

// MessageFlowValue is one delivered message flow, retained as history so the
// collaboration replay can show which message crossed to which receiving element
// and when (ADR-0038). It is produced when a message correlates a catch event or
// instantiates a message-start process. The receiving element identifies the
// message-flow edge on the diagram; the sender/receiver instance keys tie the two
// pools' instances together. ReceiverProcessInstanceKey is 0 when the message
// created the receiver via a message start event (no instance existed yet).
type MessageFlowValue struct {
	SenderProcessInstanceKey   uint64
	ReceiverProcessInstanceKey uint64
	ReceiverProcessDefKey      uint64
	ReceiverElementId          int32 // INDEX into the receiver definition's graph
	MessageName                string
	CorrelationKey             string
}

func (*MessageFlowValue) ValueType() ValueType { return VTMessageFlow }

func (v *MessageFlowValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, v.SenderProcessInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.ReceiverProcessInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.ReceiverProcessDefKey)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(v.ReceiverElementId))
	dst = appendString(dst, v.MessageName)
	return appendString(dst, v.CorrelationKey)
}

func (v *MessageFlowValue) decode(src []byte) error {
	if len(src) < 28 {
		return ErrShortBuffer
	}
	v.SenderProcessInstanceKey = binary.LittleEndian.Uint64(src[0:])
	v.ReceiverProcessInstanceKey = binary.LittleEndian.Uint64(src[8:])
	v.ReceiverProcessDefKey = binary.LittleEndian.Uint64(src[16:])
	v.ReceiverElementId = int32(binary.LittleEndian.Uint32(src[24:]))
	rest := src[28:]
	name, rest, err := readString(rest)
	if err != nil {
		return err
	}
	v.MessageName = name
	key, _, err := readString(rest)
	if err != nil {
		return err
	}
	v.CorrelationKey = key
	return nil
}

// appendString writes a uint32 length prefix followed by the bytes of s.
func appendString(dst []byte, s string) []byte {
	dst = binary.LittleEndian.AppendUint32(dst, uint32(len(s)))
	return append(dst, s...)
}

// readString reads a length-prefixed string from the front of src, returning the
// string and the remaining bytes.
func readString(src []byte) (string, []byte, error) {
	if len(src) < 4 {
		return "", nil, ErrShortBuffer
	}
	n := binary.LittleEndian.Uint32(src)
	src = src[4:]
	if uint32(len(src)) < n {
		return "", nil, ErrShortBuffer
	}
	return string(src[:n]), src[n:], nil
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
	case VTMessageSubscription:
		return &MessageSubscriptionValue{}
	case VTMessageFlow:
		return &MessageFlowValue{}
	default:
		return nil
	}
}
