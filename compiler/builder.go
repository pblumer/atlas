package compiler

import (
	"encoding/json"
	"fmt"
	"slices"

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

// ScimJobType is the reserved job type a SCIM 2.0 connector task carries. Like the
// REST connector it authors its endpoint in the model — the SCIM base URL and
// resource type — and names a server-side secret for authentication (ADR-0041); the
// in-process SCIM connector worker subscribes to it to perform the resource
// operation off the hot path and write the response back, the same way the REST
// worker subscribes to RestJobType (ADR-0153).
const ScimJobType = "io.atlas.scim"

// ScimJobTypeIndex is the interned index ScimJobType is guaranteed to occupy in
// every compiled process: NewBuilder reserves it seventeenth (after the sixteen job
// types above), so it is always 16. This lets a single in-process SCIM worker
// subscribe by one global index across every deployed process, the same way the REST
// worker uses RestJobTypeIndex (ADR-0153).
const ScimJobTypeIndex int32 = 16

// LdapJobType is the reserved job type a generic LDAP connector task carries. Like
// the REST/SCIM connectors it authors its endpoint in the model — the LDAP server
// URL, bind DN, and target/base DN — and names a server-side secret for the bind
// password (ADR-0041); the in-process LDAP connector worker subscribes to it to
// perform the directory operation (search/add/modify/delete/modify-password) off the
// hot path (ADR-0154).
const LdapJobType = "io.atlas.ldap"

// LdapJobTypeIndex is the interned index LdapJobType is guaranteed to occupy in
// every compiled process: NewBuilder reserves it eighteenth (after the seventeen job
// types above), so it is always 17. This lets a single in-process LDAP worker
// subscribe by one global index across every deployed process, the same way the SCIM
// worker uses ScimJobTypeIndex (ADR-0154).
const LdapJobTypeIndex int32 = 17

// SoapJobType is the reserved job type a SOAP / Web Services (WSDL) connector task
// carries. Like the REST/SCIM connectors it authors its endpoint in the model — the
// web-service URL, the operation, and the request body — and names a server-side
// secret for any authentication credential (ADR-0041); the in-process SOAP connector
// worker subscribes to it to wrap the body in a SOAP envelope, invoke the operation,
// and parse the response off the hot path (ADR-0165).
const SoapJobType = "io.atlas.soap"

// SoapJobTypeIndex is the interned index SoapJobType is guaranteed to occupy in every
// compiled process: NewBuilder reserves it nineteenth (after the eighteen job types
// above), so it is always 18. This lets a single in-process SOAP worker subscribe by
// one global index across every deployed process, the same way the LDAP worker uses
// LdapJobTypeIndex (ADR-0165).
const SoapJobTypeIndex int32 = 18

// AdJobType is the reserved job type an Active Directory connector task carries. AD
// speaks LDAP, so it dials like the generic LDAP connector, but it adds the
// AD-specific provisioning primitives the generic connector cannot express: setting a
// password via unicodePwd over LDAPS, enabling/disabling an account via
// userAccountControl, and adding/removing a group member incrementally (ADR-0166).
const AdJobType = "io.atlas.ad"

// AdJobTypeIndex is the interned index AdJobType is guaranteed to occupy in every
// compiled process: NewBuilder reserves it twentieth (after the nineteen job types
// above), so it is always 19. This lets a single in-process AD worker subscribe by one
// global index across every deployed process, the same way the LDAP worker uses
// LdapJobTypeIndex (ADR-0166).
const AdJobTypeIndex int32 = 19

// The three SQL connector job types (ADR-0173). They are one connector in three
// faces: the same task shape, the same engine-side resolution and the same worker
// code, differing only in the driver behind them — and therefore in the placeholder
// syntax a statement must use. That difference is why the product is part of the
// *model* rather than of the worker's configuration: a statement written with $1 is
// a PostgreSQL statement, and a kind per product makes pointing it at SQL Server
// unrepresentable instead of a runtime error.
//
// None of the three has an in-process handler. SQL is worker-only (ADR-0164/0170):
// the type exists precisely so a worker — the one process holding the DSN — can
// lease it, and the engine never learns which database it is for.

// MsSqlJobType is the reserved job type a Microsoft SQL Server connector task
// carries. Statements use @p1-style placeholders and may bind by name.
const MsSqlJobType = "io.atlas.mssql"

// MsSqlJobTypeIndex is the interned index MsSqlJobType is guaranteed to occupy in
// every compiled process: NewBuilder reserves it twenty-first (after the twenty job
// types above), so it is always 20.
const MsSqlJobTypeIndex int32 = 20

// MariaDBJobType is the reserved job type a MariaDB (or MySQL) connector task
// carries. Statements use ?-style positional placeholders only.
const MariaDBJobType = "io.atlas.mariadb"

// MariaDBJobTypeIndex is the interned index MariaDBJobType is guaranteed to occupy in
// every compiled process: NewBuilder reserves it twenty-second, so it is always 21.
const MariaDBJobTypeIndex int32 = 21

// PostgresJobType is the reserved job type a PostgreSQL connector task carries.
// Statements use $1-style positional placeholders only.
const PostgresJobType = "io.atlas.postgres"

// PostgresJobTypeIndex is the interned index PostgresJobType is guaranteed to occupy
// in every compiled process: NewBuilder reserves it twenty-third, so it is always 22.
const PostgresJobTypeIndex int32 = 22

// EntraJobType is the reserved job type a Microsoft Entra ID connector task carries.
// Entra is Graph, so a process could in principle reach it with the REST connector;
// what this type marks is a task that names a *lifecycle operation* instead of a URL
// and a JSON fragment, the same argument the AD connector makes against generic LDAP
// (ADR-0166/0171).
//
// Like the SQL types above it, no in-process handler subscribes to it: the kind is
// worker-only, so the tenant's client secret never enters the engine.
const EntraJobType = "io.atlas.entra"

// EntraJobTypeIndex is the interned index EntraJobType is guaranteed to occupy in
// every compiled process: NewBuilder reserves it twenty-fourth, so it is always 23.
const EntraJobTypeIndex int32 = 23

// LdifJobType is the reserved job type a directory-file connector task carries: LDIF
// (RFC 2849) or DSML v1, read or written (ADR-0171).
//
// It has an in-process handler as well as a worker one, and that is not a lapse from
// ADR-0164: parsing a file is pure computation with no network and no credential, the
// same category as a FEEL script or a local DMN evaluation, which that record
// explicitly leaves in the engine. It is offloadable all the same.
const LdifJobType = "io.atlas.ldif"

// LdifJobTypeIndex is the interned index LdifJobType is guaranteed to occupy in every
// compiled process: NewBuilder reserves it twenty-fifth, so it is always 24.
const LdifJobTypeIndex int32 = 24

// JiraJobType is the reserved job type a Jira connector task carries
// (ADR-0201). One job type serves every Jira operation — create an
// issue, read one, update, transition, comment, assign, or search — because they
// share an instance, a credential and an error envelope; the operation is a modeled
// value rather than a reserved index of its own, as it is for the directory
// connectors (ADR-0153/0154/0166/0172).
const JiraJobType = "io.atlas.jira"

// JiraJobTypeIndex is the interned index JiraJobType is guaranteed to occupy in every
// compiled process: NewBuilder reserves it twenty-sixth, so it is always 25. Together
// with the name it lets a job carry its type as an integer and the in-process Jira
// worker subscribe by one global index across every deployed process, the same way
// the mail worker uses MailJobTypeIndex.
const JiraJobTypeIndex int32 = 25

// reservedJobTypes is the ordered list of job types Atlas reserves: every builder
// interns these first, so a reserved name occupies the same index in every compiled
// process, and the *engine-wide* job-type registry seeds itself from the same list
// so the two cannot disagree about what a low index means. Position is identity —
// each entry's index is baked into jobs already on disk, so a name may be appended
// but never reordered or removed.
var reservedJobTypes = []string{
	DMNJobType,           // 0
	UserTaskJobType,      // 1
	PwshJobType,          // 2
	TemisDecisionJobType, // 3
	RestJobType,          // 4
	PythonJobType,        // 5
	JsJobType,            // 6
	ClioWriteJobType,     // 7
	ClioQueryJobType,     // 8
	ClioReadJobType,      // 9
	MailJobType,          // 10
	CsvImportJobType,     // 11
	SharePointJobType,    // 12
	RemedyJobType,        // 13
	WebScrapeJobType,     // 14
	UserConnectorJobType, // 15
	ScimJobType,          // 16
	LdapJobType,          // 17
	SoapJobType,          // 18
	AdJobType,            // 19
	MsSqlJobType,         // 20
	MariaDBJobType,       // 21
	PostgresJobType,      // 22
	EntraJobType,         // 23
	LdifJobType,          // 24
	JiraJobType,          // 25
}

// ReservedJobTypes returns the reserved job-type names in index order, so index i
// of the result is the job type whose reserved index is i. It returns a copy: the
// slice is the definition of those indices, and a caller reordering it would
// re-point every job already written under them.
func ReservedJobTypes() []string { return slices.Clone(reservedJobTypes) }

// ReservedJobTypeCount is how many built-in job types exist in this build, and so
// the exclusive upper bound of the reserved index range. It grows whenever a
// connector is added, which is exactly why it is not the same number as
// [FirstDynamicJobTypeIndex].
func ReservedJobTypeCount() int32 { return int32(len(reservedJobTypes)) }

// dynamicJobTypeFloor is the lowest index a model-authored job type may be issued.
//
// It is a fixed number rather than one past the reserved range, and that is the
// whole point. Adding a built-in connector grows the reserved range; when the two
// were the same number, that growth walked over indices already issued to
// model-authored types, and the jobs parked under them kept an index that had come
// to mean something else. SOAP and Active Directory did exactly that to 18 and 19.
//
// With a fixed floor the reserved range can grow by hundreds of connectors and never
// reach an issued index. The gap is dead space in an int32, which costs nothing:
// indices are dense only in the sense that they are compared, never iterated.
//
// Note what this does *not* mean: an index below the floor is not thereby invalid.
// Stores written before the floor existed hold perfectly good assignments between
// [ReservedJobTypeCount] and here, and only an index inside the reserved range is a
// collision.
const dynamicJobTypeFloor int32 = 1000

// FirstDynamicJobTypeIndex is the lowest index available to a model-authored job
// type (a <zeebe:taskDefinition type>). The engine-wide registry assigns from here
// up; see [dynamicJobTypeFloor] for why it is not simply one past the reserved range.
func FirstDynamicJobTypeIndex() int32 { return dynamicJobTypeFloor }

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
	elementDocs        []int32          // interned <bpmn:documentation> per node, -1 if undocumented (ADR-0025)
	repairForms        []int32          // interned repair form id per node, -1 if none (ADR-0169)
	lanes              []LaneDetail     // organizational lanes (ADR-0121)
	documentation      int32            // interned <bpmn:documentation> of the process itself, -1 if none
	startFormId        int32            // interned start-form id (ADR-0028), -1 if the process has none
	versionTag         int32            // interned atlas:versionTag revision label, -1 if none
	instanceTtlNanos   int64            // per-definition instance TTL in nanoseconds, 0 = off (ADR-0085)
	historyTtlNanos    int64            // per-definition history TTL in nanoseconds, 0 = off (ADR-0144)
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
		documentation: -1,
		startFormId:   -1,
		versionTag:    -1,
		isExecutable:  true, // BPMN default; the parser sets false only for isExecutable="false"
		flowScope:     -1,   // nodes land at the process root until a scope is pushed
		interner:      map[string]int32{},
	}
	// Reserve the built-in job types first, in order, so each lands on the index its
	// constant documents in every compiled process (see reservedJobTypes).
	for _, name := range reservedJobTypes {
		b.intern(name)
	}
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
	b.elementIds = append(b.elementIds, -1)   // kept in lockstep with nodes
	b.elementDocs = append(b.elementDocs, -1) // likewise: -1 = undocumented
	b.repairForms = append(b.repairForms, -1) // likewise: -1 = no repair form
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

// SetElementDocumentation records a node's <bpmn:documentation> — the prose an author
// writes about the element in the Modeler (ADR-0025). It is design-time metadata: the
// processor never reads it, so it changes no execution; it is carried so a surface that
// shows an element to a person (the Tasks app, for a user task's work instruction) can
// read it from the compiled process instead of re-parsing the model (invariant I5).
// Empty text interns to -1, so an undocumented node costs nothing.
func (b *Builder) SetElementDocumentation(nodeID int32, text string) {
	if b.validNode(nodeID) {
		b.elementDocs[nodeID] = b.intern(text)
	}
}

// SetRepairForm records the form an operator should be shown when a token parks on this
// node with an incident (ADR-0169) — the modeler's answer to "if this task goes wrong,
// these are the values worth looking at". Design-time metadata exactly like the
// documentation above: the processor never reads it, so it changes no execution, and it
// is carried in the compiled process so the incident surface can read it without
// re-parsing the model (invariant I5) and so it moves with an instance that migrates
// (ADR-0162). An empty id interns to -1, so a node without one costs nothing.
func (b *Builder) SetRepairForm(nodeID int32, formID string) {
	if b.validNode(nodeID) {
		b.repairForms[nodeID] = b.intern(formID)
	}
}

// SetDocumentation records the process's own <bpmn:documentation> — the summary a reader
// wants before following the diagram. Design-time metadata, like the element-level
// documentation above.
func (b *Builder) SetDocumentation(text string) { b.documentation = b.intern(text) }

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

// SetHistoryTtl records the process's history TTL in nanoseconds — how long a *finished*
// instance of this definition is kept before retention hard-deletes it (ADR-0144). Zero
// (the default) means the definition has no opinion: the server-wide max age applies, if
// one is configured. The parser passes an already-validated positive duration.
func (b *Builder) SetHistoryTtl(nanos int64) { b.historyTtlNanos = nanos }

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
		JobType:       b.intern(jobType),
		Retries:       retries,
		globalJobType: jobTypeUnresolved,
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
		JobType:       b.intern(jobType),
		Retries:       retries,
		globalJobType: jobTypeUnresolved,
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
// "bearer", "apiKey", or "oauth2". Username (basic), ApiKeyName (the apiKey header
// name), ClientID/TokenURL/Scope (oauth2 client-credentials) are model data.
// SecretRef names a server-side secret (ADR-0041) — the basic password, bearer
// token, api-key value, or oauth2 client secret — resolved at runtime; the secret
// value itself is never authored in the model or stored here.
//
// For Type "oauth2" the worker performs a client-credentials grant (ADR-0152):
// TokenURL is the token endpoint, ClientID the client identifier, SecretRef the
// client secret reference, and Scope the optional space-delimited scopes; the
// fetched access token is attached as a Bearer credential and cached until it
// nears expiry.
type RestAuth struct {
	Type       string `json:"type,omitempty"`
	Username   string `json:"username,omitempty"`
	ApiKeyName string `json:"apiKeyName,omitempty"`
	SecretRef  string `json:"secretRef,omitempty"`
	TokenURL   string `json:"tokenUrl,omitempty"`
	ClientID   string `json:"clientId,omitempty"`
	Scope      string `json:"scope,omitempty"`
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

// ScimConfig is the deploy-time configuration of a SCIM 2.0 connector task
// (ADR-0153). BaseURL and Resource address the service provider and resource type
// ("Users"/"Groups"); Op is the operation ("create"|"get"|"replace"|"patch"|
// "delete"|"search"); ResourceID (get/replace/patch/delete) and Filter (search)
// carry literal-or-FEEL values (the parser compiles the FEEL ones); BodyVar names the
// process variable holding the create/replace/patch payload (empty → the whole
// variable scope); Auth references a server-side secret; ResultVar receives the JSON
// response (empty → discard it).
type ScimConfig struct {
	BaseURL    RestExpr
	Resource   RestExpr
	Op         string
	ResourceID RestExpr
	Filter     RestExpr
	BodyVar    string
	ResultVar  string
	Auth       RestAuth
	Retries    int32
}

// AddScimConnectorTask adds a SCIM 2.0 connector task and returns its element id.
// Like a service task it creates a job on activation and waits; the job carries the
// reserved ScimJobType so the in-process SCIM worker picks it up, evaluates any FEEL
// base-url/resource/id/filter values over the instance's variables, performs the
// resource operation against the provider, writes the JSON response into ResultVar
// (empty = discard), and completes the job (ADR-0153). The base URL and resource live
// in the model; credentials never do (Auth references a server-side secret,
// ADR-0041).
func (b *Builder) AddScimConnectorTask(cfg ScimConfig) int32 {
	detail := int32(len(b.connectorTasks))
	b.connectorTasks = append(b.connectorTasks, ConnectorTaskDetail{
		JobType:        b.intern(ScimJobType),
		Connector:      -1, // SCIM carries its endpoint in the model, not a registry name
		Subject:        -1, // not a clio task
		EventType:      -1,
		ClioQuery:      -1,
		ReduceSpec:     -1,
		Method:         -1, // the SCIM operation, not an HTTP method, is authored
		ResultVar:      b.intern(cfg.ResultVar),
		Auth:           b.internAuth(cfg.Auth),
		Retries:        cfg.Retries,
		ScimBaseURL:    cfg.BaseURL,
		ScimResource:   cfg.Resource,
		ScimOp:         b.intern(cfg.Op),
		ScimResourceID: cfg.ResourceID,
		ScimFilter:     cfg.Filter,
		ScimBody:       b.intern(cfg.BodyVar),
	})
	return b.addNode(TypeConnectorTask, detail)
}

// LdapConfig is the deploy-time configuration of a generic LDAP connector task
// (ADR-0154). URL is the server (ldap://host:389 or ldaps://host:636) and BindDN the
// bind identity — literal-or-FEEL values; BindSecret names the server-side secret for
// the bind password (empty → an anonymous bind); StartTLS upgrades a plain ldap://
// connection with STARTTLS. Op is the operation
// ("search"|"add"|"modify"|"delete"|"modify-password"). DN is the target entry
// (add/modify/delete/modify-password); BaseDN/Filter/Scope address a search; EntryVar
// names the process variable holding the add/modify attribute object; NewPassword is
// the modify-password value. ResultVar receives a search's entries as a JSON array.
type LdapConfig struct {
	URL         RestExpr
	BindDN      RestExpr
	BindSecret  string
	StartTLS    bool
	Op          string
	DN          RestExpr
	BaseDN      RestExpr
	Filter      RestExpr
	Scope       string
	EntryVar    string
	NewPassword RestExpr
	ResultVar   string
	// PageSize and MaxEntries are the effective search bounds; the compiler has
	// already applied the defaults, and 0 means unbounded. ClientCertSecret names the
	// secret holding a PEM certificate+key bundle for a client-certificate bind.
	PageSize         int32
	MaxEntries       int32
	ClientCertSecret string
	Retries          int32
}

// AddLdapConnectorTask adds a generic LDAP connector task and returns its element id.
// Like a service task it creates a job on activation and waits; the job carries the
// reserved LdapJobType so the in-process LDAP worker picks it up, evaluates any FEEL
// url/dn/filter values over the instance's variables, binds and performs the
// directory operation, writes a search's entries into ResultVar, and completes the
// job (ADR-0154). The server and DNs live in the model; the bind password never does
// (BindSecret references a server-side secret, ADR-0041).
func (b *Builder) AddLdapConnectorTask(cfg LdapConfig) int32 {
	detail := int32(len(b.connectorTasks))
	b.connectorTasks = append(b.connectorTasks, ConnectorTaskDetail{
		JobType:              b.intern(LdapJobType),
		Connector:            -1, // LDAP carries its endpoint in the model, not a registry name
		Subject:              -1, // not a clio task
		EventType:            -1,
		ClioQuery:            -1,
		ReduceSpec:           -1,
		Method:               -1, // the LDAP operation, not an HTTP method, is authored
		Auth:                 -1, // the bind password is a dedicated secret ref, not RestAuth
		ResultVar:            b.intern(cfg.ResultVar),
		Retries:              cfg.Retries,
		LdapURL:              cfg.URL,
		LdapBindDN:           cfg.BindDN,
		LdapBindSecret:       b.intern(cfg.BindSecret),
		LdapStartTLS:         cfg.StartTLS,
		LdapOp:               b.intern(cfg.Op),
		LdapDN:               cfg.DN,
		LdapBaseDN:           cfg.BaseDN,
		LdapFilter:           cfg.Filter,
		LdapScope:            b.intern(cfg.Scope),
		LdapEntryVar:         b.intern(cfg.EntryVar),
		LdapNewPassword:      cfg.NewPassword,
		LdapPageSize:         cfg.PageSize,
		LdapMaxEntries:       cfg.MaxEntries,
		LdapClientCertSecret: b.intern(cfg.ClientCertSecret),
	})
	return b.addNode(TypeConnectorTask, detail)
}

// SoapConfig is the deploy-time configuration of a SOAP / Web Services (WSDL)
// connector task (ADR-0165). Endpoint is the service URL (from the WSDL's
// soap:address) and Op the operation name; Action overrides the SOAPAction header
// (empty → Op); Body is the XML payload placed inside the SOAP envelope's Body
// (literal-or-FEEL, so a request can interpolate the instance's variables); Version is
// the SOAP protocol version ("1.1" or "1.2"); Auth references a server-side secret;
// ResultVar receives the parsed response body (empty → discard it).
type SoapConfig struct {
	Endpoint  RestExpr
	Op        string
	Action    RestExpr
	Body      RestExpr
	Version   string
	ResultVar string
	Auth      RestAuth
	Retries   int32
}

// AddSoapConnectorTask adds a SOAP / Web Services connector task and returns its
// element id. Like a service task it creates a job on activation and waits; the job
// carries the reserved SoapJobType so the in-process SOAP worker picks it up, evaluates
// any FEEL endpoint/action/body values over the instance's variables, wraps the body in
// a SOAP envelope, invokes the operation, parses the response into ResultVar (empty =
// discard), and completes the job (ADR-0165). The endpoint and body live in the model;
// credentials never do (Auth references a server-side secret, ADR-0041).
func (b *Builder) AddSoapConnectorTask(cfg SoapConfig) int32 {
	detail := int32(len(b.connectorTasks))
	b.connectorTasks = append(b.connectorTasks, ConnectorTaskDetail{
		JobType:      b.intern(SoapJobType),
		Connector:    -1, // SOAP carries its endpoint in the model, not a registry name
		Subject:      -1, // not a clio task
		EventType:    -1,
		ClioQuery:    -1,
		ReduceSpec:   -1,
		Method:       -1, // the SOAP operation, not an HTTP method, is authored
		ResultVar:    b.intern(cfg.ResultVar),
		Auth:         b.internAuth(cfg.Auth),
		Retries:      cfg.Retries,
		SoapEndpoint: cfg.Endpoint,
		SoapOp:       b.intern(cfg.Op),
		SoapAction:   cfg.Action,
		SoapBody:     cfg.Body,
		SoapVersion:  b.intern(cfg.Version),
	})
	return b.addNode(TypeConnectorTask, detail)
}

// AdConfig is the deploy-time configuration of an Active Directory connector task
// (ADR-0166). URL is the server (ldaps://host:636 — a password set needs LDAPS) and
// BindDN the bind identity — literal-or-FEEL values; BindSecret names the server-side
// secret for the bind password (empty → an anonymous bind); StartTLS upgrades a plain
// ldap:// connection. Op is the operation ("create-user"|"set-password"|"enable"|
// "disable"|"add-group-member"|"remove-group-member"). DN is the target user or group
// entry; MemberDN is the member added/removed for the group operations; EntryVar names
// the process variable holding the create-user attribute object; NewPassword is the
// set-password value.
type AdConfig struct {
	// Connector is the Console-configured directory this task talks to, or "" for a
	// task that carries its own URL and bind DN the old way.
	Connector   string
	URL         RestExpr
	BindDN      RestExpr
	BindSecret  string
	StartTLS    bool
	Op          string
	DN          RestExpr
	MemberDN    RestExpr
	EntryVar    string
	NewPassword RestExpr
	Retries     int32
	NewDN       RestExpr
	// The fields of the two operations that read a subtree rather than acting on one
	// entry: sync (a DirSync delta) and search (what is there now). Scope belongs to
	// search alone — AD answers DirSync only for the whole subtree — and CookieVar and
	// ObjectSecurity to sync alone.
	BaseDN         RestExpr
	Filter         RestExpr
	Scope          string
	CookieVar      string
	ResultVar      string
	MaxEntries     int32
	ObjectSecurity bool
}

// AddAdConnectorTask adds an Active Directory connector task and returns its element
// id. Like a service task it creates a job on activation and waits; the job carries
// the reserved AdJobType so the in-process AD worker picks it up, evaluates any FEEL
// url/dn values over the instance's variables, binds, performs the AD operation
// (create-user / set-password via unicodePwd / enable / disable via userAccountControl
// / group-member add or remove), and completes the job (ADR-0166). The server and DNs
// live in the model; the bind password never does (BindSecret references a server-side
// secret, ADR-0041).
func (b *Builder) AddAdConnectorTask(cfg AdConfig) int32 {
	detail := int32(len(b.connectorTasks))
	b.connectorTasks = append(b.connectorTasks, ConnectorTaskDetail{
		JobType: b.intern(AdJobType),
		// The Console-configured directory, when the task names one. A task using the
		// older model-authored form leaves this -1 and carries AdURL/AdBindDN/
		// AdBindSecret instead (ADR-0206).
		Connector:  b.intern(cfg.Connector),
		Subject:    -1, // not a clio task
		EventType:  -1,
		ClioQuery:  -1,
		ReduceSpec: -1,
		Method:     -1, // the AD operation, not an HTTP method, is authored
		Auth:       -1, // the bind password is a dedicated secret ref, not RestAuth
		// Only the two reading operations — sync and search — write back to a
		// variable; every other AD operation's effect is in the directory, so this
		// interns "" (-1) for all of them.
		ResultVar:        b.intern(cfg.ResultVar),
		Retries:          cfg.Retries,
		AdURL:            cfg.URL,
		AdBindDN:         cfg.BindDN,
		AdBindSecret:     b.intern(cfg.BindSecret),
		AdStartTLS:       cfg.StartTLS,
		AdOp:             b.intern(cfg.Op),
		AdDN:             cfg.DN,
		AdMemberDN:       cfg.MemberDN,
		AdEntryVar:       b.intern(cfg.EntryVar),
		AdNewPassword:    cfg.NewPassword,
		AdNewDN:          cfg.NewDN,
		AdBaseDN:         cfg.BaseDN,
		AdFilter:         cfg.Filter,
		AdScope:          b.intern(cfg.Scope),
		AdCookieVar:      b.intern(cfg.CookieVar),
		AdMaxEntries:     cfg.MaxEntries,
		AdObjectSecurity: cfg.ObjectSecurity,
	})
	return b.addNode(TypeConnectorTask, detail)
}

// SqlConfig is the deploy-time configuration of a SQL connector task (ADR-0173),
// shared by all three products. JobType is the product's reserved job type
// (MsSqlJobType, MariaDBJobType or PostgresJobType) — the one field that differs
// between them, and what decides which driver the worker opens.
//
// Connector names the database the worker is configured for: a SQL task carries no
// DSN, because the connection string never enters the engine. Op is the operation
// ("query"|"query-one"|"execute"). Statement is the SQL text, a literal by
// construction so no process value can become part of it; ParamsVar names the
// process variable bound to its placeholders. MaxRows caps a query's result set
// (0 = the worker's default), and ResultVar receives the rows, the row, or the
// affected count (empty = discard, valid only for execute).
type SqlConfig struct {
	JobType   string
	Connector string
	Op        string
	Statement string
	ParamsVar string
	MaxRows   int32
	ResultVar string
	Retries   int32
}

// AddSqlConnectorTask adds a SQL connector task of cfg's product and returns its
// element id. Like a service task it creates a job on activation and waits; the job
// carries the product's reserved job type. Unlike every kind before it, nothing in
// the engine subscribes to that type — SQL is worker-only (ADR-0164/0170), so the job
// waits for a worker that holds the DSN. The engine's half is resolving the
// parameters against the instance's variables (ADR-0168); the statement needs no
// resolving, being literal.
func (b *Builder) AddSqlConnectorTask(cfg SqlConfig) int32 {
	detail := int32(len(b.connectorTasks))
	b.connectorTasks = append(b.connectorTasks, ConnectorTaskDetail{
		JobType:      b.intern(cfg.JobType),
		Connector:    b.intern(cfg.Connector),
		Subject:      -1, // not a clio task
		EventType:    -1,
		ClioQuery:    -1,
		ReduceSpec:   -1,
		Method:       -1, // a SQL operation, not an HTTP method, is authored
		Auth:         -1, // the DSN lives on the worker; there is nothing to reference
		ResultVar:    b.intern(cfg.ResultVar),
		Retries:      cfg.Retries,
		SqlOp:        b.intern(cfg.Op),
		SqlStatement: b.intern(cfg.Statement),
		SqlParamsVar: b.intern(cfg.ParamsVar),
		SqlMaxRows:   cfg.MaxRows,
	})
	return b.addNode(TypeConnectorTask, detail)
}

// LdifConfig is the deploy-time configuration of a directory-file connector task
// (ADR-0171). Format is "ldif" or "dsml" and Operation "read" or "write"; Source
// names the variable holding the file text (read) or the entries (write), and Result
// the variable receiving the entries (read) or the rendered file (write).
type LdifConfig struct {
	Format    string
	Operation string
	Source    string
	Result    string
	Retries   int32
}

// AddLdifConnectorTask adds a directory-file connector task and returns its element
// id. Like a service task it creates a job on activation and waits; the job carries
// the reserved LdifJobType, which an in-process worker and an `atlas worker` both
// serve — the work is a pure transform, so neither placement can block the other.
func (b *Builder) AddLdifConnectorTask(cfg LdifConfig) int32 {
	detail := int32(len(b.connectorTasks))
	b.connectorTasks = append(b.connectorTasks, ConnectorTaskDetail{
		JobType:       b.intern(LdifJobType),
		Connector:     -1, // a file carries no endpoint and no credential
		Subject:       -1, // not a clio task
		EventType:     -1,
		ClioQuery:     -1,
		ReduceSpec:    -1,
		Method:        -1,
		Auth:          -1,
		ResultVar:     -1, // LDIF uses its own LdifResult field, as CSV does
		Retries:       cfg.Retries,
		LdifFormat:    b.intern(cfg.Format),
		LdifOperation: b.intern(cfg.Operation),
		LdifSource:    b.intern(cfg.Source),
		LdifResult:    b.intern(cfg.Result),
	})
	return b.addNode(TypeConnectorTask, detail)
}

// EntraConfig is the deploy-time configuration of a Microsoft Entra ID connector
// task (ADR-0172). Connector names the tenant the worker is configured for — a task
// carries no tenant id and no client secret, because they never enter the engine. Op
// is the lifecycle operation. UserID and GroupID are literal-or-FEEL values
// addressing the user (a UPN or object id) and the group; AttributesVar names the
// process variable holding the directory properties for create-user and update-user;
// ResultVar receives what Graph returned (empty = discard).
//
// Filter, Select, PageSize, MaxUsers, Search and Advanced configure list-users: the
// OData $filter (literal-or-FEEL), the $select projection, the $top per request, the
// cap on what may reach the result variable, the $search term (literal-or-FEEL), and
// whether the query asks for Graph's advanced query support. The compiler has already
// applied their defaults, set Advanced for a search, and refused all of them on the
// operations that return one object or none.
type EntraConfig struct {
	Connector string
	// ConnectorExpr carries the connector name as a literal-or-FEEL value when the
	// author wants the tenant chosen at runtime (e.g. "=tenant" on a multi-tenant
	// joiner). It is the zero RestExpr for the ordinary static case, where Connector
	// alone names the tenant. Only entra takes this: the kind is worker-only, so no
	// deploy-time credential lookup keys off a fixed name (ADR-0172).
	ConnectorExpr RestExpr
	Op            string
	UserID        RestExpr
	GroupID       RestExpr
	NewPassword   RestExpr
	Attributes    RestExpr
	AttributesVar string
	ResultVar     string
	Filter        RestExpr
	Select        string
	PageSize      int32
	MaxUsers      int32
	Search        RestExpr
	Advanced      bool
	// DeltaLink is the resume cursor for a change-tracking query (delta-users,
	// delta-groups): a literal-or-FEEL @odata.deltaLink from a previous run, empty on
	// the first. Zero on every other operation.
	DeltaLink RestExpr
	Retries   int32
}

// AddEntraConnectorTask adds an Entra ID connector task and returns its element id.
// Like a service task it creates a job on activation and waits; the job carries the
// reserved EntraJobType, which nothing in the engine subscribes to — the kind is
// worker-only (ADR-0164/0171), so the job waits for a worker that holds the tenant's
// app credential.
func (b *Builder) AddEntraConnectorTask(cfg EntraConfig) int32 {
	detail := int32(len(b.connectorTasks))
	b.connectorTasks = append(b.connectorTasks, ConnectorTaskDetail{
		JobType:            b.intern(EntraJobType),
		Connector:          b.intern(cfg.Connector),
		EntraConnector:     cfg.ConnectorExpr,
		Subject:            -1, // not a clio task
		EventType:          -1,
		ClioQuery:          -1,
		ReduceSpec:         -1,
		Method:             -1, // an Entra operation, not an HTTP method, is authored
		Auth:               -1, // the app credential lives on the worker
		ResultVar:          b.intern(cfg.ResultVar),
		Retries:            cfg.Retries,
		EntraOp:            b.intern(cfg.Op),
		EntraUserID:        cfg.UserID,
		EntraGroupID:       cfg.GroupID,
		EntraNewPassword:   cfg.NewPassword,
		EntraAttributes:    cfg.Attributes,
		EntraAttributesVar: b.intern(cfg.AttributesVar),
		EntraFilter:        cfg.Filter,
		EntraSelect:        b.intern(cfg.Select),
		EntraPageSize:      cfg.PageSize,
		EntraMaxUsers:      cfg.MaxUsers,
		EntraSearch:        cfg.Search,
		EntraAdvanced:      cfg.Advanced,
		EntraDeltaLink:     cfg.DeltaLink,
	})
	return b.addNode(TypeConnectorTask, detail)
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
	BodyHTML  RestExpr
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
		BodyHTML:    cfg.BodyHTML,
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
	// Format is the file format and Operation the direction; empty means csv and
	// read. Widths carries each column's character width for a fixed-width file,
	// positionally alongside Columns.
	Format    string
	Operation string
	Widths    []int32
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
		CsvFormat:    b.intern(cfg.Format),
		CsvOperation: b.intern(cfg.Operation),
		CsvWidths:    cfg.Widths,
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

// JiraConfig is the deploy-time configuration of a Jira connector task
// (ADR-0201). Connector names the server-registered Jira instance (its
// base URL and credential live server-side, never in the model) and Operation is the
// issue-tracker operation. The remaining values are the ones that operation takes —
// literal-or-FEEL values (the parser compiles the FEEL ones) evaluated over the
// variables the task sees at call time. MaxResults is a search's cap, already
// defaulted by the compiler so the runtime interprets nothing (I5). ResultVar, if set,
// is the process variable what Jira returned is written back into.
type JiraConfig struct {
	Connector   string
	Operation   string
	Issue       RestExpr
	Project     RestExpr
	IssueType   RestExpr
	Summary     RestExpr
	Description RestExpr
	Transition  RestExpr
	Comment     RestExpr
	Assignee    RestExpr
	JQL         RestExpr
	MaxResults  int32
	Fields      []RestKV
	ResultVar   string
	Retries     int32
}

// AddJiraConnectorTask adds a Jira connector task and returns its element id. Like a
// service task it creates a job on activation and waits; the job carries the reserved
// JiraJobType so the in-process Jira worker picks it up, evaluates the authored
// literal-or-FEEL values over the variables the task sees, resolves the named
// connector's client, performs the one operation, writes what Jira returned into
// ResultVar (empty = discard it), and completes the job. The Jira base URL and
// credential are resolved server-side from the named connector, never authored in the
// model — mirroring Remedy and SharePoint (ADR-0106/0141).
func (b *Builder) AddJiraConnectorTask(cfg JiraConfig) int32 {
	detail := int32(len(b.connectorTasks))
	b.connectorTasks = append(b.connectorTasks, ConnectorTaskDetail{
		JobType:         b.intern(JiraJobType),
		Connector:       b.intern(cfg.Connector),
		Subject:         -1, // not a clio task
		EventType:       -1,
		ClioQuery:       -1,
		ReduceSpec:      -1,
		Method:          -1, // not a REST task
		ResultVar:       b.intern(cfg.ResultVar),
		Auth:            -1,
		JiraOp:          b.intern(cfg.Operation),
		JiraIssue:       cfg.Issue,
		JiraProject:     cfg.Project,
		JiraIssueType:   cfg.IssueType,
		JiraSummary:     cfg.Summary,
		JiraDescription: cfg.Description,
		JiraTransition:  cfg.Transition,
		JiraComment:     cfg.Comment,
		JiraAssignee:    cfg.Assignee,
		JiraJQL:         cfg.JQL,
		JiraMaxResults:  cfg.MaxResults,
		JiraFields:      cfg.Fields,
		Retries:         cfg.Retries,
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
		elementDocs:        b.elementDocs,
		repairForms:        b.repairForms,
		lanes:              b.lanes,
		documentation:      b.documentation,
		startFormId:        b.startFormId,
		versionTag:         b.versionTag,
		instanceTtlNanos:   b.instanceTtlNanos,
		historyTtlNanos:    b.historyTtlNanos,
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
