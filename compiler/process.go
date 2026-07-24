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
	TypeBoundaryEvent     // a timer/message event attached to a host activity; arms while the host runs and, when it fires, interrupts the host or spawns a parallel token (ADR-0040)
	TypeScriptJobTask     // a script task authored in a general-purpose language (PowerShell, …) that runs via the job path, not inline like a FEEL script task (ADR-0047); like a service task it creates a job and waits
	TypeTimerStartEvent   // a start event that a due timer instantiates on a schedule (duration/date/cycle/cron, ADR-0051); at runtime it behaves like a none start (flows straight on)
	TypeMessageEndEvent   // an end event that publishes a message, then ends the instance (ADR-0052); the send-and-stop counterpart of a message throw event, so it reuses the throw detail table

	// numBpmnTypes bounds behavior dispatch tables. Grow as element types land.
	numBpmnTypes = 21
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
	case TypeBoundaryEvent:
		return "BoundaryEvent"
	case TypeScriptJobTask:
		return "ScriptJobTask"
	case TypeTimerStartEvent:
		return "TimerStartEvent"
	case TypeMessageEndEvent:
		return "MessageEndEvent"
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
	BoundaryStart int32 // offset into boundaryEvents (the node ids of events attached to this activity)
	BoundaryCount int32 // number of boundary events attached (0 for a non-host node)
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
//
// Connector selects the evaluation locus (ADR-0050): -1 (the default) means the
// decision is evaluated locally by the embedded temis library (ADR-0014); a set
// Connector is the interned name of a server-registered temis connector that
// evaluates the decision centrally, and the task then carries the temis-connector
// job type instead of the local DMN job type.
type BusinessRuleTaskDetail struct {
	JobType       int32 // interned reserved job type (DMN local, or temis connector) → index
	DecisionId    int32 // interned DMN decision id → index
	Inputs        int32 // interned JSON object of static inputs → index, -1 if none
	ResultVar     int32 // interned result-variable name → index, -1 if none
	Connector     int32 // interned temis connector name → index, -1 = local (in-engine)
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
	Name            int32 // interned element name (the task's human title) → index, -1 if unset
	Assignee        int32
	CandidateGroups int32
	FormId          int32 // interned form id bound via zeebe:formDefinition → index, -1 if unset (ADR-0028)
	// Priority is the task's static importance from zeebe:priorityDefinition
	// (default 50, Camunda's convention); higher sorts first in the inbox.
	Priority int32
	// DueDateNanos is the ISO-8601 duration (from zeebe:taskSchedule dueDate),
	// in nanoseconds, after which the task is due — relative to its creation, so
	// the absolute due instant is frozen when the job is created (ADR-0051).
	// 0 means the task has no due date.
	DueDateNanos int64
}

// ConnectorTaskDetail is the per-connector-task data a behavior needs at runtime.
// A connector task delegates to a server-registered connector evaluated off the
// hot path by a job worker (ADR-0036). Like a service task it runs as a job, so
// it carries a JobType (a reserved connector sentinel) the in-process connector
// worker subscribes to, and Connector names the server-registered connector to
// resolve at runtime. The JobType also selects which connector kind this is, and
// thus which of the kind-specific fields below are populated:
//
//   - clio "write-events" (JobType == ClioWriteJobType): Subject and EventType are
//     the interned clio coordinates the appended event lands under.
//   - HTTP REST (JobType == RestJobType): Method and Path are the interned request
//     method (e.g. "POST") and the path appended to the connector's base endpoint.
//
// Unused fields for a given kind are -1 (Intern maps that back to ""). Both kinds
// send the instance's variables as the request/event body — a stand-in for full
// payload mappings until the variable subsystem matures.
type ConnectorTaskDetail struct {
	JobType   int32 // interned reserved connector job type → index
	Connector int32 // interned connector name → index
	Subject   int32 // interned clio target subject → index, -1 if not a clio task
	EventType int32 // interned clio event type → index, -1 if not a clio task
	Method    int32 // interned HTTP method → index, -1 if not a REST task
	Path      int32 // interned HTTP path → index, -1 if not a REST task
	Retries   int32
}

// ScriptJobTaskDetail is the per-script-job-task data a behavior needs at
// runtime. Unlike the inline FEEL script task (ScriptTaskDetail), a job script is
// authored in a general-purpose language (PowerShell first; Python/JavaScript
// later) and runs off the hot path in a job worker, exactly as a business rule
// task delegates to the DMN worker (ADR-0047). Like a service task it runs as a
// job, so it carries a JobType — a reserved per-language sentinel (e.g.
// PwshJobType) the in-process script worker subscribes to. Language is the
// interned language name (which also selects the worker/interpreter), Source is
// the interned script text (compiled/validated no further at deploy time — an
// interpreter runs it, invariant I5 keeps only interning and validation off the
// runtime path), and ResultVar is the process variable the script's result is
// written back into on job completion.
type ScriptJobTaskDetail struct {
	JobType   int32 // interned reserved per-language script job type → index
	Language  int32 // interned script language (e.g. "powershell") → index
	Source    int32 // interned script source text → index
	ResultVar int32 // interned result-variable name → index
	Retries   int32
}

// TimerCatchDetail is the per-timer-intermediate-catch-event data: the compiled
// schedule that decides when the waiting token continues. A catch fires once, so
// only duration and date schedules reach here — a cycle is a compile error
// (ADR-0054).
type TimerCatchDetail struct {
	Schedule TimerSchedule
}

// TimerStartDetail is the per-timer-start-event data: the compiled schedule that
// the engine arms at deploy time and consults to compute each due date (ADR-0051).
type TimerStartDetail struct {
	Schedule TimerSchedule
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

// BoundaryEventKind discriminates what a boundary event waits on.
type BoundaryEventKind uint8

const (
	BoundaryTimer   BoundaryEventKind = iota // waits a fixed duration, then fires
	BoundaryMessage                          // waits for a correlating message, then fires
)

// BoundaryEventDetail is the per-boundary-event data a behavior needs at runtime.
// A boundary event is attached to a host activity (HostNode) and arms while the
// host runs; when it fires it either interrupts the host (Interrupting) or spawns
// a parallel token. The timer fields apply when Kind is BoundaryTimer, the message
// fields when Kind is BoundaryMessage (ADR-0040).
type BoundaryEventDetail struct {
	HostNode       int32 // ElementId of the activity this event is attached to
	Interrupting   bool  // true = cancel the host on fire (BPMN cancelActivity); false = run alongside
	Kind           BoundaryEventKind
	Schedule       TimerSchedule  // BoundaryTimer: when it fires; a cycle (non-interrupting only) recurs (ADR-0054)
	MessageName    string         // BoundaryMessage: the message it subscribes to
	CorrelationKey *expr.Compiled // BoundaryMessage: correlation-key expression (ADR-0020)
}

// CompiledDataObject is one BPMN data object declared by a process: a typed,
// named datum with an optional declared structure and initial data state. Unlike
// a CompiledNode it is not a flow node — no token flows through it (ADR-0053) — so
// it lives in its own table, not the node array, and the engine seeds one under
// each instance's scope at creation. All string fields are interned indices
// (resolve with CompiledProcess.Intern); -1 means unset.
type CompiledDataObject struct {
	Name         int32 // interned data-object name → index
	ItemType     int32 // interned itemDefinition reference → index, -1 if untyped
	InitialState int32 // interned initial data state → index, -1 if none
	IsCollection bool
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
	boundaryEvents    []int32 // shared topology: boundary-event node ids grouped by host node
	serviceTasks      []ServiceTaskDetail
	scriptTasks       []ScriptTaskDetail
	scriptJobTasks    []ScriptJobTaskDetail
	businessRuleTasks []BusinessRuleTaskDetail
	timerCatches      []TimerCatchDetail
	connectorTasks    []ConnectorTaskDetail
	userTasks         []UserTaskDetail
	boundaryEventDets []BoundaryEventDetail
	messageCatches    []MessageDetail
	messageThrows     []MessageDetail
	messageStarts     []MessageDetail
	timerStarts       []TimerStartDetail
	dataObjects       []CompiledDataObject
	startEvents       []int32
	startFormId       int32    // interned start-form id (ADR-0028), -1 if none
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

// BoundaryEvents returns the element ids of the boundary events attached to the
// activity node id, as a slice into the shared topology array (no allocation).
// Empty for a node with no attached boundary events.
func (p *CompiledProcess) BoundaryEvents(id int32) []int32 {
	n := &p.nodes[id]
	return p.boundaryEvents[n.BoundaryStart : n.BoundaryStart+n.BoundaryCount]
}

// BoundaryEvent returns the boundary-event detail at the given table index.
func (p *CompiledProcess) BoundaryEvent(detail int32) *BoundaryEventDetail {
	return &p.boundaryEventDets[detail]
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

// TimerStart returns the timer-start detail at the given table index.
func (p *CompiledProcess) TimerStart(detail int32) *TimerStartDetail { return &p.timerStarts[detail] }

// TimerStartEvent pairs a timer-start event's compiled schedule with its element
// index, so the engine can arm the right timer for the right node (ADR-0051).
type TimerStartEvent struct {
	Schedule  TimerSchedule
	ElementId int32
}

// TimerStartEvents returns each timer-start event with its element index and
// compiled schedule. Computed by scanning the node table at deploy time (off the
// hot path); empty for a process with no timer start event.
func (p *CompiledProcess) TimerStartEvents() []TimerStartEvent {
	var out []TimerStartEvent
	for id := range p.nodes {
		n := &p.nodes[id]
		if n.Type == TypeTimerStartEvent {
			out = append(out, TimerStartEvent{
				Schedule:  p.timerStarts[n.Detail].Schedule,
				ElementId: int32(id),
			})
		}
	}
	return out
}

// ProcessId returns the source BPMN process id (the <process id="…">), used to
// tell one process's versions apart from another's when superseding start timers
// (ADR-0051).
func (p *CompiledProcess) ProcessId() string { return p.Intern(p.BpmnProcessId) }

// ScriptTask returns the detail at the given table index.
func (p *CompiledProcess) ScriptTask(detail int32) *ScriptTaskDetail {
	return &p.scriptTasks[detail]
}

// ScriptJobTask returns the script-job-task detail at the given table index.
func (p *CompiledProcess) ScriptJobTask(detail int32) *ScriptJobTaskDetail {
	return &p.scriptJobTasks[detail]
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
		detail := p.BusinessRuleTask(p.nodes[i].Detail)
		// A connector-mode (central) decision is evaluated by a remote temis
		// service, so it has no local model to resolve and snapshot at deploy time
		// (ADR-0050). Only local decisions contribute to the deploy-time gate.
		if detail.Connector >= 0 {
			continue
		}
		id := p.Intern(detail.DecisionId)
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

// DataObjects returns the process's declared data objects — the typed, named
// data seeded under each instance's scope at creation (ADR-0053). Empty for a
// process that declares none. String fields are interned; resolve with Intern.
func (p *CompiledProcess) DataObjects() []CompiledDataObject { return p.dataObjects }

// StartFormId returns the id of the form the UI shows before starting an
// instance, or "" if the process has no start form (ADR-0028). It is design-time
// metadata; the engine never reads it.
func (p *CompiledProcess) StartFormId() string { return p.Intern(p.startFormId) }

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
