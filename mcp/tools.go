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
// so a model receives the server's structured response verbatim. The runtime
// tools are listed here; the design-time and human-task tools (projects, drafts,
// forms, decisions, task completion) are appended from authoringTools.
func defaultTools() []Tool {
	return append(runtimeTools(), authoringTools()...)
}

// runtimeTools are the deploy/instance/inspect tools.
func runtimeTools() []Tool {
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
				"until the engine goes idle. Optionally seed the instance scope with start variables " +
				"(the same {name: value} shape a human's start form submits). Returns the resulting " +
				"live instance counts.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key": map[string]any{
						"type":        "integer",
						"description": "The process definition key to instantiate.",
					},
					"variables": objectProp("Optional start variables to seed the instance scope, e.g. {\"amount\": 42}. Omit for none."),
				},
				"required": []any{"key"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argUint(args, "key")
				if err != nil {
					return "", err
				}
				body, err := optVariablesBody(args)
				if err != nil {
					return "", err
				}
				return asText(c.post("/api/v1/processes/"+strconv.FormatUint(key, 10)+"/instances", "application/json", body))
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
			Name: "atlas_instances_summary",
			Description: "Per-definition instance counts — one row per deployed definition with its processId, " +
				"version, active and completed instance counts, and last-activity time. The operations overview.",
			InputSchema: noArgs(),
			Handler: func(c *Client, _ map[string]any) (string, error) {
				return asText(c.get("/api/v1/instances/summary"))
			},
		},
		{
			Name: "atlas_search_instances",
			Description: "Search instances by variable content. 'q' is either \"name=value\" (variable name exact, " +
				"value substring) or free text matched over variable names and values. Returns the matching instances " +
				"(active first, then most-recently-completed), each with the variables that matched; the result is capped.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"q": stringProp("The query: \"name=value\" (name exact, value substring) or free text over variable names/values."),
				},
				"required": []any{"q"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				q, err := argString(args, "q")
				if err != nil {
					return "", err
				}
				return asText(c.get(searchInstancesPath(q)))
			},
		},
		{
			Name: "atlas_instance_variables",
			Description: "Read one process instance's variables as a typed JSON object (name → value). " +
				"An instance with no variables (or an unknown key) returns an empty object.",
			InputSchema: keyArg("The instance key (from atlas_list_instances) to read variables for."),
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argUint(args, "key")
				if err != nil {
					return "", err
				}
				return asText(c.get("/api/v1/instances/" + strconv.FormatUint(key, 10) + "/variables"))
			},
		},
		{
			Name: "atlas_instance_data_objects",
			Description: "Read one process instance's BPMN data objects — each with its name, current data " +
				"state, and typed value. An instance with no data objects (or an unknown key) returns [].",
			InputSchema: keyArg("The instance key (from atlas_list_instances) whose data objects to read."),
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argUint(args, "key")
				if err != nil {
					return "", err
				}
				return asText(c.get("/api/v1/instances/" + strconv.FormatUint(key, 10) + "/data-objects"))
			},
		},
		{
			Name: "atlas_instance_jobs",
			Description: "List one process instance's activatable jobs — a token parked on a service (or " +
				"other job-backed) task exposes its job here with the job key, element, and type. Use a job key " +
				"with atlas_complete_job or atlas_fail_job. An instance with no jobs (or an unknown key) returns [].",
			InputSchema: keyArg("The instance key (from atlas_list_instances) whose jobs to list."),
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argUint(args, "key")
				if err != nil {
					return "", err
				}
				return asText(c.get("/api/v1/instances/" + strconv.FormatUint(key, 10) + "/jobs"))
			},
		},
		{
			Name: "atlas_instance_timeline",
			Description: "Read one process instance's step-by-step replay timeline: the activated elements " +
				"in order with the variable values live at each step. Refused with a not-found error if no " +
				"instance has that key.",
			InputSchema: keyArg("The instance key (from atlas_list_instances) to build a timeline for."),
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argUint(args, "key")
				if err != nil {
					return "", err
				}
				return asText(c.get("/api/v1/instances/" + strconv.FormatUint(key, 10) + "/timeline"))
			},
		},
		{
			Name: "atlas_instance_decisions",
			Description: "Read the DMN decision evaluations one process instance made — each with the decision id, " +
				"the business rule task that called it, and the inputs, outputs, and evaluation trace. An instance " +
				"that evaluated no decisions (or an unknown key) returns [].",
			InputSchema: keyArg("The instance key (from atlas_list_instances) whose decision evaluations to read."),
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argUint(args, "key")
				if err != nil {
					return "", err
				}
				return asText(c.get("/api/v1/instances/" + strconv.FormatUint(key, 10) + "/decisions"))
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
			Name: "atlas_cancel_instances",
			Description: "Bulk-cancel (terminate) a definition's running instances by its DEFINITION key — " +
				"the drain for a runaway flood where cancelling instances one at a time is infeasible. " +
				"Cancels up to a bounded batch per call (optional 'limit', default 5000, max 50000) and " +
				"returns {definitionKey, canceled, remaining, stats}. When 'remaining' is true the cap was " +
				"hit; call again with the same key until 'canceled' is 0. Pass the small DEFINITION key " +
				"from atlas_list_processes, not an instance key.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key": map[string]any{
						"type":        "integer",
						"description": "The process DEFINITION key (from atlas_list_processes) whose running instances to cancel.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum instances to cancel in this call (default 5000, capped at 50000).",
					},
				},
				"required": []any{"key"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argUint(args, "key")
				if err != nil {
					return "", err
				}
				path := "/api/v1/processes/" + strconv.FormatUint(key, 10) + "/cancel-instances"
				if _, ok := args["limit"]; ok {
					limit, err := argUint(args, "limit")
					if err != nil {
						return "", err
					}
					path += "?limit=" + strconv.FormatUint(limit, 10)
				}
				return asText(c.post(path, "application/json", []byte("{}")))
			},
		},
		{
			Name: "atlas_terminate_instances",
			Description: "Terminate a selected set of running instances in one call. Two mutually exclusive " +
				"modes: pass 'keys' — an explicit array of instance keys (from atlas_list_instances) — to " +
				"terminate exactly those; or pass 'processDefKey' (from atlas_list_processes) to terminate that " +
				"definition's active instances, optionally narrowed by 'q' (a variable query like \"name=value\" " +
				"or free text) and bounded per call by 'limit'. Returns {terminated, notFound, remaining, stats}: " +
				"in keys mode 'notFound' counts keys with no active instance; in filter mode 'remaining' true " +
				"means the cap was hit — call again with the same arguments until it is false.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"keys": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "integer"},
						"description": "Explicit instance keys to terminate. Mutually exclusive with processDefKey.",
					},
					"processDefKey": map[string]any{"type": "integer", "description": "Terminate this definition's active instances (from atlas_list_processes). Mutually exclusive with keys."},
					"q":             stringProp("Optional variable query to narrow filter mode (\"name=value\" or free text). Only with processDefKey."),
					"limit":         map[string]any{"type": "integer", "minimum": 1, "description": "Filter-mode per-call cap (capped by the API). Repeat while remaining is true."},
				},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				body, err := terminateInstancesBody(args)
				if err != nil {
					return "", err
				}
				return asText(c.post("/api/v1/instances/terminate", "application/json", body))
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
		{
			Name: "atlas_publish_message",
			Description: "Publish a message for correlation: any instance waiting at a message catch event " +
				"whose correlation key matches is delivered the message and advances. Provide the message " +
				"'name' and, when the catch event correlates on a key, the 'correlationKey' value to match. " +
				"A message that matches no waiting instance is a legal no-op. Optional 'variables' are merged " +
				"into a correlated instance's scope. Returns {name, correlationKey, stats}.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":           stringProp("The message name (matches the BPMN message's name)."),
					"correlationKey": stringProp("The correlation key value to match against waiting instances. Omit for an unkeyed message."),
					"variables":      objectProp("Optional variables merged into a correlated instance's scope, e.g. {\"approved\": true}. Omit for none."),
				},
				"required": []any{"name"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				name, err := argString(args, "name")
				if err != nil {
					return "", err
				}
				body, err := messageBody(name, args)
				if err != nil {
					return "", err
				}
				return asText(c.post("/api/v1/messages", "application/json", body))
			},
		},
		{
			Name: "atlas_complete_job",
			Description: "Complete a job by hand by its job key — the operator counterpart to an external " +
				"worker completing it, driving an instance parked on a service (or other job-backed) task " +
				"forward. Optional 'variables' are written into the instance scope as the job's outputs. " +
				"Refused with a not-found error if no job has that key. Returns {jobKey}.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key":       map[string]any{"type": "integer", "description": "The job key to complete."},
					"variables": objectProp("Optional job output variables, e.g. {\"paid\": true}. Omit for none."),
				},
				"required": []any{"key"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argUint(args, "key")
				if err != nil {
					return "", err
				}
				body, err := optVariablesBody(args)
				if err != nil {
					return "", err
				}
				return asText(c.post("/api/v1/jobs/"+strconv.FormatUint(key, 10)+"/complete", "application/json", body))
			},
		},
		{
			Name: "atlas_fail_job",
			Description: "Fail a job by its job key — the operator counterpart to atlas_complete_job. " +
				"'retries' is how many attempts the job has left after this failure: a positive value " +
				"re-activates the job for another try; 0 (the default) exhausts it and raises an incident " +
				"that blocks the instance until resolved. An optional 'message' records why it failed. " +
				"Refused with a not-found error if no job has that key. Returns {jobKey}.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key":     map[string]any{"type": "integer", "description": "The job key to fail."},
					"retries": map[string]any{"type": "integer", "minimum": 0, "description": "Attempts left after this failure. 0 (default) raises an incident; a positive value re-activates the job."},
					"message": stringProp("Optional failure message recorded on the incident/job."),
				},
				"required": []any{"key"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argUint(args, "key")
				if err != nil {
					return "", err
				}
				body, err := failJobBody(args)
				if err != nil {
					return "", err
				}
				return asText(c.post("/api/v1/jobs/"+strconv.FormatUint(key, 10)+"/fail", "application/json", body))
			},
		},
		{
			Name: "atlas_list_incidents",
			Description: "List unresolved incidents — the operator \"what's stuck\" view. Each incident carries " +
				"its elementInstanceKey (pass it to atlas_resolve_incident), processInstanceKey, jobKey, elementId, " +
				"raisedAt, and message. Optional 'limit' bounds the page. Returns {incidents, truncated}; when " +
				"'truncated' is true, more incidents exist than were returned — resolve some or raise the limit.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{
						"type":        "integer",
						"minimum":     1,
						"description": "Maximum incidents to return (API default is generous, capped at 5000).",
					},
				},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				path := "/api/v1/incidents"
				if limit, present, err := optPositiveUint(args, "limit"); err != nil {
					return "", err
				} else if present {
					path += "?limit=" + strconv.FormatUint(limit, 10)
				}
				body, headers, err := c.getWithHeaders(path)
				if err != nil {
					return "", err
				}
				return incidentsPage(body, headers)
			},
		},
		{
			Name: "atlas_resolve_incident",
			Description: "Resolve the incident on an element instance by its elementInstanceKey (from " +
				"atlas_list_incidents) and retry its blocked job. Optional 'retries' sets how many attempts to " +
				"grant (default 1). Refused with a not-found error if there is no incident on that element " +
				"instance. Returns {elementInstanceKey, jobKey, retries}.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key":     map[string]any{"type": "integer", "description": "The elementInstanceKey (from atlas_list_incidents) whose incident to resolve."},
					"retries": map[string]any{"type": "integer", "minimum": 1, "description": "Attempts to grant the re-activated job (default 1)."},
				},
				"required": []any{"key"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argUint(args, "key")
				if err != nil {
					return "", err
				}
				body, err := resolveIncidentBody(args)
				if err != nil {
					return "", err
				}
				return asText(c.post("/api/v1/incidents/"+strconv.FormatUint(key, 10)+"/resolve", "application/json", body))
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
