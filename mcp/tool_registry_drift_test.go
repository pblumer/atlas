package mcp_test

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"
)

// The MCP server is a pure adapter over the Atlas HTTP API (ADR-0016): every
// advertised tool proxies to exactly one /api/v1 operation, and MCP is strictly
// a subset of HTTP. Unlike the HTTP↔OpenAPI surface — which cannot drift because
// TestOpenAPICoversEveryRoute reads one route table — nothing structurally stops
// the MCP surface from silently falling behind the API as new endpoints land.
//
// This test is that missing guard. It classifies every HTTP operation as either
// covered by a named tool or deliberately omitted, and fails when a new route is
// neither — so adding an endpoint forces a conscious decision (expose it, or
// record why not) instead of an unnoticed capability gap. The maps below are the
// single source of that decision; keep them honest and this test keeps the two
// surfaces from drifting apart the way issue #198's follow-up warned they could.

// mcpToolRoutes maps each advertised MCP tool to the "METHOD /pattern" HTTP
// operation it proxies to. Every entry must be a real API route and every
// advertised tool must appear here (both asserted below).
var mcpToolRoutes = map[string]string{
	"atlas_info":                  "GET /api/v1/info",
	"atlas_stats":                 "GET /api/v1/stats",
	"atlas_deploy":                "POST /api/v1/deployments",
	"atlas_list_processes":        "GET /api/v1/processes",
	"atlas_get_process_xml":       "GET /api/v1/processes/{key}/xml",
	"atlas_delete_process":        "DELETE /api/v1/processes/{key}",
	"atlas_process_runtime":       "GET /api/v1/processes/{key}/runtime",
	"atlas_create_instance":       "POST /api/v1/processes/{key}/instances",
	"atlas_list_instances":        "GET /api/v1/instances",
	"atlas_cancel_instance":       "DELETE /api/v1/instances/{key}",
	"atlas_cancel_instances":      "POST /api/v1/processes/{key}/cancel-instances",
	"atlas_create_project":        "POST /api/v1/projects",
	"atlas_list_projects":         "GET /api/v1/projects",
	"atlas_delete_project":        "DELETE /api/v1/projects/{id}",
	"atlas_save_draft":            "POST /api/v1/drafts",
	"atlas_save_form":             "POST /api/v1/forms",
	"atlas_upload_decision_model": "POST /api/v1/dmn-models",
	"atlas_register_decision":     "POST /api/v1/dmnrefs",
	"atlas_deploy_project":        "POST /api/v1/projects/{id}/deploy",
	"atlas_list_tasks":            "GET /api/v1/tasks",
	"atlas_get_task":              "GET /api/v1/tasks/{key}",
	"atlas_complete_task":         "POST /api/v1/tasks/{key}/complete",
	"atlas_claim_task":            "POST /api/v1/tasks/{key}/claim",
	"atlas_unclaim_task":          "POST /api/v1/tasks/{key}/unclaim",
	"atlas_publish_message":       "POST /api/v1/messages",
	"atlas_complete_job":          "POST /api/v1/jobs/{key}/complete",
	"atlas_fail_job":              "POST /api/v1/jobs/{key}/fail",
	"atlas_list_incidents":        "GET /api/v1/incidents",
	"atlas_resolve_incident":      "POST /api/v1/incidents/{key}/resolve",
	"atlas_instance_variables":    "GET /api/v1/instances/{key}/variables",
	"atlas_instance_jobs":         "GET /api/v1/instances/{key}/jobs",
	"atlas_instance_timeline":     "GET /api/v1/instances/{key}/timeline",
	"atlas_instances_summary":     "GET /api/v1/instances/summary",
	"atlas_search_instances":      "GET /api/v1/instances/search",
	"atlas_instance_data_objects": "GET /api/v1/instances/{key}/data-objects",
	"atlas_terminate_instances":   "POST /api/v1/instances/terminate",
}

// mcpOmittedRoutes lists HTTP operations intentionally not exposed as MCP tools,
// each with the reason. A route is either here or a value in mcpToolRoutes; the
// test rejects any operation that is in neither. When you add an /api/v1 route,
// add a matching tool (and an entry above) or record here why an agent should
// not have it.
var mcpOmittedRoutes = map[string]string{
	// Server introspection / diagnostics an agent does not drive scenarios with.
	"GET /api/v1/logs": "admin diagnostics, not an agent authoring/runtime action",

	// Dry-run BPMN validation for the Modeler's Problems panel (ADR-0026): an MCP
	// agent deploys directly via atlas_deploy, which already compiles and validates.
	"POST /api/v1/validate": "modeler-time dry-run validation; atlas_deploy already compiles+validates",

	// Expression/script sandboxes: authored inside BPMN, not called standalone.
	"POST /api/v1/feel/validate": "modeler-time expression check, not a scenario action",
	"POST /api/v1/feel/evaluate": "modeler-time expression check, not a scenario action",
	"POST /api/v1/scripts/run":   "modeler-time script check, not a scenario action",

	// Read-side runtime introspection: partially exposed (candidate follow-ups).
	"GET /api/v1/collaborations/{key}/runtime": "runtime introspection not yet exposed",
	"GET /api/v1/instances/{key}/decisions":    "decision-evaluation introspection not yet exposed",
	"GET /api/v1/decisions/deployed":           "decision introspection not yet exposed",
	"GET /api/v1/decisions/{id}/evaluations":   "decision introspection not yet exposed",

	// CSV batch start: driven from a user task inside the process (ADR-0084).
	"POST /api/v1/processes/{key}/instances-from-csv": "in-process CSV ingestion, not a direct agent start",

	// Design-time read/edit: agents create artifacts; browsing/editing is the UI's.
	"GET /api/v1/drafts":                 "artifact browsing is a UI concern",
	"GET /api/v1/drafts/{id}/xml":        "artifact browsing is a UI concern",
	"PATCH /api/v1/drafts/{id}":          "artifact editing is a UI concern",
	"DELETE /api/v1/drafts/{id}":         "artifact editing is a UI concern",
	"GET /api/v1/forms":                  "artifact browsing is a UI concern",
	"GET /api/v1/forms/{id}":             "artifact browsing is a UI concern",
	"DELETE /api/v1/forms/{id}":          "artifact editing is a UI concern",
	"GET /api/v1/dmnrefs":                "artifact browsing is a UI concern",
	"PATCH /api/v1/dmnrefs/{id}":         "artifact editing is a UI concern",
	"DELETE /api/v1/dmnrefs/{id}":        "artifact editing is a UI concern",
	"POST /api/v1/dmnrefs/{id}/validate": "modeler-time validation is a UI concern",
	"GET /api/v1/decisions":              "artifact browsing is a UI concern",
	"GET /api/v1/dmnrefs/{id}/graph":     "artifact browsing is a UI concern",
	"GET /api/v1/dmn-models/{ref}/xml":   "artifact browsing is a UI concern",

	// Public start links: a human-sharing feature, not an agent action.
	"POST /api/v1/public-links":           "human share links, not an agent action",
	"GET /api/v1/public-links":            "human share links, not an agent action",
	"DELETE /api/v1/public-links/{token}": "human share links, not an agent action",

	// Project edit/membership: grouping metadata and access control, UI-owned.
	"PATCH /api/v1/projects/{id}":                   "project metadata edit is a UI concern",
	"PUT /api/v1/projects/{id}/members/{userId}":    "access control is an admin/UI concern",
	"DELETE /api/v1/projects/{id}/members/{userId}": "access control is an admin/UI concern",
	"POST /api/v1/projects/{id}/validate":           "modeler-time validation is a UI concern",

	// Connectors + inbound subscriptions: infrastructure config, admin-owned.
	"GET /api/v1/connectors":                             "connector infrastructure is admin config",
	"POST /api/v1/connectors":                            "connector infrastructure is admin config",
	"PATCH /api/v1/connectors/{id}":                      "connector infrastructure is admin config",
	"DELETE /api/v1/connectors/{id}":                     "connector infrastructure is admin config",
	"GET /api/v1/connectors/{id}/inbound-subscriptions":  "connector infrastructure is admin config",
	"POST /api/v1/connectors/{id}/inbound-subscriptions": "connector infrastructure is admin config",
	"PATCH /api/v1/inbound-subscriptions/{id}":           "connector infrastructure is admin config",
	"DELETE /api/v1/inbound-subscriptions/{id}":          "connector infrastructure is admin config",
	"POST /api/v1/connectors/{id}/provision-clio-key":    "connector infrastructure is admin config",

	// Marketplace: package management, an admin/UI concern.
	"GET /api/v1/marketplace/packages":               "marketplace management is a UI concern",
	"GET /api/v1/marketplace/packages/{id}":          "marketplace management is a UI concern",
	"POST /api/v1/marketplace/packages/{id}/install": "marketplace management is a UI concern",
	"GET /api/v1/marketplace/installed":              "marketplace management is a UI concern",
	"DELETE /api/v1/marketplace/installed/{id}":      "marketplace management is a UI concern",

	// Secrets: credential storage; an agent must never read or write it.
	"GET /api/v1/secrets":           "credential storage is not an agent capability",
	"PUT /api/v1/secrets/{name}":    "credential storage is not an agent capability",
	"DELETE /api/v1/secrets/{name}": "credential storage is not an agent capability",

	// Auth + user administration: security surface, deliberately off-limits.
	"POST /api/v1/auth/login":          "auth flow is not an agent capability",
	"POST /api/v1/auth/logout":         "auth flow is not an agent capability",
	"GET /api/v1/auth/me":              "auth flow is not an agent capability",
	"GET /api/v1/users":                "user administration is not an agent capability",
	"GET /api/v1/users/assignable":     "user administration is not an agent capability",
	"GET /api/v1/principals":           "user administration is not an agent capability",
	"POST /api/v1/users":               "user administration is not an agent capability",
	"GET /api/v1/users/{id}":           "user administration is not an agent capability",
	"PATCH /api/v1/users/{id}":         "user administration is not an agent capability",
	"POST /api/v1/users/{id}/password": "user administration is not an agent capability",
	"DELETE /api/v1/users/{id}":        "user administration is not an agent capability",
}

// httpOperations fetches the live OpenAPI document from a running Atlas server
// and returns the set of "METHOD /pattern" operations it documents. That
// document is the API's single source of truth: TestOpenAPICoversEveryRoute
// guarantees it lists exactly the registered routes, so reading it here means
// this test tracks the real served surface without duplicating the route table.
func httpOperations(t *testing.T, atlasURL string) map[string]bool {
	t.Helper()
	resp, err := http.Get(atlasURL + "/api/v1/openapi.json")
	if err != nil {
		t.Fatalf("fetch openapi.json: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("openapi.json status = %d, want 200 (is docs enabled?)", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read openapi.json: %v", err)
	}
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode openapi.json: %v", err)
	}
	ops := map[string]bool{}
	for pattern, methods := range doc.Paths {
		for method := range methods {
			ops[strings.ToUpper(method)+" "+pattern] = true
		}
	}
	if len(ops) == 0 {
		t.Fatal("openapi.json documented no operations")
	}
	return ops
}

// TestMCPToolSurfaceMatchesHTTPClassification is the drift guard: every HTTP
// operation is either covered by a tool or explicitly omitted, and the two
// classification maps reference only real, non-overlapping routes.
func TestMCPToolSurfaceMatchesHTTPClassification(t *testing.T) {
	atlas := newAtlas(t)
	ops := httpOperations(t, atlas.URL)

	covered := map[string]string{} // route -> tool
	for tool, route := range mcpToolRoutes {
		if !ops[route] {
			t.Errorf("tool %q maps to %q, which is not a documented HTTP operation", tool, route)
		}
		if other, dup := covered[route]; dup {
			t.Errorf("route %q is claimed by both %q and %q", route, other, tool)
		}
		covered[route] = tool
	}

	// A route cannot be both covered and omitted.
	for route := range mcpOmittedRoutes {
		if !ops[route] {
			t.Errorf("omitted route %q is not a documented HTTP operation (stale entry?)", route)
		}
		if tool, both := covered[route]; both {
			t.Errorf("route %q is both covered by %q and listed as omitted", route, tool)
		}
	}

	// The core assertion: no unclassified operation. A new /api/v1 route lands
	// here until it is exposed as a tool or recorded as omitted.
	var unclassified []string
	for route := range ops {
		if _, isCovered := covered[route]; isCovered {
			continue
		}
		if _, isOmitted := mcpOmittedRoutes[route]; isOmitted {
			continue
		}
		unclassified = append(unclassified, route)
	}
	if len(unclassified) > 0 {
		sort.Strings(unclassified)
		t.Fatalf("%d HTTP operation(s) are neither exposed as an MCP tool nor listed as intentionally omitted:\n\t%s\n"+
			"Add a tool (and an mcpToolRoutes entry) or record the reason in mcpOmittedRoutes.",
			len(unclassified), strings.Join(unclassified, "\n\t"))
	}
}

// TestEveryAdvertisedToolHasARouteMapping ties the tool registry to the
// classification: every tool the server advertises must appear in mcpToolRoutes,
// so a new tool cannot be added without also classifying the route it proxies.
func TestEveryAdvertisedToolHasARouteMapping(t *testing.T) {
	atlas := newAtlas(t)
	listed := run(t, atlas, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if len(listed) != 1 {
		t.Fatalf("tools/list returned %d responses, want 1", len(listed))
	}

	advertised := map[string]bool{}
	for _, name := range listedToolNames(t, listed[0]) {
		advertised[name] = true
		if _, ok := mcpToolRoutes[name]; !ok {
			t.Errorf("advertised tool %q has no mcpToolRoutes entry (classify the route it proxies)", name)
		}
	}
	// Reverse: no stale mapping for a tool that is no longer advertised.
	for name := range mcpToolRoutes {
		if !advertised[name] {
			t.Errorf("mcpToolRoutes maps %q, which tools/list does not advertise (stale entry?)", name)
		}
	}
}
