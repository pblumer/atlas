package mcp_test

import (
	"strings"
	"testing"
)

// TestTheEventLogAndTheInventoriesViaTools: a message published through the tools is
// in the event log, narrowed by channel, name and limit, and the grouped views of the
// installation's own names answer through theirs (ADR-0472, ADR-0446,
// ADR-0447).
func TestTheEventLogAndTheInventoriesViaTools(t *testing.T) {
	ts := newAtlas(t)
	if _, isErr := toolText(t, result(t, run(t, ts, callTool(1, "atlas_deploy", map[string]any{"xml": messageWaiterBPMN}))[0])); isErr {
		t.Fatal("deploy failed")
	}
	if _, isErr := toolText(t, result(t, run(t, ts, callTool(2, "atlas_create_instance", map[string]any{"key": 1, "variables": map[string]any{"orderId": "order-1"}}))[0])); isErr {
		t.Fatal("create_instance failed")
	}
	if _, isErr := toolText(t, result(t, run(t, ts, callTool(3, "atlas_publish_message", map[string]any{"name": "go", "correlationKey": "order-1"}))[0])); isErr {
		t.Fatal("publish failed")
	}

	text, isErr := toolText(t, result(t, run(t, ts, callTool(4, "atlas_event_log", map[string]any{"channel": "message", "name": "go", "limit": 5}))[0]))
	if isErr || !strings.Contains(text, `"message.sent"`) || !strings.Contains(text, `"message.received"`) {
		t.Fatalf("atlas_event_log = (%q, isErr=%v), want the send and its reception", text, isErr)
	}
	text, isErr = toolText(t, result(t, run(t, ts, callTool(5, "atlas_event_log", map[string]any{"limit": "1", "after": "0"}))[0]))
	if isErr || !strings.Contains(text, `"rows"`) {
		t.Fatalf("atlas_event_log with a string limit = (%q, isErr=%v)", text, isErr)
	}
	text, isErr = toolText(t, result(t, run(t, ts, callTool(6, "atlas_event_log", map[string]any{"channel": "fax"}))[0]))
	if !isErr || !strings.Contains(text, "channel must be signal or message") {
		t.Fatalf("atlas_event_log with a bad channel = (%q, isErr=%v), want the route's refusal", text, isErr)
	}

	text, isErr = toolText(t, result(t, run(t, ts, callTool(7, "atlas_message_names", map[string]any{}))[0]))
	if isErr || !strings.Contains(text, `"name":"go"`) {
		t.Fatalf("atlas_message_names = (%q, isErr=%v), want the waiter's name", text, isErr)
	}
	text, isErr = toolText(t, result(t, run(t, ts, callTool(8, "atlas_signals", map[string]any{}))[0]))
	if isErr || !strings.Contains(text, `"signals"`) {
		t.Fatalf("atlas_signals = (%q, isErr=%v)", text, isErr)
	}
}
