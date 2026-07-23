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
	TypeInclusiveGateway  // OR gateway: forks onto every flow whose condition holds, joins by waiting for all that could still arrive
	TypeMessageStartEvent // a start event that a correlating message instantiates (ADR-0035); at runtime it behaves like a none start (flows straight on)
	TypeConnectorTask     // a service task that delegates to a server-registered connector via the job path (ADR-0036); like a service task it creates a job and waits
	TypeUserTask          // a human task: parks a token, creates a job, waits for a person to complete it via the Tasks app (ADR-0028)

	// numBpmnTypes bounds behavior dispatch tables. Grow as element types land.
	numBpmnTypes = 17
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
	case TypeInclusiveGateway:
		return "InclusiveGateway"
	case TypeMessageStartEvent:
		return "MessageStartEvent"
	case TypeConnectorTask:
		return "ConnectorTask"
	case TypeUserTask:
		return "UserTask"
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

// DecisionInputMapping is one explicit input to a DMN decision: the decision's
// input name (Target) fed by a FEEL expression (Source) evaluated over the
// process instance's variables at evaluation time. It is the variable-driven
// replacement for a business rule task's static inputs (ADR-0014): the source
// expression is compiled once at deploy time (invariant I5) and the DMN worker
// evaluates it off the hot path against live variables, so a decision routes on
// real instance data.
type DecisionInputMapping struct {
	Target string         // the decision input name this value binds to
	Source *expr.Compiled // FEEL expression evaluated over instance variables
}

// BusinessRuleTaskDetail is the per-business-rule-task data a behavior needs at
// runtime. A business rule task delegates to a DMN decision, evaluated off the
// hot path by the temis engine (ADR-0014). Like a service task it runs as a job,
// so it carries a JobType (a reserved DMN sentinel) the in-process DMN worker
// subscribes to; DecisionId names the decision to evaluate.
//
// Its inputs come from two layers the worker merges: Inputs is an interned JSON
// object of static constant inputs (a literal base), and InputMappings are the
// variable-driven inputs — FEEL expressions evaluated over the instance's
// variables, which override a static input of the same name. ResultVar, if set,
// is the process variable the decision's result is written back into on job
// completion (the output mapping); -1 if the task discards its result.
type BusinessRuleTaskDetail struct {
	JobType       int32 // interned reserved DMN job type → index
	DecisionId    int32 // interned DMN decision id → index
	Inputs        int32 // interned JSON object of static inputs → index, -1 if none
	ResultVar     int32 // interned result-variable name → index, -1 if none
	Retries       int32
	InputMappings []DecisionInputMapping // variable-driven inputs, evaluated off the hot path
}

// UserTaskDetail is the per-user-task data a behavior needs at runtime. A user
// task parks a token and creates a job like a service task; the "worker" is a
// person using the Tasks app (ADR-0028). Assignee and CandidateGroups are
// interned strings from the zeebe:assignmentDefinition extension (-1 if unset).
type UserTaskDetail struct {
	JobType         int32
	Retries         int32
	Assignee        int32
	CandidateGroups int32
}

// ConnectorTaskDetail is the per-connector-task data a behavior needs at runtime.
// A connector task delegates to a server-registered connector (e.g. a clio event
// store) evaluated off the hot path by a job worker (ADR-0036). Like a service
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
	userTasks         []UserTaskDetail
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

// NodesReaching returns the set of node ids from which target is reachable by
// following sequence flows — target's ancestors in the flow graph. An inclusive
// join uses it to decide whether any live token upstream could still arrive
// (if none can, and at least one has, it fires). Computed by a reverse walk from
// target; target itself is not included unless a cycle leads back to it.
func (p *CompiledProcess) NodesReaching(target int32) map[int32]bool {
	preds := make([][]int32, len(p.nodes))
	for i := range p.nodes {
		for _, fid := range p.Outgoing(int32(i)) {
			t := p.Flow(fid).Target
			preds[t] = append(preds[t], int32(i))
		}
	}
	seen := map[int32]bool{}
	stack := []int32{target}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, pd := range preds[n] {
			if !seen[pd] {
				seen[pd] = true
				stack = append(stack, pd)
			}
		}
	}
	return seen
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
// message can instantiate the process (ADR-0035). Empty for a process with no
// message start event.
func (p *CompiledProcess) MessageStarts() []MessageDetail { return p.messageStarts }

// MessageStartEvent pairs a message-start event's message name with its element
// index, so the engine can index which element a starting message flows into for
// the collaboration replay (ADR-0038).
type MessageStartEvent struct {
	MessageName string
	ElementId   int32
}

// MessageStartEvents returns each message-start event with its element index.
// Computed by scanning the node table at deploy time (off the hot path); empty
// for a process with no message start event.
func (p *CompiledProcess) MessageStartEvents() []MessageStartEvent {
	var out []MessageStartEvent
	for id := range p.nodes {
		n := &p.nodes[id]
		if n.Type == TypeMessageStartEvent {
			out = append(out, MessageStartEvent{
				MessageName: p.messageStarts[n.Detail].MessageName,
				ElementId:   int32(id),
			})
		}
	}
	return out
}

// ScriptTask returns the detail at the given table index.
func (p *CompiledProcess) ScriptTask(detail int32) *ScriptTaskDetail {
	return &p.scriptTasks[detail]
}

// BusinessRuleTask returns the detail at the given table index.
func (p *CompiledProcess) BusinessRuleTask(detail int32) *BusinessRuleTaskDetail {
	return &p.businessRuleTasks[detail]
}

// BusinessRuleDecisions returns the DMN decision ids this process's business rule
// tasks reference, distinct and in node order — empty if it has none. The server
// uses it at deploy time to pick and deploy the DMN model that provides those
// decisions into the DMN registry, so the tasks can be evaluated (ADR-0014).
func (p *CompiledProcess) BusinessRuleDecisions() []string {
	var out []string
	seen := map[string]bool{}
	for i := range p.nodes {
		if p.nodes[i].Type != TypeBusinessRuleTask {
			continue
		}
		id := p.Intern(p.BusinessRuleTask(p.nodes[i].Detail).DecisionId)
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// UserTask returns the user-task detail at the given table index.
func (p *CompiledProcess) UserTask(detail int32) *UserTaskDetail {
	return &p.userTasks[detail]
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
