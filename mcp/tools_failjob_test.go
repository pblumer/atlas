package mcp_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// userTaskBPMN parks an instance on a single user task, which is a job — so its
// task key doubles as the job key that atlas_fail_job acts on.
const userTaskBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="approval" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="review" name="Review"/>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="review"/>
    <sequenceFlow id="f2" sourceRef="review" targetRef="end"/>
  </process>
</definitions>`

// firstTaskKey starts a fresh user-task instance through the tools and returns the
// parked task's key (== job key), read from the atlas_list_tasks page envelope.
func firstTaskKey(t *testing.T, atlas *httptest.Server) uint64 {
	t.Helper()
	if _, isErr := toolText(t, result(t, run(t, atlas, callTool(1, "atlas_deploy", map[string]any{"xml": userTaskBPMN}))[0])); isErr {
		t.Fatal("deploy failed")
	}
	if _, isErr := toolText(t, result(t, run(t, atlas, callTool(2, "atlas_create_instance", map[string]any{"key": 1}))[0])); isErr {
		t.Fatal("create_instance failed")
	}
	listText, isErr := toolText(t, result(t, run(t, atlas, callTool(3, "atlas_list_tasks", map[string]any{}))[0]))
	if isErr {
		t.Fatal("list_tasks failed")
	}
	var page struct {
		Items []struct {
			Key uint64 `json:"key"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(listText), &page); err != nil || len(page.Items) != 1 {
		t.Fatalf("parse tasks page: err=%v, page=%q", err, listText)
	}
	return page.Items[0].Key
}

// incidentJobKeys reads the raised incidents' job keys directly from the API (the
// read side MCP does not yet expose), so a fail-job test can confirm its effect.
func incidentJobKeys(t *testing.T, atlasURL string) []incidentRow {
	t.Helper()
	resp, err := http.Get(atlasURL + "/api/v1/incidents")
	if err != nil {
		t.Fatalf("list incidents: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var env struct {
		Incidents []incidentRow `json:"incidents"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode incidents %q: %v", body, err)
	}
	return env.Incidents
}

type incidentRow struct {
	JobKey  uint64 `json:"jobKey"`
	Message string `json:"message"`
}

// TestFailJobRaisesIncidentViaTool fails a parked job to exhaustion (retries 0)
// through the tool and confirms an incident is raised carrying the job key and
// message.
func TestFailJobRaisesIncidentViaTool(t *testing.T) {
	atlas := newAtlas(t)
	jobKey := firstTaskKey(t, atlas)

	text, isErr := toolText(t, result(t, run(t, atlas, callTool(4, "atlas_fail_job", map[string]any{"key": jobKey, "retries": 0, "message": "boom"}))[0]))
	if isErr || !strings.Contains(text, fmt.Sprintf(`"jobKey":%d`, jobKey)) {
		t.Fatalf("fail_job = (%q, isErr=%v), want the jobKey echoed", text, isErr)
	}

	inc := incidentJobKeys(t, atlas.URL)
	if len(inc) != 1 || inc[0].JobKey != jobKey || inc[0].Message != "boom" {
		t.Fatalf("incidents = %+v, want one for job %d with message \"boom\"", inc, jobKey)
	}
}

// TestFailJobWithRetriesRaisesNoIncident fails with a positive retry budget: the
// job is re-activated rather than exhausted, so no incident appears.
func TestFailJobWithRetriesRaisesNoIncident(t *testing.T) {
	atlas := newAtlas(t)
	jobKey := firstTaskKey(t, atlas)

	text, isErr := toolText(t, result(t, run(t, atlas, callTool(4, "atlas_fail_job", map[string]any{"key": jobKey, "retries": 2}))[0]))
	if isErr || !strings.Contains(text, fmt.Sprintf(`"jobKey":%d`, jobKey)) {
		t.Fatalf("fail_job (retries) = (%q, isErr=%v), want the jobKey echoed", text, isErr)
	}
	if inc := incidentJobKeys(t, atlas.URL); len(inc) != 0 {
		t.Fatalf("incidents after fail with retries = %+v, want none", inc)
	}
}

// TestFailJobDefaultsToNoRetries covers the retries-omitted path: with no retries
// argument the body defaults to 0, which exhausts the job and raises an incident.
func TestFailJobDefaultsToNoRetries(t *testing.T) {
	atlas := newAtlas(t)
	jobKey := firstTaskKey(t, atlas)

	if _, isErr := toolText(t, result(t, run(t, atlas, callTool(4, "atlas_fail_job", map[string]any{"key": jobKey}))[0])); isErr {
		t.Fatal("fail_job without retries failed")
	}
	if inc := incidentJobKeys(t, atlas.URL); len(inc) != 1 || inc[0].JobKey != jobKey {
		t.Fatalf("incidents = %+v, want one for job %d (retries defaulted to 0)", inc, jobKey)
	}
}

// TestFailJobMissingIsToolError fails a job key that does not exist, surfacing the
// server's 404 as a tool error.
func TestFailJobMissingIsToolError(t *testing.T) {
	atlas := newAtlas(t)
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(1, "atlas_fail_job", map[string]any{"key": 999999}))[0]))
	if !isErr || !strings.Contains(text, "no job") {
		t.Fatalf("fail missing job = (%q, isErr=%v), want a not-found tool error", text, isErr)
	}
}

// TestFailJobRejectsBadRetries covers the retries argument-validation branch: a
// non-integer retries value is a tool error.
func TestFailJobRejectsBadRetries(t *testing.T) {
	atlas := newAtlas(t)
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(1, "atlas_fail_job", map[string]any{"key": 1, "retries": "lots"}))[0]))
	if !isErr || !strings.Contains(text, "integer") {
		t.Fatalf("fail_job with bad retries = (%q, isErr=%v), want a tool error", text, isErr)
	}
}
