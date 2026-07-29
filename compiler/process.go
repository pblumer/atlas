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
	TypeSubProcess        // an embedded subprocess: a container that is itself a scope; a token entering it runs its inner start→…→end in a child scope, and it completes when that scope empties (ADR-0074)
	TypeCallActivity      // a call activity: starts a separate process as a child instance, waits for it, then continues; variables pass in/out by mapping (ADR-0076)

	// numBpmnTypes bounds behavior dispatch tables. Grow as element types land.
	numBpmnTypes = 23
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
	case TypeSubProcess:
		return "SubProcess"
	case TypeCallActivity:
		return "CallActivity"
	default:
		return "Unspecified"
	}
}

// CompiledNode is one BPMN element. It stays small; type-specific data lives in
// detail tables referenced by Detail.
type CompiledNode struct {
	ElementId       int32 // == index in nodes[]
	Type            BpmnType
	OutgoingStart   int32 // offset into outgoingFlows
	OutgoingCount   int32
	IncomingCount   int32 // number of sequence flows targeting this node (a parallel join waits for all)
	FlowScope       int32 // ElementId of enclosing scope, -1 = process root
	Detail          int32 // index into the matching detail table, -1 if none
	BoundaryStart   int32 // offset into boundaryEvents (the node ids of events attached to this activity)
	BoundaryCount   int32 // number of boundary events attached (0 for a non-host node)
	DataOutStart    int32 // offset into dataOutAssocs (the data-output associations of this activity)
	DataOutCount    int32 // number of data-output associations (0 for a node with none)
	DataInStart     int32 // offset into dataInAssocs (the data-input associations of this activity)
	DataInCount     int32 // number of data-input associations (0 for a node with none)
	IOInStart       int32 // offset into ioInputs (the zeebe:ioMapping inputs of this activity)
	IOInCount       int32 // number of input mappings (0 for a node with none)
	IOOutStart      int32 // offset into ioOutputs (the zeebe:ioMapping outputs of this activity)
	IOOutCount      int32 // number of output mappings (0 for a node with none)
	ScopeStartStart int32 // offset into scopeStarts (the start events nested directly in this subprocess)
	ScopeStartCount int32 // number of nested start events (0 for a non-subprocess node)
	MultiInstance   int32 // index into multiInstances, -1 if this node is not a multi-instance loop (ADR-0077)
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

// CallActivityDetail is the per-call-activity data a behavior needs at runtime: the
// bpmn process id of the process to start as a child instance (interned), the
// binding that picks its version (latest vs this deployment), and whether variables
// propagate wholesale in and out (Zeebe's propagateAll flags — when off, only the
// activity's input/output mappings pass variables, giving an isolated child)
// (ADR-0076). The called def key is resolved at deploy/runtime, not compiled here.
type CallActivityDetail struct {
	CalledProcessId    int32 // interned bpmn process id of the called process
	Binding            DecisionBinding
	PropagateAllParent bool // pass all caller variables into the child (default true)
	PropagateAllChild  bool // return all child variables to the caller (default true)
}

// MultiInstanceDetail is the per-multi-instance-activity data a behavior needs at
// runtime (ADR-0077). A multi-instance activity runs its node N times — once per
// element of InputCollection (a FEEL list), or Cardinality times — as inner element
// instances scoped under a body. InputElement (interned, -1 if none) is the local
// variable each iteration binds to its item; the standard loopCounter (1-based) is
// bound alongside it. Each iteration's OutputElement (a FEEL over its variables, nil
// if none) is appended to the OutputCollection (interned, -1 if none) list promoted
// to the parent when the loop completes. CompletionCondition (nil if none) is a FEEL
// early-exit evaluated after each iteration. Sequential runs one iteration at a time;
// parallel (the default) seeds them all at once. Exactly one of InputCollection or
// Cardinality is set — the deploy is refused otherwise.
type MultiInstanceDetail struct {
	InputCollection     *expr.Compiled // FEEL list to iterate; nil when Cardinality is used
	Cardinality         *expr.Compiled // FEEL count; nil when InputCollection is used
	InputElement        int32          // interned per-iteration variable name, -1 if none
	OutputCollection    int32          // interned result-list variable name, -1 if none
	OutputElement       *expr.Compiled // FEEL per-iteration contribution, nil if none
	CompletionCondition *expr.Compiled // FEEL early-exit, nil if none
	Sequential          bool           // one iteration at a time (else parallel)
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
	Binding       DecisionBinding        // how the decision model is resolved (ADR-0063)
	InputMappings []DecisionInputMapping // variable-driven inputs, evaluated off the hot path
}

// DecisionBinding selects which DMN model version a local business rule task
// evaluates against (ADR-0063). It mirrors Camunda's zeebe:calledDecision
// bindingType. It applies only to local decisions; a central (connector) decision
// resolves through its connector, so Binding is ignored when Connector is set.
type DecisionBinding int32

const (
	// BindingLatest evaluates the newest deployed version of the decision (the
	// default, matching Camunda). It is zero so an unset binding means "latest".
	BindingLatest DecisionBinding = iota
	// BindingDeployment evaluates the decision snapshotted with this process's own
	// deployment (the ADR-0014 behavior): pinned and reproducible.
	BindingDeployment
)

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
//   - clio "write-events" (JobType == ClioWriteJobType): Connector names the
//     server-registered clio instance; Subject and EventType are the interned clio
//     coordinates the appended event lands under. The event body is the instance's
//     variables.
//   - clio "query" (JobType == ClioQueryJobType): Connector names the clio
//     instance; the task reads projected state or runs a stored query and writes
//     the result into ResultVar. Either ClioQuery (a run_query query string) is set
//     — then the worker runs that query — or Subject (with the optional ReduceSpec
//     projection) is set — then the worker reads get_state for that subject.
//   - clio "read" (JobType == ClioReadJobType): Connector names the clio instance;
//     Subject is the subject whose events are read (up to Limit, 0 = the connector's
//     default) into ResultVar as a JSON array.
//   - HTTP REST (JobType == RestJobType): Method and Url are the interned request
//     method (e.g. "POST") and the full endpoint URL authored in the model
//     (ADR-0067, revising ADR-0036 for REST); ResultVar, if set, is the process
//     variable the JSON response is written back into on completion.
//
// Unused fields for a given kind are -1 (Intern maps that back to ""); Limit is 0
// when unset. The write and REST kinds send the instance's variables as the
// request/event body — a stand-in for full payload mappings until the variable
// subsystem matures.
type ConnectorTaskDetail struct {
	JobType    int32 // interned reserved connector job type → index
	Connector  int32 // interned connector name → index, -1 if not a clio task
	Subject    int32 // interned clio target subject → index, -1 if unused
	EventType  int32 // interned clio event type → index, -1 if not a clio write task
	ClioQuery  int32 // interned clio run_query query string → index, -1 if unused
	ReduceSpec int32 // interned clio get_state reduce-spec name → index, -1 if unused
	Limit      int32 // clio read_events limit, 0 = the connector's default
	Method     int32 // interned HTTP method → index, -1 if not a REST task
	ResultVar  int32 // interned REST/clio result variable name → index, -1 if none
	// Url is the request endpoint, Headers and Query the request headers and query
	// parameters a REST task adds (ADR-0067). Each value is literal or a FEEL
	// expression evaluated over the instance's variables at call time (the
	// Camunda-style fx toggle) — see RestExpr. Url is the zero RestExpr for a
	// non-REST (clio) task; Headers/Query are then nil. Auth is an interned JSON
	// object describing the request's authentication —
	// {"type","username","apiKeyName","secretRef"} — where secretRef names a
	// server-side secret (ADR-0041), never the value; -1 when unauthenticated.
	Url     RestExpr
	Headers []RestKV
	Query   []RestKV
	Auth    int32
	Retries int32
}

// RestExpr is a REST connector field value that is either a literal string
// (Expr == nil, use Literal) or a FEEL expression evaluated over the instance's
// variables at call time (Expr != nil), compiled once at deploy time (invariant
// I5, ADR-0008/0067). It backs the modeler's fx toggle: a model value with a
// leading '=' is an expression, otherwise a literal.
type RestExpr struct {
	Literal string
	Expr    *expr.Compiled
}

// RestKV is a named REST field value (one request header or query parameter): its
// Name and a value that may be literal or a FEEL expression.
type RestKV struct {
	Name string
	Val  RestExpr
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
	// SingletonStart marks a message *start* event as one-per-correlation-key: while
	// an instance started with a given key is live, another correlating message starts
	// no duplicate (ADR-0078). Only meaningful on a message start event; ignored on
	// catch/throw/end. Default false keeps ADR-0035's start-per-message behavior.
	SingletonStart bool
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

// DataOutputAssociation is one compiled <dataOutputAssociation> on an activity: it
// writes a value into a data object and advances that object's data state when the
// activity completes (ADR-0058). DataObject is the interned target data-object
// name; Value is the FEEL expression (the association's <assignment><from>)
// evaluated over the instance's variables to produce the written value, nil for a
// state-only transition; TargetState is the interned data state the write moves the
// object into (from the target <dataObjectReference>'s <dataState>), -1 to keep the
// object's current state.
type DataOutputAssociation struct {
	DataObject  int32 // interned target data-object name → index
	Value       *expr.Compiled
	TargetState int32
	// TargetPath is the interned member path (the association's <assignment><to>,
	// e.g. "name" or "customer.name") the write sets within a structured data
	// object, -1 to write the whole value (ADR-0060). A path write reads the object's
	// current JSON, sets that member, and writes the merged value back.
	TargetPath int32
}

// DataInputAssociation is one compiled <dataInputAssociation> on an activity: it
// reads a data object into a process variable when the activity activates, so the
// activity's FEEL can see it (ADR-0059). DataObject is the interned source
// data-object name (resolved from the association's sourceRef); Variable is the
// interned target process-variable name (its targetRef) the read value is written
// into; Value is the optional <assignment><from> FEEL transform, evaluated over the
// instance's variables plus the source object bound under its name — nil copies the
// object's value verbatim.
type DataInputAssociation struct {
	DataObject int32 // interned source data-object name → index
	Variable   int32 // interned target process-variable name → index
	Value      *expr.Compiled
}

// IOMapping is one compiled zeebe:ioMapping entry on an activity — an input or an
// output — the generic, task-agnostic variable mapping of ADR-0068. Source is a
// FEEL expression compiled once at deploy time (invariant I5); Target is the
// interned variable name it writes. The two directions differ only in where they
// read and write at runtime (phase 4): an input evaluates Source over the scope
// chain from the activity's flow scope and writes Target into the activity-local
// scope on activation; an output evaluates Source over the local scope and writes
// Target into the parent (flow) scope on completion. The compiler only records
// them; the engine applies them.
type IOMapping struct {
	Target int32          // interned target variable name → index
	Source *expr.Compiled // FEEL expression evaluated to produce the value
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
	scopeStarts       []int32 // shared topology: nested start-event node ids grouped by subprocess node
	serviceTasks      []ServiceTaskDetail
	scriptTasks       []ScriptTaskDetail
	callActivities    []CallActivityDetail
	multiInstances    []MultiInstanceDetail
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
	dataOutAssocs     []DataOutputAssociation // shared: output associations grouped by activity node
	dataInAssocs      []DataInputAssociation  // shared: input associations grouped by activity node
	ioInputs          []IOMapping             // shared: zeebe:ioMapping inputs grouped by activity node
	ioOutputs         []IOMapping             // shared: zeebe:ioMapping outputs grouped by activity node
	startEvents       []int32
	startFormId       int32    // interned start-form id (ADR-0028), -1 if none
	versionTag        int32    // interned atlas:versionTag revision label, -1 if none
	isExecutable      bool     // bpmn:isExecutable — a non-executable process can't be started
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

// ScopeStartEvents returns the element ids of the start events nested directly in
// the subprocess node id — the scope's entry points the subprocess behavior seeds
// on activation — as a slice into the shared topology array (no allocation). Empty
// for a non-subprocess node or a subprocess with no start event (ADR-0074).
func (p *CompiledProcess) ScopeStartEvents(id int32) []int32 {
	n := &p.nodes[id]
	return p.scopeStarts[n.ScopeStartStart : n.ScopeStartStart+n.ScopeStartCount]
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
// the collaboration replay (ADR-0038). CorrelationKey is the FEEL expression
// compiled at deploy time; the engine evaluates it over a starting message's
// payload so the created instance records which key it began with (ADR-0020). It
// is nil when the event declares no correlation key.
type MessageStartEvent struct {
	MessageName    string
	ElementId      int32
	CorrelationKey *expr.Compiled
	SingletonStart bool // one live instance per correlation key (ADR-0078)
}

// MessageStartEvents returns each message-start event with its element index and
// compiled correlation-key expression. Computed by scanning the node table at
// deploy time (off the hot path); empty for a process with no message start event.
func (p *CompiledProcess) MessageStartEvents() []MessageStartEvent {
	var out []MessageStartEvent
	for id := range p.nodes {
		n := &p.nodes[id]
		if n.Type == TypeMessageStartEvent {
			out = append(out, MessageStartEvent{
				MessageName:    p.messageStarts[n.Detail].MessageName,
				ElementId:      int32(id),
				CorrelationKey: p.messageStarts[n.Detail].CorrelationKey,
				SingletonStart: p.messageStarts[n.Detail].SingletonStart,
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

// CallActivity returns the call-activity detail at the given table index.
func (p *CompiledProcess) CallActivity(detail int32) *CallActivityDetail {
	return &p.callActivities[detail]
}

// MultiInstance returns the loop characteristics at the given table index — the
// per-activity multi-instance detail a node's MultiInstance field points at
// (ADR-0077).
func (p *CompiledProcess) MultiInstance(detail int32) *MultiInstanceDetail {
	return &p.multiInstances[detail]
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

// DataOutputAssociations returns the data-output associations of activity node id,
// as a slice into the shared array (no allocation). Empty for a node with none. The
// engine evaluates them when the activity completes to write its data objects
// (ADR-0058).
func (p *CompiledProcess) DataOutputAssociations(id int32) []DataOutputAssociation {
	n := &p.nodes[id]
	return p.dataOutAssocs[n.DataOutStart : n.DataOutStart+n.DataOutCount]
}

// DataInputAssociations returns the data-input associations of activity node id, as
// a slice into the shared array (no allocation). Empty for a node with none. The
// engine evaluates them when the activity activates to read its data objects into
// process variables (ADR-0059).
func (p *CompiledProcess) DataInputAssociations(id int32) []DataInputAssociation {
	n := &p.nodes[id]
	return p.dataInAssocs[n.DataInStart : n.DataInStart+n.DataInCount]
}

// IOInputs returns the zeebe:ioMapping input mappings of activity node id, as a
// slice into the shared array (no allocation). Empty for a node with none. The
// engine evaluates them when the activity activates to write its activity-local
// scope (ADR-0068).
func (p *CompiledProcess) IOInputs(id int32) []IOMapping {
	n := &p.nodes[id]
	return p.ioInputs[n.IOInStart : n.IOInStart+n.IOInCount]
}

// IOOutputs returns the zeebe:ioMapping output mappings of activity node id, as a
// slice into the shared array (no allocation). Empty for a node with none. The
// engine evaluates them when the activity completes to promote selected values to
// the parent scope (ADR-0068).
func (p *CompiledProcess) IOOutputs(id int32) []IOMapping {
	n := &p.nodes[id]
	return p.ioOutputs[n.IOOutStart : n.IOOutStart+n.IOOutCount]
}

// StartFormId returns the id of the form the UI shows before starting an
// instance, or "" if the process has no start form (ADR-0028). It is design-time
// metadata; the engine never reads it.
func (p *CompiledProcess) StartFormId() string { return p.Intern(p.startFormId) }

// IsExecutable reports the process's bpmn:isExecutable flag. A non-executable
// process is descriptive-only: the API refuses to start it and omits it from the
// start surfaces. Absent in the source defaults to true (see the parser).
func (p *CompiledProcess) IsExecutable() bool { return p.isExecutable }

// VersionTag returns the process's atlas:versionTag revision label ("" if none). It
// is design-time metadata Operations shows beside the deploy version; the engine
// never reads it.
func (p *CompiledProcess) VersionTag() string { return p.Intern(p.versionTag) }

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
