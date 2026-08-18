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

// SharePointJobType is the reserved job type a SharePoint connector task carries.
// The in-process SharePoint connector worker subscribes to it to create a list item
// in a model-authored SharePoint site/list through a server-registered SharePoint
// provider (Microsoft Graph) off the hot path (ADR-0141), the same way the mail
// worker subscribes to MailJobType.
const SharePointJobType = "io.atlas.sharepoint.createitem"

// SharePointJobTypeIndex is the interned index SharePointJobType is guaranteed to
// occupy in every compiled process: NewBuilder reserves it thirteenth (after the
// twelve job types above), so it is always 12. This lets a single in-process
// SharePoint worker subscribe by one global index across every deployed process, the
// same way the mail worker uses MailJobTypeIndex (ADR-0141).
const SharePointJobTypeIndex int32 = 12

// RemedyJobType is the reserved job type a BMC Remedy connector task carries. The
// in-process Remedy connector worker subscribes to it to create an entry (e.g. an
// incident) in a Remedy form through the BMC AR System REST API off the hot path
// (ADR-0106), the same way the mail worker subscribes to MailJobType. The provider
// host and credentials live in a server-registered connector, like clio/mail; only
// the form name and its field values are model-authored.
const RemedyJobType = "io.atlas.remedy.entry"

// RemedyJobTypeIndex is the interned index RemedyJobType is guaranteed to occupy in
// every compiled process: NewBuilder reserves it fourteenth (after the thirteen job
// types above), so it is always 13. This lets a single in-process Remedy worker
// subscribe by one global index across every deployed process, the same way the mail
// worker uses MailJobTypeIndex (ADR-0079/0106).
const RemedyJobTypeIndex int32 = 13

// WebScrapeJobType is the reserved job type a web-scraping connector task carries.
// The in-process web-scraping worker subscribes to it to fetch a model-authored URL
// and extract the elements matching a CSS selector off the hot path (ADR-0118), the
// same way the REST worker subscribes to RestJobType. The URL and selector live in
// the model (like REST's endpoint); nothing about the target is registry-held.
const WebScrapeJobType = "io.atlas.webscrape"

// WebScrapeJobTypeIndex is the interned index WebScrapeJobType is guaranteed to
// occupy in every compiled process: NewBuilder reserves it fifteenth (after the
// fourteen job types above), so it is always 14. This lets a single in-process
// web-scraping worker subscribe by one global index across every deployed process,
// the same way the mail worker uses MailJobTypeIndex (ADR-0118).
const WebScrapeJobTypeIndex int32 = 14

// UserConnectorJobType is the reserved job type a user-provisioning connector task
// carries (ADR-0123). The in-process user-provisioning worker subscribes to it to
// create, set the password of, or disable an Atlas login through the internal user
// store off the hot path, the same way the mail worker subscribes to MailJobType.
// It is gated to the protected system project and opt-in server-side; nothing about
// the credential is model-authored (there is none — it mutates the local store).
const UserConnectorJobType = "io.atlas.user.provision"

// UserConnectorJobTypeIndex is the interned index UserConnectorJobType is guaranteed
// to occupy in every compiled process: NewBuilder reserves it sixteenth (after the
// fifteen job types above), so it is always 15. This lets a single in-process
// user-provisioning worker subscribe by one global index across every deployed
// process, the same way the mail worker uses MailJobTypeIndex (ADR-0123).
const UserConnectorJobTypeIndex int32 = 15

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

	nodes              []CompiledNode
	flows              []CompiledFlow
	serviceTasks       []ServiceTaskDetail
	scriptTasks        []ScriptTaskDetail
	callActivities     []CallActivityDetail
	multiInstances     []MultiInstanceDetail
	scriptJobTasks     []ScriptJobTaskDetail
	businessRuleTasks  []BusinessRuleTaskDetail
	timerCatches       []TimerCatchDetail
	connectorTasks     []ConnectorTaskDetail
	mockupTasks        []MockupTaskDetail
	userTasks          []UserTaskDetail
	boundaryEventDets  []BoundaryEventDetail
	eventSubProcesses  []EventSubProcessDetail
	messageCatches     []MessageDetail
	receiveTasks       []MessageDetail // receive tasks (ADR-0102)
	messageThrows      []MessageDetail
	messageStarts      []MessageDetail
	signalCatches      []SignalDetail
	signalThrows       []SignalDetail // shared by signal throw and signal end events
	signalStarts       []SignalDetail
	errorEnds          []ErrorEndDetail     // error end events (ADR-0089)
	escalations        []EscalationDetail   // shared by escalation throw and end events (ADR-0125)
	conditionals       []ConditionalDetail  // conditional intermediate catch events (ADR-0137)
	adHocs             []AdHocDetail        // ad-hoc subprocess containers (ADR-0138)
	compensationThrows []CompensationDetail // shared by compensation throw and end events (ADR-0103)
	timerStarts        []TimerStartDetail
	dataObjects        []CompiledDataObject
	dataOutAssocs      []pendingDataOut // data-output associations, grouped by node in Build
	dataInAssocs       []pendingDataIn  // data-input associations, grouped by node in Build
	ioInputs           []pendingIO      // zeebe:ioMapping inputs, grouped by node in Build
	ioOutputs          []pendingIO      // zeebe:ioMapping outputs, grouped by node in Build
	elementIds         []int32          // interned source BPMN id per node, -1 if unset
	lanes              []LaneDetail     // organizational lanes (ADR-0121)
	startFormId        int32            // interned start-form id (ADR-0028), -1 if the process has none
	versionTag         int32            // interned atlas:versionTag revision label, -1 if none
	instanceTtlNanos   int64            // per-definition instance TTL in nanoseconds, 0 = off (ADR-0085)
	isExecutable       bool             // bpmn:isExecutable; defaults true (set in NewBuilder)

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
	b.intern(SharePointJobType)    // reserve SharePointJobTypeIndex == 12
	b.intern(RemedyJobType)        // reserve RemedyJobTypeIndex == 13
	b.intern(WebScrapeJobType)     // reserve WebScrapeJobTypeIndex == 14
	b.intern(UserConnectorJobType) // reserve UserConnectorJobTypeIndex == 15
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
		Lane:          -1, // in no lane unless SetLane assigns one (ADR-0121)
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

// SetStandardLoop marks an already-added node a BPMN standard loop (ADR-0133): it
// repeats its activity one iteration at a time while condition holds (nil = repeat
// until the cap), checked before the first iteration when testBefore is set, and at
// most loopMaximum times (0 = uncapped). It shares the multi-instance loop table and
// the node's MultiInstance index because it shares the runtime — a standard loop is a
// sequential loop whose iteration set is a condition rather than a collection — so a
// node carries at most one of the two markers (the parser refuses both).
func (b *Builder) SetStandardLoop(nodeID int32, testBefore bool, loopMaximum int32, condition *expr.Compiled) {
	if !b.validNode(nodeID) {
		return
	}
	idx := int32(len(b.multiInstances))
	b.multiInstances = append(b.multiInstances, MultiInstanceDetail{
		InputElement:     -1,
		OutputCollection: -1,
		Sequential:       true, // a standard loop is one iteration at a time, by definition
		Standard:         true,
		TestBefore:       testBefore,
		LoopCondition:    condition,
		LoopMaximum:      loopMaximum,
	})
	b.nodes[nodeID].MultiInstance = idx
}

// AddSubProcess adds an embedded subprocess container node and returns its element
// id. It carries no detail; its inner flow lives in the flat node/flow arrays,
// linked back to it only by the children's FlowScope. Create it first, then
// PushScope(its id) before adding its children so they land in its scope (ADR-0074).
func (b *Builder) AddSubProcess() int32 { return b.addNode(TypeSubProcess, -1) }

// AddAdHocSubProcess adds an ad-hoc subprocess container node and returns its element id
// (ADR-0138). Like an embedded subprocess it is a scope whose inner flow lives in the flat
// node/flow arrays — create it first, then PushScope(its id) before adding its children — but
// its contained activities are not sequenced from a start event: on entry the runtime activates
// every entry activity (a contained node with no incoming flow) at once. d carries the optional
// FEEL completion condition, the cancel-remaining flag, and the ordering.
func (b *Builder) AddAdHocSubProcess(d AdHocDetail) int32 {
	detail := int32(len(b.adHocs))
	b.adHocs = append(b.adHocs, d)
	return b.addNode(TypeAdHocSubProcess, detail)
}

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

// SetInstanceTtl records the process's instance TTL in nanoseconds — the self-cleaning
// expiry bound (ADR-0085). Zero (the default) means no TTL: instances never expire on
// their own. The parser passes an already-validated positive duration.
func (b *Builder) SetInstanceTtl(nanos int64) { b.instanceTtlNanos = nanos }

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

// AddSendTask adds a send task with the given job type and retries and returns its
// element id (ADR-0112). A send task is a service task under a different BPMN label: it
// creates a job and waits, so it reuses the service-task detail table and (at runtime)
// serviceTaskBehavior. Only its node type (TypeSendTask) differs, to preserve the
// send-task identity — the TypeConnectorTask "distinct type, shared behavior" pattern.
func (b *Builder) AddSendTask(jobType string, retries int32) int32 {
	detail := int32(len(b.serviceTasks))
	b.serviceTasks = append(b.serviceTasks, ServiceTaskDetail{
		JobType: b.intern(jobType),
		Retries: retries,
	})
	return b.addNode(TypeSendTask, detail)
}

// AddScriptTask adds a script task that evaluates the given compiled FEEL
// expression and writes the result to resultVar. Returns its element id.
func (b *Builder) AddScriptTask(e *expr.Compiled, resultVar string) int32 {
	detail := int32(len(b.scriptTasks))
	b.scriptTasks = append(b.scriptTasks, ScriptTaskDetail{Expr: e, ResultVar: resultVar})
	return b.addNode(TypeScriptTask, detail)
}

// MockupConfig is the authored configuration of a mockup (engine-simulated)
// service task (ADR-0120). MinNanos/MaxNanos bound the random simulated duration
// (MaxNanos >= MinNanos, both >= 0). Expr, when non-nil, is the compiled FEEL
// result expression written to ResultVar on activation (the input→output script).
// FailPerMillion is the failure probability in parts-per-million (0..1_000_000).
// FailMessage is the incident message used when a simulated failure occurs.
type MockupConfig struct {
	MinNanos       int64
	MaxNanos       int64
	ResultVar      string
	Expr           *expr.Compiled
	FailPerMillion int32
	FailMessage    string
	ErrorCode      string
}

// AddMockupTask adds a mockup service task the engine simulates itself (ADR-0120)
// and returns its element id. Unlike a service task it creates no job: at runtime
// mockupTaskBehavior writes the optional FEEL result, arms a one-shot timer for a
// random duration, and completes (or raises an incident per the fail probability).
// The result variable and fail message are stored as raw strings (like
// ScriptTaskDetail.ResultVar); the FEEL expression is compiled by the caller at
// deploy time (invariant I5), as AddScriptTask takes a pre-compiled expression.
func (b *Builder) AddMockupTask(cfg MockupConfig) int32 {
	detail := int32(len(b.mockupTasks))
	b.mockupTasks = append(b.mockupTasks, MockupTaskDetail{
		MinNanos:       cfg.MinNanos,
		MaxNanos:       cfg.MaxNanos,
		ResultVar:      cfg.ResultVar,
		Expr:           cfg.Expr,
		FailPerMillion: cfg.FailPerMillion,
		FailMessage:    cfg.FailMessage,
		ErrorCode:      cfg.ErrorCode,
	})
	return b.addNode(TypeMockupTask, detail)
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

// UserConnectorConfig is the deploy-time configuration of a user-provisioning
// connector task (ADR-0123). Operation is one of "create", "set-password", or
// "disable". Username identifies the account; Email/DisplayName/Roles/Password are
// the create/update fields — each a literal-or-FEEL value (the parser compiles the
// FEEL ones) evaluated over the instance's variables at call time. There is no
// connector name and no credential: the worker mutates the internal user store
// directly, gated to the protected system project (ADR-0122) and opt-in server-side.
type UserConnectorConfig struct {
	Operation   string
	Username    RestExpr
	Email       RestExpr
	DisplayName RestExpr
	Roles       RestExpr
	Password    RestExpr
	Retries     int32
}

// AddUserConnectorTask adds a user-provisioning connector task and returns its
// element id. Like a service task it creates a job on activation and waits; the job
// carries the reserved UserConnectorJobType so the in-process user-provisioning
// worker picks it up, evaluates any FEEL field over the instance's variables,
// performs the operation against the internal user store, and completes the job
// (ADR-0123). No provider or credential is involved.
func (b *Builder) AddUserConnectorTask(cfg UserConnectorConfig) int32 {
	detail := int32(len(b.connectorTasks))
	b.connectorTasks = append(b.connectorTasks, ConnectorTaskDetail{
		JobType:         b.intern(UserConnectorJobType),
		Connector:       -1, // no server-registered provider; it mutates the local store
		Subject:         -1,
		EventType:       -1,
		ClioQuery:       -1,
		ReduceSpec:      -1,
		Method:          -1,
		ResultVar:       -1,
		Auth:            -1,
		UserOp:          b.intern(cfg.Operation),
		UserName:        cfg.Username,
		UserEmail:       cfg.Email,
		UserDisplayName: cfg.DisplayName,
		UserRoles:       cfg.Roles,
		UserPassword:    cfg.Password,
		Retries:         cfg.Retries,
	})
	return b.addNode(TypeConnectorTask, detail)
}

// CsvConfig is the deploy-time configuration of a CSV-to-JSON connector task
// (ADR-0139). Source names the process variable holding the raw CSV text
// (empty → the worker's default "csvText"); Result the variable the parsed rows
// are written to (empty → "rows"); Delimiter the field delimiter (empty → ",");
// HasHeader whether the first row is a header; Columns the field names (empty →
// derive them from the header row). All are interned deploy-time data (I5).
type CsvConfig struct {
	Source    string
	Result    string
	Delimiter string
	HasHeader bool
	Columns   []string
	Retries   int32
}

// AddCsvConnectorTask adds a CSV-to-JSON connector task and returns its element
// id. Like a service task it creates a job on activation and waits; the job carries
// the reserved CsvImportJobType so the in-process CSV worker picks it up, reads the
// raw text from the named source variable, parses it against the authored
// delimiter/header/columns with the same parser the ingestion endpoint uses, and
// writes the JSON rows (and a rowCount) into the result variable (ADR-0139). The
// layout lives in the model — unlike the ADR-0087 convention, which read it from a
// columnConfig variable — so nothing but the file arrives at runtime.
func (b *Builder) AddCsvConnectorTask(cfg CsvConfig) int32 {
	detail := int32(len(b.connectorTasks))
	cols := make([]int32, 0, len(cfg.Columns))
	for _, c := range cfg.Columns {
		cols = append(cols, b.intern(c))
	}
	b.connectorTasks = append(b.connectorTasks, ConnectorTaskDetail{
		JobType:      b.intern(CsvImportJobType),
		Connector:    -1, // CSV carries its layout in the model, not a registry name
		Subject:      -1,
		EventType:    -1,
		ClioQuery:    -1,
		ReduceSpec:   -1,
		Method:       -1,
		ResultVar:    -1, // CSV uses its own CsvResult field, not the REST/clio one
		Auth:         -1,
		CsvSource:    b.intern(cfg.Source),
		CsvResult:    b.intern(cfg.Result),
		CsvDelimiter: b.intern(cfg.Delimiter),
		CsvHasHeader: cfg.HasHeader,
		CsvColumns:   cols,
		Retries:      cfg.Retries,
	})
	return b.addNode(TypeConnectorTask, detail)
}

// SharePointConfig is the deploy-time configuration of a SharePoint connector task
// (ADR-0141). Connector names the server-registered SharePoint provider (its Graph
// base and OAuth credential live server-side, never in the model); Site and List
// address the target list, and Fields are the created item's column values — all
// literal-or-FEEL values (the parser compiles the FEEL ones) evaluated over the
// instance's variables at call time. ResultVar, if set, is the process variable the
// created item's JSON is written back into (empty = discard it).
type SharePointConfig struct {
	Connector string
	Site      RestExpr
	List      RestExpr
	Fields    []RestKV
	ResultVar string
	Retries   int32
}

// AddSharePointConnectorTask adds a SharePoint connector task and returns its element
// id. Like a service task it creates a job on activation and waits; the job carries
// the reserved SharePointJobType so the in-process SharePoint worker picks it up,
// evaluates any FEEL site/list/field values over the instance's variables, resolves
// the named connector's Graph client, creates the list item, writes the created
// item's JSON into ResultVar, and completes the job (ADR-0141). The Graph base and
// credentials are resolved server-side from the named connector, never authored in
// the model — mirroring the mail connector (ADR-0079).
func (b *Builder) AddSharePointConnectorTask(cfg SharePointConfig) int32 {
	detail := int32(len(b.connectorTasks))
	b.connectorTasks = append(b.connectorTasks, ConnectorTaskDetail{
		JobType:    b.intern(SharePointJobType),
		Connector:  b.intern(cfg.Connector),
		Subject:    -1, // not a clio task
		EventType:  -1,
		ClioQuery:  -1,
		ReduceSpec: -1,
		Method:     -1, // not a REST task
		ResultVar:  b.intern(cfg.ResultVar),
		Auth:       -1,
		Site:       cfg.Site,
		List:       cfg.List,
		Fields:     cfg.Fields,
		Retries:    cfg.Retries,
	})
	return b.addNode(TypeConnectorTask, detail)
}

// RemedyConfig is the deploy-time configuration of a BMC Remedy connector task
// (ADR-0106). Connector names the server-registered Remedy instance (its base URL
// and credentials live server-side, never in the model). Form is the Remedy form
// the entry is created in (e.g. "HPD:IncidentInterface_Create"); Fields carries the
// entry's field values as name/literal-or-FEEL pairs evaluated over the instance's
// variables at call time (the fx toggle, ADR-0067). ResultVar, if set, is the
// process variable the created entry's id is written back into.
type RemedyConfig struct {
	Connector string
	Form      RestExpr
	Fields    []RestKV
	ResultVar string
	Retries   int32
}

// AddRemedyConnectorTask adds a BMC Remedy connector task and returns its element
// id. Like a service task it creates a job on activation and waits; the job carries
// the reserved RemedyJobType so the in-process Remedy worker picks it up, evaluates
// any FEEL form/field values over the instance's variables, resolves the named
// connector's AR System REST client, creates the entry, writes the new entry id into
// ResultVar (empty = discard it), and completes the job (ADR-0106). The Remedy base
// URL and credentials are resolved server-side from the named connector, never
// authored in the model — mirroring clio and mail (ADR-0036/0079).
func (b *Builder) AddRemedyConnectorTask(cfg RemedyConfig) int32 {
	detail := int32(len(b.connectorTasks))
	b.connectorTasks = append(b.connectorTasks, ConnectorTaskDetail{
		JobType:      b.intern(RemedyJobType),
		Connector:    b.intern(cfg.Connector),
		Subject:      -1, // not a clio task
		EventType:    -1,
		ClioQuery:    -1,
		ReduceSpec:   -1,
		Method:       -1, // not a REST task
		ResultVar:    b.intern(cfg.ResultVar),
		Auth:         -1,
		RemedyForm:   cfg.Form,
		RemedyFields: cfg.Fields,
		Retries:      cfg.Retries,
	})
	return b.addNode(TypeConnectorTask, detail)
}

// WebScrapeConfig is the deploy-time configuration of a web-scraping connector task
// (ADR-0118). Url is the page to fetch and Selector the CSS selector whose matches
// are extracted — both literal-or-FEEL values (the parser compiles the FEEL ones)
// evaluated over the instance's variables at call time. Attribute, when set, names
// the HTML attribute to read from each match (empty → each match's text content);
// Result is the process variable the extracted values are written to as a JSON
// array. Like REST, the target lives entirely in the model, not a registry.
type WebScrapeConfig struct {
	Url       RestExpr
	Selector  RestExpr
	Attribute string
	Result    string
	Retries   int32
}

// AddWebScrapeConnectorTask adds a web-scraping connector task and returns its
// element id. Like a service task it creates a job on activation and waits; the job
// carries the reserved WebScrapeJobType so the in-process web-scraping worker picks
// it up, evaluates any FEEL url/selector values over the instance's variables,
// fetches the page, extracts the text (or the named attribute) of every element
// matching the selector, writes the values into Result as a JSON array, and
// completes the job (ADR-0118). The URL and selector live in the model, mirroring
// the REST connector (ADR-0067); nothing about the target is registry-held.
func (b *Builder) AddWebScrapeConnectorTask(cfg WebScrapeConfig) int32 {
	detail := int32(len(b.connectorTasks))
	b.connectorTasks = append(b.connectorTasks, ConnectorTaskDetail{
		JobType:         b.intern(WebScrapeJobType),
		Connector:       -1, // scrape carries its URL in the model, not a registry name
		Subject:         -1, // not a clio task
		EventType:       -1,
		ClioQuery:       -1,
		ReduceSpec:      -1,
		Method:          -1, // not a REST task
		ResultVar:       b.intern(cfg.Result),
		Auth:            -1,
		Url:             cfg.Url,
		ScrapeSelector:  cfg.Selector,
		ScrapeAttribute: b.intern(cfg.Attribute),
		Retries:         cfg.Retries,
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

// AddBoundarySignalEvent adds a signal boundary event attached to host that fires when a
// signal named signalName is broadcast (ADR-0088). interrupting mirrors BPMN cancelActivity.
// Returns its element id.
func (b *Builder) AddBoundarySignalEvent(host int32, interrupting bool, signalName string) int32 {
	detail := int32(len(b.boundaryEventDets))
	b.boundaryEventDets = append(b.boundaryEventDets, BoundaryEventDetail{
		HostNode:     host,
		Interrupting: interrupting,
		Kind:         BoundarySignal,
		SignalName:   signalName,
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

// AddEventBasedGateway adds an event-based gateway (deferred choice) and returns its
// element id. It carries no detail: at runtime it arms every target catch event (each
// outgoing flow must lead to a message/timer/signal intermediate catch) and takes the
// branch whose event fires first, cancelling the rest (ADR-0110).
func (b *Builder) AddEventBasedGateway() int32 { return b.addNode(TypeEventBasedGateway, -1) }

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

// AddReceiveTask adds a receive task that, on activation, subscribes to the named message
// with a correlation key produced by the given compiled FEEL expression, then waits until a
// matching message is correlated — the message-catch semantics as an activity (ADR-0102).
// Returns its element id.
func (b *Builder) AddReceiveTask(messageName string, correlationKey *expr.Compiled) int32 {
	detail := int32(len(b.receiveTasks))
	b.receiveTasks = append(b.receiveTasks, MessageDetail{MessageName: messageName, CorrelationKey: correlationKey})
	return b.addNode(TypeReceiveTask, detail)
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

// AddSignalCatchEvent adds an intermediate signal catch event that waits for a broadcast
// signal of the given name (ADR-0088). Returns its element id.
func (b *Builder) AddSignalCatchEvent(signalName string) int32 {
	detail := int32(len(b.signalCatches))
	b.signalCatches = append(b.signalCatches, SignalDetail{SignalName: signalName})
	return b.addNode(TypeSignalCatchEvent, detail)
}

// AddSignalThrowEvent adds an intermediate signal throw event that, on activation,
// broadcasts the named signal to every waiting catch, then completes (ADR-0088).
func (b *Builder) AddSignalThrowEvent(signalName string) int32 {
	detail := int32(len(b.signalThrows))
	b.signalThrows = append(b.signalThrows, SignalDetail{SignalName: signalName})
	return b.addNode(TypeSignalThrowEvent, detail)
}

// AddSignalEndEvent adds an end event that broadcasts the named signal, then ends the
// instance — the send-and-stop counterpart of a signal throw, reusing the throw detail
// table like a message end event (ADR-0088).
func (b *Builder) AddSignalEndEvent(signalName string) int32 {
	detail := int32(len(b.signalThrows))
	b.signalThrows = append(b.signalThrows, SignalDetail{SignalName: signalName})
	return b.addNode(TypeSignalEndEvent, detail)
}

// AddSignalStartEvent adds a start event that a broadcast signal instantiates (ADR-0088);
// at runtime it flows straight on like a message start.
func (b *Builder) AddSignalStartEvent(signalName string) int32 {
	detail := int32(len(b.signalStarts))
	b.signalStarts = append(b.signalStarts, SignalDetail{SignalName: signalName})
	return b.addNode(TypeSignalStartEvent, detail)
}

// AddErrorEndEvent adds an end event that throws the given error code — ending its scope
// abnormally and propagating up to the nearest matching handler rather than completing
// normally (ADR-0089). A code-less error end throws "". Returns its element id.
func (b *Builder) AddErrorEndEvent(errorCode string) int32 {
	detail := int32(len(b.errorEnds))
	b.errorEnds = append(b.errorEnds, ErrorEndDetail{ErrorCode: errorCode})
	return b.addNode(TypeErrorEndEvent, detail)
}

// AddBoundaryErrorEvent adds an error boundary event attached to host that catches an
// error propagating up to the host whose code matches errorCode ("" is a catch-all). An
// error boundary is always interrupting (ADR-0089): it opens no subscription and waits
// only to be found by propagation. Returns its element id.
func (b *Builder) AddBoundaryErrorEvent(host int32, errorCode string) int32 {
	detail := int32(len(b.boundaryEventDets))
	b.boundaryEventDets = append(b.boundaryEventDets, BoundaryEventDetail{
		HostNode:     host,
		Interrupting: true, // an error boundary is always interrupting
		Kind:         BoundaryError,
		ErrorCode:    errorCode,
	})
	return b.addNode(TypeBoundaryEvent, detail)
}

// AddEscalationThrowEvent adds an intermediate throw event that raises the given escalation
// code — propagating up to the nearest matching handler — then continues on its outgoing
// flow (ADR-0125). A code-less escalation raises "". Returns its element id.
func (b *Builder) AddEscalationThrowEvent(escalationCode string) int32 {
	detail := int32(len(b.escalations))
	b.escalations = append(b.escalations, EscalationDetail{EscalationCode: escalationCode})
	return b.addNode(TypeEscalationThrowEvent, detail)
}

// AddEscalationEndEvent adds an end event that raises the given escalation code —
// propagating up to the nearest matching handler — then ends its path (ADR-0125). Unlike an
// error end, an uncaught escalation is benign (no incident) and a matching catch may be
// non-interrupting. A code-less escalation raises "". Returns its element id.
func (b *Builder) AddEscalationEndEvent(escalationCode string) int32 {
	detail := int32(len(b.escalations))
	b.escalations = append(b.escalations, EscalationDetail{EscalationCode: escalationCode})
	return b.addNode(TypeEscalationEndEvent, detail)
}

// AddLinkThrowEvent adds a link intermediate throw event — a goto (ADR-0133). It carries no
// detail: the link name matters only at compile, where connectScope resolves the pair to a
// synthetic sequence flow to the matching link catch. At runtime it is a pass-through, taking
// that synthetic flow. Returns its element id.
func (b *Builder) AddLinkThrowEvent() int32 { return b.addNode(TypeLinkThrowEvent, -1) }

// AddLinkCatchEvent adds a link intermediate catch event — the landing point of a link throw
// of the same name (ADR-0133). Like the throw it carries no detail and runs as a pass-through,
// flowing on its real outgoing sequence flow when the synthetic link edge activates it.
// Returns its element id.
func (b *Builder) AddLinkCatchEvent() int32 { return b.addNode(TypeLinkCatchEvent, -1) }

// AddBoundaryEscalationEvent adds an escalation boundary event attached to host that catches
// an escalation propagating up to the host whose code matches escalationCode ("" is a
// catch-all). Unlike an error boundary it honors interrupting: an interrupting escalation
// boundary tears the host down on fire, a non-interrupting one runs the handler alongside
// the still-running host (ADR-0125). It opens no subscription and waits only to be found by
// propagation. Returns its element id.
func (b *Builder) AddBoundaryEscalationEvent(host int32, escalationCode string, interrupting bool) int32 {
	detail := int32(len(b.boundaryEventDets))
	b.boundaryEventDets = append(b.boundaryEventDets, BoundaryEventDetail{
		HostNode:       host,
		Interrupting:   interrupting,
		Kind:           BoundaryEscalation,
		EscalationCode: escalationCode,
	})
	return b.addNode(TypeBoundaryEvent, detail)
}

// AddConditionalCatchEvent adds a conditional intermediate catch event that waits until the
// given boolean FEEL condition over the process's variables becomes true, then flows on
// (ADR-0137). It arms inert (opens no subscription) and is driven to Completing by a
// variable-change re-check. Returns its element id.
func (b *Builder) AddConditionalCatchEvent(condition *expr.Compiled) int32 {
	detail := int32(len(b.conditionals))
	b.conditionals = append(b.conditionals, ConditionalDetail{Condition: condition})
	return b.addNode(TypeConditionalCatchEvent, detail)
}

// AddBoundaryConditionalEvent adds a conditional boundary event attached to host that fires
// while the host runs when the given boolean FEEL condition becomes true (ADR-0137). It honors
// interrupting: an interrupting conditional boundary tears the host down on fire, a
// non-interrupting one runs the handler alongside the still-running host. It opens no
// subscription and is re-evaluated on variable change. Returns its element id.
func (b *Builder) AddBoundaryConditionalEvent(host int32, condition *expr.Compiled, interrupting bool) int32 {
	detail := int32(len(b.boundaryEventDets))
	b.boundaryEventDets = append(b.boundaryEventDets, BoundaryEventDetail{
		HostNode:     host,
		Interrupting: interrupting,
		Kind:         BoundaryConditional,
		Condition:    condition,
	})
	return b.addNode(TypeBoundaryEvent, detail)
}

// AddCompensationThrowEvent adds an intermediate throw event that, on activation, triggers
// compensation — running the handlers of completed compensable activities in its scope (or
// of the single activity later set via SetCompensationActivityRef) — then flows on (ADR-0103).
// ActivityRef defaults to -1 (compensate the whole scope). Returns its element id.
func (b *Builder) AddCompensationThrowEvent() int32 {
	detail := int32(len(b.compensationThrows))
	b.compensationThrows = append(b.compensationThrows, CompensationDetail{ActivityRef: -1})
	return b.addNode(TypeCompensationThrowEvent, detail)
}

// AddCompensationEndEvent adds an end event that triggers compensation, then ends its scope
// — the trigger-and-stop counterpart of a compensation throw, reusing the throw detail table
// like a signal end event (ADR-0103). Returns its element id.
func (b *Builder) AddCompensationEndEvent() int32 {
	detail := int32(len(b.compensationThrows))
	b.compensationThrows = append(b.compensationThrows, CompensationDetail{ActivityRef: -1})
	return b.addNode(TypeCompensationEndEvent, detail)
}

// AddCancelEndEvent adds a cancel end event: an end event inside a transaction that cancels
// it — compensating the transaction's completed activities in reverse order, then routing out
// the transaction's cancel boundary (ADR-0108). It carries no detail (a cancel always
// compensates the whole transaction). Returns its element id.
func (b *Builder) AddCancelEndEvent() int32 { return b.addNode(TypeCancelEndEvent, -1) }

// AddTerminateEndEvent adds a terminate end event: reaching it ends the enclosing flow scope at
// once — every other live token in the scope is terminated, then the scope completes (ADR-0116).
// It carries no detail (a terminate has no code, message, or handler). Returns its element id.
func (b *Builder) AddTerminateEndEvent() int32 { return b.addNode(TypeTerminateEndEvent, -1) }

// AddBoundaryCancelEvent adds a cancel boundary event attached to host (a transaction): it
// catches the transaction's cancellation and routes its recovery flow. Armed inert like an
// error boundary and always interrupting (ADR-0108). Returns its element id.
func (b *Builder) AddBoundaryCancelEvent(host int32) int32 {
	detail := int32(len(b.boundaryEventDets))
	b.boundaryEventDets = append(b.boundaryEventDets, BoundaryEventDetail{
		HostNode:     host,
		Interrupting: true, // a cancel boundary is always interrupting
		Kind:         BoundaryCancel,
	})
	return b.addNode(TypeBoundaryEvent, detail)
}

// SetTransaction marks an already-added subprocess node as a <transaction> (ADR-0108), so the
// runtime and validation know it may host a cancel boundary and hold a cancel end event. A
// no-op for an out-of-range node.
func (b *Builder) SetTransaction(nodeID int32) {
	if b.validNode(nodeID) {
		b.nodes[nodeID].Transaction = true
	}
}

// AddLane adds an organizational lane and returns its index (ADR-0121). parent is the index of
// the enclosing lane in a nested laneSet, or -1 for a top-level lane. A lane is pure metadata — it
// affects no token flow.
func (b *Builder) AddLane(name string, parent int32) int32 {
	idx := int32(len(b.lanes))
	b.lanes = append(b.lanes, LaneDetail{Name: b.intern(name), Parent: parent})
	return idx
}

// SetLane records that a flow node belongs to a lane (ADR-0121). A no-op for an unknown node.
func (b *Builder) SetLane(nodeID, laneIdx int32) {
	if b.validNode(nodeID) {
		b.nodes[nodeID].Lane = laneIdx
	}
}

// AddBoundaryCompensationEvent adds a compensation boundary event attached to host: an inert
// marker (never armed as an element instance) that makes the host compensable and links it to
// a compensation handler, resolved later from a BPMN <association> via SetCompensationHandler
// (ADR-0103). CompensationHandler starts unresolved (-1). Returns its element id.
func (b *Builder) AddBoundaryCompensationEvent(host int32) int32 {
	detail := int32(len(b.boundaryEventDets))
	b.boundaryEventDets = append(b.boundaryEventDets, BoundaryEventDetail{
		HostNode:            host,
		Interrupting:        false, // a compensation boundary never interrupts; it is inert
		Kind:                BoundaryCompensation,
		CompensationHandler: -1,
	})
	return b.addNode(TypeBoundaryEvent, detail)
}

// SetCompensationHandler resolves a compensation boundary event's handler link: it points the
// boundary node (a BoundaryCompensation) at the handler activity's element id (ADR-0103). The
// boundary must be a compensation boundary; other kinds are left untouched.
func (b *Builder) SetCompensationHandler(boundaryNodeID, handlerNodeID int32) {
	if !b.validNode(boundaryNodeID) {
		return
	}
	n := &b.nodes[boundaryNodeID]
	if n.Type != TypeBoundaryEvent {
		return
	}
	d := &b.boundaryEventDets[n.Detail]
	if d.Kind != BoundaryCompensation {
		return
	}
	d.CompensationHandler = handlerNodeID
}

// SetCompensationActivityRef narrows a compensation throw/end event to compensate a single
// activity (by element id) rather than the whole scope (ADR-0103). The node must be a
// compensation throw or end event.
func (b *Builder) SetCompensationActivityRef(throwNodeID, activityRef int32) {
	if !b.validNode(throwNodeID) {
		return
	}
	n := &b.nodes[throwNodeID]
	if n.Type != TypeCompensationThrowEvent && n.Type != TypeCompensationEndEvent {
		return
	}
	b.compensationThrows[n.Detail].ActivityRef = activityRef
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

	// Count incoming flows per node, so a parallel join knows how many tokens to
	// wait for — and so the ad-hoc grouping below can tell an entry activity (nothing
	// sequences into it) from one a contained flow reaches (ADR-0138).
	for _, f := range b.flows {
		b.nodes[f.Target].IncomingCount++
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
		switch n.Type {
		case TypeSubProcess:
			for j := range b.nodes {
				s := &b.nodes[j]
				if isStartEvent(s.Type) && s.FlowScope == n.ElementId {
					scopeStarts = append(scopeStarts, s.ElementId)
				}
			}
		case TypeAdHocSubProcess:
			// An ad-hoc subprocess has no start event: its scope's entry points are its
			// *entry activities* — the contained flow nodes nothing sequences into, which
			// the runtime activates on entry (ADR-0138). A node targeted by a flow inside
			// the ad-hoc is reached by that flow instead, a boundary event arms on its host,
			// and an event-subprocess handler is armed as a trigger — none are entries.
			for j := range b.nodes {
				s := &b.nodes[j]
				if s.FlowScope == n.ElementId && s.IncomingCount == 0 &&
					s.Type != TypeBoundaryEvent && s.EventSub < 0 {
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
		if n.Type == TypeSubProcess || n.Type == TypeAdHocSubProcess {
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

	// Does this process contain any conditional event? The runtime only schedules a
	// variable-change re-check for instances of a process that has one (ADR-0137), so a
	// process without conditionals pays nothing on a variable write.
	hasConditional := false
	for i := range b.nodes {
		n := &b.nodes[i]
		if n.Type == TypeConditionalCatchEvent ||
			(n.Type == TypeBoundaryEvent && b.boundaryEventDets[n.Detail].Kind == BoundaryConditional) ||
			(n.EventSub >= 0 && b.eventSubProcesses[n.EventSub].Kind == BoundaryConditional) {
			hasConditional = true
			break
		}
	}

	return &CompiledProcess{
		Key:                b.key,
		BpmnProcessId:      b.intern(b.bpmnProcessId),
		Version:            b.version,
		hasConditional:     hasConditional,
		nodes:              b.nodes,
		flows:              b.flows,
		outgoingFlows:      outgoing,
		boundaryEvents:     boundary,
		scopeStarts:        scopeStarts,
		serviceTasks:       b.serviceTasks,
		scriptTasks:        b.scriptTasks,
		callActivities:     b.callActivities,
		multiInstances:     b.multiInstances,
		scriptJobTasks:     b.scriptJobTasks,
		businessRuleTasks:  b.businessRuleTasks,
		timerCatches:       b.timerCatches,
		connectorTasks:     b.connectorTasks,
		mockupTasks:        b.mockupTasks,
		userTasks:          b.userTasks,
		boundaryEventDets:  b.boundaryEventDets,
		eventSubProcesses:  b.eventSubProcesses,
		eventSubs:          eventSubs,
		rootEventSubs:      rootEventSubs,
		messageCatches:     b.messageCatches,
		receiveTasks:       b.receiveTasks,
		messageThrows:      b.messageThrows,
		messageStarts:      b.messageStarts,
		signalCatches:      b.signalCatches,
		signalThrows:       b.signalThrows,
		signalStarts:       b.signalStarts,
		errorEnds:          b.errorEnds,
		escalations:        b.escalations,
		conditionals:       b.conditionals,
		adHocs:             b.adHocs,
		compensationThrows: b.compensationThrows,
		timerStarts:        b.timerStarts,
		dataObjects:        b.dataObjects,
		dataOutAssocs:      dataOut,
		dataInAssocs:       dataIn,
		ioInputs:           ioIn,
		ioOutputs:          ioOut,
		startEvents:        startEvents,
		elementIds:         b.elementIds,
		lanes:              b.lanes,
		startFormId:        b.startFormId,
		versionTag:         b.versionTag,
		instanceTtlNanos:   b.instanceTtlNanos,
		isExecutable:       b.isExecutable,
		strings:            b.strings,
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
	return t == TypeStartEvent || t == TypeMessageStartEvent || t == TypeTimerStartEvent || t == TypeSignalStartEvent
}
