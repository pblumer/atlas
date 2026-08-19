package mcp_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// messageWaiterBPMN parks an instance at a message intermediate catch event whose
// correlation key is the FEEL expression "= orderId" over its start variables, so
// a published message with a matching key runs the instance to completion.
const messageWaiterBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <message id="Msg_go" name="go">
    <extensionElements><zeebe:subscription correlationKey="= orderId"/></extensionElements>
  </message>
  <process id="waiter" isExecutable="true">
    <startEvent id="start"/>
    <intermediateCatchEvent id="wait">
      <messageEventDefinition messageRef="Msg_go"/>
    </intermediateCatchEvent>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="wait"/>
    <sequenceFlow id="f2" sourceRef="wait" targetRef="end"/>
  </process>
</definitions>`

// activeProcessInstances pulls the live active-instance count out of a tool result
// that embeds a stats object ({... "stats":{"activeProcessInstances":N ...}}).
func activeProcessInstances(t *testing.T, text string) int {
	t.Helper()
	var r struct {
		Stats struct {
			ActiveProcessInstances int `json:"activeProcessInstances"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(text), &r); err != nil {
		t.Fatalf("decode stats from %q: %v", text, err)
	}
	return r.Stats.ActiveProcessInstances
}

// TestPublishMessageViaTool drives message correlation end to end through the
// tool: park an instance at a catch event, publish a non-matching key (a legal
// no-op), then the matching key, which completes the instance.
func TestPublishMessageViaTool(t *testing.T) {
	ts := newAtlas(t)

	if _, isErr := toolText(t, result(t, run(t, ts, callTool(1, "atlas_deploy", map[string]any{"xml": messageWaiterBPMN}))[0])); isErr {
		t.Fatal("deploy failed")
	}
	// Start with orderId=order-1; the instance parks at the catch event.
	startText, isErr := toolText(t, result(t, run(t, ts, callTool(2, "atlas_create_instance", map[string]any{"key": 1, "variables": map[string]any{"orderId": "order-1"}}))[0]))
	if isErr || activeProcessInstances(t, startText) != 1 {
		t.Fatalf("create_instance = (%q, isErr=%v), want 1 active (parked at catch)", startText, isErr)
	}

	// A non-matching correlation key is accepted but correlates nothing.
	missText, isErr := toolText(t, result(t, run(t, ts, callTool(3, "atlas_publish_message", map[string]any{"name": "go", "correlationKey": "order-999"}))[0]))
	if isErr || activeProcessInstances(t, missText) != 1 {
		t.Fatalf("publish (miss) = (%q, isErr=%v), want still 1 active", missText, isErr)
	}

	// The matching key correlates the waiting instance, which then completes.
	hitText, isErr := toolText(t, result(t, run(t, ts, callTool(4, "atlas_publish_message", map[string]any{"name": "go", "correlationKey": "order-1"}))[0]))
	if isErr || activeProcessInstances(t, hitText) != 0 {
		t.Fatalf("publish (hit) = (%q, isErr=%v), want 0 active (instance completed)", hitText, isErr)
	}
}

// TestPublishMessageRequiresName rejects a publish with no message name as a tool
// error, before any request reaches the server.
func TestPublishMessageRequiresName(t *testing.T) {
	ts := newAtlas(t)
	text, isErr := toolText(t, result(t, run(t, ts, callTool(1, "atlas_publish_message", map[string]any{"correlationKey": "x"}))[0]))
	if !isErr || !strings.Contains(text, "name") {
		t.Fatalf("publish without name = (%q, isErr=%v), want a tool error naming the missing argument", text, isErr)
	}
}

// TestPublishMessageRejectsNonObjectVariables covers the variables-validation
// branch shared with the other variable-forwarding tools.
func TestPublishMessageRejectsNonObjectVariables(t *testing.T) {
	ts := newAtlas(t)
	text, isErr := toolText(t, result(t, run(t, ts, callTool(1, "atlas_publish_message", map[string]any{"name": "go", "variables": "nope"}))[0]))
	if !isErr || !strings.Contains(text, "must be an object") {
		t.Fatalf("publish with non-object variables = (%q, isErr=%v), want a tool error", text, isErr)
	}
}

// instanceJobKey deploys nothing; it reads the one activatable job key of an
// instance directly from the API (the read affordance MCP does not yet expose),
// so the job-completion tool can be driven with a real key.
func instanceJobKey(t *testing.T, atlasURL string, instanceKey uint64) uint64 {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("%s/api/v1/instances/%d/jobs", atlasURL, instanceKey))
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var jobs []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &jobs); err != nil {
		t.Fatalf("decode jobs %q: %v", body, err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want exactly 1 (%s)", len(jobs), body)
	}
	return jobs[0].Key
}

// TestCompleteJobViaTool completes a parked service-task job by hand through the
// tool (the operator counterpart to the worker protocol): the token then advances
// to the end event and the instance completes.
func TestCompleteJobViaTool(t *testing.T) {
	ts := newAtlas(t)

	if _, isErr := toolText(t, result(t, run(t, ts, callTool(1, "atlas_deploy", map[string]any{"xml": sampleBPMN}))[0])); isErr {
		t.Fatal("deploy failed")
	}
	// sampleBPMN parks a token on its "payment" service task, creating one job.
	if _, isErr := toolText(t, result(t, run(t, ts, callTool(2, "atlas_create_instance", map[string]any{"key": 1}))[0])); isErr {
		t.Fatal("create_instance failed")
	}

	listText, isErr := toolText(t, result(t, run(t, ts, callTool(3, "atlas_list_instances", map[string]any{}))[0]))
	if isErr {
		t.Fatal("list_instances failed")
	}
	var instances []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal([]byte(listText), &instances); err != nil || len(instances) == 0 {
		t.Fatalf("parse instances: err=%v, list=%q", err, listText)
	}
	jobKey := instanceJobKey(t, ts.URL, instances[0].Key)

	// Complete the job with an output variable; the instance runs to completion.
	text, isErr := toolText(t, result(t, run(t, ts, callTool(4, "atlas_complete_job", map[string]any{"key": jobKey, "variables": map[string]any{"paid": true}}))[0]))
	if isErr || !strings.Contains(text, fmt.Sprintf(`"jobKey":%d`, jobKey)) {
		t.Fatalf("complete_job = (%q, isErr=%v), want the jobKey echoed", text, isErr)
	}

	// No active instance remains: the token reached the end event.
	statsText, isErr := toolText(t, result(t, run(t, ts, callTool(5, "atlas_stats", map[string]any{}))[0]))
	if isErr {
		t.Fatal("stats failed")
	}
	var stats struct {
		ActiveProcessInstances int `json:"activeProcessInstances"`
	}
	if err := json.Unmarshal([]byte(statsText), &stats); err != nil {
		t.Fatalf("decode stats %q: %v", statsText, err)
	}
	if stats.ActiveProcessInstances != 0 {
		t.Fatalf("active instances after complete_job = %d, want 0", stats.ActiveProcessInstances)
	}
}

// TestCompleteJobMissingIsToolError completes a job key that does not exist,
// surfacing the server's 404 as a tool error.
func TestCompleteJobMissingIsToolError(t *testing.T) {
	ts := newAtlas(t)
	text, isErr := toolText(t, result(t, run(t, ts, callTool(1, "atlas_complete_job", map[string]any{"key": 999999}))[0]))
	if !isErr || !strings.Contains(text, "no job") {
		t.Fatalf("complete missing job = (%q, isErr=%v), want a not-found tool error", text, isErr)
	}
}

// TestCompleteJobRejectsNonObjectVariables covers the shared variables-validation
// branch for the job-completion tool.
func TestCompleteJobRejectsNonObjectVariables(t *testing.T) {
	ts := newAtlas(t)
	text, isErr := toolText(t, result(t, run(t, ts, callTool(1, "atlas_complete_job", map[string]any{"key": 1, "variables": 5}))[0]))
	if !isErr || !strings.Contains(text, "must be an object") {
		t.Fatalf("complete_job with non-object variables = (%q, isErr=%v), want a tool error", text, isErr)
	}
}
