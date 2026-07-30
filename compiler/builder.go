package compiler

import (
	"encoding/json"
	"fmt"

	"github.com/pblumer/atlas/expr"
)

// DMNJobType is the reserved job type business rule tasks carry. The in-process
// DMN worker subscribes to it to pick up decisions for evaluation, the same way
// an external worker subscribes to a service task's job type.
const DMNJobType = "io.atlas.dmn"

// DMNJobTypeIndex is the interned index DMNJobType is guaranteed to occupy in
// every compiled process: NewBuilder reserves it first, so it is always 0. Job
// type indices are otherwise per-process (interned in build order), which makes a
// global int32-keyed job runner ambiguous across processes — index 3 could be a
// service task's type in one process and something else in another. Pinning the
// DMN type to a single global index lets one in-process DMN worker serve every
// deployed process without colliding with any service-task type (which always
// interns to >= 1). See ADR-0014.
const DMNJobTypeIndex int32 = 0

// UserTaskJobType is the reserved job type user tasks carry. The in-process Tasks
// app (or an external task client) subscribes to it to list and complete human
// tasks, the same way the DMN worker subscribes to DMNJobType (ADR-0028).
const UserTaskJobType = "io.atlas.user-task"

// UserTaskJobTypeIndex is the interned index UserTaskJobType is guaranteed to
// occupy in every compiled process: NewBuilder reserves it second (after DMN),
// so it is always 1. This lets the task-list endpoint scan activatable jobs by
// a single global index, the same way the DMN worker uses DMNJobTypeIndex.
const UserTaskJobTypeIndex int32 = 1

// PwshJobType is the reserved job type a PowerShell script task carries. The
// in-process PowerShell script worker subscribes to it to run the script off the
// hot path and write its result back, the same way the DMN worker subscribes to
// DMNJobType (ADR-0047). Each polyglot script language gets its own reserved job
// type so a customer can deploy and secure only the worker(s) they need.
const PwshJobType = "io.atlas.script.powershell"

// PwshJobTypeIndex is the interned index PwshJobType is guaranteed to occupy in
// every compiled process: NewBuilder reserves it third (after DMN and user
// tasks), so it is always 2. This lets a single in-process PowerShell worker
// subscribe by one global index across every deployed process, the same way the
// DMN worker uses DMNJobTypeIndex (see ADR-0047).
const PwshJobTypeIndex int32 = 2

// PythonJobType is the reserved job type a Python script task carries; the
// in-process Python worker subscribes to it (ADR-0047), like the PowerShell worker.
const PythonJobType = "io.atlas.script.python"

// PythonJobTypeIndex is the interned index PythonJobType is guaranteed to occupy:
// NewBuilder reserves it sixth (after DMN, user tasks, PowerShell, the temis
// connector, and REST), so it is always 5, giving the in-process Python worker one
// global index across every deployed process.
const PythonJobTypeIndex int32 = 5

// JsJobType is the reserved job type a JavaScript script task carries; the
// in-process Node worker subscribes to it (ADR-0047), like the PowerShell worker.
const JsJobType = "io.atlas.script.javascript"

// JsJobTypeIndex is the interned index JsJobType is guaranteed to occupy:
// NewBuilder reserves it seventh, so it is always 6, giving the in-process Node
// worker one global index across every deployed process.
const JsJobTypeIndex int32 = 6

// ClioWriteJobType is the reserved job type a clio "write-events" connector task
// carries. The in-process clio connector worker subscribes to it to append the
// event to the configured clio instance (ADR-0036), the same way the DMN worker
// subscribes to DMNJobType.
const ClioWriteJobType = "io.atlas.clio.write"

// ClioWriteJobTypeIndex is the interned index ClioWriteJobType is guaranteed to
// occupy in every compiled process: NewBuilder reserves it eighth, so it is always
// 7. This lets a single in-process clio worker subscribe by one global index across
// every deployed process, the same way the DMN worker uses DMNJobTypeIndex — which
// is what wires the clio connector into the server run loop (ADR-0036).
const ClioWriteJobTypeIndex int32 = 7

// ClioQueryJobType is the reserved job type a clio "query" connector task carries.
// The in-process clio worker subscribes to it to read projected state (get_state)
// or run a stored query (run_query) on the configured clio instance and write the
// result back into the task's result variable (ADR-0036).
const ClioQueryJobType = "io.atlas.clio.query"

// ClioQueryJobTypeIndex is the interned index ClioQueryJobType is guaranteed to
// occupy: NewBuilder reserves it ninth, so it is always 8.
const ClioQueryJobTypeIndex int32 = 8

// ClioReadJobType is the reserved job type a clio "read" connector task carries.
// The in-process clio worker subscribes to it to read a subject's events
// (read_events) from the configured clio instance and write them back into the
// task's result variable as a JSON array (ADR-0036).
const ClioReadJobType = "io.atlas.clio.read"

// ClioReadJobTypeIndex is the interned index ClioReadJobType is guaranteed to
// occupy: NewBuilder reserves it tenth, so it is always 9.
const ClioReadJobTypeIndex int32 = 9

// RestJobType is the reserved job type an HTTP-REST connector task carries. The
// in-process REST connector worker subscribes to it to call the model-authored
// REST endpoint off the hot path and write the response back (ADR-0036/0067), the
// same way the clio worker subscribes to ClioWriteJobType.
const RestJobType = "io.atlas.http.rest"

// RestJobTypeIndex is the interned index RestJobType is guaranteed to occupy in
// every compiled process: NewBuilder reserves it fifth (after DMN, user tasks,
// PowerShell, and the temis connector), so it is always 4. This lets a single
// in-process REST worker subscribe by one global index across every deployed
// process, the same way the DMN worker uses DMNJobTypeIndex (ADR-0067).
const RestJobTypeIndex int32 = 4

// MailJobType is the reserved job type an outbound mail connector task carries.
// The in-process mail connector worker subscribes to it to send the model-authored
// message through a server-registered mail provider off the hot path (ADR-0079),
// the same way the clio worker subscribes to ClioWriteJobType.
const MailJobType = "io.atlas.mail.send"

// MailJobTypeIndex is the interned index MailJobType is guaranteed to occupy in
// every compiled process: NewBuilder reserves it eleventh (after the ten job types
// above), so it is always 10. This lets a single in-process mail worker subscribe by
// one global index across every deployed process, the same way the REST worker uses
// RestJobTypeIndex (ADR-0067/0078).
const MailJobTypeIndex int32 = 10

// CsvImportJobType is the reserved job type a CSV-import service task carries. An
// in-process worker parses an uploaded CSV (a `csvText` variable) against a column
// layout (a `columnConfig` variable, typically set by a preceding script task) into
// a `rows` collection — so a process ingests and validates a batch of records
// entirely on the engine, the upload arriving through a user-task form rather than a
// side-channel endpoint (ADR-0087).
const CsvImportJobType = "io.atlas.csv-import"

// CsvImportJobTypeIndex is the interned index CsvImportJobType is guaranteed to
// occupy: NewBuilder reserves it twelfth, so it is always 11. A single in-process
// CSV worker subscribes by this global index across every deployed process, the same
// way the mail worker uses MailJobTypeIndex.
const CsvImportJobTypeIndex int32 = 11

// TemisDecisionJobType is the reserved job type a *central* business rule task
// carries — one whose decision is evaluated by a remote temis service rather than
// the embedded temis library. The in-process temis decision connector worker
// subscribes to it to evaluate the decision off the hot path and write the result
// back (ADR-0050), the same way the local DMN worker subscribes to DMNJobType.
const TemisDecisionJobType = "io.atlas.temis.decision"

// TemisDecisionJobTypeIndex is the interned index TemisDecisionJobType is
// guaranteed to occupy in every compiled process: NewBuilder reserves it fourth
// (after DMN, user tasks, and PowerShell), so it is always 3. This lets a single
// in-process temis connector worker subscribe by one global index across every
// deployed process, the same way the DMN worker uses DMNJobTypeIndex (ADR-0050).
const TemisDecisionJobTypeIndex int32 = 3

// Builder constructs a CompiledProcess programmatically. It stands in for the
// XML parse/resolve/linearize pipeline until that front end exists: callers add
// nodes and flows, and Build linearizes them into the immutable form (assigning
// the shared topology array, detail tables, and start-event list).
type Builder struct {
	key           uint64
	bpmnProcessId string
	version       int32

	nodes             []CompiledNode
	flows             []CompiledFlow
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
	eventSubProcesses []EventSubProcessDetail
	messageCatches    []MessageDetail
	messageThrows     []MessageDetail
	messageStarts     []MessageDetail
	timerStarts       []TimerStartDetail
	dataObjects       []CompiledDataObject
	dataOutAssocs     []pendingDataOut // data-output associations, grouped by node in Build
	dataInAssocs      []pendingDataIn  // data-input associations, grouped by node in Build
	ioInputs          []pendingIO      // zeebe:ioMapping inputs, grouped by node in Build
	ioOutputs         []pendingIO      // zeebe:ioMapping outputs, grouped by node in Build
	elementIds        []int32          // interned source BPMN id per node, -1 if unset
	startFormId       int32            // interned start-form id (ADR-0028), -1 if the process has none
	versionTag        int32            // interned atlas:versionTag revision label, -1 if none
	isExecutable      bool             // bpmn:isExecutable; defaults true (set in NewBuilder)

	// flowScope is the enclosing scope every node added now lands in: -1 for the
	// process root, or a subprocess node's ElementId while its children are being
	// added. scopeStack saves the outer scope across nesting (ADR-0074).
	flowScope  int32
	scopeStack []int32

	interner map[string]int32
	strings  []string
}

// NewBuilder starts a builder for the process definition identified by key. It
// reserves the DMN job type as the first interned string so it always occupies
// DMNJobTypeIndex (0), giving the in-process DMN worker a stable, collision-free
// job type across every deployed process (see DMNJobTypeIndex).
func NewBuilder(key uint64, bpmnProcessId string, version int32) *Builder {
	b := &Builder{
		key:           key,
		bpmnProcessId: bpmnProcessId,
		version:       version,
		startFormId:   -1,
		versionTag:    -1,
		isExecutable:  true, // BPMN default; the parser sets false only for isExecutable="false"
		flowScope:     -1,   // nodes land at the process root until a scope is pushed
		interner:      map[string]int32{},
	}
	b.intern(DMNJobType)           // reserve DMNJobTypeIndex == 0
	b.intern(UserTaskJobType)      // reserve UserTaskJobTypeIndex == 1
	b.intern(PwshJobType)          // reserve PwshJobTypeIndex == 2
	b.intern(TemisDecisionJobType) // reserve TemisDecisionJobTypeIndex == 3
	b.intern(RestJobType)          // reserve RestJobTypeIndex == 4
	b.intern(PythonJobType)        // reserve PythonJobTypeIndex == 5
	b.intern(JsJobType)            // reserve JsJobTypeIndex == 6
	b.intern(ClioWriteJobType)     // reserve ClioWriteJobTypeIndex == 7
	b.intern(ClioQueryJobType)     // reserve ClioQueryJobTypeIndex == 8
	b.intern(ClioReadJobType)      // reserve ClioReadJobTypeIndex == 9
	b.intern(MailJobType)          // reserve MailJobTypeIndex == 10
	b.intern(CsvImportJobType)     // reserve CsvImportJobTypeIndex == 11
	return b
}

func (b *Builder) intern(s string) int32 {
	if s == "" {
		return -1
	}
	if idx, ok := b.interner[s]; ok {
		return idx
	}
	idx := int32(len(b.strings))
	b.strings = append(b.strings, s)
	b.interner[s] = idx
	return idx
}

func (b *Builder) addNode(t BpmnType, detail int32) int32 {
	id := int32(len(b.nodes))
	b.nodes = append(b.nodes, CompiledNode{
		ElementId:     id,
		Type:          t,
		FlowScope:     b.flowScope, // the scope currently open (-1 = process root)
		Detail:        detail,
		MultiInstance: -1, // not a loop unless SetMultiInstance marks it (ADR-0077)
		EventSub:      -1, // not event-triggered unless SetEventSubProcess marks it (ADR-0082)
	})
	b.elementIds = append(b.elementIds, -1) // kept in lockstep with nodes
	return id
}

// AddCallActivity adds a call activity that starts the process with the given bpmn
// id as a child instance, under the given binding and variable-propagation flags
// (ADR-0076), and returns its element id. The called process id is interned; the
// called def key is resolved at deploy/runtime, not here.
func (b *Builder) AddCallActivity(calledProcessId string, binding DecisionBinding, propagateAllParent, propagateAllChild bool) int32 {
	detail := int32(len(b.callActivities))
	b.callActivities = append(b.callActivities, CallActivityDetail{
		CalledProcessId:    b.intern(calledProcessId),
		Binding:            binding,
		PropagateAllParent: propagateAllParent,
		PropagateAllChild:  propagateAllChild,
	})
	return b.addNode(TypeCallActivity, detail)
}

// SetMultiInstance marks an already-added node a multi-instance activity carrying
// the given loop characteristics (ADR-0077), interning the per-iteration and result
// variable names. The node keeps its real activity type; its MultiInstance field is
// set to index the loop detail. Applied after the node exists (like io-mappings), so
// any activity — task, subprocess, or call activity — can be a loop. Exactly one of
// inputCollection or cardinality should be non-nil (the parser enforces it).
func (b *Builder) SetMultiInstance(nodeID int32, sequential bool, inputElement, outputCollection string, inputCollection, cardinality, outputElement, completionCondition *expr.Compiled) {
	if !b.validNode(nodeID) {
		return
	}
	idx := int32(len(b.multiInstances))
	b.multiInstances = append(b.multiInstances, MultiInstanceDetail{
		InputCollection:     inputCollection,
		Cardinality:         cardinality,
		InputElement:        b.intern(inputElement),
		OutputCollection:    b.intern(outputCollection),
		OutputElement:       outputElement,
		CompletionCondition: completionCondition,
		Sequential:          sequential,
	})
	b.nodes[nodeID].MultiInstance = idx
}

// AddSubProcess adds an embedded subprocess container node and returns its element
// id. It carries no detail; its inner flow lives in the flat node/flow arrays,
// linked back to it only by the children's FlowScope. Create it first, then
// PushScope(its id) before adding its children so they land in its scope (ADR-0074).
func (b *Builder) AddSubProcess() int32 { return b.addNode(TypeSubProcess, -1) }

// PushScope opens scope id: every node added until the matching PopScope carries id
// as its FlowScope. Scopes nest, so the outer scope is saved and restored.
func (b *Builder) PushScope(id int32) {
	b.scopeStack = append(b.scopeStack, b.flowScope)
	b.flowScope = id
}

// PopScope closes the innermost open scope, restoring the enclosing one.
func (b *Builder) PopScope() {
	n := len(b.scopeStack)
	b.flowScope = b.scopeStack[n-1]
	b.scopeStack = b.scopeStack[:n-1]
}

// CurrentScope reports the scope nodes are added into now (-1 at the process root).
func (b *Builder) CurrentScope() int32 { return b.flowScope }

// SetEventSubProcess marks an already-added subprocess node event-triggered (ADR-0082),
// carrying the trigger detail its start event describes. It is applied after the
// subprocess and its inner start exist (like SetMultiInstance), and its EventSub field
// then indexes the detail. Build groups event-subprocess handlers by their parent scope
// so the runtime can arm them when the scope is entered.
func (b *Builder) SetEventSubProcess(nodeID int32, d EventSubProcessDetail) {
	if !b.validNode(nodeID) {
		return
	}
	idx := int32(len(b.eventSubProcesses))
	b.eventSubProcesses = append(b.eventSubProcesses, d)
	b.nodes[nodeID].EventSub = idx
}

// SetElementBpmnId records the source BPMN element id (e.g. "StartEvent_1") for a
// node so it can be mapped back for diagnostics and the live diagram overlay. It
// is optional: nodes without one report "" from CompiledProcess.ElementBpmnId.
func (b *Builder) SetElementBpmnId(nodeID int32, bpmnID string) {
	if b.validNode(nodeID) {
		b.elementIds[nodeID] = b.intern(bpmnID)
	}
}

// AddStartEvent adds a none start event and returns its element id.
func (b *Builder) AddStartEvent() int32 { return b.addNode(TypeStartEvent, -1) }

// SetStartFormId records the process's start-form id — the form the UI shows
// before creating an instance, whose data becomes the start variables (ADR-0028).
// It is design-time metadata the engine ignores.
func (b *Builder) SetStartFormId(id string) { b.startFormId = b.intern(id) }

// SetExecutable records the process's bpmn:isExecutable flag. A non-executable
// process is descriptive-only — the API refuses to start it and hides it from the
// start surfaces (it still deploys and lists so it can be inspected).
func (b *Builder) SetExecutable(v bool) { b.isExecutable = v }

// SetVersionTag records the process's atlas:versionTag — an optional revision label
// (e.g. "1.4.0") shown in Operations beside the deploy version. Design-time metadata.
func (b *Builder) SetVersionTag(s string) { b.versionTag = b.intern(s) }

// AddMessageStartEvent adds a message start event and returns its element id. It
// is a process entry point like a none start event — at runtime it simply flows
// straight on — but the engine also registers it at deploy time so a correlating
// message (a throw event or an API publish of messageName) instantiates a fresh
// process instance seeded with the message's payload (ADR-0035). correlationKey
// is compiled for future use; message-start matching is by name today.
func (b *Builder) AddMessageStartEvent(messageName string, correlationKey *expr.Compiled, singletonStart bool) int32 {
	detail := int32(len(b.messageStarts))
	b.messageStarts = append(b.messageStarts, MessageDetail{MessageName: messageName, CorrelationKey: correlationKey, SingletonStart: singletonStart})
	return b.addNode(TypeMessageStartEvent, detail)
}

// AddTimerStartEvent adds a timer start event and returns its element id. Like a
// none start it is a process entry point that flows straight on once instantiated;
// what makes it a start is the deploy-time timer the engine arms from its schedule,
// which instantiates a fresh process instance each time it fires (ADR-0051).
func (b *Builder) AddTimerStartEvent(schedule TimerSchedule) int32 {
	detail := int32(len(b.timerStarts))
	b.timerStarts = append(b.timerStarts, TimerStartDetail{Schedule: schedule})
	return b.addNode(TypeTimerStartEvent, detail)
}

// AddEndEvent adds a none end event and returns its element id.
func (b *Builder) AddEndEvent() int32 { return b.addNode(TypeEndEvent, -1) }

// AddServiceTask adds a service task with the given job type and retries and
// returns its element id.
func (b *Builder) AddServiceTask(jobType string, retries int32) int32 {
	detail := int32(len(b.serviceTasks))
	b.serviceTasks = append(b.serviceTasks, ServiceTaskDetail{
		JobType: b.intern(jobType),
		Retries: retries,
	})
	return b.addNode(TypeServiceTask, detail)
}

// AddScriptTask adds a script task that evaluates the given compiled FEEL
// expression and writes the result to resultVar. Returns its element id.
func (b *Builder) AddScriptTask(e *expr.Compiled, resultVar string) int32 {
	detail := int32(len(b.scriptTasks))
	b.scriptTasks = append(b.scriptTasks, ScriptTaskDetail{Expr: e, ResultVar: resultVar})
	return b.addNode(TypeScriptTask, detail)
}

// AddScriptJobTask adds a job-based script task authored in a general-purpose
// language (ADR-0047) and returns its element id. Like a service task it creates
// a job on activation and waits; the job carries jobType — a reserved per-language
// sentinel (e.g. PwshJobType) the in-process script worker for that language picks
// up, runs source through the interpreter, and completes the job, writing the
// result into the resultVar process variable. The parser owns language validation
// and the language→jobType mapping; the builder only interns what it is given, the
// same way AddServiceTask and the connector adds do.
func (b *Builder) AddScriptJobTask(jobType, language, source, resultVar string, retries int32) int32 {
	detail := int32(len(b.scriptJobTasks))
	b.scriptJobTasks = append(b.scriptJobTasks, ScriptJobTaskDetail{
		JobType:   b.intern(jobType),
		Language:  b.intern(language),
		Source:    b.intern(source),
		ResultVar: b.intern(resultVar),
		Retries:   retries,
	})
	return b.addNode(TypeScriptJobTask, detail)
}

// AddBusinessRuleTask adds a business rule task that evaluates the named DMN
// decision with the given static input context, and returns its element id. It is
// the constant-input form of [Builder.AddBusinessRuleTaskMapped] (no variable
// mappings, result discarded).
func (b *Builder) AddBusinessRuleTask(decisionId string, inputs map[string]any, retries int32) (int32, error) {
	return b.AddBusinessRuleTaskMapped(decisionId, "", inputs, nil, retries, BindingLatest)
}

// AddBusinessRuleTaskMapped adds a business rule task that evaluates the named DMN
// decision and returns its element id. Its input context is built from two layers
// the DMN worker merges at evaluation time: staticInputs is a constant base
// (JSON-encoded and interned at deploy time, never on the hot path — invariant
// I5), and mappings are variable-driven inputs (FEEL expressions evaluated over
// the instance's variables) that override a static input of the same name. If
// resultVar is non-empty the decision's result is written back into that process
// variable on job completion; an empty resultVar discards the result. It returns
// an error if the static inputs cannot be encoded.
func (b *Builder) AddBusinessRuleTaskMapped(decisionId, resultVar string, staticInputs map[string]any, mappings []DecisionInputMapping, retries int32, binding DecisionBinding) (int32, error) {
	return b.addBusinessRuleTask("", decisionId, resultVar, staticInputs, mappings, retries, binding)
}

// AddTemisDecisionTask adds a *central* business rule task: one whose decision is
// evaluated by the named server-registered temis connector rather than the
// embedded temis library (ADR-0050). It returns its element id. Authoring is
// otherwise identical to a local business rule task — same decision id, result
// variable, static inputs, and variable mappings — the only difference is that the
// task carries the temis-connector job type so the remote worker picks it up.
func (b *Builder) AddTemisDecisionTask(connector, decisionId, resultVar string, staticInputs map[string]any, mappings []DecisionInputMapping, retries int32) (int32, error) {
	// A central decision resolves through its connector, not a local snapshot, so
	// binding does not apply (BindingLatest is a harmless placeholder).
	return b.addBusinessRuleTask(connector, decisionId, resultVar, staticInputs, mappings, retries, BindingLatest)
}

// addBusinessRuleTask is the shared constructor for local and central business
// rule tasks. An empty connector selects local evaluation (the DMN job type,
// ADR-0014); a named connector selects central evaluation (the temis-connector job
// type, ADR-0050) and records the connector name.
func (b *Builder) addBusinessRuleTask(connector, decisionId, resultVar string, staticInputs map[string]any, mappings []DecisionInputMapping, retries int32, binding DecisionBinding) (int32, error) {
	inputsIdx := int32(-1)
	if len(staticInputs) > 0 {
		encoded, err := json.Marshal(staticInputs)
		if err != nil {
			return -1, fmt.Errorf("compiler: business rule task %q inputs: %w", decisionId, err)
		}
		inputsIdx = b.intern(string(encoded))
	}
	jobType := DMNJobType
	if connector != "" {
		jobType = TemisDecisionJobType
	}
	detail := int32(len(b.businessRuleTasks))
	b.businessRuleTasks = append(b.businessRuleTasks, BusinessRuleTaskDetail{
		JobType:       b.intern(jobType),
		DecisionId:    b.intern(decisionId),
		Inputs:        inputsIdx,
		ResultVar:     b.intern(resultVar),
		Connector:     b.intern(connector),
		Retries:       retries,
		Binding:       binding,
		InputMappings: mappings,
	})
	return b.addNode(TypeBusinessRuleTask, detail), nil
}

// AddClioWriteTask adds a clio "write-events" connector task and returns its
// element id. Like a service task it creates a job on activation and waits; the
// job carries the reserved ClioWriteJobType so the in-process clio worker picks
// it up, appends an event to the named connector's clio instance under subject
// with the given event type, and completes the job (ADR-0036).
func (b *Builder) AddClioWriteTask(connector, subject, eventType string, retries int32) int32 {
	detail := int32(len(b.connectorTasks))
	b.connectorTasks = append(b.connectorTasks, ConnectorTaskDetail{
		JobType:    b.intern(ClioWriteJobType),
		Connector:  b.intern(connector),
		Subject:    b.intern(subject),
		EventType:  b.intern(eventType),
		ClioQuery:  -1,
		ReduceSpec: -1,
		Method:     -1, // not a REST task
		ResultVar:  -1,
		Url:        RestExpr{}, // REST-only fields stay empty for a clio task
		Auth:       -1,
		Retries:    retries,
	})
	return b.addNode(TypeConnectorTask, detail)
}

// AddClioQueryTask adds a clio "query" connector task and returns its element id.
// It reads from the named connector's clio instance and writes the result into
// resultVar. When query is non-empty the worker runs it as a run_query; otherwise
// it reads get_state for subject (with the optional reduceSpec projection). Like a
// service task it creates a job on activation carrying the reserved ClioQueryJobType
// and waits for the in-process clio worker to complete it (ADR-0036).
func (b *Builder) AddClioQueryTask(connector, subject, reduceSpec, query, resultVar string, retries int32) int32 {
	detail := int32(len(b.connectorTasks))
	b.connectorTasks = append(b.connectorTasks, ConnectorTaskDetail{
		JobType:    b.intern(ClioQueryJobType),
		Connector:  b.intern(connector),
		Subject:    b.intern(subject),
		EventType:  -1,
		ClioQuery:  b.intern(query),
		ReduceSpec: b.intern(reduceSpec),
		Method:     -1,
		ResultVar:  b.intern(resultVar),
		Url:        RestExpr{},
		Auth:       -1,
		Retries:    retries,
	})
	return b.addNode(TypeConnectorTask, detail)
}

// AddClioReadTask adds a clio "read" connector task and returns its element id. It
// reads subject's events (up to limit; 0 = the connector's default) from the named
// connector's clio instance and writes them into resultVar as a JSON array. Like a
// service task it creates a job on activation carrying the reserved ClioReadJobType
// and waits for the in-process clio worker to complete it (ADR-0036).
func (b *Builder) AddClioReadTask(connector, subject, resultVar string, limit, retries int32) int32 {
	detail := int32(len(b.connectorTasks))
	b.connectorTasks = append(b.connectorTasks, ConnectorTaskDetail{
		JobType:    b.intern(ClioReadJobType),
		Connector:  b.intern(connector),
		Subject:    b.intern(subject),
		EventType:  -1,
		ClioQuery:  -1,
		ReduceSpec: -1,
		Limit:      limit,
		Method:     -1,
		ResultVar:  b.intern(resultVar),
		Url:        RestExpr{},
		Auth:       -1,
		Retries:    retries,
	})
	return b.addNode(TypeConnectorTask, detail)
}

// RestAuth is a REST connector task's authentication config. Type is "", "basic",
// "bearer", or "apiKey". Username (basic) and ApiKeyName (the apiKey header name)
// are model data. SecretRef names a server-side secret (ADR-0041) — the basic
// password, bearer token, or api-key value — resolved at runtime; the secret value
// itself is never authored in the model or stored here.
type RestAuth struct {
	Type       string `json:"type,omitempty"`
	Username   string `json:"username,omitempty"`
	ApiKeyName string `json:"apiKeyName,omitempty"`
	SecretRef  string `json:"secretRef,omitempty"`
}

// RestConfig is the deploy-time configuration of an HTTP-REST connector task
// (ADR-0067). Method and ResultVar are interned; Url, Headers, and Query carry
// literal-or-FEEL values (the parser compiles the FEEL ones); Auth references a
// server-side secret.
type RestConfig struct {
	Method    string
	Url       RestExpr
	ResultVar string
	Headers   []RestKV
	Query     []RestKV
	Auth      RestAuth
	Retries   int32
}

// AddRestConnectorTask adds an HTTP-REST connector task and returns its element
// id. Like a service task it creates a job on activation and waits; the job
// carries the reserved RestJobType so the in-process REST worker picks it up,
// evaluates any FEEL url/header/query values over the instance's variables, calls
// the endpoint with the given method, writes the JSON response into ResultVar
// (empty = discard the response), and completes the job (ADR-0067). Method is
// stored as given (the parser uppercases and validates it).
func (b *Builder) AddRestConnectorTask(cfg RestConfig) int32 {
	detail := int32(len(b.connectorTasks))
	b.connectorTasks = append(b.connectorTasks, ConnectorTaskDetail{
		JobType:    b.intern(RestJobType),
		Connector:  -1, // REST carries its endpoint in the model, not a registry name
		Subject:    -1, // not a clio task
		EventType:  -1,
		ClioQuery:  -1,
		ReduceSpec: -1,
		Method:     b.intern(cfg.Method),
		ResultVar:  b.intern(cfg.ResultVar),
		Url:        cfg.Url,
		Headers:    cfg.Headers,
		Query:      cfg.Query,
		Auth:       b.internAuth(cfg.Auth),
		Retries:    cfg.Retries,
	})
	return b.addNode(TypeConnectorTask, detail)
}

// internAuth interns a REST auth config as a canonical JSON object, returning -1
// when there is no authentication (empty type).
func (b *Builder) internAuth(a RestAuth) int32 {
	if a.Type == "" {
		return -1
	}
	raw, _ := json.Marshal(a) // a fixed struct of strings always marshals
	return b.intern(string(raw))
}

// MailConfig is the deploy-time configuration of an outbound mail connector task
// (ADR-0079). Connector names the server-registered mail provider (its host and
// credentials live server-side, never in the model); To/Cc/Bcc/From/Subject/Body
// carry literal-or-FEEL values (the parser compiles the FEEL ones) evaluated over
// the instance's variables at send time. To and Subject/Body are the message; Cc,
// Bcc and From are optional (a zero RestExpr means unset).
type MailConfig struct {
	Connector string
	To        RestExpr
	Cc        RestExpr
	Bcc       RestExpr
	From      RestExpr
	Subject   RestExpr
	Body      RestExpr
	Retries   int32
}

// AddMailConnectorTask adds an outbound mail connector task and returns its element
// id. Like a service task it creates a job on activation and waits; the job carries
// the reserved MailJobType so the in-process mail worker picks it up, evaluates any
// FEEL recipient/subject/body values over the instance's variables, resolves the
// named connector's provider client, sends the message, and completes the job
// (ADR-0079). The provider endpoint and credentials are resolved server-side from
// the named connector, never authored in the model — mirroring clio (ADR-0036).
func (b *Builder) AddMailConnectorTask(cfg MailConfig) int32 {
	detail := int32(len(b.connectorTasks))
	b.connectorTasks = append(b.connectorTasks, ConnectorTaskDetail{
		JobType:     b.intern(MailJobType),
		Connector:   b.intern(cfg.Connector),
		Subject:     -1, // not a clio task
		EventType:   -1,
		ClioQuery:   -1,
		ReduceSpec:  -1,
		Method:      -1, // not a REST task
		ResultVar:   -1, // mail sends, it produces no result variable
		Auth:        -1,
		To:          cfg.To,
		Cc:          cfg.Cc,
		Bcc:         cfg.Bcc,
		From:        cfg.From,
		MailSubject: cfg.Subject,
		Body:        cfg.Body,
		Retries:     cfg.Retries,
	})
	return b.addNode(TypeConnectorTask, detail)
}

// AddUserTask adds a user task that parks a token and creates a job for a human
// to complete via the Tasks app (ADR-0028). assignee and candidateGroups are
// optional (empty strings are stored as -1). Returns its element id.
func (b *Builder) AddUserTask(name, assignee, candidateGroups, formId string, priority int32, dueDateNanos int64, retries int32) int32 {
	detail := int32(len(b.userTasks))
	b.userTasks = append(b.userTasks, UserTaskDetail{
		JobType:         b.intern(UserTaskJobType),
		Name:            b.intern(name),
		Assignee:        b.intern(assignee),
		CandidateGroups: b.intern(candidateGroups),
		FormId:          b.intern(formId),
		Priority:        priority,
		DueDateNanos:    dueDateNanos,
		Retries:         retries,
	})
	return b.addNode(TypeUserTask, detail)
}

// AddBoundaryTimerEvent adds a timer boundary event attached to host, firing
// after durationNanos. interrupting mirrors BPMN cancelActivity: true cancels the
// host when it fires, false spawns a parallel token (ADR-0040). Returns its
// element id. It is the duration convenience over AddBoundaryTimerSchedule.
func (b *Builder) AddBoundaryTimerEvent(host int32, interrupting bool, durationNanos int64) int32 {
	return b.AddBoundaryTimerSchedule(host, interrupting, TimerSchedule{Kind: TimerDuration, BaseNanos: durationNanos})
}

// AddBoundaryTimerSchedule adds a timer boundary event firing on the given
// compiled schedule. A cycle schedule on a non-interrupting boundary recurs — a
// repeating reminder (ADR-0054). Returns its element id.
func (b *Builder) AddBoundaryTimerSchedule(host int32, interrupting bool, schedule TimerSchedule) int32 {
	detail := int32(len(b.boundaryEventDets))
	b.boundaryEventDets = append(b.boundaryEventDets, BoundaryEventDetail{
		HostNode:     host,
		Interrupting: interrupting,
		Kind:         BoundaryTimer,
		Schedule:     schedule,
	})
	return b.addNode(TypeBoundaryEvent, detail)
}

// AddBoundaryMessageEvent adds a message boundary event attached to host that
// fires when a message named messageName correlates on key. interrupting mirrors
// BPMN cancelActivity (ADR-0040). Returns its element id.
func (b *Builder) AddBoundaryMessageEvent(host int32, interrupting bool, messageName string, correlationKey *expr.Compiled) int32 {
	detail := int32(len(b.boundaryEventDets))
	b.boundaryEventDets = append(b.boundaryEventDets, BoundaryEventDetail{
		HostNode:       host,
		Interrupting:   interrupting,
		Kind:           BoundaryMessage,
		MessageName:    messageName,
		CorrelationKey: correlationKey,
	})
	return b.addNode(TypeBoundaryEvent, detail)
}

// AddDataObject declares a data object on the process: a typed, named datum with
// an optional declared structure (itemType) and initial data state, seeded under
// each instance's scope at creation (ADR-0053). It is not a flow node, so it
// returns the index of the entry in the data-object table, not an element id.
// Empty itemType or initialState intern to -1 (Intern maps that back to "").
func (b *Builder) AddDataObject(name, itemType, initialState string, isCollection bool) int32 {
	idx := int32(len(b.dataObjects))
	b.dataObjects = append(b.dataObjects, CompiledDataObject{
		Name:         b.intern(name),
		ItemType:     b.intern(itemType),
		InitialState: b.intern(initialState),
		IsCollection: isCollection,
	})
	return idx
}

// pendingDataOut pairs a data-output association with the activity node it belongs
// to, until Build groups them into the shared per-node array.
type pendingDataOut struct {
	node  int32
	assoc DataOutputAssociation
}

// pendingDataIn pairs a data-input association with the activity node it belongs
// to, until Build groups them into the shared per-node array.
type pendingDataIn struct {
	node  int32
	assoc DataInputAssociation
}

// AddDataInputAssociation attaches a data-input association to activity node: when
// the activity activates, the engine reads the data object named dataObject (bound
// into the FEEL scope under its name), evaluates value (a FEEL transform over the
// instance's variables and that object, nil to copy the object's value verbatim),
// and writes the result into the process variable named variable, which the activity
// then reads (ADR-0059). Build groups a node's associations into a shared array.
func (b *Builder) AddDataInputAssociation(node int32, dataObject, variable string, value *expr.Compiled) {
	b.dataInAssocs = append(b.dataInAssocs, pendingDataIn{
		node: node,
		assoc: DataInputAssociation{
			DataObject: b.intern(dataObject),
			Variable:   b.intern(variable),
			Value:      value,
		},
	})
}

// AddDataOutputAssociation attaches a data-output association to activity node: when
// the activity completes, the engine evaluates value (a FEEL expression over the
// instance's variables, nil for a state-only transition) and writes it into the data
// object named dataObject, advancing that object's data state to targetState (empty
// keeps the object's current state) — ADR-0058. A non-empty targetPath writes only
// that member of a structured object, keeping the rest (ADR-0060). Build groups a
// node's associations into a shared array.
func (b *Builder) AddDataOutputAssociation(node int32, dataObject string, value *expr.Compiled, targetState, targetPath string) {
	b.dataOutAssocs = append(b.dataOutAssocs, pendingDataOut{
		node: node,
		assoc: DataOutputAssociation{
			DataObject:  b.intern(dataObject),
			Value:       value,
			TargetState: b.intern(targetState),
			TargetPath:  b.intern(targetPath),
		},
	})
}

// pendingIO pairs a zeebe:ioMapping entry (input or output) with the activity node
// it belongs to, until Build groups the two directions into their shared per-node
// arrays.
type pendingIO struct {
	node    int32
	mapping IOMapping
}

// AddInputMapping attaches a zeebe:ioMapping input to activity node: when the
// activity activates, the engine evaluates source (a FEEL expression over the scope
// chain from the activity's flow scope) and writes the result into the activity-local
// variable named target, which the activity then sees (ADR-0068). Build groups a
// node's input mappings into a shared array. The parser owns validation; the builder
// only interns the target, mirroring the data-association adds.
func (b *Builder) AddInputMapping(node int32, target string, source *expr.Compiled) {
	b.ioInputs = append(b.ioInputs, pendingIO{
		node:    node,
		mapping: IOMapping{Target: b.intern(target), Source: source},
	})
}

// AddOutputMapping attaches a zeebe:ioMapping output to activity node: when the
// activity completes, the engine evaluates source (a FEEL expression over the
// activity-local scope) and promotes the result into the parent (flow) scope under
// the variable named target (ADR-0068). Build groups a node's output mappings into a
// shared array.
func (b *Builder) AddOutputMapping(node int32, target string, source *expr.Compiled) {
	b.ioOutputs = append(b.ioOutputs, pendingIO{
		node:    node,
		mapping: IOMapping{Target: b.intern(target), Source: source},
	})
}

// AddTask adds an undefined/manual task — one with no execution semantics — and
// returns its element id. It carries no detail and simply passes the token
// straight through, so a model can be drafted and its routing tested before its
// tasks are given real implementations.
func (b *Builder) AddTask() int32 { return b.addNode(TypeTask, -1) }

// AddInclusiveGateway adds an inclusive (OR) gateway and returns its element id.
// As a split it takes every outgoing flow whose condition holds (or the default
// if none do); as a join it waits until every branch that could still deliver a
// token has, then fires once. Conditions and the default flow are set the same
// way as for an exclusive gateway.
func (b *Builder) AddInclusiveGateway() int32 { return b.addNode(TypeInclusiveGateway, -1) }

// AddParallelGateway adds a parallel (AND) gateway and returns its element id. It
// forks a token onto every outgoing flow and joins by waiting until a token has
// arrived on each of its incoming flows.
func (b *Builder) AddParallelGateway() int32 { return b.addNode(TypeParallelGateway, -1) }

// AddExclusiveGateway adds a data-based exclusive gateway (XOR split) and returns
// its element id. Its outgoing flows carry the conditions; see SetFlowCondition
// and SetFlowDefault.
func (b *Builder) AddExclusiveGateway() int32 { return b.addNode(TypeExclusiveGateway, -1) }

// AddTimerCatchEvent adds an intermediate timer catch event that waits the given
// fixed duration (nanoseconds) before continuing, and returns its element id. It
// is the duration convenience over AddTimerCatchSchedule.
func (b *Builder) AddTimerCatchEvent(durationNanos int64) int32 {
	return b.AddTimerCatchSchedule(TimerSchedule{Kind: TimerDuration, BaseNanos: durationNanos})
}

// AddTimerCatchSchedule adds an intermediate timer catch event that waits until
// the given schedule's first due date, then continues. A catch fires once, so the
// schedule is a duration or date, never a cycle (ADR-0054). Returns its element id.
func (b *Builder) AddTimerCatchSchedule(schedule TimerSchedule) int32 {
	detail := int32(len(b.timerCatches))
	b.timerCatches = append(b.timerCatches, TimerCatchDetail{Schedule: schedule})
	return b.addNode(TypeTimerCatchEvent, detail)
}

// AddMessageCatchEvent adds an intermediate message catch event that, on
// activation, subscribes to the named message with a correlation key produced by
// the given compiled FEEL expression (evaluated over the instance's variables),
// then waits until a matching message is correlated. Returns its element id.
func (b *Builder) AddMessageCatchEvent(messageName string, correlationKey *expr.Compiled) int32 {
	detail := int32(len(b.messageCatches))
	b.messageCatches = append(b.messageCatches, MessageDetail{MessageName: messageName, CorrelationKey: correlationKey})
	return b.addNode(TypeMessageCatchEvent, detail)
}

// AddMessageThrowEvent adds an intermediate message throw event that, on
// activation, publishes the named message with a correlation key produced by the
// given compiled FEEL expression (evaluated over the throwing instance's
// variables), then completes. Returns its element id.
func (b *Builder) AddMessageThrowEvent(messageName string, correlationKey *expr.Compiled) int32 {
	detail := int32(len(b.messageThrows))
	b.messageThrows = append(b.messageThrows, MessageDetail{MessageName: messageName, CorrelationKey: correlationKey})
	return b.addNode(TypeMessageThrowEvent, detail)
}

// AddMessageEndEvent adds an end event that, on activation, publishes the named
// message with a correlation key produced by the given compiled FEEL expression
// (evaluated over the ending instance's variables), then ends the instance.
// It reuses the throw detail table, since a message end event throws exactly like
// an intermediate throw event and only differs in its completion (ADR-0054).
// Returns its element id.
func (b *Builder) AddMessageEndEvent(messageName string, correlationKey *expr.Compiled) int32 {
	detail := int32(len(b.messageThrows))
	b.messageThrows = append(b.messageThrows, MessageDetail{MessageName: messageName, CorrelationKey: correlationKey})
	return b.addNode(TypeMessageEndEvent, detail)
}

// Connect adds a sequence flow from source to target and returns its flow id, so
// the caller can attach a condition or mark it the default.
func (b *Builder) Connect(source, target int32) int32 {
	id := int32(len(b.flows))
	b.flows = append(b.flows, CompiledFlow{
		Id:     id,
		Source: source,
		Target: target,
	})
	return id
}

// SetFlowCondition attaches a compiled FEEL guard to a flow (an exclusive gateway
// takes the first flow whose condition is true).
func (b *Builder) SetFlowCondition(flowID int32, c *expr.Compiled) {
	if flowID >= 0 && int(flowID) < len(b.flows) {
		b.flows[flowID].Condition = c
	}
}

// SetFlowDefault marks a flow as its gateway's default (taken when no condition matches).
func (b *Builder) SetFlowDefault(flowID int32) {
	if flowID >= 0 && int(flowID) < len(b.flows) {
		b.flows[flowID].Default = true
	}
}

// Build linearizes the accumulated nodes and flows into an immutable
// CompiledProcess. It returns an error if a flow references an unknown node.
func (b *Builder) Build() (*CompiledProcess, error) {
	for _, f := range b.flows {
		if !b.validNode(f.Source) || !b.validNode(f.Target) {
			return nil, fmt.Errorf("compiler: flow %d references unknown node", f.Id)
		}
	}

	// Group outgoing flow ids by source node into one shared array.
	var outgoing []int32
	for i := range b.nodes {
		n := &b.nodes[i]
		n.OutgoingStart = int32(len(outgoing))
		for _, f := range b.flows {
			if f.Source == n.ElementId {
				outgoing = append(outgoing, f.Id)
			}
		}
		n.OutgoingCount = int32(len(outgoing)) - n.OutgoingStart
	}

	// Group boundary-event node ids by their host activity into one shared array,
	// mirroring the outgoing-flow grouping, so arming a host's boundary events is
	// an allocation-free slice at runtime. Each boundary-event node's detail names
	// the host it attaches to.
	var boundary []int32
	for i := range b.nodes {
		n := &b.nodes[i]
		n.BoundaryStart = int32(len(boundary))
		for j := range b.nodes {
			be := &b.nodes[j]
			if be.Type == TypeBoundaryEvent && b.boundaryEventDets[be.Detail].HostNode == n.ElementId {
				boundary = append(boundary, be.ElementId)
			}
		}
		n.BoundaryCount = int32(len(boundary)) - n.BoundaryStart
	}

	// Group nested start events by their enclosing subprocess into one shared array,
	// mirroring the boundary-event grouping, so a subprocess behavior seeds its
	// scope's entry points as an allocation-free slice at runtime. A start event's
	// FlowScope is the subprocess node it belongs to (-1 = process root, not grouped
	// here) (ADR-0074).
	var scopeStarts []int32
	for i := range b.nodes {
		n := &b.nodes[i]
		n.ScopeStartStart = int32(len(scopeStarts))
		if n.Type == TypeSubProcess {
			for j := range b.nodes {
				s := &b.nodes[j]
				if isStartEvent(s.Type) && s.FlowScope == n.ElementId {
					scopeStarts = append(scopeStarts, s.ElementId)
				}
			}
		}
		n.ScopeStartCount = int32(len(scopeStarts)) - n.ScopeStartStart
	}

	// Group event-subprocess handler nodes by their parent scope, mirroring the nested-
	// start grouping, so the runtime arms a scope's event-subprocess triggers as an
	// allocation-free slice when the scope is entered. A handler's FlowScope is its
	// parent scope (a subprocess node, or -1 for the process root — collected separately
	// into rootEventSubs) (ADR-0082).
	var eventSubs []int32
	for i := range b.nodes {
		n := &b.nodes[i]
		n.EventSubStart = int32(len(eventSubs))
		if n.Type == TypeSubProcess {
			for j := range b.nodes {
				h := &b.nodes[j]
				if h.EventSub >= 0 && h.FlowScope == n.ElementId {
					eventSubs = append(eventSubs, h.ElementId)
				}
			}
		}
		n.EventSubCount = int32(len(eventSubs)) - n.EventSubStart
	}
	var rootEventSubs []int32
	for i := range b.nodes {
		if b.nodes[i].EventSub >= 0 && b.nodes[i].FlowScope == -1 {
			rootEventSubs = append(rootEventSubs, b.nodes[i].ElementId)
		}
	}

	// Group data-output associations by their activity node into one shared array,
	// mirroring the outgoing-flow and boundary-event grouping, so evaluating a
	// completing activity's associations is an allocation-free slice at runtime
	// (ADR-0058).
	var dataOut []DataOutputAssociation
	for i := range b.nodes {
		n := &b.nodes[i]
		n.DataOutStart = int32(len(dataOut))
		for _, p := range b.dataOutAssocs {
			if p.node == n.ElementId {
				dataOut = append(dataOut, p.assoc)
			}
		}
		n.DataOutCount = int32(len(dataOut)) - n.DataOutStart
	}

	// Group data-input associations by their activity node, mirroring the output
	// grouping (ADR-0059).
	var dataIn []DataInputAssociation
	for i := range b.nodes {
		n := &b.nodes[i]
		n.DataInStart = int32(len(dataIn))
		for _, p := range b.dataInAssocs {
			if p.node == n.ElementId {
				dataIn = append(dataIn, p.assoc)
			}
		}
		n.DataInCount = int32(len(dataIn)) - n.DataInStart
	}

	// Group zeebe:ioMapping input and output mappings by their activity node into
	// two shared arrays, mirroring the data-association grouping, so evaluating an
	// activity's mappings is an allocation-free slice at runtime (ADR-0068).
	var ioIn []IOMapping
	for i := range b.nodes {
		n := &b.nodes[i]
		n.IOInStart = int32(len(ioIn))
		for _, p := range b.ioInputs {
			if p.node == n.ElementId {
				ioIn = append(ioIn, p.mapping)
			}
		}
		n.IOInCount = int32(len(ioIn)) - n.IOInStart
	}
	var ioOut []IOMapping
	for i := range b.nodes {
		n := &b.nodes[i]
		n.IOOutStart = int32(len(ioOut))
		for _, p := range b.ioOutputs {
			if p.node == n.ElementId {
				ioOut = append(ioOut, p.mapping)
			}
		}
		n.IOOutCount = int32(len(ioOut)) - n.IOOutStart
	}

	// Count incoming flows per node, so a parallel join knows how many tokens to
	// wait for.
	for _, f := range b.flows {
		b.nodes[f.Target].IncomingCount++
	}

	// Only root-scope start events are process entry points — the engine seeds a
	// token at each when an instance starts. A start event nested in a subprocess is
	// that scope's entry and is seeded by the subprocess behavior, not at instance
	// creation (ADR-0074).
	var startEvents []int32
	for i := range b.nodes {
		if isStartEvent(b.nodes[i].Type) && b.nodes[i].FlowScope == -1 {
			startEvents = append(startEvents, b.nodes[i].ElementId)
		}
	}

	return &CompiledProcess{
		Key:               b.key,
		BpmnProcessId:     b.intern(b.bpmnProcessId),
		Version:           b.version,
		nodes:             b.nodes,
		flows:             b.flows,
		outgoingFlows:     outgoing,
		boundaryEvents:    boundary,
		scopeStarts:       scopeStarts,
		serviceTasks:      b.serviceTasks,
		scriptTasks:       b.scriptTasks,
		callActivities:    b.callActivities,
		multiInstances:    b.multiInstances,
		scriptJobTasks:    b.scriptJobTasks,
		businessRuleTasks: b.businessRuleTasks,
		timerCatches:      b.timerCatches,
		connectorTasks:    b.connectorTasks,
		userTasks:         b.userTasks,
		boundaryEventDets: b.boundaryEventDets,
		eventSubProcesses: b.eventSubProcesses,
		eventSubs:         eventSubs,
		rootEventSubs:     rootEventSubs,
		messageCatches:    b.messageCatches,
		messageThrows:     b.messageThrows,
		messageStarts:     b.messageStarts,
		timerStarts:       b.timerStarts,
		dataObjects:       b.dataObjects,
		dataOutAssocs:     dataOut,
		dataInAssocs:      dataIn,
		ioInputs:          ioIn,
		ioOutputs:         ioOut,
		startEvents:       startEvents,
		elementIds:        b.elementIds,
		startFormId:       b.startFormId,
		versionTag:        b.versionTag,
		isExecutable:      b.isExecutable,
		strings:           b.strings,
	}, nil
}

func (b *Builder) validNode(id int32) bool {
	return id >= 0 && int(id) < len(b.nodes)
}

// hasStartEvent reports whether the process has a root-scope start event — its
// entry point. A start event nested in a subprocess does not count: it is that
// scope's entry, not the process's (ADR-0074).
func (b *Builder) hasStartEvent() bool {
	for i := range b.nodes {
		if isStartEvent(b.nodes[i].Type) && b.nodes[i].FlowScope == -1 {
			return true
		}
	}
	return false
}

// isStartEvent reports whether a node type is a process entry point. A message
// start event is one too: a correlating message instantiates the process, and a
// plain create then activates it like a none start (ADR-0035). A timer start
// event likewise: a due timer instantiates it, and it then flows on (ADR-0051).
func isStartEvent(t BpmnType) bool {
	return t == TypeStartEvent || t == TypeMessageStartEvent || t == TypeTimerStartEvent
}
