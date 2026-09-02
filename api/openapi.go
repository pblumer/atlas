package api

import (
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
)

// This file makes the /api/v1 surface self-describing (ADR-0043). The route
// table returned by apiRoutes is the single source of truth: Handler registers
// every entry on the mux, and openapiDoc renders the same list as an OpenAPI
// 3.1 document served at /api/v1/openapi.json. Because registration and
// description read one table, a served route cannot exist without a documented
// one; TestOpenAPICoversEveryRoute asserts the two never drift. The document
// feeds the vendored Scalar explorer at /api/docs (both served by default,
// disabled with --docs=false), whose "Try it out" issues same-origin requests
// to this live engine.

// apiRoute is one HTTP route of the /api/v1 surface: an http.ServeMux pattern,
// the handler bound to it, and the OpenAPI operation describing it.
type apiRoute struct {
	method  string           // "GET", "POST", "PATCH", "DELETE"
	pattern string           // net/http pattern path, e.g. "/api/v1/processes/{key}/xml"
	handler http.HandlerFunc // handler registered for method+pattern
	op      apiOp            // human/tool-facing description
}

// apiOp describes a route for the OpenAPI document, and names the role it needs.
// summary, tag and role are required (the drift and inventory tests enforce
// non-empty). req/resp bodies are filled in where they add value and default to a
// permissive object otherwise (ADR-0043).
type apiOp struct {
	summary string
	tag     string

	// role is what a signed-in identity must hold to reach this route, one of
	// routeRoles (ADR-0209). It sits here, beside the
	// summary, because this table is the single source of truth for the surface and a
	// second list of who-may-what is a second list to keep in step. Empty reaches
	// nobody, and TestEveryRouteDeclaresARole makes empty a failing build.
	role string

	status     int       // primary success status; 0 means 200 OK
	deprecated bool      // renders openapi `deprecated: true` (e.g. an alias kept for compat)
	req        *bodySpec // request body, or nil when the route takes none
	resp       *bodySpec // success response body, or nil when it returns no content
}

// bodySpec is one request or response payload: a media type and an optional
// JSON-Schema object (nil renders as a permissive object).
type bodySpec struct {
	mediaType string
	schema    map[string]any
	desc      string
}

// Schema and body helpers keep the route table below readable.
func schemaObj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func tString() map[string]any  { return map[string]any{"type": "string"} }
func tInteger() map[string]any { return map[string]any{"type": "integer"} }
func tBool() map[string]any    { return map[string]any{"type": "boolean"} }
func tObject() map[string]any  { return map[string]any{"type": "object"} }
func tArray() map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "object"}}
}

func jsonBody(desc string, schema map[string]any) *bodySpec {
	return &bodySpec{mediaType: "application/json", schema: schema, desc: desc}
}

func xmlBody(desc string) *bodySpec {
	return &bodySpec{mediaType: "application/xml", schema: tString(), desc: desc}
}

// csvBody describes a text/csv download — a streamed table rather than a JSON
// document, so a caller can read it with a spreadsheet or a pipe.
func csvBody(desc string) *bodySpec {
	return &bodySpec{mediaType: "text/csv", schema: tString(), desc: desc}
}

// eventStreamBody describes a Server-Sent Events response — a long-lived
// text/event-stream of newline-delimited frames rather than a single JSON body
// (ADR-0140's live collaboration transport).
func eventStreamBody(desc string) *bodySpec {
	return &bodySpec{mediaType: "text/event-stream", schema: tString(), desc: desc}
}

// apiRoutes is the single source of truth for the /api/v1 surface. Handler
// iterates it to register handlers; openapiDoc iterates it to describe them.
// Adding an endpoint means adding one entry here — nothing is registered off to
// the side, so the spec cannot fall out of sync (ADR-0043).
func (s *Server) apiRoutes() []apiRoute {
	return []apiRoute{
		{"GET", "/api/v1/info", s.handleInfo, apiOp{
			summary: "Product and version metadata", tag: "System", role: roleAny,
			resp: jsonBody("Product metadata", schemaObj(map[string]any{
				"product": tString(), "version": tString(),
			}))}},
		// The node descriptor (ADR-0189 §6): which *runtime* is answering, as opposed
		// to /api/v1/info's account of which binary. It is what makes cross-server
		// correlation possible at all, so it is readable by any signed-in identity and
		// by the least-privilege status scope a remote correlator is given; writing it
		// is an operator act and stays with the administrator.
		{"GET", "/api/v1/node", s.handleNode, apiOp{
			summary: "This server's stable node descriptor: runtime identity, build, partition and supported features (ADR-0189)",
			tag:     "System", role: roleAny,
			resp: jsonBody("Node descriptor", schemaObj(map[string]any{
				"id": tString(), "name": tString(), "environment": tString(),
				"product": tString(), "version": tString(),
				"partition": tInteger(), "partitions": tInteger(), "features": tArray(),
			}, "id"))}},
		{"PUT", "/api/v1/node", s.handleUpdateNode, apiOp{
			summary: "Set this node's operator-owned display name, environment and labels (ADR-0189)",
			tag:     "System", role: RoleAdmin,
			req: jsonBody("Node identity", schemaObj(map[string]any{
				"name": tString(), "environment": tString(), "labels": tObject(),
			})), resp: jsonBody("Node descriptor", tObject())}},
		{"GET", "/api/v1/stats", s.handleStats, apiOp{
			summary: "Live active-instance counts, plus how many tokens are parked behind an unresolved incident", tag: "System", role: roleAny,
			resp: jsonBody("Instance counts", schemaObj(map[string]any{
				"activeProcessInstances": tInteger(), "activeElementInstances": tInteger(),
				"unresolvedIncidents": tInteger(),
			}))}},
		{"GET", "/api/v1/logs", s.handleLogs, apiOp{
			summary: "Recent server log lines (admin-only when auth is on)", tag: "System", role: RoleAdmin,
			resp: jsonBody("Recent log lines, oldest first", schemaObj(map[string]any{
				"lines": tArray(),
			}))}},
		{"GET", "/api/v1/backup", s.handleBackup, apiOp{
			summary: "Download a backup of all design-time data (projects, drafts, deployments, forms, decisions, connectors) as a gzip tar; excludes user accounts, the vault key, and runtime state (admin-only when auth is on) (ADR-0107)", tag: "System", role: RoleAdmin,
			resp: &bodySpec{mediaType: "application/gzip", schema: tString(), desc: "A gzip-compressed tar archive of the design-time data directory"}}},
		{"POST", "/api/v1/restore", s.handleRestore, apiOp{
			summary: "Restore design-time data from an uploaded backup archive; overwrites matching artifacts, skips anything outside the design-time allowlist, and needs a restart for deployed processes to take effect (admin-only when auth is on) (ADR-0107)", tag: "System", role: RoleAdmin,
			req: &bodySpec{mediaType: "application/gzip", schema: tString(), desc: "A gzip tar archive produced by GET /api/v1/backup"},
			resp: jsonBody("Restore summary", schemaObj(map[string]any{
				"restored": tInteger(), "restartRequired": tBool(), "note": tString(),
			}))}},
		{"GET", "/api/v1/backup/full", s.handleBackupFull, apiOp{
			summary: "Download a whole-instance snapshot (design-time data plus the WAL — running instances — the user accounts and the vault key) as a gzip tar; excludes only the derivable state store (admin-only when auth is on) (ADR-0109)", tag: "System", role: RoleAdmin,
			resp: &bodySpec{mediaType: "application/gzip", schema: tString(), desc: "A gzip-compressed tar archive of the whole-instance snapshot"}}},
		{"POST", "/api/v1/restore/full", s.handleRestoreFull, apiOp{
			summary: "Stage a whole-instance snapshot for restore; it is applied on the next server restart, which replaces the WAL, running instances, design-time data, users and vault key, then rebuilds state from the restored WAL (admin-only when auth is on) (ADR-0109)", tag: "System", role: RoleAdmin,
			req: &bodySpec{mediaType: "application/gzip", schema: tString(), desc: "A gzip tar archive produced by GET /api/v1/backup/full"},
			resp: jsonBody("Restore staging summary", schemaObj(map[string]any{
				"restored": tInteger(), "restartRequired": tBool(), "note": tString(),
			}))}},

		{"GET", "/api/v1/checkpoints", s.handleCheckpointStatus, apiOp{
			summary: "Recovery-checkpoint and WAL-compaction status: what is configured, every published checkpoint and whether it still verifies, the last pass's outcome, and the WAL's current footprint (admin-only when auth is on) (ADR-0131)", tag: "System", role: RoleAdmin,
			resp: jsonBody("Checkpoint status", schemaObj(map[string]any{
				"enabled": tBool(), "intervalSeconds": tInteger(), "keep": tInteger(),
				"compaction": tBool(), "root": tString(), "checkpoints": tArray(),
				"lastPass": tObject(), "walSegments": tInteger(), "walBytes": tInteger(),
			}))}},
		{"POST", "/api/v1/checkpoints", s.handleCheckpointNow, apiOp{
			summary: "Take a recovery checkpoint now — and compact the WAL if compaction is enabled — instead of waiting for the next scheduled pass; 409 when checkpointing is disabled (admin-only when auth is on) (ADR-0131)", tag: "System", role: RoleAdmin,
			resp: jsonBody("What the pass did", schemaObj(map[string]any{
				"at": tInteger(), "position": tInteger(), "checkpointError": tString(),
				"segmentsRemoved": tInteger(), "compactionError": tString(), "note": tString(),
			}))}},

		{"POST", "/api/v1/feel/validate", s.handleValidateFeel, apiOp{
			summary: "Validate a FEEL expression compiles", tag: "FEEL", role: RoleModeler,
			req: jsonBody("FEEL expression", schemaObj(map[string]any{"expression": tString()}, "expression")),
			resp: jsonBody("Validation result", schemaObj(map[string]any{
				"ok": tBool(), "error": tString(),
			}))}},
		{"POST", "/api/v1/feel/evaluate", s.handleEvaluateFeel, apiOp{
			summary: "Evaluate a FEEL expression against variables", tag: "FEEL", role: RoleModeler,
			req: jsonBody("Expression and variables", schemaObj(map[string]any{
				"expression": tString(), "variables": tObject(),
			}, "expression")),
			resp: jsonBody("Evaluation result", schemaObj(map[string]any{
				"ok": tBool(), "result": tObject(), "kind": tString(), "error": tString(),
			}))}},
		{"POST", "/api/v1/scripts/run", s.handleRunScript, apiOp{
			summary: "Run a script task against sample variables (admin-only when auth is on)", tag: "Scripts", role: RoleAdmin,
			req: jsonBody("Language, source, and sample variables", schemaObj(map[string]any{
				"language": tString(), "source": tString(), "variables": tObject(),
			}, "language", "source")),
			resp: jsonBody("Run result", schemaObj(map[string]any{
				"ok": tBool(), "result": tObject(), "error": tString(),
			}))}},

		{"POST", "/api/v1/deployments", s.handleDeploy, apiOp{
			summary: "Deploy a BPMN model; the response carries warnings for references that will not resolve at runtime (an unconfigured connector)", tag: "Deployments", role: RoleModeler,
			req: xmlBody("BPMN 2.0 XML"),
			resp: jsonBody("Deployed processes", schemaObj(map[string]any{
				"key": tInteger(), "processId": tString(), "version": tInteger(), "deployments": tArray(),
			}))}},
		{"POST", "/api/v1/validate", s.handleValidate, apiOp{
			summary: "Validate a BPMN model without deploying — a dry-run compile returning structured problems (errors and warnings) and the engine version, for the Modeler's Problems panel (ADR-0026). Pass ?applicationId= to also resolve each data object's itemSubjectRef against that application's information model and check its member writes and read order (ADR-draft-process-information-model)", tag: "Deployments", role: RoleModeler,
			req: xmlBody("BPMN 2.0 XML"),
			resp: jsonBody("Validation problems and the engine version that produced them", schemaObj(map[string]any{
				"version": tString(), "problems": tArray(),
			}))}},
		{"POST", "/api/v1/layout", s.handleLayout, apiOp{
			summary: "Regenerate a BPMN model's diagram layout — discards any existing diagram interchange and returns the model with a freshly generated left-to-right layout, backing the Modeler's Auto-layout button. A pure transform: nothing is compiled, deployed, or stored.", tag: "Deployments", role: RoleModeler,
			req:  xmlBody("BPMN 2.0 XML"),
			resp: xmlBody("BPMN 2.0 XML with regenerated diagram interchange")}},

		{"GET", "/api/v1/processes", s.handleListProcesses, apiOp{
			summary: "List deployed processes", tag: "Processes", role: roleAny, resp: jsonBody("Processes", tArray())}},
		{"GET", "/api/v1/processes/{key}/xml", s.handleProcessXML, apiOp{
			summary: "Fetch a deployed process's BPMN XML", tag: "Processes", role: roleAny,
			resp: xmlBody("BPMN 2.0 XML")}},
		{"DELETE", "/api/v1/processes/{key}", s.handleDeleteProcess, apiOp{
			summary: "Delete a deployment (must have no running instances)", tag: "Processes", role: RoleModeler,
			status: http.StatusNoContent}},
		{"GET", "/api/v1/processes/{key}/runtime", s.handleProcessRuntime, apiOp{
			summary: "Read a process's live runtime state", tag: "Processes", role: roleAny, resp: jsonBody("Runtime state", tObject())}},
		{"PUT", "/api/v1/processes/{key}/active", s.handleSetProcessActive, apiOp{
			summary: "Activate or deactivate a deployed process (a deactivated process stays deployed but does not auto-start new instances from its timer/message/signal start events)", tag: "Processes", role: RoleOperator,
			req:  jsonBody("Active flag", schemaObj(map[string]any{"active": tBool()})),
			resp: jsonBody("The key and its new active state", tObject())}},
		{"GET", "/api/v1/call-activities", s.handleCallActivities, apiOp{
			summary: "List every call activity across deployed processes with its per-server resolution status", tag: "Processes", role: roleAny, resp: jsonBody("Call activities", tArray())}},
		{"PUT", "/api/v1/call-activities/overrides/{processId}", s.handleSetCallOverride, apiOp{
			summary: "Set a per-server call-activity target override (redirect/pin/disable) for a called process id", tag: "Processes", role: RoleAdmin,
			req: jsonBody("Override", tObject()), resp: jsonBody("Stored override", tObject())}},
		{"DELETE", "/api/v1/call-activities/overrides/{processId}", s.handleDeleteCallOverride, apiOp{
			summary: "Clear a called process id's per-server target override", tag: "Processes", role: RoleAdmin, status: http.StatusNoContent}},
		{"GET", "/api/v1/collaborations/{key}/runtime", s.handleCollaborationRuntime, apiOp{
			summary: "Read a collaboration's live runtime state", tag: "Collaborations", role: roleAny, resp: jsonBody("Runtime state", tObject())}},

		{"POST", "/api/v1/processes/{key}/instances", s.handleCreateInstance, apiOp{
			summary: "Start a process instance", tag: "Instances", role: RoleOperator,
			req:  jsonBody("Initial variables", schemaObj(map[string]any{"variables": tObject()})),
			resp: jsonBody("Created instance", tObject())}},
		{"POST", "/api/v1/processes/{key}/instances-from-csv", s.handleCreateInstanceFromCSV, apiOp{
			summary: "Start a process instance from an uploaded CSV — multipart file + JSON column layout; seeds rows/rowCount/fileName as start variables (ADR-0084)", tag: "Instances", role: RoleOperator,
			req: &bodySpec{mediaType: "multipart/form-data", desc: "CSV file and a JSON column layout", schema: schemaObj(map[string]any{
				"file":   map[string]any{"type": "string", "format": "binary"},
				"config": tString(),
			}, "file", "config")},
			resp: jsonBody("Created instance with parsed row count", tObject())}},
		{"GET", "/api/v1/instances", s.handleListInstances, apiOp{
			summary: "List active and finished instances — capped per call (?limit=, default 1000, max 10000); narrow to one definition with ?process=<key>; X-Instances-Truncated: true marks a capped page", tag: "Instances", role: RoleOperator, resp: jsonBody("Instances", tArray())}},
		{"GET", "/api/v1/instances/summary", s.handleInstancesSummary, apiOp{
			summary: "Per-definition instance counts (active/completed) — lean count-only scan for the operations overview", tag: "Instances", role: RoleOperator, resp: jsonBody("Instance summary", tArray())}},
		{"GET", "/api/v1/instances/search", s.handleSearchInstances, apiOp{
			summary: "Search instances by variable content — ?q=name=value (name exact, value substring) or free text over variable names and values", tag: "Instances", role: RoleOperator,
			resp: jsonBody("Matching instances", tArray())}},
		// Every signed-in identity, not the operator role the rest of this group carries:
		// a task form is prefilled from the variables of the instance the task belongs to,
		// so a role narrower than "signed in" would hand a task worker an empty form. What
		// this route needs is the *other* axis — may you see this instance — and that is
		// open work (O-02), not something a role per endpoint group can express.
		{"GET", "/api/v1/instances/{key}/variables", s.handleInstanceVariables, apiOp{
			summary: "Read a process instance's variables as a typed JSON object", tag: "Instances", role: roleAny,
			resp: jsonBody("Instance variables", tObject())}},
		{"POST", "/api/v1/instances/{key}/variables", s.handleSetInstanceVariables, apiOp{
			summary: "Set or overwrite variables on a running instance — an operator correction to live process state (admin-only when auth is on); optional scopeKey targets a subprocess local scope; does not re-evaluate already-passed gateways", tag: "Instances", role: RoleAdmin,
			req: jsonBody("Variables to set, and an optional target scope", schemaObj(map[string]any{
				"variables": tObject(), "scopeKey": tInteger(),
			}, "variables")),
			resp: jsonBody("Set result", schemaObj(map[string]any{
				"instanceKey": tInteger(), "variablesSet": tInteger(),
			}))}},
		{"GET", "/api/v1/instances/{key}/variable-audit", s.handleInstanceVariableAudit, apiOp{
			summary: "Read the external variable overrides a process instance received — the \"who changed it\" audit trail, each with actor, scope, variable name, and typed new value (ADR-0098)", tag: "Instances", role: RoleOperator,
			resp: jsonBody("Variable overrides", tArray())}},
		{"GET", "/api/v1/instances/{key}/data-objects", s.handleInstanceDataObjects, apiOp{
			summary: "Read a process instance's data objects — each with its name, data state, typed value, declared class (itemSubjectRef), collection flag, and the trail of every state it passed through with the element that wrote it", tag: "Instances", role: RoleOperator,
			resp: jsonBody("Instance data objects", tArray())}},
		{"GET", "/api/v1/instances/{key}/object-graph", s.handleInstanceObjectGraph, apiOp{
			summary: "Derive a process instance's object diagram — its data objects as UML object nodes with their attributes and business keys, linked by containment and by matching business keys, plus the references this instance cannot resolve (ADR-draft-process-information-model)", tag: "Instances", role: RoleOperator,
			resp: jsonBody("Object graph", tObject())}},
		{"GET", "/api/v1/instances/{key}/timeline", s.handleInstanceTimeline, apiOp{
			summary: "Read a process instance's step-by-step replay timeline — each step's variables carry an actor when the value was set by an external operator override (ADR-0098)", tag: "Instances", role: RoleOperator,
			resp: jsonBody("Instance timeline", tObject())}},
		{"GET", "/api/v1/instances/{key}/decisions", s.handleInstanceDecisions, apiOp{
			summary: "Read the DMN decision evaluations a process instance made — each with its inputs, outputs, and trace", tag: "Instances", role: RoleOperator,
			resp: jsonBody("Decision evaluations", tArray())}},
		{"GET", "/api/v1/instances/{key}/jobs", s.handleListInstanceJobs, apiOp{
			summary: "List the activatable jobs an instance is parked on (any type) — the read side of POST /jobs/{key}/complete", tag: "Instances", role: RoleOperator,
			resp: jsonBody("Activatable jobs", tArray())}},
		{"GET", "/api/v1/decisions/deployed", s.handleDeployedDecisions, apiOp{
			summary: "List deployed and evaluated DMN decisions, one row per decision, with the processes that use it and its evaluation usage", tag: "Decisions", role: RoleOperator,
			resp: jsonBody("Deployed decisions", tArray())}},
		{"GET", "/api/v1/decisions/{id}/evaluations", s.handleDecisionEvaluations, apiOp{
			summary: "List every retained evaluation of one decision — its inputs, outputs, and trace — newest first, for drilling into a decision's instances", tag: "Decisions", role: RoleOperator,
			resp: jsonBody("Decision evaluations", tArray())}},
		{"POST", "/api/v1/instances/{key}/migrate/plan", s.handleMigrationPlan, apiOp{
			summary: "Answer what migrating this instance to another version of its process would do — the derived element mapping and every reason it would be refused — writing nothing (admin-only when auth is on, ADR-0162)", tag: "Instances", role: RoleAdmin,
			req: jsonBody("Target version and optional element-id overrides", schemaObj(map[string]any{
				"targetProcessDefKey": tInteger(), "mapping": tArray(),
			}, "targetProcessDefKey")),
			resp: jsonBody("Migration plan", tObject())}},
		{"POST", "/api/v1/instances/{key}/migrate", s.handleMigrateInstance, apiOp{
			summary: "Rebind a running instance to another version of its process, preserving its variables, jobs and history; refused with 409 and the same plan when the mapping does not hold. A reason is required and recorded as an operator action (admin-only when auth is on, ADR-0162)", tag: "Instances", role: RoleAdmin,
			req: jsonBody("Target version, reason, and optional element-id overrides", schemaObj(map[string]any{
				"targetProcessDefKey": tInteger(), "reason": tString(), "mapping": tArray(),
			}, "targetProcessDefKey", "reason")),
			resp: jsonBody("Migration result", tObject())}},
		{"POST", "/api/v1/processes/{key}/migrate-instances", s.handleMigrateInstancesOfProcess, apiOp{
			summary: "Migrate a bounded batch of a definition's running instances to another version (?limit=, default 500, max 5000); each instance is its own event, so a refusal does not roll back the rest — repeat while the response reports remaining=true (ADR-0162)", tag: "Instances", role: RoleAdmin,
			req: jsonBody("Target version, reason, and optional element-id overrides", schemaObj(map[string]any{
				"targetProcessDefKey": tInteger(), "reason": tString(), "mapping": tArray(),
			}, "targetProcessDefKey", "reason")),
			resp: jsonBody("Bulk migration result", tObject())}},
		{"DELETE", "/api/v1/instances/{key}", s.handleCancelInstance, apiOp{
			summary: "Cancel a running instance", tag: "Instances", role: RoleOperator, resp: jsonBody("Cancellation result", tObject())}},
		{"POST", "/api/v1/processes/{key}/cancel-instances", s.handleCancelInstancesOfProcess, apiOp{
			summary: "Cancel a bounded batch of a definition's running instances (?limit=, default 5000, max 50000); repeat while the response reports remaining=true", tag: "Instances", role: RoleOperator,
			resp: jsonBody("Bulk cancellation result", tObject())}},
		{"POST", "/api/v1/instances/terminate", s.handleTerminateInstances, apiOp{
			summary: "Terminate a selected set of running instances — body {keys:[…]} for an explicit selection, or {processDefKey, q?, limit?} to terminate a definition's matching instances (repeat while remaining=true)", tag: "Instances", role: RoleOperator,
			req:  jsonBody("Selection", schemaObj(map[string]any{"keys": tArray(), "processDefKey": tInteger(), "q": tString(), "limit": tInteger()})),
			resp: jsonBody("Termination result", tObject())}},

		{"POST", "/api/v1/messages", s.handlePublishMessage, apiOp{
			summary: "Publish a message for correlation", tag: "Messages", role: RoleOperator,
			req: jsonBody("Message", schemaObj(map[string]any{
				"name": tString(), "correlationKey": tString(), "variables": tObject(),
			}, "name")),
			resp: jsonBody("Publish result", tObject())}},

		{"POST", "/api/v1/workers/{id}/restart", s.handleRestartWorker, apiOp{
			summary: "Restart a worker process this server supervises (ADR-0157); 409 when it supervises none", tag: "Incidents", role: RoleOperator,
			resp: jsonBody("The worker that is restarting", tObject())}},
		{"GET", "/api/v1/workers", s.handleWorkers, apiOp{
			summary: "The Workers view: every job type with its queue depth, in-flight count and incidents, and every worker seen this run (ADR-0157)", tag: "Incidents", role: RoleOperator,
			resp: jsonBody("Workers and job-type queues", tObject())}},
		{"GET", "/api/v1/workers/{id}/history", s.handleWorkerHistory, apiOp{
			summary: "One worker's job history from the configured clio connector, newest first (admin-only; empty when no history connector is configured)", tag: "Incidents", role: RoleAdmin,
			resp: jsonBody("Worker job history", tObject())}},
		{"GET", "/api/v1/workers/{id}/jobs", s.handleWorkerJobs, apiOp{
			summary: "One worker's recent jobs: what it was handed, what it returned, and what failed (admin-only; a bounded in-memory tail, emptied by a restart)", tag: "Incidents", role: RoleAdmin,
			resp: jsonBody("Worker jobs", tObject())}},
		{"POST", "/api/v1/jobs/activate", s.handleActivateJobsByType, apiOp{
			summary: "Lease the next jobs of a named job type to an external worker — the type-keyed pull, optionally long-polling (ADR-0007)", tag: "Incidents", role: RoleOperator,
			req: jsonBody("Job type, worker id, lease, batch size, and how long to wait for work before answering empty", schemaObj(map[string]any{
				"type": tString(), "worker": tString(), "leaseMs": tInteger(), "maxJobs": tInteger(), "waitMs": tInteger(),
			}, "type")),
			resp: jsonBody("The leased jobs, with the variables visible at each task", tObject())}},
		{"POST", "/api/v1/jobs/{key}/activate", s.handleActivateJob, apiOp{
			summary: "Lease a job to an external worker for a bounded time (ADR-0007)", tag: "Incidents", role: RoleOperator,
			req: jsonBody("Worker id and how long to hold the job", schemaObj(map[string]any{
				"worker": tString(), "leaseMs": tInteger(),
			})),
			resp: jsonBody("Job key, holder, and when the lease runs out", tObject())}},
		{"POST", "/api/v1/jobs/{key}/complete", s.handleCompleteJob, apiOp{
			summary: "Complete a job — as its lease-holding worker (\"worker\" + \"leaseToken\"), or by hand as an operator (\"reason\", recorded for audit)", tag: "Incidents", role: RoleOperator,
			req: jsonBody("Either the holding worker id with the lease token its activation returned (protocol completion) or a reason (operator intervention), plus optional completion variables", schemaObj(map[string]any{
				"worker": tString(), "leaseToken": tInteger(), "reason": tString(), "variables": tObject(),
			})),
			resp: jsonBody("Job key", tObject())}},
		{"POST", "/api/v1/jobs/{key}/fail", s.handleFailJob, apiOp{
			summary: "Fail a job, carrying remaining retries (0 raises an incident)", tag: "Incidents", role: RoleOperator,
			req: jsonBody("Retries left and a failure message; a worker also presents its id and the lease token its activation returned", schemaObj(map[string]any{
				"retries": tInteger(), "message": tString(), "worker": tString(), "leaseToken": tInteger(),
			})),
			resp: jsonBody("Job key and stats", tObject())}},
		{"GET", "/api/v1/incidents", s.handleListIncidents, apiOp{
			summary: "List unresolved incidents, optionally scoped to one instance (?instance=) or definition (?process=) — capped per call (?limit=, max 5000); X-Incidents-Truncated: true marks a capped page", tag: "Incidents", role: RoleOperator, resp: jsonBody("Incidents", tArray())}},
		{"POST", "/api/v1/incidents/{key}/resolve", s.handleResolveIncident, apiOp{
			summary: "Resolve the incident on an element instance and retry its job", tag: "Incidents", role: RoleOperator,
			req:  jsonBody("Retries to grant the resumed job (default 1)", schemaObj(map[string]any{"retries": tInteger()})),
			resp: jsonBody("Element instance key and stats", tObject())}},

		{"GET", "/api/v1/tasks", s.handleListTasks, apiOp{
			summary: "List active user tasks, newest first — capped per call (?limit=, default 500, max 5000). A capped page sets X-Tasks-Truncated: true and X-Tasks-Next-Cursor: <jobKey>; pass it as ?before= to page to older tasks. ?processInstance=<key> scopes the list to one instance (flood-proof, for embedded clients)", tag: "Tasks", role: RoleUser, resp: jsonBody("Tasks", tArray())}},
		{"GET", "/api/v1/tasks/{key}", s.handleGetTask, apiOp{
			summary: "Fetch one open user task by key — the deep-link primitive so a task stays reachable outside a capped list page", tag: "Tasks", role: RoleUser, resp: jsonBody("Task", tObject())}},
		{"POST", "/api/v1/tasks/{key}/complete", s.handleCompleteTask, apiOp{
			summary: "Complete a user task", tag: "Tasks", role: RoleUser,
			req:  jsonBody("Completion variables", schemaObj(map[string]any{"variables": tObject()})),
			resp: jsonBody("Task key", tObject())}},
		{"POST", "/api/v1/tasks/{key}/claim", s.handleClaimTask, apiOp{
			summary: "Claim a user task (self, or assign a named user)", tag: "Tasks", role: RoleUser,
			req:  jsonBody("Optional assignee (empty claims for the signed-in user)", schemaObj(map[string]any{"assignee": tString()})),
			resp: jsonBody("Task and assignee", tObject())}},
		{"POST", "/api/v1/tasks/{key}/unclaim", s.handleUnclaimTask, apiOp{
			summary: "Release a user task's claim", tag: "Tasks", role: RoleUser, resp: jsonBody("Task key", tObject())}},

		{"POST", "/api/v1/drafts", s.handleSaveDraft, apiOp{
			summary: "Save a diagram draft, keyed by its process id. ?from=<draft id> names the draft being edited (empty for a never-saved diagram): a changed process id then renames the draft instead of leaving a second copy behind, and a save onto an id another draft already holds is refused with 409 (ADR-0222). Omit ?from= for the plain upsert-by-id an import or an agent wants", tag: "Drafts", role: RoleModeler, req: jsonBody("Draft", tObject()), resp: jsonBody("Saved draft", tObject())}},
		{"GET", "/api/v1/drafts", s.handleListDrafts, apiOp{
			summary: "List diagram drafts", tag: "Drafts", role: RoleModeler, resp: jsonBody("Drafts", tArray())}},
		{"GET", "/api/v1/drafts/{id}/availability", s.handleDraftIDAvailability, apiOp{
			summary: "Report whether a process id is free to save a draft under — the live check behind the Modeler's Process ID field, so a collision shows while the id is typed rather than at Save", tag: "Drafts", role: RoleModeler, resp: jsonBody("Availability", tObject())}},
		{"GET", "/api/v1/drafts/{id}/xml", s.handleDraftXML, apiOp{
			summary: "Fetch a draft's BPMN XML", tag: "Drafts", role: RoleModeler, resp: xmlBody("BPMN 2.0 XML")}},
		{"PATCH", "/api/v1/drafts/{id}", s.handleMoveDraft, apiOp{
			summary: "Move a draft to a project", tag: "Drafts", role: RoleModeler, req: jsonBody("Move", tObject()), resp: jsonBody("Updated draft", tObject())}},
		{"DELETE", "/api/v1/drafts/{id}", s.handleDeleteDraft, apiOp{
			summary: "Delete a draft", tag: "Drafts", role: RoleModeler, status: http.StatusNoContent}},

		{"POST", "/api/v1/imports/mim", s.handleImportMIM, apiOp{
			summary: "Import a MIM/FIM XOML workflow as a BPMN draft (with a per-node conversion report)", tag: "Drafts", role: RoleModeler,
			req:  xmlBody("MIM/FIM XOML, or an Export-FIMConfig XML that embeds one"),
			resp: jsonBody("Created draft identity and conversion report", tObject())}},

		{"GET", "/api/v1/drafts/{id}/session", s.handleDraftSession, apiOp{
			summary: "Join a draft's live collaboration session — a Server-Sent Events stream of sync, presence, lock, and change frames for real-time co-editing by people and AI agents (ADR-0140)", tag: "Live Sessions", role: RoleModeler,
			resp: eventStreamBody("SSE stream of session frames")}},
		{"POST", "/api/v1/drafts/{id}/session/join", s.handleDraftSessionJoin, apiOp{
			summary: "Join a draft's live session without an event stream — for an AI agent over MCP that cannot hold an SSE connection; returns the sync snapshot (self id, roster, locks) and is driven with poll/presence/lock/change (ADR-0140 M2)", tag: "Live Sessions", role: RoleModeler,
			req:  jsonBody("Optional display name", schemaObj(map[string]any{"name": tString()})),
			resp: jsonBody("Sync snapshot with the joined participant's id", tObject())}},
		{"POST", "/api/v1/drafts/{id}/session/poll", s.handleDraftSessionPoll, apiOp{
			summary: "Drain a participant's buffered frames and read the current roster and locks — the request/response read side for an agent with no live stream, and its liveness signal (ADR-0140 M2)", tag: "Live Sessions", role: RoleModeler,
			req:  jsonBody("Polling participant", schemaObj(map[string]any{"participantId": tString()}, "participantId")),
			resp: jsonBody("Roster, locks, and buffered events", tObject())}},
		{"POST", "/api/v1/drafts/{id}/session/leave", s.handleDraftSessionLeave, apiOp{
			summary: "Leave a draft's live session, releasing the participant's locks — idempotent (ADR-0140 M2)", tag: "Live Sessions", role: RoleModeler,
			req:    jsonBody("Leaving participant", schemaObj(map[string]any{"participantId": tString()}, "participantId")),
			status: http.StatusNoContent}},
		{"POST", "/api/v1/drafts/{id}/session/presence", s.handleDraftSessionPresence, apiOp{
			summary: "Update a participant's presence (selected element) in a draft's live session (ADR-0140)", tag: "Live Sessions", role: RoleModeler,
			req: jsonBody("Presence update", schemaObj(map[string]any{
				"participantId": tString(), "selection": tString(),
			}, "participantId")),
			status: http.StatusNoContent}},
		{"POST", "/api/v1/drafts/{id}/session/lock", s.handleDraftSessionLock, apiOp{
			summary: "Acquire or release a per-element edit lock in a draft's live session; acquiring an element another participant holds is a 409 (ADR-0140)", tag: "Live Sessions", role: RoleModeler,
			req: jsonBody("Lock action", schemaObj(map[string]any{
				"participantId": tString(), "elementId": tString(), "action": tString(),
			}, "participantId", "elementId", "action")),
			status: http.StatusNoContent}},
		{"POST", "/api/v1/drafts/{id}/session/change", s.handleDraftSessionChange, apiOp{
			summary: "Broadcast an element change to a draft's live session participants — relayed live, not persisted (ADR-0140)", tag: "Live Sessions", role: RoleModeler,
			req: jsonBody("Element change", schemaObj(map[string]any{
				"participantId": tString(), "elementId": tString(), "xml": tString(),
			}, "participantId", "elementId")),
			status: http.StatusNoContent}},

		{"POST", "/api/v1/forms", s.handleSaveForm, apiOp{
			summary: "Save a form definition. \"from\" names the form being edited (empty for a never-saved one): a changed id then renames the form instead of leaving a second copy behind, and a save onto an id another form already holds is refused with 409 (ADR-0222). Omit \"from\" for the plain upsert-by-id an import or an agent wants", tag: "Forms", role: RoleModeler, req: jsonBody("Form", tObject()), resp: jsonBody("Saved form", tObject())}},
		{"GET", "/api/v1/forms", s.handleListForms, apiOp{
			summary: "List form definitions", tag: "Forms", role: roleAny, resp: jsonBody("Forms", tArray())}},
		{"GET", "/api/v1/forms/{id}/availability", s.handleFormIDAvailability, apiOp{
			summary: "Report whether a form id is free — the live check behind the form editor's ID field, so a collision shows while the id is typed rather than at Save", tag: "Forms", role: RoleModeler, resp: jsonBody("Availability", tObject())}},
		{"GET", "/api/v1/forms/{id}", s.handleGetForm, apiOp{
			summary: "Fetch a form definition", tag: "Forms", role: roleAny, resp: jsonBody("Form", tObject())}},
		{"DELETE", "/api/v1/forms/{id}", s.handleDeleteForm, apiOp{
			summary: "Delete a form definition", tag: "Forms", role: RoleModeler, resp: jsonBody("Deleted id", tObject())}},

		// Panorama architecture models (ADR-0189) are application-owned Open Group
		// ArchiMate Model Exchange documents. Metadata and XML travel separately so a
		// listing never hauls every landscape document through the browser.
		{"POST", "/api/v1/panorama/validate", s.panorama.HandleValidate, apiOp{
			summary: "Validate an ArchiMate Open Exchange document without storing it (ADR-0189)", tag: "Panorama", role: RoleModeler,
			req: xmlBody("ArchiMate Open Exchange XML"), resp: jsonBody("Validation result", tObject())}},
		// The derived landscape mesh (ADR-0211): computed from this server's own
		// resources per requesting principal, never stored, and never mixed into the
		// ArchiMate documents above. Its status block declares which of ADR-0189 §6's
		// observation states this build cannot produce, so a consumer knows what the
		// absence of a finding is worth.
		{"GET", "/api/v1/panorama/mesh", s.panoramaMesh.HandleGraph, apiOp{
			summary: "Derive the landscape mesh from this server's resources with severity, filtered for the caller (ADR-0211)", tag: "Panorama", role: RoleModeler,
			resp: jsonBody("Derived landscape graph", tObject())}},
		{"GET", "/api/v1/panorama/models", s.panorama.HandleList, apiOp{
			summary: "List application-owned Panorama model metadata visible to the caller (ADR-0189)", tag: "Panorama", role: RoleModeler,
			resp: jsonBody("Panorama models", tArray())}},
		{"POST", "/api/v1/panorama/models", s.panorama.HandleCreate, apiOp{
			summary: "Import an ArchiMate Open Exchange document as a Panorama model (ADR-0189)", tag: "Panorama", role: RoleModeler,
			status: http.StatusCreated,
			req: jsonBody("Panorama model import", schemaObj(map[string]any{
				"applicationId": tString(), "name": tString(), "notation": tString(), "xml": tString(),
			}, "applicationId", "xml")), resp: jsonBody("Created Panorama model metadata", tObject())}},
		{"GET", "/api/v1/panorama/models/{id}", s.panorama.HandleGet, apiOp{
			summary: "Fetch Panorama model metadata without its XML (ADR-0189)", tag: "Panorama", role: RoleModeler,
			resp: jsonBody("Panorama model metadata", tObject())}},
		{"PUT", "/api/v1/panorama/models/{id}", s.panorama.HandleUpdate, apiOp{
			summary: "Update a Panorama model using optimistic revision matching (ADR-0189)", tag: "Panorama", role: RoleModeler,
			req: jsonBody("Panorama model update", schemaObj(map[string]any{
				"expectedRevision": tInteger(), "name": tString(), "xml": tString(),
			}, "expectedRevision")), resp: jsonBody("Updated Panorama model metadata", tObject())}},
		{"DELETE", "/api/v1/panorama/models/{id}", s.panorama.HandleDelete, apiOp{
			summary: "Delete a Panorama model (ADR-0189)", tag: "Panorama", role: RoleModeler, status: http.StatusNoContent}},
		// Atlas bindings (ADR-0189 §4): which Atlas resource an ArchiMate element
		// refers to. Read resolves ids to names for this caller; write sets one key
		// on one element and leaves the rest of the document byte-for-byte alone.
		// The C4 projection (ADR-0211 §8). Read-only by construction: ArchiMate stays
		// the only authored notation, and there is deliberately no write counterpart.
		{"GET", "/api/v1/panorama/models/{id}/c4", s.panorama.HandleC4, apiOp{
			summary: "Project a Panorama model into C4, reporting what the mapping cannot express (ADR-0211)", tag: "Panorama", role: RoleModeler,
			resp: jsonBody("C4 projection", tObject())}},
		// The observation projection (ADR-0189 §6): the same bindings, read for what
		// the instance is doing rather than for names. It writes nothing — the stored
		// document is never mutated by observing it — and it is a separate route from
		// the model so a caller who wants the drawing does not pay for a scan of
		// every live instance.
		{"GET", "/api/v1/panorama/models/{id}/observations", s.panorama.HandleObservations, apiOp{
			summary: "Observe what a Panorama model's bound Atlas resources are currently doing (ADR-0189)",
			tag:     "Panorama", role: RoleModeler,
			resp: jsonBody("Observation document", tObject())}},
		// What has been seen to change (ADR-0189 P5). A separate route from the
		// observations because the two cost different things: "what is happening"
		// reads the engine, "what changed" reads what previous answers established.
		{"GET", "/api/v1/panorama/models/{id}/drift", s.panorama.HandleDrift, apiOp{
			summary: "What has been seen to change about a Panorama model's bound resources, newest first (ADR-0189)",
			tag:     "Panorama", role: RoleModeler,
			resp: jsonBody("Drift journal", tObject())}},
		// Historical context from stores outside Atlas (ADR-0189 P5b). Element-scoped
		// because every bound value costs a query against somebody else's cluster,
		// and a model-wide answer would multiply that by the whole landscape.
		{"GET", "/api/v1/panorama/models/{id}/context", s.panorama.HandleContext, apiOp{
			summary: "Read historical context for one element from the stores outside Atlas (ADR-0189)",
			tag:     "Panorama", role: RoleModeler,
			resp: jsonBody("Historical context document", tObject())}},
		// The authoring subset (ADR-0189 §2): the palette and the relationship matrix
		// the canvas enforces. Served rather than duplicated in the browser, so the
		// rule the canvas applies during a drag and the rule the server applies on
		// write cannot disagree.
		{"GET", "/api/v1/panorama/subset", s.panorama.HandleSubset, apiOp{
			summary: "The ArchiMate element and relationship subset Atlas can author (ADR-0189)",
			tag:     "Panorama", role: RoleModeler,
			resp: jsonBody("Authoring subset and relationship matrix", tObject())}},
		// Moving shapes on a view (ADR-0189 §2). Separate from the model update
		// because it can do exactly one thing: the canvas sends what moved, never a
		// document, so a browser's serialiser can never rewrite somebody's model.
		{"PUT", "/api/v1/panorama/models/{id}/layout", s.panorama.HandleSetLayout, apiOp{
			summary: "Write new positions for shapes on a Panorama view (ADR-0189)",
			tag:     "Panorama", role: RoleModeler,
			req:  jsonBody("Moved shapes and the revision they were read at", tObject()),
			resp: jsonBody("The updated model summary", tObject())}},
		// Creating content (ADR-0189 §2). Two routes rather than one: adding a box and
		// joining two boxes fail in different ways and refuse for different reasons.
		// Neither takes a document — the canvas sends what it did, and the server
		// writes it, so §2's round-trip guarantee stays where the document is.
		{"POST", "/api/v1/panorama/models/{id}/elements", s.panorama.HandleAddElement, apiOp{
			summary: "Create an ArchiMate element and place it on a view (ADR-0189)",
			tag:     "Panorama", role: RoleModeler,
			req:  jsonBody("The element to create and where it goes", tObject()),
			resp: jsonBody("The updated model and the shape's identifier", tObject())}},
		{"POST", "/api/v1/panorama/models/{id}/relationships", s.panorama.HandleAddRelationship, apiOp{
			summary: "Draw a relationship between two elements on a view, within the authoring subset (ADR-0189)",
			tag:     "Panorama", role: RoleModeler,
			req:  jsonBody("The relationship to draw", tObject()),
			resp: jsonBody("The updated model and the relationship's identifier", tObject())}},
		{"GET", "/api/v1/panorama/models/{id}/bindings", s.panorama.HandleBindings, apiOp{
			summary: "Resolve a Panorama model's Atlas bindings for the caller (ADR-0189)", tag: "Panorama", role: RoleModeler,
			resp: jsonBody("Resolved Atlas bindings", tObject())}},
		{"GET", "/api/v1/panorama/models/{id}/bindings/candidates", s.panorama.HandleBindingCandidates, apiOp{
			summary: "List the Atlas resources the caller may bind one key to (ADR-0189)", tag: "Panorama", role: RoleModeler,
			resp: jsonBody("Binding candidates", tObject())}},
		{"PUT", "/api/v1/panorama/models/{id}/bindings", s.panorama.HandleSetBinding, apiOp{
			summary: "Set one Atlas binding on one ArchiMate element (ADR-0189)", tag: "Panorama", role: RoleModeler,
			req: jsonBody("Binding assignment", schemaObj(map[string]any{
				"expectedRevision": tInteger(), "elementId": tString(), "key": tString(), "values": tArray(),
			}, "expectedRevision", "elementId", "key")), resp: jsonBody("Updated Panorama model metadata", tObject())}},
		{"GET", "/api/v1/panorama/models/{id}/xml", s.panorama.HandleXML, apiOp{
			summary: "Export a Panorama model as its original ArchiMate Open Exchange XML (ADR-0189)", tag: "Panorama", role: RoleModeler,
			resp: xmlBody("ArchiMate Open Exchange XML")}},
		{"GET", "/api/v1/data-objects", s.handleDataObjectsAcrossInstances, apiOp{
			summary: "The data-centric index: which instances carry which data, newest instance first — the landscape read from the data's side rather than the process's. Filter with ?class= (the declared itemSubjectRef type) and ?key= (the business key, which is what makes a datum the same one across processes); ?history=true also sweeps finished instances. The answer says how many instances it examined and whether a bound stopped it", tag: "Information model", role: RoleOperator,
			resp: jsonBody("Data objects across instances", tObject())}},
		{"GET", "/api/v1/infomodel/subset", s.infomodel.HandleSubset, apiOp{
			summary: "Read the information model's authoring subset — the class kinds, association kinds, primitive types and multiplicities this build authors, the matrix of what may be drawn between what, and what it deliberately does not author (ADR-draft-process-information-model)", tag: "Information model", role: RoleModeler,
			resp: jsonBody("Authoring subset", tObject())}},
		{"GET", "/api/v1/infomodel/models", s.infomodel.HandleList, apiOp{
			summary: "List information models — the UML class-diagram documents that give a BPMN data object's itemSubjectRef a type to resolve against; filter with ?applicationId=", tag: "Information model", role: RoleModeler,
			resp: jsonBody("Information models", tArray())}},
		{"POST", "/api/v1/infomodel/models", s.infomodel.HandleCreate, apiOp{
			summary: "Start an empty information model for a process application", tag: "Information model", role: RoleModeler,
			req: jsonBody("New information model", schemaObj(map[string]any{
				"applicationId": tString(), "name": tString(), "documentation": tString(),
			}, "applicationId", "name")),
			resp: jsonBody("Information model", tObject()), status: http.StatusCreated}},
		{"GET", "/api/v1/infomodel/models/{id}", s.infomodel.HandleGet, apiOp{
			summary: "Read one information model whole — its classes, their attributes and business keys, its associations, and the validation verdict on all of it", tag: "Information model", role: RoleModeler,
			resp: jsonBody("Information model", tObject())}},
		{"PUT", "/api/v1/infomodel/models/{id}", s.infomodel.HandleUpdate, apiOp{
			summary: "Replace an information model's content. The whole document is sent; a model that does not validate is refused with its findings, and a stale revision is refused as a conflict", tag: "Information model", role: RoleModeler,
			req: jsonBody("Information model content", schemaObj(map[string]any{
				"name": tString(), "documentation": tString(), "classes": tArray(),
				"associations": tArray(), "stores": tArray(), "revision": tInteger(),
			})),
			resp: jsonBody("Information model", tObject())}},
		{"DELETE", "/api/v1/infomodel/models/{id}", s.infomodel.HandleDelete, apiOp{
			summary: "Delete an information model", tag: "Information model", role: RoleModeler,
			status: http.StatusNoContent}},
		{"GET", "/api/v1/infomodel/models/{id}/schema", s.infomodel.HandleSchema, apiOp{
			summary: "Project one class (?class=Order) to a JSON Schema — the derived, read-only contract a value of that class is checked against, together with what the projection could not carry", tag: "Information model", role: RoleModeler,
			resp: jsonBody("JSON Schema projection", tObject())}},
		{"POST", "/api/v1/public-links", s.handleCreatePublicLink, apiOp{
			summary: "Publish a process: mint a public start link (ADR-0029)", tag: "Forms", role: RoleModeler,
			req:  jsonBody("Target", schemaObj(map[string]any{"processId": tString()}, "processId")),
			resp: jsonBody("Public link", tObject())}},
		{"GET", "/api/v1/public-links", s.handleListPublicLinks, apiOp{
			summary: "List public start links", tag: "Forms", role: RoleModeler, resp: jsonBody("Public links", tArray())}},
		{"DELETE", "/api/v1/public-links/{token}", s.handleRevokePublicLink, apiOp{
			summary: "Revoke a public start link", tag: "Forms", role: RoleModeler, resp: jsonBody("Revoked token", tObject())}},

		// Process documentation (ADR-0143): a process published as one structured PDF
		// — the diagram plus every element's documentation and annotations — as an
		// immutable, per-process numbered version, optionally shared through a
		// revocable public link. The document is produced in the browser, where
		// bpmn-js already holds the authoritative picture; the server validates,
		// numbers, stores, and serves it.
		{"POST", "/api/v1/processes/{processId}/documentation", s.processDocs.HandleCreate, apiOp{
			summary: "Publish the next documentation version of a process: the produced PDF plus the element prose it describes (ADR-0143)", tag: "Documentation", role: RoleModeler,
			req: jsonBody("Documentation upload", schemaObj(map[string]any{
				"title": tString(), "note": tString(), "processName": tString(),
				"xml": tString(), "elements": tArray(), "pdfBase64": tString(),
			}, "pdfBase64")),
			resp: jsonBody("The minted documentation version", tObject())}},
		{"GET", "/api/v1/processes/{processId}/documentation", s.processDocs.HandleList, apiOp{
			summary: "A process's documentation history, newest version first (ADR-0143)", tag: "Documentation", role: roleAny,
			resp: jsonBody("Documentation versions", tArray())}},
		{"POST", "/api/v1/processes/{processId}/documentation/prune", s.processDocs.HandlePrune, apiOp{
			summary: "Prune a process's documentation history to the newest `keep` versions, deleting older ones and their PDFs (ADR-0143 retention)", tag: "Documentation", role: RoleModeler,
			req: jsonBody("Retention limit", schemaObj(map[string]any{
				"keep": tInteger(),
			}, "keep")),
			resp: jsonBody("The versions that were pruned", tObject())}},
		{"GET", "/api/v1/documentation/{id}", s.processDocs.HandleGet, apiOp{
			summary: "Fetch one documentation version in full: metadata, per-element prose, and the BPMN source it was produced from (ADR-0143)", tag: "Documentation", role: roleAny,
			resp: jsonBody("Documentation version", tObject())}},
		{"GET", "/api/v1/documentation/{id}/pdf", s.processDocs.HandleGetPDF, apiOp{
			summary: "Download a documentation version's PDF (ADR-0143)", tag: "Documentation", role: roleAny,
			resp: &bodySpec{mediaType: "application/pdf", schema: tString(), desc: "The published PDF document"}}},
		{"POST", "/api/v1/documentation/{id}/share", s.processDocs.HandleShare, apiOp{
			summary: "Share one documentation version: mint (or return) its revocable public link. Idempotent — a URL readers already hold never rotates (ADR-0143)", tag: "Documentation", role: RoleModeler,
			resp: jsonBody("The version with its share link", tObject())}},
		{"DELETE", "/api/v1/documentation/{id}/share", s.processDocs.HandleUnshare, apiOp{
			summary: "Revoke a documentation version's public link (ADR-0143)", tag: "Documentation", role: RoleModeler,
			resp: jsonBody("The version, now private", tObject())}},
		{"DELETE", "/api/v1/documentation/{id}", s.processDocs.HandleDelete, apiOp{
			summary: "Prune a documentation version, taking its public link with it (ADR-0143)", tag: "Documentation", role: RoleModeler,
			status: http.StatusNoContent}},

		// Process applications (ADR-0128) are the ADR-0034 project reframed as the
		// design-time unit of bundling, versioning, and portability. The canonical
		// surface is /api/v1/applications; each route binds to the same handler as
		// its /api/v1/projects twin below, which is kept as a deprecated alias for
		// one release so existing callers and saved MCP scripts keep working. The
		// on-disk store stays `projects/` and the artifact tag stays `projectId`
		// (zero migration) — the rename is at the API/UI boundary only.
		{"POST", "/api/v1/applications", s.handleCreateProject, apiOp{
			summary: "Create a process application (ADR-0128)", tag: "Applications", role: RoleModeler, req: jsonBody("Application", tObject()), resp: jsonBody("Created application", tObject())}},
		{"GET", "/api/v1/applications", s.handleListProjects, apiOp{
			summary: "List process applications", tag: "Applications", role: roleAny, resp: jsonBody("Applications", tArray())}},
		{"PATCH", "/api/v1/applications/{id}", s.handleUpdateProject, apiOp{
			summary: "Update an application: rename, set visibility (private/shared), or transfer ownership (ADR-0071)", tag: "Applications", role: RoleModeler,
			req:  jsonBody("Update", schemaObj(map[string]any{"name": tString(), "visibility": tString(), "ownerId": tString()})),
			resp: jsonBody("Updated application", tObject())}},
		{"DELETE", "/api/v1/applications/{id}", s.handleDeleteProject, apiOp{
			summary: "Delete a process application", tag: "Applications", role: RoleModeler, status: http.StatusNoContent}},
		{"PUT", "/api/v1/applications/{id}/members/{userId}", s.handleSetProjectMember, apiOp{
			summary: "Share an application with a user, or change their role (ADR-0071)", tag: "Applications", role: RoleModeler,
			req:  jsonBody("Member role", schemaObj(map[string]any{"role": tString()}, "role")),
			resp: jsonBody("Updated application", tObject())}},
		{"DELETE", "/api/v1/applications/{id}/members/{userId}", s.handleRemoveProjectMember, apiOp{
			summary: "Revoke a user's membership on an application (ADR-0071)", tag: "Applications", role: RoleModeler, resp: jsonBody("Updated application", tObject())}},
		{"POST", "/api/v1/applications/{id}/validate", s.handleValidateProject, apiOp{
			summary: "Validate an application's artifacts", tag: "Applications", role: RoleModeler, resp: jsonBody("Validation result", tObject())}},
		{"POST", "/api/v1/applications/{id}/deploy", s.handleDeployProject, apiOp{
			summary: "Deploy an application's artifacts as one bundle, without recording a release (ADR-0128)", tag: "Applications", role: RoleModeler, resp: jsonBody("Deploy result", tObject())}},
		{"POST", "/api/v1/applications/{id}/publish", s.handlePublishApplication, apiOp{
			summary: "Publish an application: deploy its artifacts as one bundle and record the next application release (ADR-0128)", tag: "Applications", role: RoleModeler,
			req:  jsonBody("Publish options", schemaObj(map[string]any{"note": tString()})),
			resp: jsonBody("Publish result with the minted release", tObject())}},
		{"GET", "/api/v1/applications/{id}/releases", s.handleListReleases, apiOp{
			summary: "An application's release history, newest first (ADR-0128)", tag: "Applications", role: roleAny, resp: jsonBody("Releases", tArray())}},
		{"GET", "/api/v1/applications/{id}/audit", s.handleListProjectAudit, apiOp{
			summary: "An application's access-control history — shares, revokes, visibility flips, and ownership transfers, newest first; owner-only (ADR-0071)", tag: "Applications", role: RoleModeler, resp: jsonBody("Grant audit events", tArray())}},
		{"GET", "/api/v1/applications/{id}/deployments", s.handleApplicationDeployments, apiOp{
			summary: "What this application currently has deployed on this server, with per-definition instance counts (ADR-0128)", tag: "Applications", role: roleAny, resp: jsonBody("Application deployments", tObject())}},

		{"POST", "/api/v1/applications/import", s.handleImportBundle, apiOp{
			summary: "Receive a published application bundle from a peer Atlas: validate and deploy it all-or-nothing, then record the publisher's release (ADR-0129). The only operation a deploy token may reach.", tag: "Applications", role: RoleModeler,
			req: jsonBody("Bundle", schemaObj(map[string]any{
				"application": tString(), "release": tObject(), "artifacts": tArray(),
			}, "application", "release", "artifacts")),
			resp: jsonBody("Import result", tObject())}},

		{"POST", "/api/v1/applications/{id}/releases/{version}/promote", s.handlePromoteRelease, apiOp{
			summary: "Promote an existing release to one or more deployment targets: ship the frozen artifacts to peer Atlas servers, reported per target (ADR-0129)", tag: "Applications", role: RoleModeler,
			req:  jsonBody("Targets", schemaObj(map[string]any{"targetIds": tArray()}, "targetIds")),
			resp: jsonBody("Per-target promotion results", tObject())}},

		{"GET", "/api/v1/applications/{id}/source", s.handleExportApplicationSource, apiOp{
			summary: "Download an application's source — its drafts, forms, and decision references — as the curated source layout (a manifest plus native .bpmn and .form.json files) in a gzip tar (ADR-0134)", tag: "Applications", role: RoleModeler,
			resp: &bodySpec{mediaType: "application/gzip", schema: tString(), desc: "A gzip-compressed tar of the application's source tree"}}},
		{"POST", "/api/v1/applications/source", s.handleImportApplicationSource, apiOp{
			summary: "Read a source tree into this server. The application is identified by the portable key in the manifest — created when this server has never seen it, updated in place when it has. Never deletes: local artifacts the tree omits are reported, not removed (ADR-0134).", tag: "Applications", role: RoleModeler,
			req:  &bodySpec{mediaType: "application/gzip", schema: tString(), desc: "A gzip tar of a source tree, as produced by GET /api/v1/applications/{id}/source"},
			resp: jsonBody("Import result", tObject())}},

		{"GET", "/api/v1/applications/{id}/targets", s.handleApplicationTargets, apiOp{
			summary: "What each configured deployment target currently runs for this application; best-effort, an unreachable peer is reported as such (ADR-0129)", tag: "Applications", role: RoleModeler,
			resp: jsonBody("Per-target status", tArray())}},

		{"POST", "/api/v1/targets", s.handleCreateTarget, apiOp{
			summary: "Register a deployment target: a peer Atlas this server can promote releases to; the credential is stored by reference, never by value (admin-only, ADR-0129)", tag: "Deployment targets", role: RoleAdmin,
			req: jsonBody("Target", schemaObj(map[string]any{
				"name": tString(), "baseUrl": tString(), "kind": tString(), "credentialRef": tString(),
			}, "name", "baseUrl")),
			resp: jsonBody("Created target", tObject())}},
		{"GET", "/api/v1/targets", s.handleListTargets, apiOp{
			summary: "List deployment targets and the application bindings learned from them (ADR-0129)", tag: "Deployment targets", role: RoleModeler,
			resp: jsonBody("Targets", tArray())}},
		{"DELETE", "/api/v1/targets/{id}", s.handleDeleteTarget, apiOp{
			summary: "Remove a deployment target (admin-only, ADR-0129)", tag: "Deployment targets", role: RoleAdmin, status: http.StatusNoContent}},

		{"POST", "/api/v1/deploy-tokens", s.handleCreateDeployToken, apiOp{
			summary: "Mint a deploy token for a peer Atlas to publish here; the secret is returned once and never again (admin-only, ADR-0129)", tag: "Deploy tokens", role: RoleAdmin,
			req:  jsonBody("Token name", schemaObj(map[string]any{"name": tString()}, "name")),
			resp: jsonBody("Minted token, including its one-time secret", tObject())}},
		{"GET", "/api/v1/deploy-tokens", s.handleListDeployTokens, apiOp{
			summary: "List deploy tokens by identity and provenance; secrets are not stored and never returned (admin-only, ADR-0129)", tag: "Deploy tokens", role: RoleAdmin,
			resp: jsonBody("Deploy tokens", tArray())}},
		{"DELETE", "/api/v1/deploy-tokens/{id}", s.handleRevokeDeployToken, apiOp{
			summary: "Revoke a deploy token, effective immediately (admin-only, ADR-0129)", tag: "Deploy tokens", role: RoleAdmin,
			status: http.StatusNoContent}},

		{"POST", "/api/v1/api-tokens", s.handleCreateAPIToken, apiOp{
			summary: "Mint an API token for a machine — a worker on another host, a stdio MCP adapter, a CI job. The secret is returned once and never again; the scope bounds what it may reach and the lifetime when it stops working (admin-only, ADR-0194)", tag: "API tokens", role: RoleAdmin,
			req: jsonBody("Token name, scope (full|worker) and lifetime in days (0 = never expires)", schemaObj(map[string]any{
				"name": tString(), "scope": tString(), "expiresInDays": tInteger(),
			}, "name", "scope")),
			resp: jsonBody("Minted token, including its one-time secret", tObject())}},
		{"GET", "/api/v1/api-tokens", s.handleListAPITokens, apiOp{
			summary: "List API tokens by identity, scope, lifetime and provenance; secrets are not stored and never returned (admin-only, ADR-0194)", tag: "API tokens", role: RoleAdmin,
			resp: jsonBody("API tokens", tArray())}},
		{"DELETE", "/api/v1/api-tokens/{id}", s.handleRevokeAPIToken, apiOp{
			summary: "Revoke an API token, effective immediately (admin-only, ADR-0194)", tag: "API tokens", role: RoleAdmin,
			status: http.StatusNoContent}},

		{"POST", "/api/v1/oauth-clients", s.handleRegisterOAuthClient, apiOp{
			summary: "Register an OAuth client — a hosted application allowed to ask a person for access. The secret is returned once and never again (admin-only, ADR-0200)", tag: "OAuth", role: RoleAdmin,
			req: jsonBody("Client name and the exact redirect URIs it may be sent back to", schemaObj(map[string]any{
				"name": tString(), "redirectUris": tArray(),
			}, "name", "redirectUris")),
			resp: jsonBody("Registered client, including its one-time secret", tObject())}},
		{"GET", "/api/v1/oauth-clients", s.handleListOAuthClients, apiOp{
			summary: "List registered OAuth clients; secrets are not stored and never returned (admin-only, ADR-0200)", tag: "OAuth", role: RoleAdmin,
			resp: jsonBody("OAuth clients", tArray())}},
		{"DELETE", "/api/v1/oauth-clients/{id}", s.handleDeleteOAuthClient, apiOp{
			summary: "Remove an OAuth client and revoke every grant approved for it (admin-only, ADR-0200)", tag: "OAuth", role: RoleAdmin,
			status: http.StatusNoContent}},
		{"GET", "/api/v1/oauth-grants", s.handleListOAuthGrants, apiOp{
			summary: "List standing OAuth approvals — your own, or everyone's for an administrator (ADR-0200)", tag: "OAuth", role: roleAny,
			resp: jsonBody("OAuth grants", tArray())}},
		{"DELETE", "/api/v1/oauth-grants/{id}", s.handleRevokeOAuthGrant, apiOp{
			summary: "Withdraw an OAuth approval, effective on the next request. Your own, or anyone's for an administrator (ADR-0200)", tag: "OAuth", role: roleAny,
			status: http.StatusNoContent}},
		{"GET", "/api/v1/oauth/authorize-context", s.handleAuthorizeContext, apiOp{
			summary: "What the consent screen is being asked to approve: the client, the resource, and who is signed in (ADR-0200)", tag: "OAuth", role: roleAny,
			resp: jsonBody("Consent context", tObject())}},
		{"POST", "/api/v1/oauth/authorize", s.handleApprove, apiOp{
			summary: "Record a person's decision on an authorization request and return where their browser should go next (ADR-0200)", tag: "OAuth", role: roleAny,
			req:  jsonBody("The authorization request, repeated, plus the decision", tObject()),
			resp: jsonBody("Where to send the browser", tObject())}},

		// Deprecated aliases (ADR-0128): the pre-rename /projects surface. Same
		// handlers as /applications above; retained for one release for compat.
		{"POST", "/api/v1/projects", s.handleCreateProject, apiOp{
			summary: "Create a project (deprecated: use POST /api/v1/applications)", tag: "Projects", role: RoleModeler, deprecated: true, req: jsonBody("Project", tObject()), resp: jsonBody("Created project", tObject())}},
		{"GET", "/api/v1/projects", s.handleListProjects, apiOp{
			summary: "List projects (deprecated: use GET /api/v1/applications)", tag: "Projects", role: roleAny, deprecated: true, resp: jsonBody("Projects", tArray())}},
		{"PATCH", "/api/v1/projects/{id}", s.handleUpdateProject, apiOp{
			summary: "Update a project (deprecated: use PATCH /api/v1/applications/{id})", tag: "Projects", role: RoleModeler, deprecated: true,
			req:  jsonBody("Update", schemaObj(map[string]any{"name": tString(), "visibility": tString(), "ownerId": tString()})),
			resp: jsonBody("Updated project", tObject())}},
		{"DELETE", "/api/v1/projects/{id}", s.handleDeleteProject, apiOp{
			summary: "Delete a project (deprecated: use DELETE /api/v1/applications/{id})", tag: "Projects", role: RoleModeler, deprecated: true, status: http.StatusNoContent}},
		{"PUT", "/api/v1/projects/{id}/members/{userId}", s.handleSetProjectMember, apiOp{
			summary: "Share a project with a user (deprecated: use PUT /api/v1/applications/{id}/members/{userId})", tag: "Projects", role: RoleModeler, deprecated: true,
			req:  jsonBody("Member role", schemaObj(map[string]any{"role": tString()}, "role")),
			resp: jsonBody("Updated project", tObject())}},
		{"DELETE", "/api/v1/projects/{id}/members/{userId}", s.handleRemoveProjectMember, apiOp{
			summary: "Revoke a user's membership on a project (deprecated: use DELETE /api/v1/applications/{id}/members/{userId})", tag: "Projects", role: RoleModeler, deprecated: true, resp: jsonBody("Updated project", tObject())}},
		{"POST", "/api/v1/projects/{id}/validate", s.handleValidateProject, apiOp{
			summary: "Validate a project's artifacts (deprecated: use POST /api/v1/applications/{id}/validate)", tag: "Projects", role: RoleModeler, deprecated: true, resp: jsonBody("Validation result", tObject())}},
		{"POST", "/api/v1/projects/{id}/deploy", s.handleDeployProject, apiOp{
			summary: "Deploy a project's artifacts (deprecated: use POST /api/v1/applications/{id}/deploy)", tag: "Projects", role: RoleModeler, deprecated: true, resp: jsonBody("Deploy result", tObject())}},
		{"GET", "/api/v1/projects/{id}/audit", s.handleListProjectAudit, apiOp{
			summary: "A project's access-control history (deprecated: use GET /api/v1/applications/{id}/audit)", tag: "Projects", role: RoleModeler, deprecated: true, resp: jsonBody("Grant audit events", tArray())}},

		{"POST", "/api/v1/playground/sessions", s.playground.HandleOpen, apiOp{
			summary: "Open a Playground session on a draft, a deployed definition, or an inline model", tag: "Playground", role: RoleModeler,
			req: jsonBody("Session to open", tObject()), resp: jsonBody("Session", tObject())}},
		{"GET", "/api/v1/playground/sessions/{id}", s.playground.HandleStatus, apiOp{
			summary: "Read a Playground session's state", tag: "Playground", role: RoleModeler, resp: jsonBody("Session", tObject())}},
		{"DELETE", "/api/v1/playground/sessions/{id}", s.playground.HandleClose, apiOp{
			summary: "Close a Playground session and discard its sandbox", tag: "Playground", role: RoleModeler, resp: jsonBody("Closed", tObject())}},
		{"POST", "/api/v1/playground/sessions/{id}/cases", s.playground.HandleStartCase, apiOp{
			summary: "Start one case in a Playground session", tag: "Playground", role: RoleModeler,
			req: jsonBody("Start variables", tObject()), resp: jsonBody("Case", tObject())}},
		{"GET", "/api/v1/playground/sessions/{id}/cases/{caseKey}", s.playground.HandleCase, apiOp{
			summary: "Read what became of one Playground case", tag: "Playground", role: RoleModeler, resp: jsonBody("Case", tObject())}},
		{"POST", "/api/v1/playground/sessions/{id}/run", s.playground.HandleRun, apiOp{
			summary: "Run a Playground session until it comes to rest, its budget stops it, or it is paused", tag: "Playground", role: RoleModeler,
			resp: jsonBody("Progress", tObject())}},
		{"POST", "/api/v1/playground/sessions/{id}/step", s.playground.HandleStep, apiOp{
			summary: "Carry out exactly one occurrence in a Playground session", tag: "Playground", role: RoleModeler, resp: jsonBody("Occurrence", tObject())}},
		{"POST", "/api/v1/playground/sessions/{id}/pause", s.playground.HandlePause, apiOp{
			summary: "Hold a Playground run at its next occurrence", tag: "Playground", role: RoleModeler, resp: jsonBody("Paused", tObject())}},
		{"POST", "/api/v1/playground/sessions/{id}/resume", s.playground.HandleResume, apiOp{
			summary: "Let a paused Playground session run again", tag: "Playground", role: RoleModeler, resp: jsonBody("Paused", tObject())}},
		{"POST", "/api/v1/playground/sessions/{id}/clock", s.playground.HandleAdvanceClock, apiOp{
			summary: "Jump a Playground session's simulated clock and fire what came due", tag: "Playground", role: RoleModeler,
			req: jsonBody("Advance", tObject()), resp: jsonBody("Simulated time", tObject())}},
		{"POST", "/api/v1/playground/sessions/{id}/messages", s.playground.HandlePublishMessage, apiOp{
			summary: "Publish a message into a Playground session", tag: "Playground", role: RoleModeler,
			req: jsonBody("Message", tObject()), resp: jsonBody("Published", tObject())}},
		{"GET", "/api/v1/playground/sessions/{id}/tasks", s.playground.HandleTasks, apiOp{
			summary: "List the Playground jobs waiting for a person", tag: "Playground", role: RoleModeler, resp: jsonBody("Tasks", tArray())}},
		{"POST", "/api/v1/playground/sessions/{id}/tasks/{jobKey}/complete", s.playground.HandleCompleteTask, apiOp{
			summary: "Complete a parked Playground task by hand", tag: "Playground", role: RoleModeler,
			req: jsonBody("Output variables", tObject()), resp: jsonBody("Completed", tObject())}},
		{"GET", "/api/v1/playground/sessions/{id}/overlay", s.playground.HandleOverlay, apiOp{
			summary: "Read a Playground run's per-element visit counts", tag: "Playground", role: RoleModeler, resp: jsonBody("Element visits", tObject())}},
		{"GET", "/api/v1/playground/sessions/{id}/heatmap", s.playground.HandleHeatMap, apiOp{
			summary: "Read a Playground run's element and sequence-flow token counts, including the paths it never took",
			tag:     "Playground", role: RoleModeler, resp: jsonBody("Heat map", tObject())}},
		{"POST", "/api/v1/playground/sessions/{id}/runs", s.playground.HandleStartRun, apiOp{
			summary: "Start a Playground batch over a dataset listed inline or described field by field", tag: "Playground", role: RoleModeler,
			req: jsonBody("Cases or a dataset description, and an arrival profile", tObject()), resp: jsonBody("Run status", tObject())}},
		{"POST", "/api/v1/playground/sessions/{id}/generate", s.playground.HandleGeneratePreview, apiOp{
			summary: "Preview the first cases a Playground dataset description would produce", tag: "Playground", role: RoleModeler,
			req: jsonBody("A dataset description", tObject()), resp: jsonBody("The first generated cases", tObject())}},
		{"POST", "/api/v1/playground/sessions/{id}/arrivals", s.playground.HandleArrivalProfile, apiOp{
			summary: "Preview the shape of a Playground arrival stream: how many cases land in each slice of the time it covers",
			tag:     "Playground", role: RoleModeler,
			req: jsonBody("A case count and an arrival profile", tObject()), resp: jsonBody("The stream's shape", tObject())}},
		{"POST", "/api/v1/playground/sessions/{id}/runs/csv", s.playground.HandleStartRunFromCSV, apiOp{
			summary: "Start a Playground batch over an uploaded CSV, one case per row", tag: "Playground", role: RoleModeler,
			resp: jsonBody("Run status", tObject())}},
		{"GET", "/api/v1/playground/sessions/{id}/runs", s.playground.HandleRunStatus, apiOp{
			summary: "Read how far a Playground batch has got", tag: "Playground", role: RoleModeler, resp: jsonBody("Run status", tObject())}},
		{"POST", "/api/v1/playground/sessions/{id}/runs/cancel", s.playground.HandleCancelRun, apiOp{
			summary: "Stop a Playground batch, leaving what it did readable", tag: "Playground", role: RoleModeler, resp: jsonBody("Run status", tObject())}},
		{"GET", "/api/v1/playground/sessions/{id}/report", s.playground.HandleReport, apiOp{
			summary: "Read a Playground run's summary: outcomes, durations, elements and pools", tag: "Playground", role: RoleModeler,
			resp: jsonBody("Run report", tObject())}},
		{"GET", "/api/v1/playground/sessions/{id}/results", s.playground.HandleResults, apiOp{
			summary: "Read one page of a Playground run's per-case results", tag: "Playground", role: RoleModeler,
			resp: jsonBody("Results page", tObject())}},
		{"GET", "/api/v1/playground/sessions/{id}/results.csv", s.playground.HandleResultsCSV, apiOp{
			summary: "Download a Playground run's per-case results as CSV", tag: "Playground", role: RoleModeler,
			resp: csvBody("Results as CSV")}},
		{"POST", "/api/v1/playground/sessions/{id}/verdict", s.playground.HandleVerdict, apiOp{
			summary: "Judge a Playground run against a set of expectations, run-wide and per case", tag: "Playground", role: RoleModeler,
			req: jsonBody("Expectations, including per-case rules in FEEL", tObject()), resp: jsonBody("Verdict", tObject())}},
		{"POST", "/api/v1/playground/sessions/{id}/compare", s.playground.HandleCompare, apiOp{
			summary: "Set a Playground run beside an earlier run's report", tag: "Playground", role: RoleModeler,
			req: jsonBody("A baseline report", tObject()), resp: jsonBody("Comparison", tObject())}},

		{"POST", "/api/v1/playground/scenarios", s.handleSaveScenario, apiOp{
			summary: "Save a Playground scenario: the requests that make a run, and what it must show", tag: "Playground",
			role: RoleModeler, req: jsonBody("Scenario", tObject()), resp: jsonBody("Saved scenario", tObject())}},
		{"GET", "/api/v1/playground/scenarios", s.handleListScenarios, apiOp{
			summary: "List saved Playground scenarios", tag: "Playground", role: RoleModeler,
			resp: jsonBody("Scenarios", tArray())}},
		{"GET", "/api/v1/playground/scenarios/{id}", s.handleGetScenario, apiOp{
			summary: "Read one Playground scenario with its spec and baseline", tag: "Playground", role: RoleModeler,
			resp: jsonBody("Scenario", tObject())}},
		{"PUT", "/api/v1/playground/scenarios/{id}/baseline", s.handleSaveScenarioBaseline, apiOp{
			summary: "Keep a run's report as the scenario's baseline", tag: "Playground", role: RoleModeler,
			req: jsonBody("A run report", tObject()), resp: jsonBody("Saved scenario", tObject())}},
		{"DELETE", "/api/v1/playground/scenarios/{id}", s.handleDeleteScenario, apiOp{
			summary: "Delete a saved Playground scenario", tag: "Playground", role: RoleModeler,
			resp: jsonBody("Deleted", tObject())}},

		{"POST", "/api/v1/dmnrefs", s.handleCreateDmnRef, apiOp{
			summary: "Create a DMN reference artifact", tag: "DMN References", role: RoleModeler, req: jsonBody("DMN reference", tObject()), resp: jsonBody("Created reference", tObject())}},
		{"GET", "/api/v1/dmnrefs", s.handleListDmnRefs, apiOp{
			summary: "List DMN reference artifacts", tag: "DMN References", role: RoleModeler, resp: jsonBody("References", tArray())}},
		{"PATCH", "/api/v1/dmnrefs/{id}", s.handleUpdateDmnRef, apiOp{
			summary: "Update a DMN reference: move it to a project and/or rename it", tag: "DMN References", role: RoleModeler, req: jsonBody("Update", tObject()), resp: jsonBody("Updated reference", tObject())}},
		{"DELETE", "/api/v1/dmnrefs/{id}", s.handleDeleteDmnRef, apiOp{
			summary: "Delete a DMN reference", tag: "DMN References", role: RoleModeler, status: http.StatusNoContent}},
		{"POST", "/api/v1/dmnrefs/{id}/validate", s.handleValidateDmnRef, apiOp{
			summary: "Validate a DMN reference compiles", tag: "DMN References", role: RoleModeler, resp: jsonBody("Validation result", tObject())}},
		{"GET", "/api/v1/decisions", s.handleListDecisions, apiOp{
			summary: "List DMN decisions (with inputs and outputs) available from DMN references", tag: "DMN References", role: RoleModeler, resp: jsonBody("Decisions", tArray())}},
		{"GET", "/api/v1/dmnrefs/{id}/graph", s.handleDmnRefGraph, apiOp{
			summary: "A DMN reference's decision requirements graph for the read-only viewer", tag: "DMN References", role: RoleModeler, resp: jsonBody("Model graph", tObject())}},
		{"GET", "/api/v1/dmn-models/{ref}/xml", s.handleDmnModelXML, apiOp{
			summary: "The raw DMN model XML for a model handle, for the embedded DMN editor", tag: "DMN References", role: RoleModeler, resp: jsonBody("DMN XML", tObject())}},
		{"POST", "/api/v1/dmn-models", s.handleUploadDmnModel, apiOp{
			summary: "Upload a DMN model file into the local model store and return its reference handle", tag: "DMN References", role: RoleModeler, req: jsonBody("DMN XML", tObject()), resp: jsonBody("Stored model", tObject())}},

		// ADR-0203: configured Workers are the design-time configuration resource;
		// /api/v1/workers remains the existing runtime/Operations view above.
		{"GET", "/api/v1/configured-workers", s.handleListConnectors, apiOp{
			summary: "List configured Workers", tag: "Workers", role: roleAny, resp: jsonBody("Configured Workers", tArray())}},
		{"POST", "/api/v1/configured-workers", s.handleCreateConnector, apiOp{
			summary: "Create a configured Worker", tag: "Workers", role: RoleModeler, req: jsonBody("Configured Worker", tObject()), resp: jsonBody("Created configured Worker", tObject())}},
		{"PATCH", "/api/v1/configured-workers/{id}", s.handleUpdateConnector, apiOp{
			summary: "Update a configured Worker (endpoint, provider, sender, credential reference, enabled)", tag: "Workers", role: RoleModeler, req: jsonBody("Configured Worker update", tObject()), resp: jsonBody("Updated configured Worker", tObject())}},
		{"DELETE", "/api/v1/configured-workers/{id}", s.handleDeleteConnector, apiOp{
			summary: "Delete a configured Worker; refused while deployed models still reference it unless ?force=true", tag: "Workers", role: RoleModeler, status: http.StatusNoContent}},
		{"GET", "/api/v1/worker-types", s.handleConnectorKinds, apiOp{
			summary: "List available Worker Types and where this server runs them", tag: "Workers", role: roleAny,
			resp: jsonBody("Worker Type placements", schemaObj(map[string]any{"kinds": tArray()}))}},

		// Legacy connector names remain compatibility aliases during the ADR-0203
		// migration. They bind to the same handlers and stores, so no second source
		// of truth is introduced.
		{"GET", "/api/v1/connectors", s.handleListConnectors, apiOp{
			summary: "List managed connector instances (deprecated: use GET /api/v1/configured-workers)", tag: "Connectors", role: roleAny, deprecated: true, resp: jsonBody("Connectors", tArray())}},
		{"POST", "/api/v1/connectors", s.handleCreateConnector, apiOp{
			summary: "Create a managed connector instance (deprecated: use POST /api/v1/configured-workers)", tag: "Connectors", role: RoleModeler, deprecated: true, req: jsonBody("Connector", tObject()), resp: jsonBody("Created connector", tObject())}},
		{"PATCH", "/api/v1/connectors/{id}", s.handleUpdateConnector, apiOp{
			summary: "Update a managed connector instance (deprecated: use PATCH /api/v1/configured-workers/{id})", tag: "Connectors", role: RoleModeler, deprecated: true, req: jsonBody("Connector update", tObject()), resp: jsonBody("Updated connector", tObject())}},
		{"DELETE", "/api/v1/connectors/{id}", s.handleDeleteConnector, apiOp{
			summary: "Delete a managed connector instance (deprecated: use DELETE /api/v1/configured-workers/{id})", tag: "Connectors", role: RoleModeler, deprecated: true, status: http.StatusNoContent}},

		{"GET", "/api/v1/connector-kinds", s.handleConnectorKinds, apiOp{
			summary: "List connector kinds (deprecated: use GET /api/v1/worker-types)", tag: "Connectors", role: roleAny, deprecated: true,
			resp: jsonBody("Connector kind placements", schemaObj(map[string]any{"kinds": tArray()}))}},

		{"POST", "/api/v1/connectors/test", s.handleTestConnector, apiOp{
			summary: "Check a connector without saving it — a mail connector connects and authenticates (or sends a test message to \"to\"), a SQL connector dials its connection string", tag: "Connectors", role: RoleModeler,
			req: jsonBody("Connector check", tObject()), resp: jsonBody("Check result", tObject())}},

		{"GET", "/api/v1/mail/outbox", s.handleMailOutbox, apiOp{
			summary: "List the messages the preview mail provider delivered, newest first (?limit=)", tag: "Connectors", role: RoleOperator, resp: jsonBody("Outbox", tObject())}},
		{"POST", "/api/v1/mail/outbox", s.handleDeliverMailOutbox, apiOp{
			summary: "Deliver a framed message into the preview outbox (used by a mail worker running a preview connector)", tag: "Connectors", role: RoleOperator,
			req: jsonBody("Outbox message", tObject()), status: http.StatusNoContent}},
		{"DELETE", "/api/v1/mail/outbox", s.handleClearMailOutbox, apiOp{
			summary: "Empty the preview mail outbox", tag: "Connectors", role: RoleOperator, status: http.StatusNoContent}},

		{"GET", "/api/v1/ad/mock-directory", s.handleADMockDirectory, apiOp{
			summary: "Show the mock Active Directories this server's workers hold — one forest per LDAP URL, with what a mockup run put in them", tag: "Connectors", role: RoleAdmin,
			resp: jsonBody("Mock directories", schemaObj(map[string]any{"workers": tArray()}))}},
		{"POST", "/api/v1/ad/mock-directory", s.handleReportADMockDirectory, apiOp{
			summary: "Report a mock directory (used by an AD worker running in mockup mode)", tag: "Connectors", role: RoleOperator,
			req: jsonBody("Mock directory", tObject()), status: http.StatusNoContent}},

		{"GET", "/api/v1/sql/mock-journal", s.handleSQLMockJournal, apiOp{
			summary: "Show what a database mockup run was asked — every statement, with the values the process bound. Admin-gated: a bound parameter is whatever the process bound, and nothing can tell a password from an id", tag: "Connectors", role: RoleAdmin,
			resp: jsonBody("Mock journals", schemaObj(map[string]any{"workers": tArray()}))}},
		{"POST", "/api/v1/sql/mock-journal", s.handleReportSQLMockJournal, apiOp{
			summary: "Report a mockup journal (used by a SQL worker running in mockup mode)", tag: "Connectors", role: RoleOperator,
			req: jsonBody("Mock journal", tObject()), status: http.StatusNoContent}},

		{"GET", "/api/v1/connectors/{id}/inbound-subscriptions", s.handleListInboundSubscriptions, apiOp{
			summary: "List a clio connector's inbound event subscriptions", tag: "Connectors", role: RoleModeler, resp: jsonBody("Subscriptions", tArray())}},
		{"POST", "/api/v1/connectors/{id}/inbound-subscriptions", s.handleCreateInboundSubscription, apiOp{
			summary: "Create an inbound event subscription for a clio connector", tag: "Connectors", role: RoleModeler, req: jsonBody("Subscription", tObject()), resp: jsonBody("Created subscription", tObject())}},
		{"PATCH", "/api/v1/inbound-subscriptions/{id}", s.handleUpdateInboundSubscription, apiOp{
			summary: "Update an inbound event subscription", tag: "Connectors", role: RoleModeler, req: jsonBody("Subscription update", tObject()), resp: jsonBody("Updated subscription", tObject())}},
		{"DELETE", "/api/v1/inbound-subscriptions/{id}", s.handleDeleteInboundSubscription, apiOp{
			summary: "Delete an inbound event subscription", tag: "Connectors", role: RoleModeler, status: http.StatusNoContent}},
		{"GET", "/api/v1/message-sources", s.handleListMessageSources, apiOp{
			summary: "List every inbound event watch by the message name it publishes, so a model can be told whether its message start event has a source", tag: "Connectors", role: RoleModeler, resp: jsonBody("Message sources", tArray())}},

		{"PUT", "/api/v1/connectors/{id}/members/{principalId}", s.handleSetConnectorMember, apiOp{
			summary: "Share a connector with a user or a group, or change their role (ADR-0205); owner only", tag: "Connectors", role: RoleModeler, req: jsonBody("Member role", tObject()), resp: jsonBody("Updated connector", tObject())}},
		{"DELETE", "/api/v1/connectors/{id}/members/{principalId}", s.handleRemoveConnectorMember, apiOp{
			summary: "Withdraw a user's or a group's access to a connector (ADR-0205); owner only", tag: "Connectors", role: RoleModeler, resp: jsonBody("Updated connector", tObject())}},
		{"PUT", "/api/v1/connectors/{id}/visibility", s.handleSetConnectorVisibility, apiOp{
			summary: "Seal a connector again, or open it to its member list (ADR-0205); owner only", tag: "Connectors", role: RoleModeler, req: jsonBody("Visibility", tObject()), resp: jsonBody("Updated connector", tObject())}},
		{"PUT", "/api/v1/connectors/{id}/owner/{userId}", s.handleTransferConnector, apiOp{
			summary: "Hand a connector to another account (ADR-0205); owner only", tag: "Connectors", role: RoleModeler, resp: jsonBody("Updated connector", tObject())}},
		{"POST", "/api/v1/connectors/{id}/provision-clio-key", s.handleProvisionClioKey, apiOp{
			summary: "Mint a scoped clio key (admin token supplied once) and seal it as this connector's credential", tag: "Connectors", role: RoleAdmin, req: jsonBody("Provision request", tObject()), resp: jsonBody("Provisioned credential", tObject())}},

		{"GET", "/api/v1/repository/packages", s.handleListRepository, apiOp{
			summary: "Browse the repository catalog (filter by ?kind and ?q)", tag: "Repository", role: RoleModeler, resp: jsonBody("Catalog packages", tArray())}},
		{"GET", "/api/v1/repository/packages/{id}", s.handleGetRepositoryPackage, apiOp{
			summary: "Get one repository package with its element-template payload", tag: "Repository", role: RoleModeler, resp: jsonBody("Package", tObject())}},
		{"POST", "/api/v1/repository/packages/{id}/install", s.handleInstallRepositoryPackage, apiOp{
			summary: "Install a package's template (script tasks are admin-gated and imported for review)", tag: "Repository", role: RoleModeler, resp: jsonBody("Installed template", tObject())}},
		{"GET", "/api/v1/repository/installed", s.handleListInstalled, apiOp{
			summary: "List templates installed from the repository", tag: "Repository", role: RoleModeler, resp: jsonBody("Installed templates", tArray())}},
		{"DELETE", "/api/v1/repository/installed/{id}", s.handleUninstall, apiOp{
			summary: "Uninstall a repository template", tag: "Repository", role: RoleModeler, status: http.StatusNoContent}},

		{"GET", "/api/v1/secrets", s.handleListSecrets, apiOp{
			summary: "List secret names and metadata in the encrypted vault (never values)", tag: "Secrets", role: RoleAdmin, resp: jsonBody("Secrets", tArray())}},
		{"PUT", "/api/v1/secrets/{name}", s.handleSetSecret, apiOp{
			summary: "Store or overwrite a secret value in the encrypted vault", tag: "Secrets", role: RoleAdmin, req: jsonBody("Secret value", schemaObj(map[string]any{"value": tString()}, "value")), resp: jsonBody("Secret metadata", tObject())}},
		{"DELETE", "/api/v1/secrets/{name}", s.handleDeleteSecret, apiOp{
			summary: "Delete a secret from the encrypted vault", tag: "Secrets", role: RoleAdmin, status: http.StatusNoContent}},

		{"GET", "/api/v1/settings/theme", s.handleGetTheme, apiOp{
			summary: "Get the org-wide UI brand accent colour (public; applied before login)", tag: "System", role: roleAny,
			resp: jsonBody("Theme", schemaObj(map[string]any{"accent": tString()}))}},
		{"PUT", "/api/v1/settings/theme", s.handleSetTheme, apiOp{
			summary: "Set the org-wide UI brand accent colour (admin-only when auth is on) (ADR-0113)", tag: "System", role: RoleAdmin,
			req:  jsonBody("Theme", schemaObj(map[string]any{"accent": tString()}, "accent")),
			resp: jsonBody("Theme", schemaObj(map[string]any{"accent": tString()}))}},
		{"DELETE", "/api/v1/settings/theme", s.handleDeleteTheme, apiOp{
			summary: "Reset the org-wide UI theme to the built-in default (admin-only when auth is on) (ADR-0113)", tag: "System", role: RoleAdmin, status: http.StatusNoContent}},

		{"GET", "/api/v1/settings/logo", s.handleGetLogo, apiOp{
			summary: "Get the org-wide brand logo image; 404 when none is set (public; shown before login) (ADR-0148)", tag: "System", role: roleAny,
			resp: &bodySpec{mediaType: "image/png", desc: "Brand logo image (PNG or SVG)", schema: map[string]any{"type": "string", "format": "binary"}}}},
		{"PUT", "/api/v1/settings/logo", s.handleSetLogo, apiOp{
			summary: "Upload the org-wide brand logo — raw PNG or SVG body, max 512 KiB (admin-only when auth is on) (ADR-0148)", tag: "System", role: RoleAdmin, status: http.StatusNoContent,
			req: &bodySpec{mediaType: "image/png", desc: "PNG or SVG logo bytes (Content-Type sets the format)", schema: map[string]any{"type": "string", "format": "binary"}}}},
		{"DELETE", "/api/v1/settings/logo", s.handleDeleteLogo, apiOp{
			summary: "Remove the org-wide brand logo, restoring the built-in Atlas mark (admin-only when auth is on) (ADR-0148)", tag: "System", role: RoleAdmin, status: http.StatusNoContent}},

		{"GET", "/api/v1/settings/ad-mock", s.handleGetADMock, apiOp{
			summary: "The org-wide Active Directory mockup switch: whether directory writes are simulated in the worker's memory instead of reaching a domain controller, and the seed file it starts from (ADR-0181)", tag: "Settings", role: roleAny,
			resp: jsonBody("ADMock", tObject())}},
		{"PUT", "/api/v1/settings/ad-mock", s.handleSetADMock, apiOp{
			summary: "Turn the Active Directory mockup on or off. Admin-gated; the supervised AD worker is restarted holding the new setting, so no server restart is needed", tag: "Settings", role: RoleAdmin,
			req:  jsonBody("ADMockRequest", tObject()),
			resp: jsonBody("ADMock", tObject())}},
		{"GET", "/api/v1/settings/sql-mock", s.handleGetSQLMock, apiOp{
			summary: "The org-wide database mockup switch: whether SQL Server, MariaDB and PostgreSQL tasks are answered from seeded answers in the worker's memory instead of reaching a database, and the seed it starts from", tag: "Settings", role: roleAny,
			resp: jsonBody("SQLMock", tObject())}},
		{"PUT", "/api/v1/settings/sql-mock", s.handleSetSQLMock, apiOp{
			summary: "Turn the database mockup on or off. Admin-gated; the supervised SQL workers are restarted holding the new setting, so no server restart is needed", tag: "Settings", role: RoleAdmin,
			req:  jsonBody("SQLMockRequest", tObject()),
			resp: jsonBody("SQLMock", tObject())}},
		{"GET", "/api/v1/settings/registration", s.handleGetRegistration, apiOp{
			summary: "Whether the login screen offers a self-service registration link, and its public URL (public; read before login) (ADR-0126)", tag: "System", role: roleAny,
			resp: jsonBody("Registration config", schemaObj(map[string]any{
				"enabled": tBool(), "processId": tString(), "url": tString(),
			}))}},
		{"PUT", "/api/v1/settings/registration", s.handleSetRegistration, apiOp{
			summary: "Configure the self-service registration process and mint its public link; empty processId disables it (admin-only when auth is on) (ADR-0126)", tag: "System", role: RoleAdmin,
			req: jsonBody("Registration process", schemaObj(map[string]any{"processId": tString()})),
			resp: jsonBody("Registration config", schemaObj(map[string]any{
				"enabled": tBool(), "processId": tString(), "url": tString(),
			}))}},
		{"DELETE", "/api/v1/settings/registration", s.handleDeleteRegistration, apiOp{
			summary: "Switch self-service registration off (admin-only when auth is on) (ADR-0126)", tag: "System", role: RoleAdmin, status: http.StatusNoContent}},

		{"GET", "/api/v1/settings/oidc-mapping", s.handleGetOIDCMapping, apiOp{
			summary: "Read the rule set that turns an identity provider's claim into Atlas roles and group membership (ADR-0210)", tag: "Auth", role: RoleAdmin,
			resp: jsonBody("Claim mapping", tObject())}},
		{"PUT", "/api/v1/settings/oidc-mapping", s.handleSetOIDCMapping, apiOp{
			summary: "Store that rule set. While it is on, whoever administers the provider's groups administers this instance's roles; a rule naming a role Atlas does not enforce or a group that does not exist is refused here rather than granting nothing on every login (ADR-0210)", tag: "Auth", role: RoleAdmin,
			req:  jsonBody("Claim mapping", tObject()),
			resp: jsonBody("Claim mapping", tObject())}},

		{"POST", "/api/v1/auth/login", s.handleLogin, apiOp{
			summary: "Log in with a username and password", tag: "Auth", role: roleAny,
			req: jsonBody("Credentials", schemaObj(map[string]any{
				"username": tString(), "password": tString(),
			}, "username", "password")),
			resp: jsonBody("Authenticated user", tObject())}},
		{"POST", "/api/v1/auth/logout", s.handleLogout, apiOp{
			summary: "Log out the current session", tag: "Auth", role: roleAny, resp: jsonBody("Logout result", tObject())}},
		{"POST", "/api/v1/auth/presence", s.handlePresenceBeacon, apiOp{
			summary: "Report that the caller's own session is still open, and with active=true that somebody is using it (ADR-0228)", tag: "Auth", role: roleAny,
			req:  jsonBody("Activity", schemaObj(map[string]any{"active": tBool()})),
			resp: jsonBody("Accepted", tObject())}},
		{"GET", "/api/v1/auth/providers", s.handleAuthProviders, apiOp{
			summary: "List the identity providers this server offers besides the password form — empty unless an operator configured one (ADR-0210)", tag: "Auth", role: roleAny,
			resp: jsonBody("Configured identity providers", tArray())}},
		{"GET", "/api/v1/auth/me", s.handleMe, apiOp{
			summary: "Report auth status and the current user", tag: "Auth", role: roleAny,
			resp: jsonBody("Auth status", schemaObj(map[string]any{
				"authEnabled": tBool(), "user": tObject(),
			}))}},

		{"GET", "/api/v1/users", s.handleListUsers, apiOp{
			summary: "List user accounts, each with whether somebody is signed in as it right now (ADR-0228)", tag: "Users", role: RoleAdmin, resp: jsonBody("Users", tArray())}},
		{"GET", "/api/v1/users/presence", s.handleUserPresence, apiOp{
			summary: "Who is signed in right now: one entry per account holding a live session, online / idle / offline (ADR-0228)", tag: "Users", role: RoleAdmin,
			resp: jsonBody("Presence", tArray())}},
		{"GET", "/api/v1/users/assignable", s.handleListAssignableUsers, apiOp{
			summary: "List users a task can be assigned to", tag: "Users", role: roleAny, resp: jsonBody("Assignable users", tArray())}},
		{"GET", "/api/v1/principals", s.handleListPrincipals, apiOp{
			summary: "List principals (users; groups later) for member and assignee pickers — id-referenced, any authenticated caller (ADR-0073)", tag: "Users", role: roleAny, resp: jsonBody("Principals", tArray())}},
		{"POST", "/api/v1/users", s.handleCreateUser, apiOp{
			summary: "Create a user account", tag: "Users", role: RoleAdmin, status: http.StatusCreated,
			req: jsonBody("New user", schemaObj(map[string]any{
				"username": tString(), "email": tString(), "displayName": tString(),
				"password": tString(), "roles": map[string]any{"type": "array", "items": tString()},
			}, "username", "password")),
			resp: jsonBody("Created user", tObject())}},
		{"GET", "/api/v1/users/{id}", s.handleGetUser, apiOp{
			summary: "Fetch a user account", tag: "Users", role: RoleAdmin, resp: jsonBody("User", tObject())}},
		{"PATCH", "/api/v1/users/{id}", s.handlePatchUser, apiOp{
			summary: "Update a user account", tag: "Users", role: RoleAdmin,
			req: jsonBody("User changes", schemaObj(map[string]any{
				"email": tString(), "displayName": tString(),
				"roles": map[string]any{"type": "array", "items": tString()}, "disabled": tBool(),
			})),
			resp: jsonBody("Updated user", tObject())}},
		{"POST", "/api/v1/users/{id}/password", s.handleSetUserPassword, apiOp{
			summary: "Set a user's password", tag: "Users", role: RoleAdmin,
			req:  jsonBody("New password", schemaObj(map[string]any{"password": tString()}, "password")),
			resp: jsonBody("User id", tObject())}},
		{"DELETE", "/api/v1/users/{id}", s.handleDeleteUser, apiOp{
			summary: "Delete a user account", tag: "Users", role: RoleAdmin, status: http.StatusNoContent}},

		{"GET", "/api/v1/groups", s.handleListGroups, apiOp{
			summary: "List user groups (admin)", tag: "Groups", role: RoleAdmin, resp: jsonBody("Groups", tArray())}},
		{"POST", "/api/v1/groups", s.handleCreateGroup, apiOp{
			summary: "Create a user group (admin)", tag: "Groups", role: RoleAdmin, status: http.StatusCreated,
			req:  jsonBody("New group", schemaObj(map[string]any{"name": tString()}, "name")),
			resp: jsonBody("Created group", tObject())}},
		{"PATCH", "/api/v1/groups/{id}", s.handleRenameGroup, apiOp{
			summary: "Rename a user group (admin)", tag: "Groups", role: RoleAdmin,
			req:  jsonBody("Group changes", schemaObj(map[string]any{"name": tString()}, "name")),
			resp: jsonBody("Updated group", tObject())}},
		{"DELETE", "/api/v1/groups/{id}", s.handleDeleteGroup, apiOp{
			summary: "Delete a user group (admin)", tag: "Groups", role: RoleAdmin, status: http.StatusNoContent}},
		{"PUT", "/api/v1/groups/{id}/members/{userId}", s.handleAddGroupMember, apiOp{
			summary: "Add a user to a group (admin)", tag: "Groups", role: RoleAdmin, resp: jsonBody("Updated group", tObject())}},
		{"DELETE", "/api/v1/groups/{id}/members/{userId}", s.handleRemoveGroupMember, apiOp{
			summary: "Remove a user from a group (admin)", tag: "Groups", role: RoleAdmin, resp: jsonBody("Updated group", tObject())}},

		{"GET", "/api/v1/audit", s.handleListAudit, apiOp{
			summary: "The access-control history across every application, newest first — the global admin audit view (ADR-0184). Admin-only. Optional filters: applicationId, action (share|unshare|visibility|transfer); limit caps the window (default 200, max 1000)", tag: "Audit", role: RoleAdmin, resp: jsonBody("Grant audit events", tArray())}},
	}
}

// pathParamRe matches an http.ServeMux path wildcard, e.g. {key} in
// /api/v1/processes/{key}/xml.
var pathParamRe = regexp.MustCompile(`\{([^}]+)\}`)

// openapiDoc renders the route table as an OpenAPI 3.1 document. It is pure
// (reads only the static table), so a nil-engine Server can produce it for the
// drift test. Map values marshal with sorted keys, so the JSON is deterministic.
func (s *Server) openapiDoc() map[string]any {
	paths := map[string]any{}
	tagset := map[string]struct{}{}
	for _, r := range s.apiRoutes() {
		item, ok := paths[r.pattern].(map[string]any)
		if !ok {
			item = map[string]any{}
			paths[r.pattern] = item
		}
		item[strings.ToLower(r.method)] = operationDoc(r)
		tagset[r.op.tag] = struct{}{}
	}
	names := make([]string, 0, len(tagset))
	for t := range tagset {
		names = append(names, t)
	}
	sort.Strings(names)
	tags := make([]any, 0, len(names))
	for _, t := range names {
		tags = append(tags, map[string]any{"name": t})
	}
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":   "Atlas HTTP API",
			"version": Version,
			"description": "The Atlas single-binary HTTP API: deploy BPMN models, run " +
				"instances, and inspect live runtime state. This surface is " +
				"unauthenticated by design — put auth in front before exposing it " +
				"publicly (see ADR-0016).",
		},
		"servers": []any{map[string]any{"url": "/"}},
		"tags":    tags,
		"paths":   paths,
		"components": map[string]any{"schemas": map[string]any{
			"Error": schemaObj(map[string]any{"error": tString()}, "error"),
		}},
	}
}

// operationDoc builds the OpenAPI operation object for one route: its summary,
// tag, path parameters (derived from the pattern's wildcards), optional request
// body, the primary success response, and a shared default error response.
func operationDoc(r apiRoute) map[string]any {
	op := map[string]any{
		"summary":     r.op.summary,
		"operationId": operationID(r),
		"tags":        []any{r.op.tag},
	}
	if r.op.deprecated {
		op["deprecated"] = true
	}
	// Who may call it, in the document that describes it. A client author reading
	// the explorer would otherwise learn the answer from a 403 in production, and
	// the answer already lives one field away in the same table
	// (ADR-0209).
	if r.op.role != roleAny {
		op["description"] = "Requires the `" + r.op.role + "` role when authentication is on."
	}
	if params := pathParams(r.pattern); len(params) > 0 {
		op["parameters"] = params
	}
	if r.op.req != nil {
		op["requestBody"] = map[string]any{
			"required":    true,
			"description": r.op.req.desc,
			"content":     mediaContent(r.op.req),
		}
	}
	status := r.op.status
	if status == 0 {
		status = http.StatusOK
	}
	success := map[string]any{"description": http.StatusText(status)}
	if status != http.StatusNoContent && r.op.resp != nil {
		success["content"] = mediaContent(r.op.resp)
	}
	op["responses"] = map[string]any{
		strconv.Itoa(status): success,
		"default": map[string]any{
			"description": "Error",
			"content": map[string]any{"application/json": map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/Error"},
			}},
		},
	}
	return op
}

// pathParams turns each {wildcard} in a pattern into a required string path
// parameter, so the explorer prompts for it in "Try it out".
func pathParams(pattern string) []any {
	var out []any
	for _, m := range pathParamRe.FindAllStringSubmatch(pattern, -1) {
		out = append(out, map[string]any{
			"name":     m[1],
			"in":       "path",
			"required": true,
			"schema":   tString(),
		})
	}
	return out
}

// mediaContent wraps a body's schema under its media type, defaulting a nil
// schema to a permissive object so a route without a hand-written schema still
// produces a valid document.
func mediaContent(b *bodySpec) map[string]any {
	schema := b.schema
	if schema == nil {
		schema = tObject()
	}
	return map[string]any{b.mediaType: map[string]any{"schema": schema}}
}

// operationID is a stable, unique id derived from method and pattern, e.g.
// "get_processes_key_xml", used by client generators and deep links.
func operationID(r apiRoute) string {
	id := strings.ToLower(r.method)
	for _, seg := range strings.Split(strings.TrimPrefix(r.pattern, "/api/v1/"), "/") {
		seg = strings.Trim(seg, "{}")
		if seg != "" {
			id += "_" + seg
		}
	}
	return id
}

// docsHTML is the /api/docs page: it loads the vendored Scalar standalone build
// (served by the file server at /vendor/scalar/) and points it at this server's
// own OpenAPI document, so the explorer and its "Try it out" run same-origin
// against the live engine.
const docsHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>Atlas API</title>
</head>
<body>
<div id="app"></div>
<script src="/vendor/scalar/standalone.js"></script>
<script>
  Scalar.createApiReference('#app', { url: '/api/v1/openapi.json' })
</script>
</body>
</html>
`

// handleOpenAPI serves the generated OpenAPI 3.1 document. Registered only when
// docs are enabled (--docs), so the API surface is not described to anonymous
// callers unless an operator opts in (ADR-0043).
func (s *Server) handleOpenAPI(w http.ResponseWriter, _ *http.Request) {
	httpapi.JSON(w, http.StatusOK, s.openapiDoc())
}

// handleDocs serves the Scalar API explorer shell. Registered only when docs
// are enabled (--docs).
func (s *Server) handleDocs(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(docsHTML))
}
