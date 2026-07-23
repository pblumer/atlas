// Package compiler turns a BPMN model into an immutable, integer-indexed
// CompiledProcess (ADR-0004). Element ids become array indices, topology lives
// in shared contiguous arrays, and per-type data lives in detail tables, so the
// runtime hot path is pointer arithmetic with no strings, maps, or locks
// (invariant I5).
//
// This is the minimal target structure plus a programmatic [Builder]. The XML
// parse/resolve/validate front end (compiler.md stages 1–5) is a later
// milestone; the linearized result here is the shape the engine consumes.
package compiler

import "github.com/pblumer/atlas/expr"

// BpmnType is the kind of a BPMN element. It is stored in element-instance state
// (as uint8) for O(1) behavior dispatch.
type BpmnType uint8

const (
	TypeUnspecified BpmnType = iota
	TypeStartEvent
	TypeEndEvent
	TypeServiceTask
	TypeScriptTask
	TypeBusinessRuleTask
	TypeExclusiveGateway
	TypeTimerCatchEvent
	TypeMessageCatchEvent
	TypeMessageThrowEvent
	TypeTask              // an undefined/manual task: no execution semantics, passes straight through
	TypeParallelGateway   // AND gateway: forks a token onto every outgoing flow, joins by waiting for all incoming
	TypeMessageStartEvent // a start event that a correlating message instantiates (ADR-0025); at runtime it behaves like a none start (flows straight on)
	TypeConnectorTask     // a service task that delegates to a server-registered connector via the job path (ADR-0026); like a service task it creates a job and waits

	// numBpmnTypes bounds behavior dispatch tables. Grow as element types land.
	numBpmnTypes = 16
)

// NumBpmnTypes is the size a behavior dispatch table indexed by BpmnType needs.
const NumBpmnTypes = numBpmnTypes

func (t BpmnType) String() string {
	switch t {
	case TypeStartEvent:
		return "StartEvent"
	case TypeEndEvent:
		return "EndEvent"
	case TypeServiceTask:
		return "ServiceTask"
	case TypeScriptTask:
		return "ScriptTask"
	case TypeBusinessRuleTask:
		return "BusinessRuleTask"
	case TypeExclusiveGateway:
		return "ExclusiveGateway"
	case TypeTimerCatchEvent:
		return "TimerCatchEvent"
	case TypeMessageCatchEvent:
		return "MessageCatchEvent"
	case TypeMessageThrowEvent:
		return "MessageThrowEvent"
	case TypeTask:
		return "Task"
	case TypeParallelGateway:
		return "ParallelGateway"
	case TypeMessageStartEvent:
		return "MessageStartEvent"
	case TypeConnectorTask:
		return "ConnectorTask"
	default:
		return "Unspecified"
	}
}

// CompiledNode is one BPMN element. It stays small; type-specific data lives in
// detail tables referenced by Detail.
type CompiledNode struct {
	ElementId     int32 // == index in nodes[]
	Type          BpmnType
	OutgoingStart int32 // offset into outgoingFlows
	OutgoingCount int32
	IncomingCount int32 // number of sequence flows targeting this node (a parallel join waits for all)
	FlowScope     int32 // ElementId of enclosing scope, -1 = process root
	Detail        int32 // index into the matching detail table, -1 if none
}

// CompiledFlow is a sequence flow between two nodes. Condition is the compiled
// FEEL guard an exclusive gateway evaluates to decide whether to take this flow
// (nil = unconditional); Default marks the flow taken when no condition matches.
type CompiledFlow struct {
	Id        int32
	Source    int32 // ElementId
	Target    int32 // ElementId
	Condition *expr.Compiled
	Default   bool
}

// ServiceTaskDetail is the per-service-task data a behavior needs at runtime.
type ServiceTaskDetail struct {
	JobType int32 // interned string → index
	Retries int32
}

// ScriptTaskDetail is the per-script-task data a behavior needs at runtime: a
// FEEL expression compiled once at deploy time (ADR-0008/0015) and the name of
// the variable its result is written to.
type ScriptTaskDetail struct {
	Expr      *expr.Compiled
	ResultVar string
}

// BusinessRuleTaskDetail is the per-business-rule-task data a behavior needs at
// runtime. A business rule task delegates to a DMN decision, evaluated off the
// hot path by the temis engine (ADR-0014). Like a service task it runs as a job,
// so it carries a JobType (a reserved DMN sentinel) the in-process DMN worker
// subscribes to; DecisionId names the decision to evaluate, and Inputs is an
// interned JSON object of the static input context to feed it (a stand-in until
// the variable subsystem lands in Milestone 1).
type BusinessRuleTaskDetail struct {
	JobType    int32 // interned reserved DMN job type → index
	DecisionId int32 // interned DMN decision id → index
	Inputs     int32 // interned JSON object of static inputs → index, -1 if none
	Retries    int32
}

// ConnectorTaskDetail is the per-connector-task data a behavior needs at runtime.
// A connector task delegates to a server-registered connector (e.g. a clio event
// store) evaluated off the hot path by a job worker (ADR-0026). Like a service
// task it runs as a job, so it carries a JobType (a reserved connector sentinel)
// the in-process connector worker subscribes to. Connector names the
// server-registered connector to resolve at runtime; Subject and EventType are
// the interned target coordinates the worker sends (a stand-in for full payload
// mappings until the variable subsystem matures — the worker sends the instance's
// variables as the event body).
type ConnectorTaskDetail struct {
	JobType   int32 // interned reserved connector job type → index
	Connector int32 // interned connector name → index
	Subject   int32 // interned target subject → index
	EventType int32 // interned event type → index
	Retries   int32
}

// TimerCatchDetail is the per-timer-intermediate-catch-event data: how long the
// event waits before continuing, as a fixed duration in nanoseconds (a literal
// ISO-8601 duration today; FEEL duration expressions and date/cycle timers later).
type TimerCatchDetail struct {
	DurationNanos int64
}

// MessageDetail is the per-message-event data a behavior needs at runtime,
// shared by the message intermediate catch and throw events. MessageName is the
// message's name (a subscription matches on it); CorrelationKey is the FEEL
// expression compiled once at deploy time (ADR-0015) that each side evaluates
// over its own variables to produce the correlation key (ADR-0020).
type MessageDetail struct {
	MessageName    string
	CorrelationKey *expr.Compiled
}

// CompiledProcess is the immutable result of compiling one process definition.
// It is safe for concurrent reads without synchronization.
type CompiledProcess struct {
	Key           uint64 // ProcessDefinitionKey
	BpmnProcessId int32  // interned
	Version       int32

	nodes []CompiledNode
	flows []CompiledFlow

	outgoingFlows     []int32 // shared topology: flow ids grouped by source node
	serviceTasks      []ServiceTaskDetail
	scriptTasks       []ScriptTaskDetail
	businessRuleTasks []BusinessRuleTaskDetail
	timerCatches      []TimerCatchDetail
	connectorTasks    []ConnectorTaskDetail
	messageCatches    []MessageDetail
	messageThrows     []MessageDetail
	messageStarts     []MessageDetail
	startEvents       []int32
	elementIds        []int32  // interned source BPMN id per node id (-1 if unset)
	strings           []string // intern table (index → string), for debug/export
}

// Node returns the node with the given ElementId.
func (p *CompiledProcess) Node(id int32) *CompiledNode { return &p.nodes[id] }

// Flow returns the flow with the given id.
func (p *CompiledProcess) Flow(id int32) *CompiledFlow { return &p.flows[id] }

// Outgoing returns the flow ids leaving node id, as a slice into the shared
// topology array (no allocation).
func (p *CompiledProcess) Outgoing(id int32) []int32 {
	n := &p.nodes[id]
	return p.outgoingFlows[n.OutgoingStart : n.OutgoingStart+n.OutgoingCount]
}

// ServiceTask returns the detail at the given table index.
func (p *CompiledProcess) ServiceTask(detail int32) *ServiceTaskDetail {
	return &p.serviceTasks[detail]
}

// TimerCatch returns the timer-catch detail at the given table index.
func (p *CompiledProcess) TimerCatch(detail int32) *TimerCatchDetail {
	return &p.timerCatches[detail]
}

// MessageCatch returns the message-catch detail at the given table index.
func (p *CompiledProcess) MessageCatch(detail int32) *MessageDetail {
	return &p.messageCatches[detail]
}

// MessageThrow returns the message-throw detail at the given table index.
func (p *CompiledProcess) MessageThrow(detail int32) *MessageDetail {
	return &p.messageThrows[detail]
}

// MessageStart returns the message-start detail at the given table index.
func (p *CompiledProcess) MessageStart(detail int32) *MessageDetail {
	return &p.messageStarts[detail]
}

// MessageStarts returns the definition's message-start-event details, one per
// message start event. The engine indexes these at deploy time so a correlating
// message can instantiate the process (ADR-0025). Empty for a process with no
// message start event.
func (p *CompiledProcess) MessageStarts() []MessageDetail { return p.messageStarts }

// ScriptTask returns the detail at the given table index.
func (p *CompiledProcess) ScriptTask(detail int32) *ScriptTaskDetail {
	return &p.scriptTasks[detail]
}

// BusinessRuleTask returns the detail at the given table index.
func (p *CompiledProcess) BusinessRuleTask(detail int32) *BusinessRuleTaskDetail {
	return &p.businessRuleTasks[detail]
}

// ConnectorTask returns the connector-task detail at the given table index.
func (p *CompiledProcess) ConnectorTask(detail int32) *ConnectorTaskDetail {
	return &p.connectorTasks[detail]
}

// StartEvents returns the process's entry-point element ids.
func (p *CompiledProcess) StartEvents() []int32 { return p.startEvents }

// Intern returns the string for an interned index, or "" if out of range.
func (p *CompiledProcess) Intern(idx int32) string {
	if idx < 0 || int(idx) >= len(p.strings) {
		return ""
	}
	return p.strings[idx]
}

// ElementBpmnId returns the source BPMN element id for a node (the string id
// bpmn-js uses, e.g. "StartEvent_1"), or "" if the node index is out of range or
// no id was recorded. Used to map runtime element instances back onto a diagram.
func (p *CompiledProcess) ElementBpmnId(id int32) string {
	if id < 0 || int(id) >= len(p.elementIds) {
		return ""
	}
	return p.Intern(p.elementIds[id])
}
