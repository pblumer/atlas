package mcp

import (
	"net/url"
	"strconv"
)

// The published-interface tools (ADR-0373): what another domain may read about a
// process here, and what a deployed definition offers for publication.
//
// They are reads. An agent modelling an interaction with another domain needs the
// contract — the entry point names, whether each starts or continues an instance,
// its correlation key and payload, the errors, escalations and replies that may come
// back, and the outcomes — and a publisher's agent needs the candidates. Publishing,
// rebinding, deprecating and granting stay on the HTTP surface: each is a deliberate
// act on a contract other domains bind to, and the grant is the application owner's
// own, from their session.

func pubInterfaceTools() []Tool {
	return []Tool{
		{
			Name: "atlas_list_published_interfaces",
			Description: "The published process interfaces you may see (ADR-0373): each with its latest contract " +
				"version, whether it is deprecated and available (unavailableReasons), its entry point names, " +
				"'public' and 'maySend'. An interface you may not view is not listed. Read one whole with " +
				"atlas_get_published_interface before modelling a message to it.",
			InputSchema: noArgs(),
			Handler: func(c *Client, _ map[string]any) (string, error) {
				return asText(c.get("/api/v1/published-interfaces"))
			},
		},
		{
			Name: "atlas_get_published_interface",
			Description: "One published interface with every contract version (ADR-0373), or only 'version' when " +
				"given. A contract names its entry points — 'name' (what a sender addresses, never an element id), " +
				"'kind', 'starts' (creates an instance) or not (continues a waiting one), 'correlationKey' and " +
				"'payload' (an information-model class, field names, or untyped) — the 'returns' a consumer models " +
				"a boundary event for (errors, escalations, reply messages) and the terminal 'outcomes'. A " +
				"deprecated version still works; move off it. 'available' false says the bound definition is " +
				"missing or inactive. Members of the publishing application also see each version's binding.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":      stringProp("The interface id (from atlas_list_published_interfaces)."),
					"version": integerProp("Optional: one contract version only."),
				},
				"required": []any{"id"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				path := "/api/v1/published-interfaces/" + url.PathEscape(id)
				if _, ok := args["version"]; ok {
					v, err := argUint(args, "version")
					if err != nil {
						return "", err
					}
					path += "/versions/" + strconv.FormatUint(v, 10)
				}
				return asText(c.get(path))
			},
		},
		{
			Name: "atlas_interface_candidates",
			Description: "What one deployed definition offers for publication as an interface (ADR-0373): every " +
				"element a message can reach with its kind, message name, correlation key and derived payload; " +
				"every root-scope end event as an outcome; the error and escalation codes that can leave an " +
				"instance uncaught; the messages it throws; and the interfaces the process already publishes. " +
				"For the application's members.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key": integerProp("The process DEFINITION key (from atlas_list_processes)."),
				},
				"required": []any{"key"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argUint(args, "key")
				if err != nil {
					return "", err
				}
				return asText(c.get("/api/v1/processes/" + strconv.FormatUint(key, 10) + "/interface-candidates"))
			},
		},
	}
}
