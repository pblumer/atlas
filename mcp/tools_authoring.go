package mcp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// optString returns a string argument, or "" when it is absent or not a string —
// for the optional fields (projectId, name) the authoring tools accept.
func optString(args map[string]any, name string) string {
	s, _ := args[name].(string)
	return s
}

// optPositiveUint extracts an optional positive integer argument.
func optPositiveUint(args map[string]any, name string) (uint64, bool, error) {
	if _, ok := args[name]; !ok {
		return 0, false, nil
	}
	n, err := argUint(args, name)
	if err != nil {
		return 0, false, err
	}
	if n == 0 {
		return 0, false, fmt.Errorf("argument %q must be a positive integer", name)
	}
	return n, true, nil
}

// argObjectJSON marshals a required object argument (a form schema, completion
// variables) back to JSON to forward as a request body.
func argObjectJSON(args map[string]any, name string) ([]byte, error) {
	v, ok := args[name]
	if !ok {
		return nil, fmt.Errorf("missing required argument: %s", name)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("argument %q must be an object", name)
	}
	return json.Marshal(m)
}

// objectProp is a JSON-Schema object property (used for form schemas and
// completion variables that are free-form objects).
func objectProp(desc string) map[string]any {
	return map[string]any{"type": "object", "description": desc}
}

func stringProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

type taskListPage struct {
	Items      []json.RawMessage `json:"items"`
	Truncated  bool              `json:"truncated"`
	NextCursor uint64            `json:"nextCursor,omitempty"`
}

// authoringTools are the design-time and human-task tools that let an agent set a
// scenario up end to end: create a project (a "folder"), save its diagram draft,
// forms, and decision, deploy the project (bundling the decision), and drive the
// resulting human tasks. Each is a thin adapter over one /api/v1 endpoint, exactly
// like the runtime tools (ADR-0016).
func authoringTools() []Tool {
	return []Tool{
		{
			Name: "atlas_create_project",
			Description: "Create a project (a folder that groups a scenario's diagrams, forms, and " +
				"decision references). Returns the project's id — pass it to the other authoring tools " +
				"to file artifacts under this project.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"name": stringProp("The project (folder) name, e.g. \"CSV_Test\".")},
				"required":   []any{"name"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				name, err := argString(args, "name")
				if err != nil {
					return "", err
				}
				body, _ := json.Marshal(map[string]string{"name": name})
				return asText(c.post("/api/v1/projects", "application/json", body))
			},
		},
		{
			Name:        "atlas_list_projects",
			Description: "List projects (folders) with their id, name, and metadata.",
			InputSchema: noArgs(),
			Handler: func(c *Client, _ map[string]any) (string, error) {
				return asText(c.get("/api/v1/projects"))
			},
		},
		{
			Name: "atlas_delete_project",
			Description: "Delete a design-time project by id. The operation is idempotent. It removes only " +
				"the project folder; tagged drafts and decision references remain and become ungrouped. " +
				"When authentication is enabled, the caller must have the project owner role.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"id": stringProp("The project id from atlas_create_project or atlas_list_projects.")},
				"required":   []any{"id"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				if _, err := c.del("/api/v1/projects/" + url.PathEscape(id)); err != nil {
					return "", err
				}
				confirmation, _ := json.Marshal(map[string]any{"deleted": true, "id": id})
				return string(confirmation), nil
			},
		},
		{
			Name: "atlas_save_draft",
			Description: "Save a BPMN 2.0 XML diagram as a draft, optionally filed under a project " +
				"(projectId). The process id and name are read from the diagram. A draft is design-time " +
				"only — deploy it with atlas_deploy_project (for a project) or atlas_deploy (raw).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"xml":       stringProp("The full BPMN 2.0 XML diagram to save as a draft."),
					"projectId": stringProp("Optional project id to file the draft under (from atlas_create_project)."),
				},
				"required": []any{"xml"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				xml, err := argString(args, "xml")
				if err != nil {
					return "", err
				}
				path := "/api/v1/drafts"
				if pid := optString(args, "projectId"); pid != "" {
					path += "?projectId=" + url.QueryEscape(pid)
				}
				return asText(c.post(path, "application/xml", []byte(xml)))
			},
		},
		{
			Name: "atlas_save_form",
			Description: "Create or overwrite a form (a form-js JSON schema) by id, optionally filed " +
				"under a project. A user task binds to it by its formId. Overwriting the same id updates it.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":        stringProp("The form id a user task's formId references."),
					"name":      stringProp("Optional display name (defaults to the id)."),
					"schema":    objectProp("The form-js schema document (an object with type and components)."),
					"projectId": stringProp("Optional project id to file the form under."),
				},
				"required": []any{"id", "schema"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				schema, err := argObjectJSON(args, "schema")
				if err != nil {
					return "", err
				}
				payload := map[string]any{"id": id, "schema": json.RawMessage(schema)}
				if n := optString(args, "name"); n != "" {
					payload["name"] = n
				}
				if p := optString(args, "projectId"); p != "" {
					payload["projectId"] = p
				}
				body, _ := json.Marshal(payload)
				return asText(c.post("/api/v1/forms", "application/json", body))
			},
		},
		{
			Name: "atlas_upload_decision_model",
			Description: "Upload a DMN 1.x XML model under a handle so a decision it declares can be " +
				"resolved at deploy time. Pair it with atlas_register_decision (modelRef = this handle) " +
				"and deploy via atlas_deploy_project to bundle the decision into a business rule task.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"handle": stringProp("The model handle to store it under (referenced by a decision's modelRef)."),
					"xml":    stringProp("The full DMN XML document."),
				},
				"required": []any{"handle", "xml"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				handle, err := argString(args, "handle")
				if err != nil {
					return "", err
				}
				xml, err := argString(args, "xml")
				if err != nil {
					return "", err
				}
				return asText(c.post("/api/v1/dmn-models?handle="+url.QueryEscape(handle), "application/xml", []byte(xml)))
			},
		},
		{
			Name: "atlas_register_decision",
			Description: "Register a decision reference (name + modelRef) so a business rule task's " +
				"calledDecision resolves to an uploaded model at deploy time, optionally under a project. " +
				"modelRef is the handle used with atlas_upload_decision_model.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":      stringProp("The decision reference name (typically the decisionId a rule task calls)."),
					"modelRef":  stringProp("The model handle (from atlas_upload_decision_model) that provides the decision."),
					"projectId": stringProp("Optional project id to file the reference under."),
				},
				"required": []any{"name", "modelRef"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				name, err := argString(args, "name")
				if err != nil {
					return "", err
				}
				modelRef, err := argString(args, "modelRef")
				if err != nil {
					return "", err
				}
				payload := map[string]any{"name": name, "modelRef": modelRef}
				if p := optString(args, "projectId"); p != "" {
					payload["projectId"] = p
				}
				body, _ := json.Marshal(payload)
				return asText(c.post("/api/v1/dmnrefs", "application/json", body))
			},
		},
		{
			Name: "atlas_deploy_project",
			Description: "Deploy a project by id: compiles its diagram draft(s) and bundles the decisions " +
				"its registered references resolve, so a deployed business rule task can evaluate them. " +
				"Returns the deployed definitions.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"id": stringProp("The project id to deploy (from atlas_create_project).")},
				"required":   []any{"id"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				return asText(c.post("/api/v1/projects/"+url.PathEscape(id)+"/deploy", "application/json", []byte("")))
			},
		},
		{
			Name: "atlas_list_tasks",
			Description: "List active (open) user tasks with optional limit, newest-first cursor, or " +
				"process-instance filter. Returns {items, truncated, nextCursor}; use nextCursor as before " +
				"to fetch the next older global page. Use a task key from items with atlas_complete_task.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{
						"type":        "integer",
						"minimum":     1,
						"description": "Maximum tasks to return (API default 500, capped at 5000).",
					},
					"before": map[string]any{
						"type":        "integer",
						"minimum":     1,
						"description": "Newest-first job-key cursor from nextCursor; use for global listing without processInstance.",
					},
					"processInstance": map[string]any{
						"type":        "integer",
						"minimum":     1,
						"description": "Restrict tasks to one process instance key.",
					},
				},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				query := url.Values{}
				for _, name := range []string{"limit", "before", "processInstance"} {
					value, present, err := optPositiveUint(args, name)
					if err != nil {
						return "", err
					}
					if present {
						query.Set(name, strconv.FormatUint(value, 10))
					}
				}
				path := "/api/v1/tasks"
				if encoded := query.Encode(); encoded != "" {
					path += "?" + encoded
				}

				body, headers, err := c.getWithHeaders(path)
				if err != nil {
					return "", err
				}
				var items []json.RawMessage
				if err := json.Unmarshal(body, &items); err != nil {
					return "", fmt.Errorf("decode Atlas task list: %w", err)
				}
				page := taskListPage{
					Items:     items,
					Truncated: strings.EqualFold(strings.TrimSpace(headers.Get("X-Tasks-Truncated")), "true"),
				}
				if raw := strings.TrimSpace(headers.Get("X-Tasks-Next-Cursor")); raw != "" {
					cursor, err := parseUint(raw)
					if err != nil {
						return "", fmt.Errorf("decode Atlas task cursor %q: %w", raw, err)
					}
					page.NextCursor = cursor
				}
				encoded, err := json.Marshal(page)
				if err != nil {
					return "", err
				}
				return string(encoded), nil
			},
		},
		{
			Name: "atlas_complete_task",
			Description: "Complete an open user task by its task key, submitting its form data as " +
				"variables (the same shape a human submitting the form would). Drives the instance forward. " +
				"Use the task key from atlas_list_tasks, not a process or instance key.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key":       map[string]any{"type": "integer", "description": "The task key (from atlas_list_tasks) to complete."},
					"variables": objectProp("The completion variables (form data), e.g. {\"csvText\": \"...\"}. Omit for none."),
				},
				"required": []any{"key"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argUint(args, "key")
				if err != nil {
					return "", err
				}
				vars := map[string]any{}
				if v, ok := args["variables"].(map[string]any); ok {
					vars = v
				}
				body, _ := json.Marshal(map[string]any{"variables": vars})
				return asText(c.post("/api/v1/tasks/"+strconv.FormatUint(key, 10)+"/complete", "application/json", body))
			},
		},
	}
}
