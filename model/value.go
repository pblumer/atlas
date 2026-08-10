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
	// AttachedToKey links a boundary event's element instance to the host activity
	// instance it is attached to (0 for every non-boundary element). It lets an
	// interrupting boundary find and terminate its host, and a completing host find
	// and disarm its boundary events (ADR-0040).
	AttachedToKey uint64
	TokenID       uint64
	ParentTokenID uint64
	SourceFlowId  int32
	// MultiInstance marks an element instance's role in a multi-instance activity
	// (ADR-0077): 0 = not multi-instance, 1 = the body (the scope that seeds the
	// iterations), 2 = an inner iteration (running the node's real behavior, scoped
	// under the body). Append-compatible: an old record without it decodes to 0.
	MultiInstance uint8
	// EventGatewayKey labels a catch event armed by an event-based gateway with the
	// gateway's element-instance key — its race group (ADR-0110). The first armed catch to
	// fire cancels every other live instance sharing this key. 0 for every element not armed
	// by an event gateway. Append-compatible: an old record without it decodes to 0.
	EventGatewayKey uint64
}

const elementInstanceLegacySize = 8 + 8 + 4 + 8 + 1 + 8
const elementInstanceSize = elementInstanceLegacySize + 8 + 8 + 4
const elementInstanceMISize = elementInstanceSize + 1
const elementInstanceEGSize = elementInstanceMISize + 8

func (*ElementInstanceValue) ValueType() ValueType { return VTElementInstance }

func (v *ElementInstanceValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessDefKey)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(v.ElementId))
	dst = binary.LittleEndian.AppendUint64(dst, v.FlowScopeKey)
	dst = append(dst, v.BpmnElementType)
	dst = binary.LittleEndian.AppendUint64(dst, v.AttachedToKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.TokenID)
	dst = binary.LittleEndian.AppendUint64(dst, v.ParentTokenID)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(v.SourceFlowId))
	dst = append(dst, v.MultiInstance)
	return binary.LittleEndian.AppendUint64(dst, v.EventGatewayKey)
}

func (v *ElementInstanceValue) decode(src []byte) error {
	if len(src) < elementInstanceLegacySize {
		return ErrShortBuffer
	}
	v.ProcessInstanceKey = binary.LittleEndian.Uint64(src[0:])
	v.ProcessDefKey = binary.LittleEndian.Uint64(src[8:])
	v.ElementId = int32(binary.LittleEndian.Uint32(src[16:]))
	v.FlowScopeKey = binary.LittleEndian.Uint64(src[20:])
	v.BpmnElementType = src[28]
	v.AttachedToKey = binary.LittleEndian.Uint64(src[29:])
	if len(src) >= elementInstanceSize {
		v.TokenID = binary.LittleEndian.Uint64(src[37:])
		v.ParentTokenID = binary.LittleEndian.Uint64(src[45:])
		v.SourceFlowId = int32(binary.LittleEndian.Uint32(src[53:]))
	}
	if len(src) >= elementInstanceMISize {
		v.MultiInstance = src[57]
	}
	if len(src) >= elementInstanceEGSize {
		v.EventGatewayKey = binary.LittleEndian.Uint64(src[58:])
	}
	return nil
}

// JobValue is service-task work waiting for an external worker. Variables are
// referenced via the element/instance scope, not embedded here. Assignee is the
// user-task assignee (ADR-0042): empty for a service-task job, and for a user
// task it starts at the model's default and is rewritten by claim/unclaim. It is
// the one variable-length field; for a service job it encodes as a 4-byte zero
// length and decodes to "" with no allocation, keeping the hot path clean (I1).
type JobValue struct {
	ProcessInstanceKey uint64
	ElementInstanceKey uint64
	JobType            int32 // interned string → index
	Retries            int32
	Deadline           int64
	Assignee           string
}

const jobSize = 8 + 8 + 4 + 4 + 8

func (*JobValue) ValueType() ValueType { return VTJob }

func (v *JobValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.ElementInstanceKey)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(v.JobType))
	dst = binary.LittleEndian.AppendUint32(dst, uint32(v.Retries))
	dst = binary.LittleEndian.AppendUint64(dst, uint64(v.Deadline))
	return appendString(dst, v.Assignee)
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
	assignee, _, err := readString(src[jobSize:])
	if err != nil {
		return err
	}
	v.Assignee = assignee
	return nil
}

// TimerValue is a timer-event subscription. The due-date index makes "which
// timers are due now" a range scan; see data-model.md.
type TimerValue struct {
	ProcessInstanceKey uint64
	ElementInstanceKey uint64
	TargetElementId    int32
	DueDate            int64
	Repetitions        int32 // remaining fires after this one; -1 = infinite (timer cycle), 0 = fire once
	// ProcessDefKey names the definition a *start* timer instantiates when it
	// fires (ADR-0051). It is 0 for an instance-owned timer (catch/boundary),
	// which is identified instead by ProcessInstanceKey/ElementInstanceKey. A
	// start timer is precisely one with ProcessInstanceKey == 0 and
	// ProcessDefKey != 0; TargetElementId then names its timer-start element.
	ProcessDefKey uint64
}

const timerSize = 8 + 8 + 4 + 8 + 4 + 8

func (*TimerValue) ValueType() ValueType { return VTTimer }

func (v *TimerValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.ElementInstanceKey)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(v.TargetElementId))
	dst = binary.LittleEndian.AppendUint64(dst, uint64(v.DueDate))
	dst = binary.LittleEndian.AppendUint32(dst, uint32(v.Repetitions))
	return binary.LittleEndian.AppendUint64(dst, v.ProcessDefKey)
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
	v.ProcessDefKey = binary.LittleEndian.Uint64(src[32:])
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
// process's element instances live under. CreatedAt is set at activation (the
// activation event's timestamp, so it replays identically — invariant I6);
// CorrelationKey records the message key a message-start instance was created
// with ("" for API/timer/none starts, ADR-0035). State and CompletedAt are set
// only on the history record written when an instance ends (ADR-0017); while
// live, they carry their zero values (Active, 0).
type ProcessInstanceValue struct {
	ProcessDefKey  uint64
	State          ProcessInstanceState
	CompletedAt    int64  // unix nano when it reached a terminal state; 0 while active
	CreatedAt      int64  // unix nano when the instance was activated
	CorrelationKey string // message correlation key a message-start instance began with; "" otherwise
	// ParentElementInstanceKey is the call-activity element instance that started
	// this instance as its child, 0 for a root instance (API/message/timer start).
	// A completing child resumes its caller through it (ADR-0076).
	ParentElementInstanceKey uint64
	// ExpiryDueDate is the due date (unix nano) of this instance's TTL expiry timer,
	// 0 when the definition has no TTL (ADR-0085). Stored so completion/termination can
	// cancel that timer by key (the instance key) without scanning the timer index.
	ExpiryDueDate int64
}

// processInstanceLegacySize is the original fixed layout (ProcessDefKey, State,
// CompletedAt). CreatedAt and the variable-length CorrelationKey were appended
// later; a record written before them decodes with those fields left zero, so
// old logs replay unchanged (ADR-0017).
const processInstanceLegacySize = 8 + 1 + 8

func (*ProcessInstanceValue) ValueType() ValueType { return VTProcessInstance }

func (v *ProcessInstanceValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessDefKey)
	dst = append(dst, byte(v.State))
	dst = binary.LittleEndian.AppendUint64(dst, uint64(v.CompletedAt))
	dst = binary.LittleEndian.AppendUint64(dst, uint64(v.CreatedAt))
	dst = appendString(dst, v.CorrelationKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.ParentElementInstanceKey)
	return binary.LittleEndian.AppendUint64(dst, uint64(v.ExpiryDueDate))
}

func (v *ProcessInstanceValue) decode(src []byte) error {
	if len(src) < processInstanceLegacySize {
		return ErrShortBuffer
	}
	v.ProcessDefKey = binary.LittleEndian.Uint64(src[0:])
	v.State = ProcessInstanceState(src[8])
	v.CompletedAt = int64(binary.LittleEndian.Uint64(src[9:]))
	// CreatedAt and CorrelationKey are appended fields: a legacy record ends here
	// and leaves them zero-valued.
	rest := src[processInstanceLegacySize:]
	if len(rest) < 8 {
		return nil
	}
	v.CreatedAt = int64(binary.LittleEndian.Uint64(rest))
	key, tail, err := readString(rest[8:])
	if err != nil {
		return err
	}
	v.CorrelationKey = key
	// ParentElementInstanceKey is a later appended field: a record written before it
	// ends after the correlation key and leaves it zero (a root instance).
	if len(tail) >= 8 {
		v.ParentElementInstanceKey = binary.LittleEndian.Uint64(tail)
	}
	// ExpiryDueDate is the newest appended field: a record written before it ends after
	// the parent key and leaves it zero (no TTL).
	if len(tail) >= 16 {
		v.ExpiryDueDate = int64(binary.LittleEndian.Uint64(tail[8:]))
	}
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

// DataObjectValue is a BPMN data object owned by a scope (the process instance
// root today). It is variable-shaped — a name and a typed value reusing the same
// VarKind machinery, so a data object can hold a structured VarJSON payload
// (ADR-0037) — plus a State: the BPMN data state (e.g. "received", "approved").
// Its encoding mirrors VariableValue with the extra State string between the name
// and the kind byte; like a variable it carries genuine runtime data, so it is
// length-prefixed rather than fixed-size (ADR-0053).
type DataObjectValue struct {
	ScopeKey uint64 // owning scope (process instance key today)
	Name     string
	State    string // BPMN data state; "" when the object declares none
	Kind     VarKind
	Bool     bool
	Text     string // number canonical string, string contents, or canonical JSON; empty otherwise
}

func (*DataObjectValue) ValueType() ValueType { return VTDataObject }

func (v *DataObjectValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, v.ScopeKey)
	dst = appendString(dst, v.Name)
	dst = appendString(dst, v.State)
	dst = append(dst, byte(v.Kind))
	if v.Bool {
		dst = append(dst, 1)
	} else {
		dst = append(dst, 0)
	}
	return appendString(dst, v.Text)
}

func (v *DataObjectValue) decode(src []byte) error {
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
	state, rest, err := readString(rest)
	if err != nil {
		return err
	}
	v.State = state
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

// DecisionEvaluationValue is one business rule task's DMN decision evaluation,
// retained for debugging (ADR-0066). It freezes what the worker computed off the
// processor goroutine: the input context the decision was given, the outputs it
// produced, and the temis trace explaining which rules fired. The three payloads
// are canonical JSON text (InputsJSON and OutputsJSON are objects; TraceJSON is the
// temis trace tree, or "" when the evaluator produced none — e.g. a literal-
// expression decision or a remote decision whose connector returned no trace).
//
// It is keyed under its owning ProcessInstanceKey as append-only history — one
// record per evaluation, never overwritten — so an operator can inspect after the
// fact exactly how a decision was made. ElementInstanceKey, ProcessDefKey and
// ElementId locate the business rule task on its instance and diagram. Like a
// variable it carries genuine runtime data, so its encoding is length-prefixed.
type DecisionEvaluationValue struct {
	ProcessInstanceKey uint64 // owning instance (the scope this record is keyed under)
	ElementInstanceKey uint64 // the business rule task instance that evaluated
	ProcessDefKey      uint64 // the process definition, to map ElementId onto the diagram
	ElementId          int32  // interned diagram node id of the business rule task
	DecisionId         string // the evaluated DMN decision id
	InputsJSON         string // canonical JSON object: the decision's input context
	OutputsJSON        string // canonical JSON object: the decision's outputs (name → value)
	TraceJSON          string // temis trace JSON (which rules fired); "" when none
}

func (*DecisionEvaluationValue) ValueType() ValueType { return VTDecisionEvaluation }

func (v *DecisionEvaluationValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.ElementInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessDefKey)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(v.ElementId))
	dst = appendString(dst, v.DecisionId)
	dst = appendString(dst, v.InputsJSON)
	dst = appendString(dst, v.OutputsJSON)
	return appendString(dst, v.TraceJSON)
}

func (v *DecisionEvaluationValue) decode(src []byte) error {
	if len(src) < 28 {
		return ErrShortBuffer
	}
	v.ProcessInstanceKey = binary.LittleEndian.Uint64(src)
	v.ElementInstanceKey = binary.LittleEndian.Uint64(src[8:])
	v.ProcessDefKey = binary.LittleEndian.Uint64(src[16:])
	v.ElementId = int32(binary.LittleEndian.Uint32(src[24:]))
	rest := src[28:]
	decisionId, rest, err := readString(rest)
	if err != nil {
		return err
	}
	v.DecisionId = decisionId
	inputs, rest, err := readString(rest)
	if err != nil {
		return err
	}
	v.InputsJSON = inputs
	outputs, rest, err := readString(rest)
	if err != nil {
		return err
	}
	v.OutputsJSON = outputs
	trace, _, err := readString(rest)
	if err != nil {
		return err
	}
	v.TraceJSON = trace
	return nil
}

// VariableAuditValue records one external variable override for audit (ADR-0098):
// who set which variable, to what value, on which scope. It is keyed under its
// owning ProcessInstanceKey as append-only history — one record per variable an
// operator sets — so the "who changed it" trail folds into the same instance
// timeline as the variable snapshot at the same log position, and survives the
// instance finishing. Actor is the acting principal's username, or "" when auth is
// off (single-user) or the caller is unidentified. Name/Kind/Bool/Text mirror the
// VariableValue that was written, so the audit row is self-contained. Like a variable
// it carries genuine runtime data, so its encoding is length-prefixed.
type VariableAuditValue struct {
	ProcessInstanceKey uint64 // owning instance (the scope this record is keyed under)
	ScopeKey           uint64 // the scope the variable was written to (root or a sub-scope)
	Actor              string // who performed the override; "" when auth is off / unidentified
	Name               string // the variable that was set
	Kind               VarKind
	Bool               bool
	Text               string // number canonical string, string contents, or canonical JSON; empty otherwise
}

func (*VariableAuditValue) ValueType() ValueType { return VTVariableAudit }

func (v *VariableAuditValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.ScopeKey)
	dst = appendString(dst, v.Actor)
	dst = appendString(dst, v.Name)
	dst = append(dst, byte(v.Kind))
	if v.Bool {
		dst = append(dst, 1)
	} else {
		dst = append(dst, 0)
	}
	return appendString(dst, v.Text)
}

func (v *VariableAuditValue) decode(src []byte) error {
	if len(src) < 16 {
		return ErrShortBuffer
	}
	v.ProcessInstanceKey = binary.LittleEndian.Uint64(src)
	v.ScopeKey = binary.LittleEndian.Uint64(src[8:])
	rest := src[16:]
	actor, rest, err := readString(rest)
	if err != nil {
		return err
	}
	v.Actor = actor
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

// SignalSubscriptionValue is an open subscription to a broadcast signal: an
// element instance (a signal intermediate catch event, later a signal boundary
// or event subprocess) waiting for a named signal. It is the
// MessageSubscriptionValue shape (ADR-0020) minus the correlation key — a signal
// matches by name alone and fans out 1:n, so there is nothing to correlate on
// (ADR-0088). The SignalName is the sole match key a broadcast scans for.
type SignalSubscriptionValue struct {
	ProcessInstanceKey uint64
	ElementInstanceKey uint64
	SignalName         string
	// ProcessDefKey and ElementId identify the waiting catch on its diagram; set at
	// subscribe time from the element instance and carried so the record locates its
	// own state index entry (invariant I4), mirroring MessageSubscriptionValue.
	ProcessDefKey uint64
	ElementId     int32
}

func (*SignalSubscriptionValue) ValueType() ValueType { return VTSignal }

func (v *SignalSubscriptionValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.ElementInstanceKey)
	dst = appendString(dst, v.SignalName)
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessDefKey)
	return binary.LittleEndian.AppendUint32(dst, uint32(v.ElementId))
}

func (v *SignalSubscriptionValue) decode(src []byte) error {
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
	v.SignalName = name
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

// InboundDeliveryValue advances an external event source's inbound high-water mark
// (ADR-0075). SourceID is an opaque per-source identifier (e.g. a clio connector +
// watched subject) the engine never interprets; SourceSeq is that source's
// monotonic sequence up to which delivery has been applied. Folding these into a
// per-source high-water mark lets a replayed at-least-once publish be skipped.
type InboundDeliveryValue struct {
	SourceID  string
	SourceSeq uint64
}

func (*InboundDeliveryValue) ValueType() ValueType { return VTInboundDelivery }

func (v *InboundDeliveryValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, v.SourceSeq)
	return appendString(dst, v.SourceID)
}

func (v *InboundDeliveryValue) decode(src []byte) error {
	if len(src) < 8 {
		return ErrShortBuffer
	}
	v.SourceSeq = binary.LittleEndian.Uint64(src[0:])
	id, _, err := readString(src[8:])
	if err != nil {
		return err
	}
	v.SourceID = id
	return nil
}

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

// IncidentValue is a durable fault attached to the element instance where progress
// stalled — today, a job whose retries were exhausted (ADR-0061). It is keyed by
// ElementInstanceKey (one activity holds at most one job, so at most one incident)
// and points at the job, so resolving the incident can re-activate it. Message is
// the worker-reported failure reason; it is genuine runtime data, so the value is
// length-prefixed rather than fixed-size.
type IncidentValue struct {
	ProcessInstanceKey uint64
	ElementInstanceKey uint64
	JobKey             uint64
	ElementId          int32 // the compiled-graph element the token is stuck on (maps to a BPMN element id for the operator)
	RaisedAt           int64 // unix nanoseconds the incident was raised; read at command time and frozen into the event (I6)
	Message            string
}

func (*IncidentValue) ValueType() ValueType { return VTIncident }

func (v *IncidentValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.ElementInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.JobKey)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(v.ElementId))
	dst = binary.LittleEndian.AppendUint64(dst, uint64(v.RaisedAt))
	return appendString(dst, v.Message)
}

func (v *IncidentValue) decode(src []byte) error {
	const incidentFixed = 8 + 8 + 8 + 4 + 8
	if len(src) < incidentFixed {
		return ErrShortBuffer
	}
	v.ProcessInstanceKey = binary.LittleEndian.Uint64(src[0:])
	v.ElementInstanceKey = binary.LittleEndian.Uint64(src[8:])
	v.JobKey = binary.LittleEndian.Uint64(src[16:])
	v.ElementId = int32(binary.LittleEndian.Uint32(src[24:]))
	v.RaisedAt = int64(binary.LittleEndian.Uint64(src[28:]))
	msg, _, err := readString(src[incidentFixed:])
	if err != nil {
		return err
	}
	v.Message = msg
	return nil
}

// CompensableValue is one completed compensable activity: an activity that bore a
// compensation boundary and finished successfully, retained so a later compensation
// throw can run its handler (ADR-0103). It is keyed under ScopeKey in completion order
// (by the event's log position), so a reverse scan yields reverse completion order.
// ElementId identifies the compensated activity (for activityRef matching); HandlerNode
// is the compensation handler to activate; Seq carries the record's key sequence back on
// the consume event so its index entry can be deleted. All fields are fixed-width.
type CompensableValue struct {
	ProcessInstanceKey uint64
	ProcessDefKey      uint64
	ScopeKey           uint64 // FlowScopeKey the compensable activity lived in
	ElementInstanceKey uint64 // the completed activity's element-instance key
	Seq                uint64 // the record's key sequence (log position), set on consume
	ElementId          int32  // the compensable activity's compiled node id
	HandlerNode        int32  // the compensation handler's compiled node id
}

const compensableSize = 8 + 8 + 8 + 8 + 8 + 4 + 4

func (*CompensableValue) ValueType() ValueType { return VTCompensable }

func (v *CompensableValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessDefKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.ScopeKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.ElementInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.Seq)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(v.ElementId))
	return binary.LittleEndian.AppendUint32(dst, uint32(v.HandlerNode))
}

func (v *CompensableValue) decode(src []byte) error {
	if len(src) < compensableSize {
		return ErrShortBuffer
	}
	v.ProcessInstanceKey = binary.LittleEndian.Uint64(src[0:])
	v.ProcessDefKey = binary.LittleEndian.Uint64(src[8:])
	v.ScopeKey = binary.LittleEndian.Uint64(src[16:])
	v.ElementInstanceKey = binary.LittleEndian.Uint64(src[24:])
	v.Seq = binary.LittleEndian.Uint64(src[32:])
	v.ElementId = int32(binary.LittleEndian.Uint32(src[40:]))
	v.HandlerNode = int32(binary.LittleEndian.Uint32(src[44:]))
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
	case VTMessageSubscription:
		return &MessageSubscriptionValue{}
	case VTSignal:
		return &SignalSubscriptionValue{}
	case VTMessageFlow:
		return &MessageFlowValue{}
	case VTDataObject:
		return &DataObjectValue{}
	case VTIncident:
		return &IncidentValue{}
	case VTDecisionEvaluation:
		return &DecisionEvaluationValue{}
	case VTInboundDelivery:
		return &InboundDeliveryValue{}
	case VTVariableAudit:
		return &VariableAuditValue{}
	case VTCompensable:
		return &CompensableValue{}
	default:
		return nil
	}
}
