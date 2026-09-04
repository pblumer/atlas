package mcp

import (
	"encoding/json"
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
	tools := append(runtimeTools(), authoringTools()...)
	tools = append(tools, collabTools()...)
	return append(tools, infomodelTools()...)
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
			Name: "atlas_save_process_diagram",
			Description: "Save an adjusted diagram onto a deployed definition without redeploying it: " +
				"the submitted model's layout replaces the stored one, and nothing else about the " +
				"definition changes — same key, same version, same compiled process, same running " +
				"instances. Read the model with atlas_get_process_xml, move shapes, labels or edges, " +
				"and send the whole document back. Refused with a conflict if anything but the layout " +
				"differs from the deployed model; use atlas_deploy for that. A collaboration's pools " +
				"are all updated together, and the adjusted picture is what every view of that " +
				"definition draws from then on, finished instances included.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key": map[string]any{
						"type":        "integer",
						"description": "The process definition key whose diagram to replace.",
					},
					"xml": map[string]any{
						"type":        "string",
						"description": "The full BPMN 2.0 XML document: the deployed model with its diagram adjusted.",
					},
				},
				"required": []any{"key", "xml"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argUint(args, "key")
				if err != nil {
					return "", err
				}
				xml, err := argString(args, "xml")
				if err != nil {
					return "", err
				}
				return asText(c.put("/api/v1/processes/"+strconv.FormatUint(key, 10)+"/diagram",
					"application/xml", []byte(xml)))
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
			Name: "atlas_call_activities",
			Description: "List every call activity across all deployed processes on this server, with " +
				"its caller, the process id it calls, its version binding and propagation flags, whether " +
				"it is a multi-instance loop, and whether the called process is currently deployed here " +
				"(resolved) or not (would park at runtime). The per-server call-activity management view.",
			InputSchema: noArgs(),
			Handler: func(c *Client, _ map[string]any) (string, error) {
				return asText(c.get("/api/v1/call-activities"))
			},
		},
		{
			Name: "atlas_collaboration_runtime",
			Description: "Get live runtime state for a collaboration (a multi-pool model) by one of its pool " +
				"definition keys: the pools, the token counts on each element, and the message flows between pools. " +
				"Refused with a not-found error if no deployment has that key.",
			InputSchema: keyArg("A pool's process definition key (from atlas_list_processes) in the collaboration to inspect."),
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argUint(args, "key")
				if err != nil {
					return "", err
				}
				return asText(c.get("/api/v1/collaborations/" + strconv.FormatUint(key, 10) + "/runtime"))
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
			Description: "Find instances by key or variable content. 'q' is a bare process instance key (looked up " +
				"directly, live or finished, and returned with its whole variable set), or \"name=value\" (variable " +
				"name exact), or a term matched over variable names and values. A term is matched whole — " +
				"\"kdnr=MT-100\" finds MT-100 and not MT-10001 — with * for any run of characters and ? for exactly " +
				"one, so \"*MT-1*\" is the substring search; \\* and \\? match those characters literally. A content " +
				"query returns the matching instances (active first, then most-recently-completed), each with the " +
				"variables that matched; that result is capped.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"q":       stringProp("The query: a bare instance key, or \"name=value\" (name exact), or a term over variable names/values. The term is matched whole; use * for any run of characters and ? for exactly one, and \\* or \\? for those characters themselves. When 'process' names a definition that declares the variable atlas:searchable, the value index answers it — exactly for a literal term, and from the pattern's literal head for a wild one."),
					"process": stringProp("Optional process definition key: narrows the search to that version, which also makes it read that version's index instead of every instance."),
				},
				"required": []any{"q"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				q, err := argString(args, "q")
				if err != nil {
					return "", err
				}
				return asText(c.get(searchInstancesPath(q, optString(args, "process"))))
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
			Name: "atlas_variable_audit",
			Description: "Read one process instance's variable-override audit trail — the \"who changed it\" " +
				"history of external corrections to live state (ADR-0098): each entry has the actor, target scope, " +
				"variable name, and typed new value. An instance with no overrides (or an unknown key) returns [].",
			InputSchema: keyArg("The instance key (from atlas_list_instances) whose override audit trail to read."),
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argUint(args, "key")
				if err != nil {
					return "", err
				}
				return asText(c.get("/api/v1/instances/" + strconv.FormatUint(key, 10) + "/variable-audit"))
			},
		},
		{
			Name: "atlas_instance_data_objects",
			Description: "Read one process instance's BPMN data objects — each with its name, current data " +
				"state, typed value, declared class (itemSubjectRef) and collection flag, plus the trail of " +
				"every state it passed through and which element wrote each one. An instance with no data " +
				"objects (or an unknown key) returns [].",
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
			Name: "atlas_mail_outbox",
			Description: "List what a mail worker on the \"preview\" provider delivered in-server instead of " +
				"sending (ADR-0150) — how a scenario checks what a mail task actually produced, with no mail " +
				"server, no credential, and no real recipient involved. Newest first; each message carries its " +
				"worker, addressing, subject, bodies, and the framed RFC 5322 source that would have gone " +
				"out on the wire. Optional 'limit' returns only the newest n. Returns {messages, truncated}.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{
						"type":        "integer",
						"minimum":     1,
						"description": "Maximum messages to return, newest first (default: everything the outbox holds).",
					},
				},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				path := "/api/v1/mail/outbox"
				if limit, present, err := optPositiveUint(args, "limit"); err != nil {
					return "", err
				} else if present {
					path += "?limit=" + strconv.FormatUint(limit, 10)
				}
				return asText(c.get(path))
			},
		},
		{
			Name: "atlas_clear_mail_outbox",
			Description: "Empty the preview mail outbox so the next run's messages are the only ones in it — " +
				"the reset between two scenario runs. Nothing in the outbox was ever delivered to a recipient, " +
				"so this discards no record of a real send.",
			InputSchema: noArgs(),
			Handler: func(c *Client, _ map[string]any) (string, error) {
				body, err := c.del("/api/v1/mail/outbox")
				if err != nil {
					return "", err
				}
				// 204 No Content on success: answer with a confirmation rather than "".
				if len(body) == 0 {
					return `{"cleared":true}`, nil
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
				"A 'reason' is REQUIRED: forcing a step the engine would not have taken on its own is an " +
				"operator intervention, recorded with who did it and why in append-only audit history that " +
				"the instance timeline and replay surface (ADR-0159). " +
				"Refused with a not-found error if no job has that key. Returns {jobKey}.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key":       map[string]any{"type": "integer", "description": "The job key to complete."},
					"reason":    map[string]any{"type": "string", "description": "Why this job is being completed by hand — recorded in the instance's audit trail. Required."},
					"variables": objectProp("Optional job output variables, e.g. {\"paid\": true}. Omit for none."),
				},
				"required": []any{"key", "reason"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argUint(args, "key")
				if err != nil {
					return "", err
				}
				reason, err := argString(args, "reason")
				if err != nil {
					return "", err
				}
				body, err := optVariablesBodyWith(args, map[string]any{"reason": reason})
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
			Name: "atlas_workers",
			Description: "The Workers view — who is doing the engine's out-of-process work, and what is waiting. " +
				"Returns {types, workers}. Each 'types' row is a job type with its 'parked' queue depth, " +
				"'inFlight' count (leased to a worker right now), 'incidents', and 'servedInProcess' — true when " +
				"Atlas works that type itself, in which case no external worker can lease it. Each 'workers' row " +
				"is a worker seen since this server started: the 'types' it pulls, how many it holds 'inFlight', " +
				"and its 'pulled' / 'completed' / 'failed' counts with 'lastSeen'. The diagnosis to look for is a " +
				"type with a growing 'parked' count, zero 'inFlight' and no worker pulling it — work nobody is " +
				"serving. Worker counters cover this server run only and are not restored on restart.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Handler: func(c *Client, args map[string]any) (string, error) {
				return asText(c.get("/api/v1/workers"))
			},
		},
		{
			Name: "atlas_list_incidents",
			Description: "List unresolved incidents — the operator \"what's stuck\" view. Each incident carries " +
				"its elementInstanceKey (pass it to atlas_resolve_incident), processInstanceKey, processDefKey, " +
				"processId, jobKey, elementId (the BPMN id of the stuck element; elementIndex is its compiled " +
				"index), type (\"job\" or \"timer\"), raisedAt, and message. Optional 'instance' / 'process' " +
				"scope the list to one process instance or one deployed definition, and 'limit' bounds the page. " +
				"Returns {incidents, truncated}; when 'truncated' is true, more incidents exist than were " +
				"returned — resolve some, scope the query, or raise the limit.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{
						"type":        "integer",
						"minimum":     1,
						"description": "Maximum incidents to return (API default is generous, capped at 5000).",
					},
					"instance": map[string]any{
						"type":        "integer",
						"minimum":     1,
						"description": "Only incidents of this process instance key.",
					},
					"process": map[string]any{
						"type":        "integer",
						"minimum":     1,
						"description": "Only incidents of instances of this deployed definition key.",
					},
				},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				path, sep := "/api/v1/incidents", "?"
				for _, name := range []string{"limit", "instance", "process"} {
					v, present, err := optPositiveUint(args, name)
					if err != nil {
						return "", err
					}
					if present {
						path += sep + name + "=" + strconv.FormatUint(v, 10)
						sep = "&"
					}
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
		{
			Name: "atlas_migration_plan",
			Description: "Answer what migrating a running instance to another version of its process would " +
				"do — without changing anything. Returns {instanceKey, fromProcessDefKey, fromVersion, " +
				"toProcessDefKey, toVersion, mapping, problems, migratable}: 'mapping' pairs BPMN element ids " +
				"matched between the two versions, and 'problems' is every reason the migration would be " +
				"refused (a token on an element the target does not have, a changed element type or scope, a " +
				"catch waiting on a different message). Call this before atlas_migrate_instance — the mapping " +
				"is derived from two graphs and 'which of my tokens would be stranded' is not answerable by " +
				"eye (ADR-0162).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key":                 map[string]any{"type": "integer", "description": "The running instance key to plan a migration for."},
					"targetProcessDefKey": map[string]any{"type": "integer", "description": "The deployed definition key to migrate to — another version of the same process."},
					"mapping":             migrationMappingSchema(),
				},
				"required": []any{"key", "targetProcessDefKey"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argUint(args, "key")
				if err != nil {
					return "", err
				}
				body, err := migrationBody(args, false)
				if err != nil {
					return "", err
				}
				return asText(c.post("/api/v1/instances/"+strconv.FormatUint(key, 10)+"/migrate/plan", "application/json", body))
			},
		},
		{
			Name: "atlas_migrate_instance",
			Description: "Rebind a running instance to another version of its process, keeping its variables, " +
				"jobs, history and element instance keys — the alternative to cancelling and restarting it " +
				"when a model fix cannot otherwise reach an instance that is already running. A 'reason' is " +
				"required and recorded as an operator action with the caller's identity. Refused (409) with " +
				"the same plan atlas_migration_plan returns when the mapping does not hold; nothing is " +
				"written in that case (ADR-0162).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key":                 map[string]any{"type": "integer", "description": "The running instance key to migrate."},
					"targetProcessDefKey": map[string]any{"type": "integer", "description": "The deployed definition key to migrate to — another version of the same process."},
					"reason":              map[string]any{"type": "string", "description": "Why this instance is being migrated. Recorded in the instance's audit trail and shown on its replay."},
					"mapping":             migrationMappingSchema(),
				},
				"required": []any{"key", "targetProcessDefKey", "reason"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argUint(args, "key")
				if err != nil {
					return "", err
				}
				body, err := migrationBody(args, true)
				if err != nil {
					return "", err
				}
				return asText(c.post("/api/v1/instances/"+strconv.FormatUint(key, 10)+"/migrate", "application/json", body))
			},
		},
		{
			Name: "atlas_migrate_instances",
			Description: "Migrate a bounded batch of one deployed definition's running instances to another " +
				"version of the same process. Each instance is migrated independently, so a refusal on one " +
				"does not roll back the others: returns {toProcessDefKey, migrated, refused, remaining}, " +
				"where 'refused' carries a full plan per instance that could not move. Repeat while " +
				"'remaining' is true. A 'reason' is required (ADR-0162).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key":                 map[string]any{"type": "integer", "description": "The deployed definition key whose running instances to migrate."},
					"targetProcessDefKey": map[string]any{"type": "integer", "description": "The deployed definition key to migrate them to."},
					"reason":              map[string]any{"type": "string", "description": "Why these instances are being migrated. Recorded per instance in its audit trail."},
					"limit":               map[string]any{"type": "integer", "minimum": 1, "description": "Maximum instances to migrate in this call (default 500, capped at 5000)."},
					"mapping":             migrationMappingSchema(),
				},
				"required": []any{"key", "targetProcessDefKey", "reason"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argUint(args, "key")
				if err != nil {
					return "", err
				}
				body, err := migrationBody(args, true)
				if err != nil {
					return "", err
				}
				path := "/api/v1/processes/" + strconv.FormatUint(key, 10) + "/migrate-instances"
				if limit, present, err := optPositiveUint(args, "limit"); err != nil {
					return "", err
				} else if present {
					path += "?limit=" + strconv.FormatUint(limit, 10)
				}
				return asText(c.post(path, "application/json", body))
			},
		},
	}
}

// migrationMappingSchema describes the optional element-id overrides all three
// migration tools accept. Ids, never compiled indices: an index is an internal encoding
// that shifts between versions, and pairing by the id a modeler wrote is what makes the
// default mapping work at all (ADR-0162).
func migrationMappingSchema() map[string]any {
	return map[string]any{
		"type": "array",
		"description": "Optional element-id overrides, for elements whose BPMN id changed between the two " +
			"versions. Elements whose ids match are paired automatically and need no entry here.",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"from": map[string]any{"type": "string", "description": "BPMN element id in the source version."},
				"to":   map[string]any{"type": "string", "description": "BPMN element id in the target version."},
			},
			"required": []any{"from", "to"},
		},
	}
}

// migrationBody assembles the shared request body for the migration tools. withReason
// is true for the two that actually write, where the API requires one.
func migrationBody(args map[string]any, withReason bool) ([]byte, error) {
	target, err := argUint(args, "targetProcessDefKey")
	if err != nil {
		return nil, err
	}
	body := map[string]any{"targetProcessDefKey": target}
	if withReason {
		reason, err := argString(args, "reason")
		if err != nil {
			return nil, err
		}
		body["reason"] = reason
	}
	if raw, ok := args["mapping"]; ok && raw != nil {
		body["mapping"] = raw
	}
	return json.Marshal(body)
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
