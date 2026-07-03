// Package model defines the records that flow through Atlas and their
// on-disk binary encoding.
//
// Everything the engine processes is a keyed [Record] discriminated by a
// (ValueType, Intent) pair. Commands are intentions and are never persisted;
// events are facts and are appended to the write-ahead log. State is the fold
// of all events. See docs/architecture/data-model.md for the full reference.
//
// The encoding here is the internal log format: a hand-written binary layout
// behind an explicit version byte (ADR-0009). It is deliberately reflection-
// free so that encoding an event on the processor hot path does not allocate
// (invariant I1); callers reuse a single buffer across records.
package model

// RecordType distinguishes the three kinds of record. Only events are written
// to the log; commands and rejections are processed but never persisted.
type RecordType uint8

const (
	// RecordCommand is an intention submitted to the processor. It may be
	// rejected and is never persisted.
	RecordCommand RecordType = iota
	// RecordEvent is a fact that already happened. It is appended to the log
	// and is the only record type that is persisted.
	RecordEvent
	// RecordCommandRejection records that a command was refused. It is not
	// persisted; it is surfaced to the submitter.
	RecordCommandRejection
)

func (t RecordType) String() string {
	switch t {
	case RecordCommand:
		return "Command"
	case RecordEvent:
		return "Event"
	case RecordCommandRejection:
		return "CommandRejection"
	default:
		return "RecordType(?)"
	}
}

// ValueType identifies which entity a record concerns and therefore which
// payload struct its bytes carry. The set mirrors docs/architecture/data-model.md.
type ValueType uint8

const (
	VTProcessInstance     ValueType = iota // the running instance as a whole
	VTElementInstance                      // a single active BPMN element instance (token carrier)
	VTJob                                  // service-task work for external workers
	VTTimer                                // timer-event subscription
	VTMessageSubscription                  // waiting for a message (receive task, message event)
	VTMessage                              // an incoming message (buffered)
	VTVariable                             // a process variable
	VTIncident                             // a fault state (job failed, expression error)
	VTSignal
	VTError             // error-event propagation
	VTProcessDefinition // a deployed definition
	VTUserTask          // a human task awaiting completion via the tasklist (ADR-0013)
)

func (t ValueType) String() string {
	switch t {
	case VTProcessInstance:
		return "ProcessInstance"
	case VTElementInstance:
		return "ElementInstance"
	case VTJob:
		return "Job"
	case VTTimer:
		return "Timer"
	case VTMessageSubscription:
		return "MessageSubscription"
	case VTMessage:
		return "Message"
	case VTVariable:
		return "Variable"
	case VTIncident:
		return "Incident"
	case VTSignal:
		return "Signal"
	case VTError:
		return "Error"
	case VTProcessDefinition:
		return "ProcessDefinition"
	case VTUserTask:
		return "UserTask"
	default:
		return "ValueType(?)"
	}
}

// Intent is the verb of a record: what transition it represents. The element-
// instance lifecycle (Activating → Activated → Completing → Completed, plus
// Terminating → Terminated) is the heart of the model.
type Intent uint8

const (
	// ElementInstance lifecycle (every BPMN element goes through this).
	IntentActivating Intent = iota
	IntentActivated
	IntentCompleting
	IntentCompleted
	IntentTerminating
	IntentTerminated

	// SequenceFlow.
	IntentSequenceFlowTaken

	// Job.
	IntentJobCreated
	IntentJobActivated // picked up by a worker
	IntentJobCompleted
	IntentJobFailed
	IntentJobTimedOut

	// Timer.
	IntentTimerCreated
	IntentTimerTriggered

	// Message / Signal.
	IntentSubscriptionCreated
	IntentSubscriptionCorrelated
	IntentMessagePublished

	// Variable.
	IntentVariableCreated
	IntentVariableUpdated

	// Incident.
	IntentIncidentCreated
	IntentIncidentResolved

	// User task (ADR-0013). Created on activation; claimed/completed by a human
	// via the tasklist, each transition flowing back as a command.
	IntentUserTaskCreated
	IntentUserTaskClaimed
	IntentUserTaskCompleted
)

func (i Intent) String() string {
	switch i {
	case IntentActivating:
		return "Activating"
	case IntentActivated:
		return "Activated"
	case IntentCompleting:
		return "Completing"
	case IntentCompleted:
		return "Completed"
	case IntentTerminating:
		return "Terminating"
	case IntentTerminated:
		return "Terminated"
	case IntentSequenceFlowTaken:
		return "SequenceFlowTaken"
	case IntentJobCreated:
		return "JobCreated"
	case IntentJobActivated:
		return "JobActivated"
	case IntentJobCompleted:
		return "JobCompleted"
	case IntentJobFailed:
		return "JobFailed"
	case IntentJobTimedOut:
		return "JobTimedOut"
	case IntentTimerCreated:
		return "TimerCreated"
	case IntentTimerTriggered:
		return "TimerTriggered"
	case IntentSubscriptionCreated:
		return "SubscriptionCreated"
	case IntentSubscriptionCorrelated:
		return "SubscriptionCorrelated"
	case IntentMessagePublished:
		return "MessagePublished"
	case IntentVariableCreated:
		return "VariableCreated"
	case IntentVariableUpdated:
		return "VariableUpdated"
	case IntentIncidentCreated:
		return "IncidentCreated"
	case IntentIncidentResolved:
		return "IncidentResolved"
	case IntentUserTaskCreated:
		return "UserTaskCreated"
	case IntentUserTaskClaimed:
		return "UserTaskClaimed"
	case IntentUserTaskCompleted:
		return "UserTaskCompleted"
	default:
		return "Intent(?)"
	}
}

// RecordHeader is the fixed-size metadata every record carries. SourcePos
// threads a causal chain through the log: every record points at the record
// that produced it.
type RecordHeader struct {
	Position    uint64 // monotonic log position (sequence number)
	SourcePos   uint64 // position of the record that caused this one (causality)
	Key         uint64 // entity key
	Timestamp   int64  // unix nano
	RecordType  RecordType
	ValueType   ValueType
	Intent      Intent
	PartitionId uint16
}

// Record bundles a header with its typed payload. Value is nil for records
// whose ValueType carries no payload yet.
type Record struct {
	Header RecordHeader
	Value  Value
}

// Key layout: the partition is baked into the high bits so routing an entity to
// its owning partition is a bit-shift, never a lookup. See data-model.md.
const (
	partitionShift = 48
	counterMask    = (uint64(1) << partitionShift) - 1
)

// NewKey composes a globally unique 64-bit key from a partition id and a
// per-partition monotonic counter.
func NewKey(partition uint16, counter uint64) uint64 {
	return uint64(partition)<<partitionShift | (counter & counterMask)
}

// PartitionOf returns the partition that owns key.
func PartitionOf(key uint64) uint16 { return uint16(key >> partitionShift) }

// CounterOf returns the per-partition counter component of key.
func CounterOf(key uint64) uint64 { return key & counterMask }
