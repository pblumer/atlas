package mcp

import (
	"encoding/json"
	"net/url"
)

// collabTools expose ADR-0103's live modeling sessions to an AI agent, so Claude
// can join a draft and co-edit it alongside people in real time (the ADR-0103 M2
// slice). The engine is never touched: each tool is a thin call onto the same
// design-time HTTP session endpoints the browser uses (ADR-0016), and the
// session lives entirely in the server's in-memory registry, off the six
// invariants.
//
// Because an MCP tool call is request/response, the agent cannot hold the SSE
// stream a browser does. Instead it joins without a stream (atlas_join_session),
// then reads what others changed by polling (atlas_session_poll) and drives its
// own edits with the lock / change / presence tools — appearing in the room as a
// participant like anyone else. It must call atlas_leave_session when done, since
// there is no stream disconnect to reap it.

// sessionPath builds the session sub-path for a draft, escaping the draft id as a
// path segment.
func sessionPath(draftID, suffix string) string {
	return "/api/v1/drafts/" + url.PathEscape(draftID) + "/session" + suffix
}

func collabTools() []Tool {
	draftIDProp := stringProp("The draft's process id (from atlas_list_drafts) whose live session to act on.")
	participantProp := stringProp("Your participant id, returned by atlas_join_session under \"self\".")
	return []Tool{
		{
			Name: "atlas_join_session",
			Description: "Join a draft's live collaboration session to co-edit it with people in real time " +
				"(ADR-0103). Returns a sync snapshot: your own participant id (\"self\"), the current " +
				"participants, and any element locks. Poll for others' changes with atlas_session_poll, and " +
				"make your own edits with atlas_session_lock / atlas_session_change. Call atlas_leave_session " +
				"when finished so your locks are released.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"draftId": draftIDProp,
					"name":    stringProp("Optional display name others see for you in the session (e.g. \"Claude\")."),
				},
				"required": []any{"draftId"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				draftID, err := argString(args, "draftId")
				if err != nil {
					return "", err
				}
				payload := map[string]any{}
				if n := optString(args, "name"); n != "" {
					payload["name"] = n
				}
				body, _ := json.Marshal(payload)
				return asText(c.post(sessionPath(draftID, "/join"), "application/json", body))
			},
		},
		{
			Name: "atlas_session_poll",
			Description: "Read what changed in a draft's live session since your last poll: the buffered " +
				"presence / lock / change events plus the current participants and locks (ADR-0103). This is " +
				"the agent's read side (there is no live stream) and doubles as your liveness signal — poll " +
				"periodically while you are participating.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"draftId":       draftIDProp,
					"participantId": participantProp,
				},
				"required": []any{"draftId", "participantId"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				draftID, err := argString(args, "draftId")
				if err != nil {
					return "", err
				}
				pid, err := argString(args, "participantId")
				if err != nil {
					return "", err
				}
				body, _ := json.Marshal(map[string]any{"participantId": pid})
				return asText(c.post(sessionPath(draftID, "/poll"), "application/json", body))
			},
		},
		{
			Name: "atlas_session_lock",
			Description: "Acquire or release a per-element edit lock in a draft's live session (ADR-0103). " +
				"Acquire an element before changing it; if another participant already holds it the call is " +
				"refused (409 conflict) — pick a different element or wait. Release it when you are done so " +
				"others can edit it.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"draftId":       draftIDProp,
					"participantId": participantProp,
					"elementId":     stringProp("The BPMN element id to lock or unlock."),
					"action":        stringProp("\"acquire\" to lock the element, \"release\" to unlock it."),
				},
				"required": []any{"draftId", "participantId", "elementId", "action"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				draftID, err := argString(args, "draftId")
				if err != nil {
					return "", err
				}
				pid, err := argString(args, "participantId")
				if err != nil {
					return "", err
				}
				elementID, err := argString(args, "elementId")
				if err != nil {
					return "", err
				}
				action, err := argString(args, "action")
				if err != nil {
					return "", err
				}
				body, _ := json.Marshal(map[string]any{"participantId": pid, "elementId": elementID, "action": action})
				return asText(c.post(sessionPath(draftID, "/lock"), "application/json", body))
			},
		},
		{
			Name: "atlas_session_change",
			Description: "Broadcast an element edit to a draft's live session so every open canvas updates " +
				"immediately (ADR-0103). Lock the element with atlas_session_lock first. This relays the " +
				"change live; it does not persist the draft — save the finished diagram with atlas_save_draft.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"draftId":       draftIDProp,
					"participantId": participantProp,
					"elementId":     stringProp("The BPMN element id you changed."),
					"xml":           stringProp("Optional BPMN XML fragment for the changed element, for peers to apply."),
				},
				"required": []any{"draftId", "participantId", "elementId"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				draftID, err := argString(args, "draftId")
				if err != nil {
					return "", err
				}
				pid, err := argString(args, "participantId")
				if err != nil {
					return "", err
				}
				elementID, err := argString(args, "elementId")
				if err != nil {
					return "", err
				}
				payload := map[string]any{"participantId": pid, "elementId": elementID}
				if x := optString(args, "xml"); x != "" {
					payload["xml"] = x
				}
				body, _ := json.Marshal(payload)
				return asText(c.post(sessionPath(draftID, "/change"), "application/json", body))
			},
		},
		{
			Name: "atlas_session_presence",
			Description: "Announce which element you are focused on in a draft's live session, so people see " +
				"where you are working (ADR-0103). Optional but collaborative.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"draftId":       draftIDProp,
					"participantId": participantProp,
					"selection":     stringProp("The BPMN element id you are focused on (empty clears your selection)."),
				},
				"required": []any{"draftId", "participantId"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				draftID, err := argString(args, "draftId")
				if err != nil {
					return "", err
				}
				pid, err := argString(args, "participantId")
				if err != nil {
					return "", err
				}
				body, _ := json.Marshal(map[string]any{"participantId": pid, "selection": optString(args, "selection")})
				return asText(c.post(sessionPath(draftID, "/presence"), "application/json", body))
			},
		},
		{
			Name: "atlas_leave_session",
			Description: "Leave a draft's live session, releasing any element locks you held (ADR-0103). " +
				"Always call this when you finish co-editing; an agent is not reaped on disconnect the way a " +
				"browser is. Idempotent.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"draftId":       draftIDProp,
					"participantId": participantProp,
				},
				"required": []any{"draftId", "participantId"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				draftID, err := argString(args, "draftId")
				if err != nil {
					return "", err
				}
				pid, err := argString(args, "participantId")
				if err != nil {
					return "", err
				}
				body, _ := json.Marshal(map[string]any{"participantId": pid})
				return asText(c.post(sessionPath(draftID, "/leave"), "application/json", body))
			},
		},
	}
}
