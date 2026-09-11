package mcp

import (
	"encoding/json"
	"net/url"
)

// The business-architecture tools: the business capabilities an organisation must be
// able to perform, and the value streams whose stages they perform
// (ADR-draft-business-capabilities-and-value-streams).
//
// An agent authoring BPMN through these tools runs into the gap the registry exists to
// close, in a sharper form than a person does. It can deploy a process and have no way
// to say what part of the business that process is *for*, who owns it, or what it
// promised — and no way to find out whether the thing it is about to build already
// exists under another name. The map answers all three, and the gap report is what
// stops it from going stale as the agent works.
//
// Every write goes through the same validation the HTTP surface applies, so a refusal
// teaches the vocabulary rather than only refusing. Read the subset first.

func capabilityKeyArg(desc string) map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"key": stringProp(desc)},
		"required":   []any{"key"},
	}
}

// capabilityRecordProps are the writable fields of a capability, shared by create and
// save so the two cannot drift into describing different records.
func capabilityRecordProps() map[string]any {
	return map[string]any{
		"key": stringProp("The identity: lower-case letters, digits and dashes, e.g. \"loan-underwriting\". " +
			"It is the URL, the filename, and what every requires and value-stream stage names — so it " +
			"cannot be renamed later."),
		"name":    stringProp("What people call it, e.g. \"Loan Underwriting\"."),
		"summary": stringProp("Optional one line for a list."),
		"scope": stringProp("What this capability is responsible for AND what it is not — " +
			"\"the credit decision, not identity or address checks\". The half that says what it is " +
			"not is what settles boundary arguments later."),
		"inputs": arrayProp("Optional triggers: [{name, kind: api|event|message|manual, description}]. " +
			"manual is a real kind — a capability nobody has automated still has an interface."),
		"outputs": arrayProp("Optional results: [{name, kind: api|event|message|manual, description}]."),
		"owner": map[string]any{"type": "object",
			"description": "The BUSINESS owner, accountable for how the capability performs: " +
				"{name, role, contact, username}. Not the application's sharing scope, which decides who " +
				"may edit a model; those are usually different people. username is optional and grants nothing."},
		"resources": arrayProp("Optional: what it draws on, [{kind: team|system|capability, name, ref}]."),
		"realizations": arrayProp("How it is CURRENTLY done. Four kinds: " +
			"{kind:\"process\", applicationKey, processId} for an executable process here — the PORTABLE " +
			"application key, not a local id; {kind:\"worker\", workerRef}; {kind:\"system\", note} for a " +
			"purchased system; {kind:\"manual\", note} for work a person does. An EMPTY list is valid and " +
			"expected: it means nothing automates this yet, which is the most useful row in the map."),
		"requires": arrayProp("Capability keys this one depends on, as BLACK BOXES. Declared, never " +
			"derived: from the caller's side a required capability is normally a service task or a " +
			"message, not a call activity."),
		"kpis": arrayProp("Optional targets: [{name, metric, goal, direction: up|down, note}]. " +
			"DECLARATIONS — Atlas computes none of them."),
		"slas": arrayProp("Optional commitments: [{name, metric, threshold, window, " +
			"scope: internal|external, counterparty, note}]. The difference from a KPI is commitment: an " +
			"SLA is how an end-to-end target is distributed across the capabilities beneath it."),
		"tags": arrayProp("The ONLY classification there is: capabilities are a flat list with no " +
			"hierarchy. Prefix them for whatever view you need — \"area:lending\", \"type:end-to-end\"."),
		"state": stringProp("proposed (default), active, or deprecated."),
	}
}

func valueStreamRecordProps() map[string]any {
	return map[string]any{
		"key":         stringProp("The identity, e.g. \"consumer-loan\". Cannot be renamed later."),
		"name":        stringProp("What people call it, e.g. \"Consumer Loan\"."),
		"description": stringProp("Optional: the customer need this stream meets."),
		"owner": map[string]any{"type": "object",
			"description": "The business owner: {name, role, contact, username}. At this level usually an executive."},
		"stages": arrayProp("The ORDERED stages: [{key, name, description, capabilities: [key, ...]}]. " +
			"A capability may appear in several stages — an end-to-end capability spans them, which is " +
			"the method's own accepted inconsistency and not an error."),
		"kpis": arrayProp("Optional stream targets, distributed downward as the capabilities' SLAs."),
		"tags": arrayProp("Optional tags."),
	}
}

// recordFromArgs copies the properties a tool declares out of its arguments, so a
// handler forwards exactly the fields the schema advertises and nothing a caller
// invented.
func recordFromArgs(args map[string]any, props map[string]any) map[string]any {
	out := make(map[string]any, len(props))
	for name := range props {
		if v, ok := args[name]; ok && v != nil {
			out[name] = v
		}
	}
	return out
}

// confirm posts a confirmation for either record kind. Neither optional field is sent
// when empty, so an omitted note clears nothing it did not mean to.
func confirm(c *Client, args map[string]any, prefix string) (string, error) {
	key, err := argString(args, "key")
	if err != nil {
		return "", err
	}
	payload := map[string]any{}
	for _, name := range []string{"with", "note"} {
		if v := optString(args, name); v != "" {
			payload[name] = v
		}
	}
	body, _ := json.Marshal(payload)
	return asText(c.post(prefix+url.PathEscape(key)+"/confirmation", "application/json", body))
}

func capabilityTools() []Tool {
	return []Tool{
		{
			Name: "atlas_business_architecture_subset",
			Description: "Read the business-architecture vocabularies this build accepts: capability " +
				"states, interface kinds, resource kinds, realization kinds, KPI directions, SLA scopes, " +
				"the gap-report finding kinds, and the key pattern. It also states that capabilities have " +
				"NO hierarchy — they are a flat list classified by tags, and an end-to-end process is a " +
				"capability carrying a tag rather than one at a higher level. Read it before writing.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Handler: func(c *Client, _ map[string]any) (string, error) {
				return asText(c.get("/api/v1/business-architecture/subset"))
			},
		},
		{
			Name: "atlas_list_capabilities",
			Description: "List business capabilities: what the organisation must be able to do, stated " +
				"independently of how. Each row says whether anything currently realizes it. Filter by " +
				"tag, state, a substring of the name or key, realized and stale. realized=false is the " +
				"adoption backlog, everything nothing currently does; stale=true is its twin, the review " +
				"backlog: everything nobody has confirmed within the installation's horizon.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tag":      stringProp("Optional: only capabilities carrying this exact tag."),
					"state":    stringProp("Optional: proposed, active or deprecated."),
					"q":        stringProp("Optional: a substring of the name or key."),
					"realized": stringProp("Optional: \"false\" for the backlog, \"true\" for what is realized."),
					"stale": stringProp("Optional: \"true\" for the review backlog — everything nobody has " +
						"confirmed within the installation's horizon, which is the twin of realized=false."),
				},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				q := url.Values{}
				for _, name := range []string{"tag", "state", "q", "realized", "stale"} {
					if v := optString(args, name); v != "" {
						q.Set(name, v)
					}
				}
				path := "/api/v1/capabilities"
				if len(q) > 0 {
					path += "?" + q.Encode()
				}
				return asText(c.get(path))
			},
		},
		{
			Name: "atlas_get_capability",
			Description: "Read one business capability whole: its scope, interface, business owner, " +
				"resources, realizations, dependencies, KPIs, SLAs and tags.",
			InputSchema: capabilityKeyArg("The capability key (from atlas_list_capabilities)."),
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argString(args, "key")
				if err != nil {
					return "", err
				}
				return asText(c.get("/api/v1/capabilities/" + url.PathEscape(key)))
			},
		},
		{
			Name: "atlas_capability_coverage",
			Description: "Resolve one capability against this server: which deployed process or Worker " +
				"actually does it (with the current version and running instance count, resolved now and " +
				"stored nowhere), what it depends on and what each of those has PROMISED through its " +
				"SLAs, which capabilities depend on it, and which value-stream stages it performs. Use it " +
				"before changing a process: it says what would stop. The KPIs and SLAs it returns are " +
				"declarations — the answer says so, and nothing here is measured.",
			InputSchema: capabilityKeyArg("The capability key."),
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argString(args, "key")
				if err != nil {
					return "", err
				}
				return asText(c.get("/api/v1/capabilities/" + url.PathEscape(key) + "/coverage"))
			},
		},
		{
			Name: "atlas_create_capability",
			Description: "File a business capability. A capability nothing realizes yet is VALID and " +
				"expected — it is the work still done by hand or by a system nobody recorded, and it is " +
				"the row the gap report counts. Only key and name are required; everything else can be " +
				"filled in later with atlas_save_capability.",
			InputSchema: map[string]any{
				"type": "object", "properties": capabilityRecordProps(),
				"required": []any{"key", "name"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				if _, err := argString(args, "key"); err != nil {
					return "", err
				}
				if _, err := argString(args, "name"); err != nil {
					return "", err
				}
				body, _ := json.Marshal(recordFromArgs(args, capabilityRecordProps()))
				return asText(c.post("/api/v1/capabilities", "application/json", body))
			},
		},
		{
			Name: "atlas_save_capability",
			Description: "Replace a capability's content. Send the WHOLE record — anything omitted is " +
				"cleared, so read it with atlas_get_capability first and send back what you changed. Pass " +
				"the revision you read to be refused if somebody else edited it meanwhile. The key cannot " +
				"be changed: every requires and value-stream stage naming it would be left pointing at nothing.",
			InputSchema: func() map[string]any {
				props := capabilityRecordProps()
				props["revision"] = integerProp("Optional: the revision you read. A stale one is refused as a conflict.")
				return map[string]any{"type": "object", "properties": props, "required": []any{"key", "name"}}
			}(),
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argString(args, "key")
				if err != nil {
					return "", err
				}
				if _, err := argString(args, "name"); err != nil {
					return "", err
				}
				props := capabilityRecordProps()
				props["revision"] = nil
				body, _ := json.Marshal(recordFromArgs(args, props))
				return asText(c.put("/api/v1/capabilities/"+url.PathEscape(key), "application/json", body))
			},
		},
		{
			Name: "atlas_delete_capability",
			Description: "Delete a capability. It does not cascade: the answer names the dependencies " +
				"and value-stream stages that are now pointing at nothing, so you can fix them.",
			InputSchema: capabilityKeyArg("The capability key to delete."),
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argString(args, "key")
				if err != nil {
					return "", err
				}
				return asText(c.del("/api/v1/capabilities/" + url.PathEscape(key)))
			},
		},
		{
			Name: "atlas_list_value_streams",
			Description: "List value streams: the ordered activity the organisation performs to meet a " +
				"customer need, whose stages name the capabilities performing them.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tag":   stringProp("Optional: only streams carrying this exact tag."),
					"stale": stringProp("Optional: \"true\" for the ones nobody has confirmed recently."),
				},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				q := url.Values{}
				for _, name := range []string{"tag", "stale"} {
					if v := optString(args, name); v != "" {
						q.Set(name, v)
					}
				}
				path := "/api/v1/value-streams"
				if len(q) > 0 {
					path += "?" + q.Encode()
				}
				return asText(c.get(path))
			},
		},
		{
			Name:        "atlas_get_value_stream",
			Description: "Read one value stream whole, with its ordered stages and the capabilities each names.",
			InputSchema: capabilityKeyArg("The value stream key (from atlas_list_value_streams)."),
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argString(args, "key")
				if err != nil {
					return "", err
				}
				return asText(c.get("/api/v1/value-streams/" + url.PathEscape(key)))
			},
		},
		{
			Name: "atlas_create_value_stream",
			Description: "File a value stream with its ordered stages. A stage names the capabilities " +
				"that perform it; a stage with none is a step nobody owns, which the gap report reports.",
			InputSchema: map[string]any{
				"type": "object", "properties": valueStreamRecordProps(),
				"required": []any{"key", "name"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				if _, err := argString(args, "key"); err != nil {
					return "", err
				}
				if _, err := argString(args, "name"); err != nil {
					return "", err
				}
				body, _ := json.Marshal(recordFromArgs(args, valueStreamRecordProps()))
				return asText(c.post("/api/v1/value-streams", "application/json", body))
			},
		},
		{
			Name: "atlas_save_value_stream",
			Description: "Replace a value stream's content. Send the WHOLE record; pass the revision you " +
				"read to be refused if somebody else edited it meanwhile. The key cannot be changed.",
			InputSchema: func() map[string]any {
				props := valueStreamRecordProps()
				props["revision"] = integerProp("Optional: the revision you read.")
				return map[string]any{"type": "object", "properties": props, "required": []any{"key", "name"}}
			}(),
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argString(args, "key")
				if err != nil {
					return "", err
				}
				if _, err := argString(args, "name"); err != nil {
					return "", err
				}
				props := valueStreamRecordProps()
				props["revision"] = nil
				body, _ := json.Marshal(recordFromArgs(args, props))
				return asText(c.put("/api/v1/value-streams/"+url.PathEscape(key), "application/json", body))
			},
		},
		{
			Name:        "atlas_delete_value_stream",
			Description: "Delete a value stream. Nothing references a stream, so nothing is left dangling.",
			InputSchema: capabilityKeyArg("The value stream key to delete."),
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argString(args, "key")
				if err != nil {
					return "", err
				}
				if _, err := c.del("/api/v1/value-streams/" + url.PathEscape(key)); err != nil {
					return "", err
				}
				return "deleted value stream " + key, nil
			},
		},
		{
			Name: "atlas_confirm_capability",
			Description: "Record that you have READ a capability and that it still describes reality. " +
				"It is its own call and no edit sets it: if saving refreshed the date, fixing a typo in " +
				"the summary would assert that the owner, the scope and every SLA had been re-checked. " +
				"Only confirm what you actually re-read — a date nobody looked behind tells the next " +
				"reader it was verified when it was not. Name who you asked in `with` where somebody " +
				"other than you stood behind it; leaving it empty says you spoke for the record alone, " +
				"which is legitimate and weaker, and the record says so either way.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key": stringProp("The capability key."),
					"with": stringProp("Optional: who was asked — the business owner, a team lead. " +
						"Empty means you spoke for the record alone."),
					"note": stringProp("Optional: one line on what the review found, e.g. " +
						"\"SLA renegotiated to 3 days\". It replaces the previous note rather than being appended."),
				},
				"required": []any{"key"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				return confirm(c, args, "/api/v1/capabilities/")
			},
		},
		{
			Name: "atlas_confirm_value_stream",
			Description: "Record that you have read a value stream and that it still describes reality. " +
				"The twin of atlas_confirm_capability, and the same rule: only confirm what you re-read.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key":  stringProp("The value stream key."),
					"with": stringProp("Optional: who was asked."),
					"note": stringProp("Optional: one line on what the review found."),
				},
				"required": []any{"key"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				return confirm(c, args, "/api/v1/value-streams/")
			},
		},
		{
			Name: "atlas_business_architecture_gaps",
			Description: "Compare the whole capability map against what this server actually runs. It " +
				"reports capabilities nothing realizes (the work still done by hand), realizations " +
				"pointing at what is not here, deployed processes no capability claims, value-stream " +
				"stages with no capability, dependencies naming no capability, and — the one worth the " +
				"most — a call activity crossing from one capability's process into another's that the " +
				"caller never declared. It is a comparison, never a merge: a declared dependency with no " +
				"call activity is the ordinary case and raises nothing. It also reports records nobody " +
				"has confirmed within the installation's horizon — the one finding that is NOT something " +
				"Atlas verified, because the owner, the scope and the SLAs are prose it cannot check, so " +
				"the only honest thing it can say is that nobody has stood behind them. Nothing is " +
				"stored; the answer says which horizon it applied, how much your own access hid from it, " +
				"and what it looked at.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Handler: func(c *Client, _ map[string]any) (string, error) {
				return asText(c.get("/api/v1/business-architecture/gaps"))
			},
		},
	}
}
