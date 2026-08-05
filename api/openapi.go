package api

import (
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
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

// apiOp describes a route for the OpenAPI document. summary and tag are required
// (the drift test enforces non-empty). req/resp bodies are filled in where they
// add value and default to a permissive object otherwise (ADR-0043).
type apiOp struct {
	summary string
	tag     string
	status  int       // primary success status; 0 means 200 OK
	req     *bodySpec // request body, or nil when the route takes none
	resp    *bodySpec // success response body, or nil when it returns no content
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

// apiRoutes is the single source of truth for the /api/v1 surface. Handler
// iterates it to register handlers; openapiDoc iterates it to describe them.
// Adding an endpoint means adding one entry here — nothing is registered off to
// the side, so the spec cannot fall out of sync (ADR-0043).
func (s *Server) apiRoutes() []apiRoute {
	return []apiRoute{
		{"GET", "/api/v1/info", s.handleInfo, apiOp{
			summary: "Product and version metadata", tag: "System",
			resp: jsonBody("Product metadata", schemaObj(map[string]any{
				"product": tString(), "version": tString(),
			}))}},
		{"GET", "/api/v1/stats", s.handleStats, apiOp{
			summary: "Live active-instance counts", tag: "System",
			resp: jsonBody("Instance counts", schemaObj(map[string]any{
				"activeProcessInstances": tInteger(), "activeElementInstances": tInteger(),
			}))}},
		{"GET", "/api/v1/logs", s.handleLogs, apiOp{
			summary: "Recent server log lines (admin-only when auth is on)", tag: "System",
			resp: jsonBody("Recent log lines, oldest first", schemaObj(map[string]any{
				"lines": tArray(),
			}))}},

		{"POST", "/api/v1/feel/validate", s.handleValidateFeel, apiOp{
			summary: "Validate a FEEL expression compiles", tag: "FEEL",
			req: jsonBody("FEEL expression", schemaObj(map[string]any{"expression": tString()}, "expression")),
			resp: jsonBody("Validation result", schemaObj(map[string]any{
				"ok": tBool(), "error": tString(),
			}))}},
		{"POST", "/api/v1/feel/evaluate", s.handleEvaluateFeel, apiOp{
			summary: "Evaluate a FEEL expression against variables", tag: "FEEL",
			req: jsonBody("Expression and variables", schemaObj(map[string]any{
				"expression": tString(), "variables": tObject(),
			}, "expression")),
			resp: jsonBody("Evaluation result", schemaObj(map[string]any{
				"ok": tBool(), "result": tObject(), "kind": tString(), "error": tString(),
			}))}},
		{"POST", "/api/v1/scripts/run", s.handleRunScript, apiOp{
			summary: "Run a script task against sample variables (admin-only when auth is on)", tag: "Scripts",
			req: jsonBody("Language, source, and sample variables", schemaObj(map[string]any{
				"language": tString(), "source": tString(), "variables": tObject(),
			}, "language", "source")),
			resp: jsonBody("Run result", schemaObj(map[string]any{
				"ok": tBool(), "result": tObject(), "error": tString(),
			}))}},

		{"POST", "/api/v1/deployments", s.handleDeploy, apiOp{
			summary: "Deploy a BPMN model", tag: "Deployments",
			req: xmlBody("BPMN 2.0 XML"),
			resp: jsonBody("Deployed processes", schemaObj(map[string]any{
				"key": tInteger(), "processId": tString(), "version": tInteger(), "deployments": tArray(),
			}))}},
		{"POST", "/api/v1/validate", s.handleValidate, apiOp{
			summary: "Validate a BPMN model without deploying — a dry-run compile returning structured problems (errors and warnings) and the engine version, for the Modeler's Problems panel (ADR-0026)", tag: "Deployments",
			req: xmlBody("BPMN 2.0 XML"),
			resp: jsonBody("Validation problems and the engine version that produced them", schemaObj(map[string]any{
				"version": tString(), "problems": tArray(),
			}))}},

		{"GET", "/api/v1/processes", s.handleListProcesses, apiOp{
			summary: "List deployed processes", tag: "Processes", resp: jsonBody("Processes", tArray())}},
		{"GET", "/api/v1/processes/{key}/xml", s.handleProcessXML, apiOp{
			summary: "Fetch a deployed process's BPMN XML", tag: "Processes",
			resp: xmlBody("BPMN 2.0 XML")}},
		{"DELETE", "/api/v1/processes/{key}", s.handleDeleteProcess, apiOp{
			summary: "Delete a deployment (must have no running instances)", tag: "Processes",
			status: http.StatusNoContent}},
		{"GET", "/api/v1/processes/{key}/runtime", s.handleProcessRuntime, apiOp{
			summary: "Read a process's live runtime state", tag: "Processes", resp: jsonBody("Runtime state", tObject())}},
		{"GET", "/api/v1/collaborations/{key}/runtime", s.handleCollaborationRuntime, apiOp{
			summary: "Read a collaboration's live runtime state", tag: "Collaborations", resp: jsonBody("Runtime state", tObject())}},

		{"POST", "/api/v1/processes/{key}/instances", s.handleCreateInstance, apiOp{
			summary: "Start a process instance", tag: "Instances",
			req:  jsonBody("Initial variables", schemaObj(map[string]any{"variables": tObject()})),
			resp: jsonBody("Created instance", tObject())}},
		{"POST", "/api/v1/processes/{key}/instances-from-csv", s.handleCreateInstanceFromCSV, apiOp{
			summary: "Start a process instance from an uploaded CSV — multipart file + JSON column layout; seeds rows/rowCount/fileName as start variables (ADR-0084)", tag: "Instances",
			req: &bodySpec{mediaType: "multipart/form-data", desc: "CSV file and a JSON column layout", schema: schemaObj(map[string]any{
				"file":   map[string]any{"type": "string", "format": "binary"},
				"config": tString(),
			}, "file", "config")},
			resp: jsonBody("Created instance with parsed row count", tObject())}},
		{"GET", "/api/v1/instances", s.handleListInstances, apiOp{
			summary: "List active and finished instances — capped per call (?limit=, default 1000, max 10000); narrow to one definition with ?process=<key>; X-Instances-Truncated: true marks a capped page", tag: "Instances", resp: jsonBody("Instances", tArray())}},
		{"GET", "/api/v1/instances/summary", s.handleInstancesSummary, apiOp{
			summary: "Per-definition instance counts (active/completed) — lean count-only scan for the operations overview", tag: "Instances", resp: jsonBody("Instance summary", tArray())}},
		{"GET", "/api/v1/instances/search", s.handleSearchInstances, apiOp{
			summary: "Search instances by variable content — ?q=name=value (name exact, value substring) or free text over variable names and values", tag: "Instances",
			resp: jsonBody("Matching instances", tArray())}},
		{"GET", "/api/v1/instances/{key}/variables", s.handleInstanceVariables, apiOp{
			summary: "Read a process instance's variables as a typed JSON object", tag: "Instances",
			resp: jsonBody("Instance variables", tObject())}},
		{"GET", "/api/v1/instances/{key}/data-objects", s.handleInstanceDataObjects, apiOp{
			summary: "Read a process instance's data objects — each with its name, data state, and typed value", tag: "Instances",
			resp: jsonBody("Instance data objects", tArray())}},
		{"GET", "/api/v1/instances/{key}/timeline", s.handleInstanceTimeline, apiOp{
			summary: "Read a process instance's step-by-step replay timeline", tag: "Instances",
			resp: jsonBody("Instance timeline", tObject())}},
		{"GET", "/api/v1/instances/{key}/decisions", s.handleInstanceDecisions, apiOp{
			summary: "Read the DMN decision evaluations a process instance made — each with its inputs, outputs, and trace", tag: "Instances",
			resp: jsonBody("Decision evaluations", tArray())}},
		{"GET", "/api/v1/instances/{key}/jobs", s.handleListInstanceJobs, apiOp{
			summary: "List the activatable jobs an instance is parked on (any type) — the read side of POST /jobs/{key}/complete", tag: "Instances",
			resp: jsonBody("Activatable jobs", tArray())}},
		{"GET", "/api/v1/decisions/deployed", s.handleDeployedDecisions, apiOp{
			summary: "List deployed and evaluated DMN decisions, one row per decision, with the processes that use it and its evaluation usage", tag: "Decisions",
			resp: jsonBody("Deployed decisions", tArray())}},
		{"GET", "/api/v1/decisions/{id}/evaluations", s.handleDecisionEvaluations, apiOp{
			summary: "List every retained evaluation of one decision — its inputs, outputs, and trace — newest first, for drilling into a decision's instances", tag: "Decisions",
			resp: jsonBody("Decision evaluations", tArray())}},
		{"DELETE", "/api/v1/instances/{key}", s.handleCancelInstance, apiOp{
			summary: "Cancel a running instance", tag: "Instances", resp: jsonBody("Cancellation result", tObject())}},
		{"POST", "/api/v1/processes/{key}/cancel-instances", s.handleCancelInstancesOfProcess, apiOp{
			summary: "Cancel a bounded batch of a definition's running instances (?limit=, default 5000, max 50000); repeat while the response reports remaining=true", tag: "Instances",
			resp: jsonBody("Bulk cancellation result", tObject())}},
		{"POST", "/api/v1/instances/terminate", s.handleTerminateInstances, apiOp{
			summary: "Terminate a selected set of running instances — body {keys:[…]} for an explicit selection, or {processDefKey, q?, limit?} to terminate a definition's matching instances (repeat while remaining=true)", tag: "Instances",
			req:  jsonBody("Selection", schemaObj(map[string]any{"keys": tArray(), "processDefKey": tInteger(), "q": tString(), "limit": tInteger()})),
			resp: jsonBody("Termination result", tObject())}},

		{"POST", "/api/v1/messages", s.handlePublishMessage, apiOp{
			summary: "Publish a message for correlation", tag: "Messages",
			req: jsonBody("Message", schemaObj(map[string]any{
				"name": tString(), "correlationKey": tString(), "variables": tObject(),
			}, "name")),
			resp: jsonBody("Publish result", tObject())}},

		{"POST", "/api/v1/jobs/{key}/complete", s.handleCompleteJob, apiOp{
			summary: "Complete a job by hand (operator counterpart to fail; not the worker protocol)", tag: "Incidents",
			req:  jsonBody("Completion variables", schemaObj(map[string]any{"variables": tObject()})),
			resp: jsonBody("Job key", tObject())}},
		{"POST", "/api/v1/jobs/{key}/fail", s.handleFailJob, apiOp{
			summary: "Fail a job, carrying remaining retries (0 raises an incident)", tag: "Incidents",
			req: jsonBody("Retries left and a failure message", schemaObj(map[string]any{
				"retries": tInteger(), "message": tString(),
			})),
			resp: jsonBody("Job key and stats", tObject())}},
		{"GET", "/api/v1/incidents", s.handleListIncidents, apiOp{
			summary: "List unresolved incidents — capped per call (?limit=, max 5000); X-Incidents-Truncated: true marks a capped page", tag: "Incidents", resp: jsonBody("Incidents", tArray())}},
		{"POST", "/api/v1/incidents/{key}/resolve", s.handleResolveIncident, apiOp{
			summary: "Resolve the incident on an element instance and retry its job", tag: "Incidents",
			req:  jsonBody("Retries to grant the resumed job (default 1)", schemaObj(map[string]any{"retries": tInteger()})),
			resp: jsonBody("Element instance key and stats", tObject())}},

		{"GET", "/api/v1/tasks", s.handleListTasks, apiOp{
			summary: "List active user tasks, newest first — capped per call (?limit=, default 500, max 5000). A capped page sets X-Tasks-Truncated: true and X-Tasks-Next-Cursor: <jobKey>; pass it as ?before= to page to older tasks. ?processInstance=<key> scopes the list to one instance (flood-proof, for embedded clients)", tag: "Tasks", resp: jsonBody("Tasks", tArray())}},
		{"GET", "/api/v1/tasks/{key}", s.handleGetTask, apiOp{
			summary: "Fetch one open user task by key — the deep-link primitive so a task stays reachable outside a capped list page", tag: "Tasks", resp: jsonBody("Task", tObject())}},
		{"POST", "/api/v1/tasks/{key}/complete", s.handleCompleteTask, apiOp{
			summary: "Complete a user task", tag: "Tasks",
			req:  jsonBody("Completion variables", schemaObj(map[string]any{"variables": tObject()})),
			resp: jsonBody("Task key", tObject())}},
		{"POST", "/api/v1/tasks/{key}/claim", s.handleClaimTask, apiOp{
			summary: "Claim a user task (self, or assign a named user)", tag: "Tasks",
			req:  jsonBody("Optional assignee (empty claims for the signed-in user)", schemaObj(map[string]any{"assignee": tString()})),
			resp: jsonBody("Task and assignee", tObject())}},
		{"POST", "/api/v1/tasks/{key}/unclaim", s.handleUnclaimTask, apiOp{
			summary: "Release a user task's claim", tag: "Tasks", resp: jsonBody("Task key", tObject())}},

		{"POST", "/api/v1/drafts", s.handleSaveDraft, apiOp{
			summary: "Save a diagram draft", tag: "Drafts", req: jsonBody("Draft", tObject()), resp: jsonBody("Saved draft", tObject())}},
		{"GET", "/api/v1/drafts", s.handleListDrafts, apiOp{
			summary: "List diagram drafts", tag: "Drafts", resp: jsonBody("Drafts", tArray())}},
		{"GET", "/api/v1/drafts/{id}/xml", s.handleDraftXML, apiOp{
			summary: "Fetch a draft's BPMN XML", tag: "Drafts", resp: xmlBody("BPMN 2.0 XML")}},
		{"PATCH", "/api/v1/drafts/{id}", s.handleMoveDraft, apiOp{
			summary: "Move a draft to a project", tag: "Drafts", req: jsonBody("Move", tObject()), resp: jsonBody("Updated draft", tObject())}},
		{"DELETE", "/api/v1/drafts/{id}", s.handleDeleteDraft, apiOp{
			summary: "Delete a draft", tag: "Drafts", status: http.StatusNoContent}},

		{"POST", "/api/v1/forms", s.handleSaveForm, apiOp{
			summary: "Save a form definition", tag: "Forms", req: jsonBody("Form", tObject()), resp: jsonBody("Saved form", tObject())}},
		{"GET", "/api/v1/forms", s.handleListForms, apiOp{
			summary: "List form definitions", tag: "Forms", resp: jsonBody("Forms", tArray())}},
		{"GET", "/api/v1/forms/{id}", s.handleGetForm, apiOp{
			summary: "Fetch a form definition", tag: "Forms", resp: jsonBody("Form", tObject())}},
		{"DELETE", "/api/v1/forms/{id}", s.handleDeleteForm, apiOp{
			summary: "Delete a form definition", tag: "Forms", resp: jsonBody("Deleted id", tObject())}},
		{"POST", "/api/v1/public-links", s.handleCreatePublicLink, apiOp{
			summary: "Publish a process: mint a public start link (ADR-0029)", tag: "Forms",
			req:  jsonBody("Target", schemaObj(map[string]any{"processId": tString()}, "processId")),
			resp: jsonBody("Public link", tObject())}},
		{"GET", "/api/v1/public-links", s.handleListPublicLinks, apiOp{
			summary: "List public start links", tag: "Forms", resp: jsonBody("Public links", tArray())}},
		{"DELETE", "/api/v1/public-links/{token}", s.handleRevokePublicLink, apiOp{
			summary: "Revoke a public start link", tag: "Forms", resp: jsonBody("Revoked token", tObject())}},

		{"POST", "/api/v1/projects", s.handleCreateProject, apiOp{
			summary: "Create a project", tag: "Projects", req: jsonBody("Project", tObject()), resp: jsonBody("Created project", tObject())}},
		{"GET", "/api/v1/projects", s.handleListProjects, apiOp{
			summary: "List projects", tag: "Projects", resp: jsonBody("Projects", tArray())}},
		{"PATCH", "/api/v1/projects/{id}", s.handleUpdateProject, apiOp{
			summary: "Update a project: rename, set visibility (private/shared), or transfer ownership (ADR-0071)", tag: "Projects",
			req:  jsonBody("Update", schemaObj(map[string]any{"name": tString(), "visibility": tString(), "ownerId": tString()})),
			resp: jsonBody("Updated project", tObject())}},
		{"DELETE", "/api/v1/projects/{id}", s.handleDeleteProject, apiOp{
			summary: "Delete a project", tag: "Projects", status: http.StatusNoContent}},
		{"PUT", "/api/v1/projects/{id}/members/{userId}", s.handleSetProjectMember, apiOp{
			summary: "Share a project with a user, or change their role (ADR-0071)", tag: "Projects",
			req:  jsonBody("Member role", schemaObj(map[string]any{"role": tString()}, "role")),
			resp: jsonBody("Updated project", tObject())}},
		{"DELETE", "/api/v1/projects/{id}/members/{userId}", s.handleRemoveProjectMember, apiOp{
			summary: "Revoke a user's membership on a project (ADR-0071)", tag: "Projects", resp: jsonBody("Updated project", tObject())}},
		{"POST", "/api/v1/projects/{id}/validate", s.handleValidateProject, apiOp{
			summary: "Validate a project's artifacts", tag: "Projects", resp: jsonBody("Validation result", tObject())}},
		{"POST", "/api/v1/projects/{id}/deploy", s.handleDeployProject, apiOp{
			summary: "Deploy a project's artifacts", tag: "Projects", resp: jsonBody("Deploy result", tObject())}},

		{"POST", "/api/v1/dmnrefs", s.handleCreateDmnRef, apiOp{
			summary: "Create a DMN reference artifact", tag: "DMN References", req: jsonBody("DMN reference", tObject()), resp: jsonBody("Created reference", tObject())}},
		{"GET", "/api/v1/dmnrefs", s.handleListDmnRefs, apiOp{
			summary: "List DMN reference artifacts", tag: "DMN References", resp: jsonBody("References", tArray())}},
		{"PATCH", "/api/v1/dmnrefs/{id}", s.handleUpdateDmnRef, apiOp{
			summary: "Update a DMN reference: move it to a project and/or rename it", tag: "DMN References", req: jsonBody("Update", tObject()), resp: jsonBody("Updated reference", tObject())}},
		{"DELETE", "/api/v1/dmnrefs/{id}", s.handleDeleteDmnRef, apiOp{
			summary: "Delete a DMN reference", tag: "DMN References", status: http.StatusNoContent}},
		{"POST", "/api/v1/dmnrefs/{id}/validate", s.handleValidateDmnRef, apiOp{
			summary: "Validate a DMN reference compiles", tag: "DMN References", resp: jsonBody("Validation result", tObject())}},
		{"GET", "/api/v1/decisions", s.handleListDecisions, apiOp{
			summary: "List DMN decisions (with inputs and outputs) available from DMN references", tag: "DMN References", resp: jsonBody("Decisions", tArray())}},
		{"GET", "/api/v1/dmnrefs/{id}/graph", s.handleDmnRefGraph, apiOp{
			summary: "A DMN reference's decision requirements graph for the read-only viewer", tag: "DMN References", resp: jsonBody("Model graph", tObject())}},
		{"GET", "/api/v1/dmn-models/{ref}/xml", s.handleDmnModelXML, apiOp{
			summary: "The raw DMN model XML for a model handle, for the embedded DMN editor", tag: "DMN References", resp: jsonBody("DMN XML", tObject())}},
		{"POST", "/api/v1/dmn-models", s.handleUploadDmnModel, apiOp{
			summary: "Upload a DMN model file into the local model store and return its reference handle", tag: "DMN References", req: jsonBody("DMN XML", tObject()), resp: jsonBody("Stored model", tObject())}},

		{"GET", "/api/v1/connectors", s.handleListConnectors, apiOp{
			summary: "List managed connector instances", tag: "Connectors", resp: jsonBody("Connectors", tArray())}},
		{"POST", "/api/v1/connectors", s.handleCreateConnector, apiOp{
			summary: "Create a managed connector instance", tag: "Connectors", req: jsonBody("Connector", tObject()), resp: jsonBody("Created connector", tObject())}},
		{"PATCH", "/api/v1/connectors/{id}", s.handleUpdateConnector, apiOp{
			summary: "Update a managed connector instance", tag: "Connectors", req: jsonBody("Connector update", tObject()), resp: jsonBody("Updated connector", tObject())}},
		{"DELETE", "/api/v1/connectors/{id}", s.handleDeleteConnector, apiOp{
			summary: "Delete a managed connector instance", tag: "Connectors", status: http.StatusNoContent}},

		{"GET", "/api/v1/connectors/{id}/inbound-subscriptions", s.handleListInboundSubscriptions, apiOp{
			summary: "List a clio connector's inbound event subscriptions", tag: "Connectors", resp: jsonBody("Subscriptions", tArray())}},
		{"POST", "/api/v1/connectors/{id}/inbound-subscriptions", s.handleCreateInboundSubscription, apiOp{
			summary: "Create an inbound event subscription for a clio connector", tag: "Connectors", req: jsonBody("Subscription", tObject()), resp: jsonBody("Created subscription", tObject())}},
		{"PATCH", "/api/v1/inbound-subscriptions/{id}", s.handleUpdateInboundSubscription, apiOp{
			summary: "Update an inbound event subscription", tag: "Connectors", req: jsonBody("Subscription update", tObject()), resp: jsonBody("Updated subscription", tObject())}},
		{"DELETE", "/api/v1/inbound-subscriptions/{id}", s.handleDeleteInboundSubscription, apiOp{
			summary: "Delete an inbound event subscription", tag: "Connectors", status: http.StatusNoContent}},

		{"POST", "/api/v1/connectors/{id}/provision-clio-key", s.handleProvisionClioKey, apiOp{
			summary: "Mint a scoped clio key (admin token supplied once) and seal it as this connector's credential", tag: "Connectors", req: jsonBody("Provision request", tObject()), resp: jsonBody("Provisioned credential", tObject())}},

		{"GET", "/api/v1/marketplace/packages", s.handleListMarketplace, apiOp{
			summary: "Browse the marketplace catalog (filter by ?kind and ?q)", tag: "Marketplace", resp: jsonBody("Catalog packages", tArray())}},
		{"GET", "/api/v1/marketplace/packages/{id}", s.handleGetMarketplacePackage, apiOp{
			summary: "Get one marketplace package with its element-template payload", tag: "Marketplace", resp: jsonBody("Package", tObject())}},
		{"POST", "/api/v1/marketplace/packages/{id}/install", s.handleInstallMarketplacePackage, apiOp{
			summary: "Install a package's template (script tasks are admin-gated and imported for review)", tag: "Marketplace", resp: jsonBody("Installed template", tObject())}},
		{"GET", "/api/v1/marketplace/installed", s.handleListInstalled, apiOp{
			summary: "List templates installed from the marketplace", tag: "Marketplace", resp: jsonBody("Installed templates", tArray())}},
		{"DELETE", "/api/v1/marketplace/installed/{id}", s.handleUninstall, apiOp{
			summary: "Uninstall a marketplace template", tag: "Marketplace", status: http.StatusNoContent}},

		{"GET", "/api/v1/secrets", s.handleListSecrets, apiOp{
			summary: "List secret names and metadata in the encrypted vault (never values)", tag: "Secrets", resp: jsonBody("Secrets", tArray())}},
		{"PUT", "/api/v1/secrets/{name}", s.handleSetSecret, apiOp{
			summary: "Store or overwrite a secret value in the encrypted vault", tag: "Secrets", req: jsonBody("Secret value", schemaObj(map[string]any{"value": tString()}, "value")), resp: jsonBody("Secret metadata", tObject())}},
		{"DELETE", "/api/v1/secrets/{name}", s.handleDeleteSecret, apiOp{
			summary: "Delete a secret from the encrypted vault", tag: "Secrets", status: http.StatusNoContent}},

		{"POST", "/api/v1/auth/login", s.handleLogin, apiOp{
			summary: "Log in with a username and password", tag: "Auth",
			req: jsonBody("Credentials", schemaObj(map[string]any{
				"username": tString(), "password": tString(),
			}, "username", "password")),
			resp: jsonBody("Authenticated user", tObject())}},
		{"POST", "/api/v1/auth/logout", s.handleLogout, apiOp{
			summary: "Log out the current session", tag: "Auth", resp: jsonBody("Logout result", tObject())}},
		{"GET", "/api/v1/auth/me", s.handleMe, apiOp{
			summary: "Report auth status and the current user", tag: "Auth",
			resp: jsonBody("Auth status", schemaObj(map[string]any{
				"authEnabled": tBool(), "user": tObject(),
			}))}},

		{"GET", "/api/v1/users", s.handleListUsers, apiOp{
			summary: "List user accounts", tag: "Users", resp: jsonBody("Users", tArray())}},
		{"GET", "/api/v1/users/assignable", s.handleListAssignableUsers, apiOp{
			summary: "List users a task can be assigned to", tag: "Users", resp: jsonBody("Assignable users", tArray())}},
		{"GET", "/api/v1/principals", s.handleListPrincipals, apiOp{
			summary: "List principals (users; groups later) for member and assignee pickers — id-referenced, any authenticated caller (ADR-0073)", tag: "Users", resp: jsonBody("Principals", tArray())}},
		{"POST", "/api/v1/users", s.handleCreateUser, apiOp{
			summary: "Create a user account", tag: "Users", status: http.StatusCreated,
			req: jsonBody("New user", schemaObj(map[string]any{
				"username": tString(), "email": tString(), "displayName": tString(),
				"password": tString(), "roles": map[string]any{"type": "array", "items": tString()},
			}, "username", "password")),
			resp: jsonBody("Created user", tObject())}},
		{"GET", "/api/v1/users/{id}", s.handleGetUser, apiOp{
			summary: "Fetch a user account", tag: "Users", resp: jsonBody("User", tObject())}},
		{"PATCH", "/api/v1/users/{id}", s.handlePatchUser, apiOp{
			summary: "Update a user account", tag: "Users",
			req: jsonBody("User changes", schemaObj(map[string]any{
				"email": tString(), "displayName": tString(),
				"roles": map[string]any{"type": "array", "items": tString()}, "disabled": tBool(),
			})),
			resp: jsonBody("Updated user", tObject())}},
		{"POST", "/api/v1/users/{id}/password", s.handleSetUserPassword, apiOp{
			summary: "Set a user's password", tag: "Users",
			req:  jsonBody("New password", schemaObj(map[string]any{"password": tString()}, "password")),
			resp: jsonBody("User id", tObject())}},
		{"DELETE", "/api/v1/users/{id}", s.handleDeleteUser, apiOp{
			summary: "Delete a user account", tag: "Users", status: http.StatusNoContent}},
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
	writeJSON(w, http.StatusOK, s.openapiDoc())
}

// handleDocs serves the Scalar API explorer shell. Registered only when docs
// are enabled (--docs).
func (s *Server) handleDocs(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(docsHTML))
}
