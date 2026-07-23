package mcp

import (
	"strconv"
)

// Tool is one MCP tool: its advertised name, human/model-facing description,
// JSON Schema for arguments, and the handler that fulfils a call by talking to
// the Atlas server.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	Handler     func(c *Client, args map[string]any) (string, error)
}

// noArgs is the JSON Schema for a tool that takes no arguments.
func noArgs() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

// keyArg is the JSON Schema for a tool whose only argument is a process
// definition key.
func keyArg(desc string) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"key": map[string]any{
				"type":        "integer",
				"description": desc,
			},
		},
		"required": []any{"key"},
	}
}

// defaultTools is the set of tools this server exposes. Each maps directly onto
// an Atlas HTTP endpoint; the returned text is the endpoint's JSON (or XML) body
// so a model receives the server's structured response verbatim.
func defaultTools() []Tool {
	return []Tool{
		{
			Name:        "atlas_info",
			Description: "Get Atlas server product and version information.",
			InputSchema: noArgs(),
			Handler: func(c *Client, _ map[string]any) (string, error) {
				return asText(c.get("/api/v1/info"))
			},
		},
		{
			Name: "atlas_deploy",
			Description: "Deploy a BPMN 2.0 XML process definition to Atlas. The model is " +
				"compiled and validated; only elements Atlas can execute are accepted. " +
				"Returns the assigned definition key, process id, and version.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"xml": map[string]any{
						"type":        "string",
						"description": "The full BPMN 2.0 XML document to deploy.",
					},
				},
				"required": []any{"xml"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				xml, err := argString(args, "xml")
				if err != nil {
					return "", err
				}
				return asText(c.post("/api/v1/deployments", "application/xml", []byte(xml)))
			},
		},
		{
			Name:        "atlas_list_processes",
			Description: "List all deployed process definitions with their key, process id, version, and deploy time.",
			InputSchema: noArgs(),
			Handler: func(c *Client, _ map[string]any) (string, error) {
				return asText(c.get("/api/v1/processes"))
			},
		},
		{
			Name:        "atlas_get_process_xml",
			Description: "Get the original BPMN XML of a deployed process definition by its key.",
			InputSchema: keyArg("The process definition key returned by atlas_deploy or atlas_list_processes."),
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argUint(args, "key")
				if err != nil {
					return "", err
				}
				return asText(c.get("/api/v1/processes/" + strconv.FormatUint(key, 10) + "/xml"))
			},
		},
		{
			Name: "atlas_process_runtime",
			Description: "Get live runtime state for one process definition: how many instances are " +
				"active and how many tokens sit on each BPMN element right now.",
			InputSchema: keyArg("The process definition key to inspect."),
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argUint(args, "key")
				if err != nil {
					return "", err
				}
				return asText(c.get("/api/v1/processes/" + strconv.FormatUint(key, 10) + "/runtime"))
			},
		},
		{
			Name: "atlas_create_instance",
			Description: "Start a new instance of a deployed process definition by its key and run it " +
				"until the engine goes idle. Returns the resulting live instance counts.",
			InputSchema: keyArg("The process definition key to instantiate."),
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argUint(args, "key")
				if err != nil {
					return "", err
				}
				return asText(c.post("/api/v1/processes/"+strconv.FormatUint(key, 10)+"/instances", "application/json", []byte("{}")))
			},
		},
		{
			Name:        "atlas_list_instances",
			Description: "List running process instances with their definition, version, token count, and state.",
			InputSchema: noArgs(),
			Handler: func(c *Client, _ map[string]any) (string, error) {
				return asText(c.get("/api/v1/instances"))
			},
		},
		{
			Name: "atlas_cancel_instance",
			Description: "Cancel (terminate) one running process instance by its instance key. All " +
				"its tokens are discarded and the instance moves to the 'terminated' state. " +
				"Use the large instance key from atlas_list_instances, not a definition key. " +
				"Returns the instance key, its new state, and live engine stats.",
			InputSchema: keyArg("The instance key (from atlas_list_instances) to cancel."),
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argUint(args, "key")
				if err != nil {
					return "", err
				}
				return asText(c.del("/api/v1/instances/" + strconv.FormatUint(key, 10)))
			},
		},
		{
			Name: "atlas_delete_process",
			Description: "Delete a deployed process definition by its key, removing it from the engine " +
				"and from disk. Refused with a conflict error if the definition still has running " +
				"instances — cancel them with atlas_cancel_instance first. Returns a confirmation.",
			InputSchema: keyArg("The process definition key (from atlas_list_processes) to delete."),
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argUint(args, "key")
				if err != nil {
					return "", err
				}
				body, err := c.del("/api/v1/processes/" + strconv.FormatUint(key, 10))
				if err != nil {
					return "", err
				}
				// The endpoint answers 204 No Content on success; give the model an
				// explicit confirmation rather than an empty string.
				if len(body) == 0 {
					return `{"deleted":true,"key":` + strconv.FormatUint(key, 10) + `}`, nil
				}
				return string(body), nil
			},
		},
		{
			Name:        "atlas_stats",
			Description: "Get live engine counts: active process instances and active element instances (tokens).",
			InputSchema: noArgs(),
			Handler: func(c *Client, _ map[string]any) (string, error) {
				return asText(c.get("/api/v1/stats"))
			},
		},
	}
}

// asText adapts a client call's (body, error) into a tool handler's
// (text, error): the raw body becomes the tool's text content on success.
func asText(body []byte, err error) (string, error) {
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// parseUint parses a base-10 unsigned integer, used by argUint for string- and
// json.Number-typed key arguments.
func parseUint(s string) (uint64, error) {
	return strconv.ParseUint(s, 10, 64)
}
