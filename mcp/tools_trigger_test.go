package mcp_test

import (
	"strings"
	"testing"
)

// Two message starts and no none start: only its triggers can start it (ADR-0425).
const twoTriggersBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <message id="m_join" name="hr.joiner"/>
  <message id="m_leave" name="hr.leaver"/>
  <process id="hr-events" isExecutable="true">
    <startEvent id="Join"><messageEventDefinition messageRef="m_join"/></startEvent>
    <startEvent id="Leave"><messageEventDefinition messageRef="m_leave"/></startEvent>
    <endEvent id="JEnd"/>
    <endEvent id="LEnd"/>
    <sequenceFlow id="f1" sourceRef="Join" targetRef="JEnd"/>
    <sequenceFlow id="f2" sourceRef="Leave" targetRef="LEnd"/>
  </process>
</definitions>`

// TestTriggerStartViaTool: the tool starts a process at the named start event, a
// repeated trigger id replays, and a missing argument or a variables value that is
// not an object is a tool error rather than a request.
func TestTriggerStartViaTool(t *testing.T) {
	ts := newAtlas(t)
	if _, isErr := toolText(t, result(t, run(t, ts, callTool(1, "atlas_deploy", map[string]any{"xml": twoTriggersBPMN}))[0])); isErr {
		t.Fatal("deploy failed")
	}
	args := map[string]any{"processId": "hr-events", "message": "hr.leaver", "triggerId": "leaver-1",
		"variables": map[string]any{"employeeId": "E-1"}}
	first, isErr := toolText(t, result(t, run(t, ts, callTool(2, "atlas_trigger_start", args))[0]))
	if isErr || !strings.Contains(first, "instanceKey") {
		t.Fatalf("trigger = (%q, isErr=%v), want an instance", first, isErr)
	}
	again, isErr := toolText(t, result(t, run(t, ts, callTool(3, "atlas_trigger_start", args))[0]))
	if isErr || !strings.Contains(again, `"replayed":true`) {
		t.Fatalf("repeat = (%q, isErr=%v), want a replay", again, isErr)
	}
	for i, bad := range []map[string]any{
		{"processId": "hr-events", "message": "hr.leaver"},
		{"processId": "hr-events", "triggerId": "x"},
		{"message": "hr.leaver", "triggerId": "x"},
		{"processId": "hr-events", "message": "hr.leaver", "triggerId": "x", "variables": "nope"},
	} {
		if text, isErr := toolText(t, result(t, run(t, ts, callTool(10+i, "atlas_trigger_start", bad))[0])); !isErr {
			t.Errorf("args %v: %q, want a tool error", bad, text)
		}
	}
}

// TestRebindCatalogProductViaTool: the tool needs a reason and answers the server's
// refusal for a product that does not exist as a tool error (ADR-0427).
func TestRebindCatalogProductViaTool(t *testing.T) {
	ts := newAtlas(t)
	if text, isErr := toolText(t, result(t, run(t, ts, callTool(1, "atlas_rebind_catalog_product",
		map[string]any{"id": "laptop"}))[0])); !isErr {
		t.Errorf("no reason: %q, want a tool error", text)
	}
	if text, isErr := toolText(t, result(t, run(t, ts, callTool(2, "atlas_rebind_catalog_product",
		map[string]any{"reason": "x"}))[0])); !isErr {
		t.Errorf("no id: %q, want a tool error", text)
	}
	text, isErr := toolText(t, result(t, run(t, ts, callTool(3, "atlas_rebind_catalog_product",
		map[string]any{"id": "nothing", "reason": "the old IdM is gone"}))[0]))
	if !isErr || !strings.Contains(text, "nothing") {
		t.Errorf("unknown product: (%q, isErr=%v), want the server's 404", text, isErr)
	}
}
