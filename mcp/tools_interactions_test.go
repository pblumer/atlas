package mcp_test

import (
	"strings"
	"testing"
)

// TestTheInteractionGraphAndItsReplayViaTools: a message published to a waiting
// process arrives from the API in the process's graph and in its replay, the window
// and the cursors travel to the routes, and a bad cursor is the route's refusal
// (ADR-0559).
func TestTheInteractionGraphAndItsReplayViaTools(t *testing.T) {
	ts := newAtlas(t)
	if _, isErr := toolText(t, result(t, run(t, ts, callTool(1, "atlas_deploy", map[string]any{"xml": messageWaiterBPMN, "projectId": testApp(t, ts)}))[0])); isErr {
		t.Fatal("deploy failed")
	}
	if _, isErr := toolText(t, result(t, run(t, ts, callTool(2, "atlas_create_instance", map[string]any{"key": 1, "variables": map[string]any{"orderId": "order-1"}}))[0])); isErr {
		t.Fatal("create_instance failed")
	}
	if _, isErr := toolText(t, result(t, run(t, ts, callTool(3, "atlas_publish_message", map[string]any{"name": "go", "correlationKey": "order-1"}))[0])); isErr {
		t.Fatal("publish failed")
	}

	text, isErr := toolText(t, result(t, run(t, ts, callTool(4, "atlas_process_interactions", map[string]any{"processId": "waiter", "window": "24h"}))[0]))
	if isErr || !strings.Contains(text, `"from":"publish"`) || !strings.Contains(text, `"received":1`) {
		t.Fatalf("atlas_process_interactions = (%q, isErr=%v), want the publish observed once", text, isErr)
	}

	text, isErr = toolText(t, result(t, run(t, ts, callTool(5, "atlas_process_interaction_replay", map[string]any{"processId": "waiter", "window": "24h", "limit": 5}))[0]))
	if isErr || !strings.Contains(text, `"kind":"received"`) || !strings.Contains(text, `"correlationKey":"order-1"`) {
		t.Fatalf("atlas_process_interaction_replay = (%q, isErr=%v), want the arrival with its key", text, isErr)
	}
	text, isErr = toolText(t, result(t, run(t, ts, callTool(6, "atlas_process_interaction_replay", map[string]any{"processId": "waiter", "limit": "2", "after": "1"}))[0]))
	if isErr || !strings.Contains(text, `"events"`) {
		t.Fatalf("atlas_process_interaction_replay after a cursor, with a string limit = (%q, isErr=%v)", text, isErr)
	}
	text, isErr = toolText(t, result(t, run(t, ts, callTool(7, "atlas_process_interaction_replay", map[string]any{"processId": "waiter", "before": "x"}))[0]))
	if !isErr || !strings.Contains(text, "before must be a cursor") {
		t.Fatalf("atlas_process_interaction_replay with a bad cursor = (%q, isErr=%v), want the route's refusal", text, isErr)
	}
}
