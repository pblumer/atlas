package mcp

import (
	"encoding/json"
	"fmt"
	"net/url"
)

// mockAPITools expose the Console-managed mock REST APIs
// (ADR-draft-console-managed-openapi-mock) so an agent that just authored a REST task
// can stand up the API it calls, without a shell on the host or a port to pick.
//
// They are here rather than among the authoring tools because what they configure is
// the *installation*, not an application: a mock is server-wide, like a Worker record.
// Each is a thin adapter over one /api/v1 endpoint, exactly like every other tool
// (ADR-0016).
func mockAPITools() []Tool {
	return []Tool{
		{
			Name: "atlas_list_mock_apis",
			Description: "List the mock REST APIs this server holds. Each entry has the id, the name, " +
				"what compiling its OpenAPI document found (title, version, operation count, and " +
				"`skipped` responses it cannot give a body), whether it is switched on (`enabled`), " +
				"whether it is actually being served (`serving` — an enabled document that stopped " +
				"compiling is enabled and not serving), `serverUrl` (the real API it stands in for) and " +
				"`baseUrl` — the path to point a REST task at instead. `baseUrl` is a path: prefix it " +
				"with the address this atlas is reached under from wherever the worker runs.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Handler: func(c *Client, args map[string]any) (string, error) {
				return asText(c.get("/api/v1/mock-apis"))
			},
		},
		{
			Name: "atlas_create_mock_api",
			Description: "Serve an OpenAPI 3 document as a mock REST API at /mock-api/{name}, so a process " +
				"with a REST task runs end to end before the real API exists. Pass the document itself " +
				"(JSON or YAML) as `document` — atlas keeps it, so there is no file path to give and " +
				"nothing to place on a host. The mock answers each operation with the `example` the " +
				"document states, the first of its `examples`, or a value generated from the schema; a " +
				"caller can ask for a stated path with the `Prefer: code=404` header. The document is " +
				"compiled first: one that cannot be served is refused with the reason rather than mocked " +
				"badly. Returns the record, including the `baseUrl` to point the task at — only the base " +
				"of the task's URL changes, so the run exercises the real task with its headers, its " +
				"authentication and its result mapping. It does not validate what is sent to it and it " +
				"has no memory: a POST followed by a GET returns what the document says, not what was " +
				"posted. Read what a run called with atlas_mock_reports.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": stringProp("The mock's address and label: lower-case letters, digits and hyphens, " +
						"as in `petstore`. It becomes /mock-api/<name>."),
					"document": stringProp("The OpenAPI 3 document itself, as JSON or YAML text. Swagger 2.0 is refused."),
					"filename": stringProp("Optional: what the document was called, so the Console can say which one is loaded."),
					"enabled": map[string]any{"type": "boolean",
						"description": "Whether to serve it. Omitted means true; false stores the document without serving it."},
				},
				"required": []any{"name", "document"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				body, err := mockAPIBody(args, true)
				if err != nil {
					return "", err
				}
				return asText(c.post("/api/v1/mock-apis", "application/json", body))
			},
		},
		{
			Name: "atlas_update_mock_api",
			Description: "Rename a mock REST API, switch it on or off, or replace its document. An omitted " +
				"`document` leaves the stored one alone, which is what a rename and a flip of the switch " +
				"both want. Switching one back on recompiles what is stored and is refused with the " +
				"reason if that no longer compiles. Switching one off is inert: it serves nothing and the " +
				"document stays.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":       stringProp("The mock's id (from atlas_list_mock_apis)."),
					"name":     stringProp("Optional new name. Omitted keeps the current one."),
					"document": stringProp("Optional replacement OpenAPI document. Omitted keeps the stored one."),
					"filename": stringProp("Optional: what the replacement document was called."),
					"enabled": map[string]any{"type": "boolean",
						"description": "Whether to serve it. Omitted leaves the switch where it is."},
				},
				"required": []any{"id"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				body, err := mockAPIBody(args, false)
				if err != nil {
					return "", err
				}
				return asText(c.put("/api/v1/mock-apis/"+url.PathEscape(id), "application/json", body))
			},
		},
		{
			Name: "atlas_delete_mock_api",
			Description: "Delete a mock REST API and stop serving it. The OpenAPI document atlas holds goes " +
				"with it — it is the only copy, so read it with atlas_mock_api_document first if it is " +
				"needed again.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"id": stringProp("The mock's id (from atlas_list_mock_apis).")},
				"required":   []any{"id"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				return asText(c.del("/api/v1/mock-apis/" + url.PathEscape(id)))
			},
		},
		{
			Name: "atlas_mock_api_document",
			Description: "Read back the OpenAPI document atlas holds for a mock REST API, verbatim as it was " +
				"uploaded. Use it to check what is actually being served, or before replacing or deleting " +
				"the mock.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"id": stringProp("The mock's id (from atlas_list_mock_apis).")},
				"required":   []any{"id"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				return asText(c.get("/api/v1/mock-apis/" + url.PathEscape(id) + "/document"))
			},
		},
	}
}

// mockAPIBody builds a create or update body from the arguments actually supplied.
// Absence matters on an update — an omitted document must leave the stored one alone
// and an omitted switch must not flip it — so a field is sent only when it is there.
func mockAPIBody(args map[string]any, creating bool) ([]byte, error) {
	payload := map[string]any{}
	for _, field := range []string{"name", "document", "filename"} {
		v, ok := args[field]
		if !ok {
			continue
		}
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%s must be a string", field)
		}
		payload[field] = s
	}
	if creating {
		for _, required := range []string{"name", "document"} {
			if payload[required] == "" {
				return nil, fmt.Errorf("%s is required", required)
			}
		}
	}
	if v, ok := args["enabled"]; ok {
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("enabled must be a boolean")
		}
		payload["enabled"] = b
	}
	return json.Marshal(payload)
}
