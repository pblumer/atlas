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
// feeds the vendored Scalar explorer at /api/docs (both gated behind
// --docs), whose "Try it out" issues same-origin requests to this live engine.

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

		{"POST", "/api/v1/deployments", s.handleDeploy, apiOp{
			summary: "Deploy a BPMN model", tag: "Deployments",
			req: xmlBody("BPMN 2.0 XML"),
			resp: jsonBody("Deployed processes", schemaObj(map[string]any{
				"key": tInteger(), "processId": tString(), "version": tInteger(), "deployments": tArray(),
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
		{"GET", "/api/v1/instances", s.handleListInstances, apiOp{
			summary: "List active and finished instances", tag: "Instances", resp: jsonBody("Instances", tArray())}},
		{"GET", "/api/v1/instances/{key}/timeline", s.handleInstanceTimeline, apiOp{
			summary: "Read a process instance's step-by-step replay timeline", tag: "Instances",
			resp: jsonBody("Instance timeline", tObject())}},
		{"DELETE", "/api/v1/instances/{key}", s.handleCancelInstance, apiOp{
			summary: "Cancel a running instance", tag: "Instances", resp: jsonBody("Cancellation result", tObject())}},

		{"POST", "/api/v1/messages", s.handlePublishMessage, apiOp{
			summary: "Publish a message for correlation", tag: "Messages",
			req: jsonBody("Message", schemaObj(map[string]any{
				"name": tString(), "correlationKey": tString(), "variables": tObject(),
			}, "name")),
			resp: jsonBody("Publish result", tObject())}},

		{"GET", "/api/v1/tasks", s.handleListTasks, apiOp{
			summary: "List active user tasks", tag: "Tasks", resp: jsonBody("Tasks", tArray())}},
		{"POST", "/api/v1/tasks/{key}/complete", s.handleCompleteTask, apiOp{
			summary: "Complete a user task", tag: "Tasks",
			req:  jsonBody("Completion variables", schemaObj(map[string]any{"variables": tObject()})),
			resp: jsonBody("Task key", tObject())}},
		{"POST", "/api/v1/tasks/{key}/claim", s.handleClaimTask, apiOp{
			summary: "Claim a user task for an assignee", tag: "Tasks",
			req:  jsonBody("Assignee", schemaObj(map[string]any{"assignee": tString()}, "assignee")),
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

		{"POST", "/api/v1/projects", s.handleCreateProject, apiOp{
			summary: "Create a project", tag: "Projects", req: jsonBody("Project", tObject()), resp: jsonBody("Created project", tObject())}},
		{"GET", "/api/v1/projects", s.handleListProjects, apiOp{
			summary: "List projects", tag: "Projects", resp: jsonBody("Projects", tArray())}},
		{"PATCH", "/api/v1/projects/{id}", s.handleRenameProject, apiOp{
			summary: "Rename a project", tag: "Projects", req: jsonBody("Rename", tObject()), resp: jsonBody("Updated project", tObject())}},
		{"DELETE", "/api/v1/projects/{id}", s.handleDeleteProject, apiOp{
			summary: "Delete a project", tag: "Projects", status: http.StatusNoContent}},
		{"POST", "/api/v1/projects/{id}/validate", s.handleValidateProject, apiOp{
			summary: "Validate a project's artifacts", tag: "Projects", resp: jsonBody("Validation result", tObject())}},
		{"POST", "/api/v1/projects/{id}/deploy", s.handleDeployProject, apiOp{
			summary: "Deploy a project's artifacts", tag: "Projects", resp: jsonBody("Deploy result", tObject())}},

		{"POST", "/api/v1/dmnrefs", s.handleCreateDmnRef, apiOp{
			summary: "Create a DMN reference artifact", tag: "DMN References", req: jsonBody("DMN reference", tObject()), resp: jsonBody("Created reference", tObject())}},
		{"GET", "/api/v1/dmnrefs", s.handleListDmnRefs, apiOp{
			summary: "List DMN reference artifacts", tag: "DMN References", resp: jsonBody("References", tArray())}},
		{"PATCH", "/api/v1/dmnrefs/{id}", s.handleMoveDmnRef, apiOp{
			summary: "Move a DMN reference to a project", tag: "DMN References", req: jsonBody("Move", tObject()), resp: jsonBody("Updated reference", tObject())}},
		{"DELETE", "/api/v1/dmnrefs/{id}", s.handleDeleteDmnRef, apiOp{
			summary: "Delete a DMN reference", tag: "DMN References", status: http.StatusNoContent}},
		{"POST", "/api/v1/dmnrefs/{id}/validate", s.handleValidateDmnRef, apiOp{
			summary: "Validate a DMN reference compiles", tag: "DMN References", resp: jsonBody("Validation result", tObject())}},
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
