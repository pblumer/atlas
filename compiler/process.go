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

import (
	"fmt"

	"github.com/pblumer/atlas/expr"
)

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
	// TypeEventSubProcessStart is a runtime-only element type: the armed trigger of an
	// event subprocess (ADR-0082). No compiled node carries it (the handler compiles as
	// TypeSubProcess); the engine arms one waiting instance per event subprocess in a
	// scope, and its firing activates the handler. It is excluded from the scope's
	// active-child counter so it never blocks scope completion.
	TypeEventSubProcessStart

	TypeSignalCatchEvent // an intermediate catch event that waits for a broadcast signal by name (ADR-0088)
	TypeSignalThrowEvent // an intermediate throw event that broadcasts a signal by name to every waiting catch (ADR-0088)
	TypeSignalEndEvent   // an end event that broadcasts a signal, then ends the instance (ADR-0088); reuses the throw detail table
	TypeSignalStartEvent // a start event that a broadcast signal instantiates (ADR-0088); at runtime it flows straight on like a message start

	TypeErrorEndEvent // an end event that throws an error, ending its scope abnormally and propagating up to the nearest matching handler (ADR-0089); the send-and-stop counterpart of a BPMN error throw

	TypeReceiveTask // an activity that waits for a correlating message, then continues (ADR-0102); the message intermediate catch's semantics in task form, so it accepts boundary events, I/O mappings, and multi-instance

	TypeCompensationThrowEvent // an intermediate throw event that triggers compensation — runs the handlers of completed compensable activities in its scope, or of one named activity (ADR-0103)
	TypeCompensationEndEvent   // an end event that triggers compensation, then ends its scope (ADR-0103); the trigger-and-stop counterpart of a compensation throw, reusing the throw detail table

	TypeCancelEndEvent // an end event inside a transaction that cancels it: compensates the transaction's completed activities in reverse order, then routes out the transaction's cancel boundary (ADR-0108)

	TypeEventBasedGateway // a deferred choice: arms every target catch event (message/timer/signal) at once and takes the branch whose event fires first, cancelling the rest (ADR-0110)

	TypeSendTask // a send task: a job-creating activity identical in execution to a service task (ADR-0112) — it creates a job and waits, reusing ServiceTaskDetail and serviceTaskBehavior; a distinct type only to preserve the send-task identity, like TypeConnectorTask

	TypeTerminateEndEvent // an end event that ends its enclosing flow scope at once (ADR-0116): it terminates every other live token in the scope (cancelling their jobs), then completes the scope — at the root the instance ends, inside a subprocess that subprocess ends and the parent continues. cancelEndEventBehavior minus compensation and the cancel boundary

	TypeMockupTask // a service task simulated by the engine itself (ADR-0120): on activation it writes an optional FEEL result and arms a one-shot timer for a random duration, then completes (or, per a fail probability, raises an incident) — no external worker or connector. A distinct type because its execution (timer-wait, no job) differs from a service task, like TypeConnectorTask.

	TypeEscalationThrowEvent // an intermediate throw event that raises an escalation, propagating up to the nearest matching handler, then continues on its outgoing flow (ADR-0125); the continue-after-throw counterpart of TypeMessageThrowEvent
	TypeEscalationEndEvent   // an end event that raises an escalation, propagating up to the nearest matching handler, then ends its path (ADR-0125); unlike an error end the catch may be non-interrupting and an uncaught escalation is benign (no incident)

	TypeLinkThrowEvent // a link intermediate throw event: a goto to the link catch of the same name in the same scope (ADR-0133). Resolved at compile to a synthetic sequence flow to the catch; runs as a pass-through (no execution semantics of its own)
	TypeLinkCatchEvent // a link intermediate catch event: the landing point of a link throw of the same name (ADR-0133). Reached only via the compile-time synthetic flow; runs as a pass-through, flowing on its real outgoing flow

	TypeConditionalCatchEvent // a conditional intermediate catch event: waits until its boolean FEEL condition over the process's variables becomes true, then flows on (ADR-0137). Arms inert (no subscription) and is driven to Completing by a variable-change re-check; a conditional boundary/event-sub reuses TypeBoundaryEvent/TypeEventSubProcessStart with BoundaryConditional

	TypeAdHocSubProcess // an ad-hoc subprocess: a container scope whose contained activities run on demand, in any order, zero or more times — not driven by sequence flow from a start event (ADR-0138). On entry it activates every entry activity (a contained node with no incoming flow) at once; after each contained activity completes an optional boolean FEEL completion condition is re-evaluated, and the first time it holds the remaining work is cancelled and the ad-hoc completes (else it completes on scope-drain)

	// numBpmnTypes bounds behavior dispatch tables. Grow as element types land.
	numBpmnTypes = 42
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
	case TypeEventBasedGateway:
		return "EventBasedGateway"
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
	case TypeAdHocSubProcess:
		return "AdHocSubProcess"
	case TypeCallActivity:
		return "CallActivity"
	case TypeEventSubProcessStart:
		return "EventSubProcessStart"
	case TypeSignalCatchEvent:
		return "SignalCatchEvent"
	case TypeSignalThrowEvent:
		return "SignalThrowEvent"
	case TypeSignalEndEvent:
		return "SignalEndEvent"
	case TypeSignalStartEvent:
		return "SignalStartEvent"
	case TypeErrorEndEvent:
		return "ErrorEndEvent"
	case TypeReceiveTask:
		return "ReceiveTask"
	case TypeSendTask:
		return "SendTask"
	case TypeCompensationThrowEvent:
		return "CompensationThrowEvent"
	case TypeCompensationEndEvent:
		return "CompensationEndEvent"
	case TypeCancelEndEvent:
		return "CancelEndEvent"
	case TypeTerminateEndEvent:
		return "TerminateEndEvent"
	case TypeMockupTask:
		return "MockupTask"
	case TypeEscalationThrowEvent:
		return "EscalationThrowEvent"
	case TypeEscalationEndEvent:
		return "EscalationEndEvent"
	case TypeLinkThrowEvent:
		return "LinkThrowEvent"
	case TypeLinkCatchEvent:
		return "LinkCatchEvent"
	case TypeConditionalCatchEvent:
		return "ConditionalCatchEvent"
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
	EventSub        int32 // index into eventSubProcesses, -1 if this subprocess is not event-triggered (ADR-0082)
	EventSubStart   int32 // offset into eventSubs (the event-subprocess handler nodes nested directly in this scope)
	EventSubCount   int32 // number of event subprocesses in this scope (0 for a node that hosts none)
	Transaction     bool  // this subprocess is a <transaction>: it may hold a cancel end event and host a cancel boundary (ADR-0108)
	Lane            int32 // index into lanes, -1 if this node is in no lane; organizational metadata with no execution effect (ADR-0121)
}

// LaneDetail is one BPMN lane: an organizational partition of the process's flow nodes with no
// execution semantics (ADR-0121). Name is the interned lane label; Parent is the index of the
// enclosing lane in a nested laneSet (-1 for a top-level lane), so a node's full lane path can be
// walked leaf-to-root for display.
type LaneDetail struct {
	Name   int32 // interned lane name → index, -1 if unnamed
	Parent int32 // index into lanes of the enclosing lane, -1 for a top-level lane
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

// SafeLoopCeiling is how many runs a standard loop that states no loopMaximum gets
// before the engine stops it and raises an incident (ADR-0133, amended). It is a
// safety net for the one construct that can spin on its own — a FEEL loop condition
// that never turns false — not a semantic limit: a loop that states its own
// loopMaximum is bounded by that number alone, however large, because the author said
// it out loud and the deploy validated it. The engine enforces the ceiling; the
// compiler names it in the loop.unbounded warning, so both read the same number.
const SafeLoopCeiling = 1000

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
//
// Standard marks the other BPMN loop marker, <standardLoopCharacteristics> — the
// loop (circular arrow) icon (ADR-0133). It shares this struct because it shares the
// runtime: a standard loop is a sequential loop whose iteration set is not a
// collection but a condition, so it has no InputCollection, Cardinality,
// InputElement, or OutputCollection, and is driven instead by LoopCondition (nil
// means "repeat until LoopMaximum"), TestBefore (check the condition before the first
// iteration — a while loop; else a repeat-until that always runs at least once), and
// LoopMaximum (a hard iteration cap; 0 means uncapped). At least one of LoopCondition
// and LoopMaximum is set — the deploy is refused otherwise, since a loop with neither
// has no way to end.
type MultiInstanceDetail struct {
	InputCollection     *expr.Compiled // FEEL list to iterate; nil when Cardinality is used
	Cardinality         *expr.Compiled // FEEL count; nil when InputCollection is used
	InputElement        int32          // interned per-iteration variable name, -1 if none
	OutputCollection    int32          // interned result-list variable name, -1 if none
	OutputElement       *expr.Compiled // FEEL per-iteration contribution, nil if none
	CompletionCondition *expr.Compiled // FEEL early-exit, nil if none
	Sequential          bool           // one iteration at a time (else parallel)
	Standard            bool           // a <standardLoopCharacteristics> loop (ADR-0133)
	TestBefore          bool           // standard loop: check the condition before iteration 1
	LoopCondition       *expr.Compiled // standard loop: FEEL repeat-while, nil if none
	LoopMaximum         int32          // standard loop: iteration cap, 0 = uncapped
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

// String renders a binding as the lower-case token used on the wire and in the
// Modeler (`bindingType`): "latest" or "deployment". Any unknown value is
// reported verbatim so a drift is visible rather than silently mapped to latest.
func (b DecisionBinding) String() string {
	switch b {
	case BindingLatest:
		return "latest"
	case BindingDeployment:
		return "deployment"
	default:
		return fmt.Sprintf("DecisionBinding(%d)", int32(b))
	}
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
//   - SharePoint (JobType == SharePointJobType): Connector names the
//     server-registered SharePoint provider; Site and List address the target list
//     and Fields are the created item's column values (all literal-or-FEEL); the
//     created item's JSON is written into ResultVar when set (ADR-0141).
//   - BMC Remedy (JobType == RemedyJobType): Connector names the server-registered
//     Remedy instance; RemedyForm and RemedyFields are the form and the entry's field
//     values (literal-or-FEEL) an incident/entry is created with through the AR System
//     REST API; ResultVar, if set, receives the created entry's id (ADR-0106).
//   - web scrape (JobType == WebScrapeJobType): Url is the model-authored page to
//     fetch (literal-or-FEEL, like REST); ScrapeSelector is the CSS selector whose
//     matches are extracted; ScrapeAttribute names the HTML attribute to read from
//     each match (-1 → each match's text content); ResultVar receives the extracted
//     values as a JSON array (ADR-0118).
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
	// Mail connector fields (JobType == MailJobType, ADR-0079). Connector (above)
	// names the server-registered mail provider; the message is authored in the
	// model as literal-or-FEEL values evaluated over the instance's variables at
	// send time. To and Bcc/Cc are comma-separated recipient lists; From overrides
	// the provider's default sender; MailSubject and Body are the message, and
	// BodyHTML is its optional HTML half (sent as multipart/alternative beside Body,
	// or alone as text/html). Each is the zero RestExpr for a non-mail task. Cc/Bcc/
	// From/BodyHTML are also zero when a mail task omits them.
	To          RestExpr
	Cc          RestExpr
	Bcc         RestExpr
	From        RestExpr
	MailSubject RestExpr
	Body        RestExpr
	BodyHTML    RestExpr
	// CSV connector fields (JobType == CsvImportJobType, ADR-0139). CsvSource is the
	// interned name of the process variable holding the raw CSV text (-1 → the
	// default "csvText"); CsvResult the variable the parsed rows are written to
	// (-1 → "rows"); CsvDelimiter the field delimiter (-1 → ","); CsvHasHeader
	// whether the first row is a header; CsvColumns the interned field names (empty →
	// derive them from the header row). Each is the zero value for a non-CSV task and
	// is read only by the in-process CSV worker, which the runner dispatches by the
	// CSV job type alone.
	CsvSource    int32
	CsvResult    int32
	CsvDelimiter int32
	CsvHasHeader bool
	CsvColumns   []int32
	// SharePoint connector fields (JobType == SharePointJobType, ADR-0141). Connector
	// (above) names the server-registered SharePoint provider (its Graph base and
	// OAuth credential live server-side). Site and List address the target list (a
	// site host/path or id, and a list name or id); Fields are the created item's
	// column values. Each is a literal-or-FEEL value evaluated over the instance's
	// variables at call time; Site/List are the zero RestExpr and Fields is nil for a
	// non-SharePoint task. ResultVar (above), if set, receives the created item's JSON.
	Site   RestExpr
	List   RestExpr
	Fields []RestKV
	// Remedy connector fields (JobType == RemedyJobType, ADR-0106). Connector (above)
	// names the server-registered BMC Remedy instance; ResultVar (above), if set,
	// receives the created entry's id. RemedyForm is the Remedy form the entry is
	// created in (literal-or-FEEL, the zero RestExpr for a non-remedy task);
	// RemedyFields are the entry's field values as name/literal-or-FEEL pairs, evaluated
	// over the instance's variables at call time (nil for a non-remedy task).
	RemedyForm   RestExpr
	RemedyFields []RestKV
	// Web-scrape connector fields (JobType == WebScrapeJobType, ADR-0118). Url (above)
	// is the model-authored page to fetch; ScrapeSelector is the CSS selector whose
	// matches are extracted (literal-or-FEEL, the zero RestExpr for a non-scrape task);
	// ScrapeAttribute is the interned HTML attribute read from each match (-1 → each
	// match's text content). ResultVar (above) receives the extracted values as a JSON
	// array. Read only by the in-process web-scraping worker.
	ScrapeSelector  RestExpr
	ScrapeAttribute int32
	// User-provisioning connector fields (JobType == UserConnectorJobType, ADR-0123).
	// UserOp is the interned operation ("create" | "set-password" | "disable").
	// UserName identifies the account; UserEmail/UserDisplayName/UserRoles/UserPassword
	// are the create/update values — each a literal-or-FEEL value evaluated over the
	// instance's variables at call time. Each is the zero value for a non-user task and
	// is read only by the in-process user-provisioning worker, which the runner
	// dispatches by the user job type alone. There is no Connector and no credential:
	// the worker mutates the internal user store directly, gated to the system project.
	UserOp          int32
	UserName        RestExpr
	UserEmail       RestExpr
	UserDisplayName RestExpr
	UserRoles       RestExpr
	UserPassword    RestExpr
	// SCIM connector fields (JobType == ScimJobType, ADR-0153). ScimBaseURL is the
	// service provider's SCIM v2 base endpoint and ScimResource the resource-type path
	// segment ("Users"/"Groups") — each a literal-or-FEEL value evaluated over the
	// instance's variables at call time. ScimOp is the interned operation
	// ("create"|"get"|"replace"|"patch"|"delete"|"search"), which the worker maps to an
	// HTTP method. ScimResourceID addresses a single resource (get/replace/patch/
	// delete); ScimFilter is the SCIM filter for a search. ScimBody is the interned name
	// of the process variable holding the create/replace/patch payload (interned "" →
	// the whole variable scope, mirroring REST). Each is the zero value for a non-SCIM
	// task; ResultVar (above) receives the JSON response and Auth (above) the
	// bearer/basic/apiKey credential reference. Read only by the in-process SCIM worker.
	ScimBaseURL    RestExpr
	ScimResource   RestExpr
	ScimOp         int32
	ScimResourceID RestExpr
	ScimFilter     RestExpr
	ScimBody       int32
	// LDAP connector fields (JobType == LdapJobType, ADR-0154). LdapURL is the server
	// (ldap://host:389 or ldaps://host:636) and LdapBindDN the bind identity — each a
	// literal-or-FEEL value evaluated over the instance's variables at call time.
	// LdapBindSecret is the interned name of the server-side secret holding the bind
	// password (interned "" → an anonymous bind); LdapStartTLS upgrades a plain
	// connection with STARTTLS. LdapOp is the interned operation
	// ("search"|"add"|"modify"|"delete"|"modify-password"). LdapDN is the target entry
	// (add/modify/delete/modify-password); LdapBaseDN/LdapFilter/LdapScope (interned
	// "base"|"one"|"sub") address a search. LdapEntryVar is the interned name of the
	// process variable holding the add/modify attribute object; LdapNewPassword is the
	// modify-password value. Each is the zero value for a non-LDAP task; ResultVar
	// (above) receives a search's entries as a JSON array. Read only by the in-process
	// LDAP worker.
	LdapURL         RestExpr
	LdapBindDN      RestExpr
	LdapBindSecret  int32
	LdapStartTLS    bool
	LdapOp          int32
	LdapDN          RestExpr
	LdapBaseDN      RestExpr
	LdapFilter      RestExpr
	LdapScope       int32
	LdapEntryVar    int32
	LdapNewPassword RestExpr
}

// MockupTaskDetail is the per-mockup-task data the engine reads to simulate a
// service task itself (ADR-0120), instead of dispatching a job to an external
// worker or connector. On activation the behavior arms a one-shot timer for a
// random duration in [MinNanos, MaxNanos] and, if Expr is set, evaluates it over
// the instance's variables and writes the result into ResultVar (the input→output
// "script", e.g. a simulated REST response). When the timer fires the task
// completes — unless the fail draw selects failure, in which case a job-less
// incident is raised with FailMessage.
//
// The random duration and the fail decision are derived deterministically from
// the frozen timer key at command time (never re-drawn on replay), so no new
// nondeterministic source enters the engine (invariant I6). FailPerMillion is the
// failure probability scaled to parts-per-million (0 = never fail, 1_000_000 =
// always) so the whole decision stays integer-pure across live and replay.
type MockupTaskDetail struct {
	MinNanos       int64          // minimum simulated duration in nanoseconds
	MaxNanos       int64          // maximum simulated duration in nanoseconds (>= MinNanos)
	ResultVar      string         // result-variable name, "" if none (a raw string, like ScriptTaskDetail.ResultVar)
	Expr           *expr.Compiled // FEEL result expression compiled at deploy time (I5), nil if none
	FailPerMillion int32          // failure probability in parts-per-million, 0..1_000_000
	FailMessage    string         // incident message on a simulated failure, "" for a default
	// ErrorCode, when non-empty, makes a simulated failure throw a BPMN error with this
	// code (caught by a matching error boundary/event subprocess, ADR-0089) instead of
	// raising an incident — so business error paths, not just technical ones, are
	// exercisable. Empty keeps the incident behavior.
	ErrorCode string
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
	// no duplicate (ADR-0094). Only meaningful on a message start event; ignored on
	// catch/throw/end. Default false keeps ADR-0035's start-per-message behavior.
	SingletonStart bool
}

// SignalDetail is the per-signal-event data a behavior needs at runtime, shared by the
// signal intermediate catch, throw, end, and start events (ADR-0088). A signal is
// broadcast by name: it carries no correlation key and no code, so the name is all a
// catch subscribes on and a throw broadcasts.
type SignalDetail struct {
	SignalName string
}

// EventSubProcessDetail is the per-event-subprocess data the runtime needs to arm its
// trigger (ADR-0082). An event subprocess (`<subProcess triggeredByEvent="true">`) is
// not entered by a sequence flow; instead its start event's event definition is armed
// while the parent scope runs. Interrupting (from the start event's isInterrupting,
// default true) decides whether firing terminates the parent scope's other work before
// the handler runs. Kind reuses BoundaryEventKind: the timer field applies for a timer
// trigger, the message fields for a message trigger. StartNode is the handler's inner
// start event, seeded (like any message/timer start, flowing straight on) when the
// handler is activated on a trigger.
type EventSubProcessDetail struct {
	StartNode      int32 // the handler's inner start event node id
	Interrupting   bool  // true = terminate the parent scope's other work on trigger (isInterrupting)
	Kind           BoundaryEventKind
	Schedule       TimerSchedule  // BoundaryTimer: when the trigger fires
	MessageName    string         // BoundaryMessage: the message it subscribes to
	CorrelationKey *expr.Compiled // BoundaryMessage: correlation-key expression (ADR-0020)
	SignalName     string         // BoundarySignal: the signal it subscribes to (ADR-0088)
	ErrorCode      string         // BoundaryError: the error code it catches; "" is a catch-all (ADR-0089)
	EscalationCode string         // BoundaryEscalation: the escalation code it catches; "" is a catch-all (ADR-0125)
	Condition      *expr.Compiled // BoundaryConditional: the boolean FEEL condition it fires on (ADR-0137)
}

// BoundaryEventKind discriminates what a boundary event waits on.
type BoundaryEventKind uint8

const (
	BoundaryTimer        BoundaryEventKind = iota // waits a fixed duration, then fires
	BoundaryMessage                               // waits for a correlating message, then fires
	BoundarySignal                                // waits for a broadcast signal by name, then fires (ADR-0088)
	BoundaryError                                 // catches an error propagating up to it by code, then fires; always interrupting (ADR-0089)
	BoundaryCompensation                          // links a host activity to its compensation handler; inert — never armed as an element instance, only read on host completion to record the activity as compensable (ADR-0103)
	BoundaryCancel                                // on a transaction only: catches the transaction's cancellation and routes its recovery flow; armed inert like an error boundary, and always interrupting (ADR-0108)
	BoundaryEscalation                            // catches an escalation propagating up to it by code, then fires; honors cancelActivity — may be interrupting or non-interrupting (ADR-0125)
	BoundaryConditional                           // fires while the host runs when its boolean FEEL condition becomes true; armed inert (no subscription), re-evaluated on variable change; honors cancelActivity — may be interrupting or non-interrupting (ADR-0137)
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
	SignalName     string         // BoundarySignal: the signal it subscribes to (ADR-0088)
	ErrorCode      string         // BoundaryError: the error code it catches; "" is a catch-all (ADR-0089)
	EscalationCode string         // BoundaryEscalation: the escalation code it catches; "" is a catch-all (ADR-0125)
	Condition      *expr.Compiled // BoundaryConditional: the boolean FEEL condition it fires on (ADR-0137)
	// CompensationHandler is the ElementId of the compensation handler activity this
	// boundary links its host to (BoundaryCompensation, ADR-0103). It is resolved at
	// compile time from the BPMN <association> joining the boundary to the handler;
	// -1 means unresolved (a compensation boundary with no association — a deploy error).
	CompensationHandler int32
}

// CompensationDetail is the per-compensation-throw data the runtime needs (ADR-0103),
// shared by the compensation throw and end events like the message/signal throw table.
// ActivityRef is the ElementId of the single activity to compensate, or -1 to compensate
// every completed compensable activity in the throw's scope (reverse completion order).
type CompensationDetail struct {
	ActivityRef int32
}

// ErrorEndDetail is the per-error-end-event data the runtime needs: the code it throws
// (ADR-0089). A code-less error end throws "", which a code-less catch-all catches. It is
// its own small table (an error end carries no name, correlation key, or schedule).
type ErrorEndDetail struct {
	ErrorCode string
}

// EscalationDetail is the per-escalation-event data the runtime needs: the code it raises
// (ADR-0125). Shared by the escalation throw and end events (like CompensationDetail is
// shared by the compensation throw and end), since both just carry the escalation code. A
// code-less escalation raises "", which a code-less catch-all catches.
type EscalationDetail struct {
	EscalationCode string
}

// ConditionalDetail is the per-conditional-catch-event data the runtime needs: the boolean
// FEEL condition it waits on (ADR-0137). A conditional intermediate catch arms inert and is
// driven to Completing when a variable-change re-check finds the condition true. (Conditional
// boundaries and event subprocesses carry their condition on BoundaryEventDetail /
// EventSubProcessDetail instead.)
type ConditionalDetail struct {
	Condition *expr.Compiled
}

// AdHocDetail is the per-ad-hoc-subprocess configuration the runtime needs (ADR-0138).
// CompletionCondition is an optional boolean FEEL expression re-evaluated after each contained
// activity completes; nil means the ad-hoc completes when its scope drains instead. When it
// holds, CancelRemaining (the BPMN cancelRemainingInstances default, true) decides whether the
// still-running contained activities are cancelled. Ordering is always the BPMN default,
// parallel — every entry activity is activated at once; a model asking for sequential ordering
// is refused at deploy until that driver lands, so no flag is carried for it.
type AdHocDetail struct {
	CompletionCondition *expr.Compiled
	CancelRemaining     bool
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

	// hasConditional is true if any node is a conditional event (ADR-0137); the runtime
	// only re-checks conditionals for instances of a process that has one.
	hasConditional bool

	nodes []CompiledNode
	flows []CompiledFlow

	outgoingFlows      []int32 // shared topology: flow ids grouped by source node
	boundaryEvents     []int32 // shared topology: boundary-event node ids grouped by host node
	scopeStarts        []int32 // shared topology: nested start-event node ids grouped by subprocess node
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
	eventSubs          []int32 // shared topology: event-subprocess handler node ids grouped by parent scope node
	rootEventSubs      []int32 // event-subprocess handler node ids whose parent scope is the process root
	messageCatches     []MessageDetail
	receiveTasks       []MessageDetail // receive tasks — the message-catch shape as an activity (ADR-0102)
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
	dataOutAssocs      []DataOutputAssociation // shared: output associations grouped by activity node
	dataInAssocs       []DataInputAssociation  // shared: input associations grouped by activity node
	ioInputs           []IOMapping             // shared: zeebe:ioMapping inputs grouped by activity node
	ioOutputs          []IOMapping             // shared: zeebe:ioMapping outputs grouped by activity node
	startEvents        []int32
	startFormId        int32        // interned start-form id (ADR-0028), -1 if none
	versionTag         int32        // interned atlas:versionTag revision label, -1 if none
	instanceTtlNanos   int64        // per-definition instance TTL in nanoseconds, 0 = off (ADR-0085)
	historyTtlNanos    int64        // per-definition history TTL in nanoseconds, 0 = off (ADR-0144)
	isExecutable       bool         // bpmn:isExecutable — a non-executable process can't be started
	elementIds         []int32      // interned source BPMN id per node id (-1 if unset)
	elementDocs        []int32      // interned <bpmn:documentation> per node id (-1 if undocumented, ADR-0025)
	documentation      int32        // interned <bpmn:documentation> of the process itself, -1 if none
	lanes              []LaneDetail // organizational lanes (ADR-0121); a node's CompiledNode.Lane indexes this
	strings            []string     // intern table (index → string), for debug/export
}

// Node returns the node with the given ElementId.
func (p *CompiledProcess) Node(id int32) *CompiledNode { return &p.nodes[id] }

// IsTransaction reports whether node id is a <transaction> subprocess — a subprocess
// that may hold a cancel end event and host a cancel boundary (ADR-0108).
func (p *CompiledProcess) IsTransaction(id int32) bool { return p.nodes[id].Transaction }

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

// IsEventSubProcess reports whether the subprocess node id is event-triggered — a
// `<subProcess triggeredByEvent="true">` armed by its start event's event definition
// rather than entered by a flow (ADR-0082).
func (p *CompiledProcess) IsEventSubProcess(id int32) bool { return p.nodes[id].EventSub >= 0 }

// EventSubProcess returns the event-subprocess detail at the given table index — the
// trigger the runtime arms while the parent scope runs (ADR-0082).
func (p *CompiledProcess) EventSubProcess(detail int32) *EventSubProcessDetail {
	return &p.eventSubProcesses[detail]
}

// EventSubprocesses returns the handler node ids of the event subprocesses nested
// directly in the subprocess scope id — the triggers the runtime arms when that
// subprocess is entered — as a slice into the shared topology array (no allocation).
// Empty for a scope that hosts none. Use RootEventSubprocesses for the process root.
func (p *CompiledProcess) EventSubprocesses(id int32) []int32 {
	n := &p.nodes[id]
	return p.eventSubs[n.EventSubStart : n.EventSubStart+n.EventSubCount]
}

// RootEventSubprocesses returns the handler node ids of the event subprocesses at the
// process root — the triggers armed when an instance is created (ADR-0082).
func (p *CompiledProcess) RootEventSubprocesses() []int32 { return p.rootEventSubs }

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

// Lane returns the lane at the given table index (ADR-0121).
func (p *CompiledProcess) Lane(idx int32) *LaneDetail { return &p.lanes[idx] }

// NodeLane returns the leaf lane index a node belongs to, or -1 if it is in no lane (ADR-0121).
func (p *CompiledProcess) NodeLane(nodeID int32) int32 { return p.nodes[nodeID].Lane }

// LanePath returns a node's lane names from the outermost lane to the leaf, for display (e.g.
// ["Finance", "Approver"] for a node in a nested lane). Empty when the node is in no lane (ADR-0121).
func (p *CompiledProcess) LanePath(nodeID int32) []string {
	idx := p.nodes[nodeID].Lane
	var leafToRoot []string
	for idx != -1 {
		leafToRoot = append(leafToRoot, p.Intern(p.lanes[idx].Name))
		idx = p.lanes[idx].Parent
	}
	// Reverse to outermost-first.
	for i, j := 0, len(leafToRoot)-1; i < j; i, j = i+1, j-1 {
		leafToRoot[i], leafToRoot[j] = leafToRoot[j], leafToRoot[i]
	}
	return leafToRoot
}

// SendTask returns the detail at the given table index (ADR-0112). A send task is a
// service task under a different label — it reuses ServiceTaskDetail and the same detail
// table, so this is ServiceTask by another name, kept for call-site clarity.
func (p *CompiledProcess) SendTask(detail int32) *ServiceTaskDetail {
	return &p.serviceTasks[detail]
}

// TimerCatch returns the timer-catch detail at the given table index.
func (p *CompiledProcess) TimerCatch(detail int32) *TimerCatchDetail {
	return &p.timerCatches[detail]
}

// MockupTask returns the mockup-task detail at the given table index (ADR-0120).
func (p *CompiledProcess) MockupTask(detail int32) *MockupTaskDetail {
	return &p.mockupTasks[detail]
}

// ReceiveTask returns the receive-task detail at the given table index (ADR-0102). A
// receive task carries the same MessageDetail as a message catch — the message name and the
// compiled correlation-key expression it waits on.
func (p *CompiledProcess) ReceiveTask(detail int32) *MessageDetail {
	return &p.receiveTasks[detail]
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
	SingletonStart bool // one live instance per correlation key (ADR-0094)
}

// MessageStartEvents returns each message-start event with its element index and
// compiled correlation-key expression. Computed by scanning the node table at
// deploy time (off the hot path); empty for a process with no message start event.
func (p *CompiledProcess) MessageStartEvents() []MessageStartEvent {
	var out []MessageStartEvent
	for id := range p.nodes {
		n := &p.nodes[id]
		// Only a root-scope message start instantiates the process; a message start
		// nested in an event subprocess is that scope's trigger, not an entry point (ADR-0082).
		if n.Type == TypeMessageStartEvent && n.FlowScope == -1 {
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

// SignalCatch returns the signal-catch detail at the given table index (ADR-0088).
func (p *CompiledProcess) SignalCatch(detail int32) *SignalDetail { return &p.signalCatches[detail] }

// SignalThrow returns the signal-throw detail at the given table index — shared by the
// signal throw and signal end events (ADR-0088).
func (p *CompiledProcess) SignalThrow(detail int32) *SignalDetail { return &p.signalThrows[detail] }

// ErrorEnd returns the error-end detail at the given table index (ADR-0089).
func (p *CompiledProcess) ErrorEnd(detail int32) *ErrorEndDetail { return &p.errorEnds[detail] }

// Escalation returns the escalation-event detail (throw or end) at the given table index (ADR-0125).
func (p *CompiledProcess) Escalation(detail int32) *EscalationDetail { return &p.escalations[detail] }

// Conditional returns the conditional-catch detail at the given table index (ADR-0137).
func (p *CompiledProcess) Conditional(detail int32) *ConditionalDetail {
	return &p.conditionals[detail]
}

// HasConditionalEvents reports whether the process contains any conditional event, so the
// runtime only re-checks conditionals for instances that can have one (ADR-0137).
func (p *CompiledProcess) HasConditionalEvents() bool { return p.hasConditional }

// AdHoc returns the ad-hoc subprocess detail at the given table index (ADR-0138).
func (p *CompiledProcess) AdHoc(detail int32) *AdHocDetail { return &p.adHocs[detail] }

// AdHocEntries returns the element ids of an ad-hoc subprocess's entry activities — the
// contained flow nodes with no incoming sequence flow, which the runtime activates when the
// ad-hoc is entered (ADR-0138). Like ScopeStartEvents it is a slice into the shared topology
// array (no allocation); empty for a non-ad-hoc node or an ad-hoc with no contained activity.
func (p *CompiledProcess) AdHocEntries(id int32) []int32 {
	n := &p.nodes[id]
	return p.scopeStarts[n.ScopeStartStart : n.ScopeStartStart+n.ScopeStartCount]
}

// CompensationThrow returns the compensation-throw detail at the given table index —
// shared by the compensation throw and end events (ADR-0103).
func (p *CompiledProcess) CompensationThrow(detail int32) *CompensationDetail {
	return &p.compensationThrows[detail]
}

// SignalStart returns the signal-start detail at the given table index (ADR-0088).
func (p *CompiledProcess) SignalStart(detail int32) *SignalDetail { return &p.signalStarts[detail] }

// SignalStartEvents returns each root-scope signal-start event's signal name and element
// index. The engine indexes these at deploy time so a broadcast signal can instantiate the
// process (ADR-0088), mirroring MessageStartEvents. A signal start nested in an event
// subprocess is that scope's trigger, not a process entry point.
func (p *CompiledProcess) SignalStartEvents() []SignalStartEvent {
	var out []SignalStartEvent
	for id := range p.nodes {
		n := &p.nodes[id]
		if n.Type == TypeSignalStartEvent && n.FlowScope == -1 {
			out = append(out, SignalStartEvent{SignalName: p.signalStarts[n.Detail].SignalName, ElementId: int32(id)})
		}
	}
	return out
}

// SignalStartEvent pairs a signal-start event's signal name with its element index, so the
// engine can index which element a starting signal flows into (ADR-0088).
type SignalStartEvent struct {
	SignalName string
	ElementId  int32
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
		// Only a root-scope timer start arms a process-instantiating timer; a timer start
		// nested in an event subprocess is that scope's trigger, not an entry point (ADR-0082).
		if n.Type == TypeTimerStartEvent && n.FlowScope == -1 {
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

// CallActivityRef is the static, read-only view of one call activity: the BPMN
// element that hosts it, the process id it calls, the version binding, whether
// variables propagate wholesale in/out, and whether it is a multi-instance loop
// (spawning one child per collection element). It carries no resolved def key —
// which deployed definition the call reaches is a per-server, deploy-time fact
// the server layer computes on top of this (ADR-0076).
type CallActivityRef struct {
	ElementId          string
	CalledProcessId    string
	Binding            DecisionBinding
	PropagateAllParent bool
	PropagateAllChild  bool
	MultiInstance      bool
	// Loop is true when the call activity carries a standard loop marker instead —
	// it calls the process again and again while a condition holds (ADR-0133), where
	// MultiInstance calls it once per collection element. At most one is ever true.
	Loop bool
}

// ConnectorRef is one model reference to a server-registered connector: the element
// carrying it, the reserved job type that says which *kind* of connector it needs, and
// the name it asks for. A model refers to a connector by name only and never carries
// an endpoint or a secret (ADR-0036/0041), so nothing inside the model can tell whether
// that name is configured anywhere — which is exactly why the references have to be
// enumerable from outside, where the connector store is (ADR-0158).
type ConnectorRef struct {
	ElementId string
	JobType   int32
	Connector string
}

// NodeConnectorRef returns the connector reference one node makes, and false when it
// makes none. Both shapes are covered: a connector task (mail, REST, SharePoint, …)
// and a business rule task delegating to a remote decision service, which names its
// connector the same way. An element that names no connector — a local decision, a
// REST task with its URL in the model, anything that is not one of those two task
// types — is not a reference.
//
// It answers the question one element at a time because that is how an *incident* asks
// it: a token is parked on this element, which connector is it stuck on (ADR-0160)?
// ConnectorRefs asks the same question of every node.
func (p *CompiledProcess) NodeConnectorRef(id int32) (ConnectorRef, bool) {
	if id < 0 || int(id) >= len(p.nodes) {
		return ConnectorRef{}, false
	}
	var jobType, connector int32 = -1, -1
	switch p.nodes[id].Type {
	case TypeConnectorTask:
		d := p.ConnectorTask(p.nodes[id].Detail)
		jobType, connector = d.JobType, d.Connector
	case TypeBusinessRuleTask:
		d := p.BusinessRuleTask(p.nodes[id].Detail)
		jobType, connector = d.JobType, d.Connector
	default:
		return ConnectorRef{}, false
	}
	if connector < 0 {
		return ConnectorRef{}, false
	}
	return ConnectorRef{
		ElementId: p.ElementBpmnId(id),
		JobType:   jobType,
		Connector: p.Intern(connector),
	}, true
}

// ConnectorRefs returns every connector reference the process makes, in node order.
// An element that names no connector is left out; see NodeConnectorRef.
func (p *CompiledProcess) ConnectorRefs() []ConnectorRef {
	var out []ConnectorRef
	for i := range p.nodes {
		if ref, ok := p.NodeConnectorRef(int32(i)); ok {
			out = append(out, ref)
		}
	}
	return out
}

// CallActivities returns every call activity in this process, in node order —
// empty if it has none. It mirrors BusinessRuleDecisions: a static enumeration of
// an outbound reference (here the called process id) that the server surfaces so
// operators can see and manage the call activities deployed on a server — which
// process calls which, and whether the target resolves (ADR-0076).
func (p *CompiledProcess) CallActivities() []CallActivityRef {
	var out []CallActivityRef
	for i := range p.nodes {
		if p.nodes[i].Type != TypeCallActivity {
			continue
		}
		d := p.CallActivity(p.nodes[i].Detail)
		out = append(out, CallActivityRef{
			ElementId:          p.ElementBpmnId(int32(i)),
			CalledProcessId:    p.Intern(d.CalledProcessId),
			Binding:            d.Binding,
			PropagateAllParent: d.PropagateAllParent,
			PropagateAllChild:  d.PropagateAllChild,
			MultiInstance:      p.nodes[i].MultiInstance >= 0 && !p.loops(int32(i)),
			Loop:               p.loops(int32(i)),
		})
	}
	return out
}

// loops reports whether a node's loop marker is a standard loop rather than a
// multi-instance one (ADR-0133) — the two share the loop table, so the flag on the
// detail is what tells them apart.
func (p *CompiledProcess) loops(node int32) bool {
	idx := p.nodes[node].MultiInstance
	return idx >= 0 && p.multiInstances[idx].Standard
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

// ConnectorTaskOf returns the connector-task detail for element node id, or an
// error if id is not a connector task in this compiled process. It is the
// bounds-checked accessor for the job-worker path: a persisted job can outlive
// the process definition that compiled its element as a connector task (e.g. a
// job created before a redeploy that recompiled the element into something else,
// or dropped its connector-task table), and resolving such a stale job must fail
// it into an incident (ADR-0061) rather than index out of range and panic the
// job-runner goroutine — an unrecovered panic there crashes the whole server. A
// worker that gets an error returns it, and FailJob retries then parks the token.
func (p *CompiledProcess) ConnectorTaskOf(id int32) (*ConnectorTaskDetail, error) {
	if id < 0 || int(id) >= len(p.nodes) {
		return nil, fmt.Errorf("element %d out of range (%d nodes)", id, len(p.nodes))
	}
	detail := p.nodes[id].Detail
	if detail < 0 || int(detail) >= len(p.connectorTasks) {
		return nil, fmt.Errorf("element %d is not a connector task (detail index %d, %d connector tasks)", id, detail, len(p.connectorTasks))
	}
	return &p.connectorTasks[detail], nil
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

// InstanceTtlNanos returns the process's instance TTL in nanoseconds, or 0 when no TTL
// is configured. A positive value is the self-cleaning expiry bound (ADR-0085): the
// engine schedules a durable expiry timer at CreatedAt+TTL when an instance activates.
func (p *CompiledProcess) InstanceTtlNanos() int64 { return p.instanceTtlNanos }

// HistoryTtlNanos returns the process's history TTL in nanoseconds, or 0 when none is
// configured. A positive value is this definition's own retention max age (ADR-0144):
// the retention sweep hard-deletes a finished instance of this definition once it is
// older than the TTL and its events are provably exported. Zero falls back to the
// server-wide max age.
func (p *CompiledProcess) HistoryTtlNanos() int64 { return p.historyTtlNanos }

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

// ElementDocumentation returns the prose an author wrote about a node — its
// <bpmn:documentation> (ADR-0025) — or "" when the node is undocumented or the index is
// out of range. It is design-time metadata the engine never reads: it changes no
// execution, and is carried so a surface that shows an element to a person can read it
// here rather than re-parsing the model. The Tasks app uses it as a user task's work
// instruction.
func (p *CompiledProcess) ElementDocumentation(id int32) string {
	if id < 0 || int(id) >= len(p.elementDocs) {
		return ""
	}
	return p.Intern(p.elementDocs[id])
}

// Documentation returns the process's own <bpmn:documentation> — the summary that
// describes the process as a whole — or "" if it has none.
func (p *CompiledProcess) Documentation() string { return p.Intern(p.documentation) }
