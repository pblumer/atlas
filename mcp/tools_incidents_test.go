package mcp_test

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

// incidentsPage mirrors the atlas_list_incidents envelope: the endpoint's
// {incidents:[…]} body folded together with its truncation header.
type incidentsPage struct {
	Incidents []struct {
		ElementInstanceKey uint64 `json:"elementInstanceKey"`
		ProcessInstanceKey uint64 `json:"processInstanceKey"`
		JobKey             uint64 `json:"jobKey"`
		Message            string `json:"message"`
	} `json:"incidents"`
	Truncated bool `json:"truncated"`
}

// listIncidents drives atlas_list_incidents and decodes its page envelope.
func listIncidents(t *testing.T, atlas *httptest.Server, args map[string]any) incidentsPage {
	t.Helper()
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(9, "atlas_list_incidents", args))[0]))
	if isErr {
		t.Fatalf("list_incidents = (%q, isErr=%v)", text, isErr)
	}
	var page incidentsPage
	if err := json.Unmarshal([]byte(text), &page); err != nil {
		t.Fatalf("decode incidents page %q: %v", text, err)
	}
	return page
}

// TestListAndResolveIncidentViaTools drives the whole operator loop through the
// tools: fail a parked job to exhaustion → the incident is listed → resolve it →
// the incident is gone and the blocked job's task returns.
func TestListAndResolveIncidentViaTools(t *testing.T) {
	atlas := newAtlas(t)
	jobKey := firstTaskKey(t, atlas)

	// Exhaust the job → an incident is raised.
	if _, isErr := toolText(t, result(t, run(t, atlas, callTool(4, "atlas_fail_job", map[string]any{"key": jobKey, "retries": 0, "message": "boom"}))[0])); isErr {
		t.Fatal("fail_job failed")
	}

	page := listIncidents(t, atlas, map[string]any{"limit": 50})
	if page.Truncated || len(page.Incidents) != 1 {
		t.Fatalf("incidents page = %+v, want exactly 1 and not truncated", page)
	}
	inc := page.Incidents[0]
	if inc.JobKey != jobKey || inc.Message != "boom" {
		t.Fatalf("incident = %+v, want job %d message \"boom\"", inc, jobKey)
	}

	// Resolve by the element-instance key → the job is retried.
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(5, "atlas_resolve_incident", map[string]any{"key": inc.ElementInstanceKey, "retries": 2}))[0]))
	if isErr ||
		!strings.Contains(text, fmt.Sprintf(`"elementInstanceKey":%d`, inc.ElementInstanceKey)) ||
		!strings.Contains(text, fmt.Sprintf(`"jobKey":%d`, jobKey)) {
		t.Fatalf("resolve_incident = (%q, isErr=%v), want the element+job keys echoed", text, isErr)
	}

	// The incident is gone and the task is back in the inbox.
	if after := listIncidents(t, atlas, map[string]any{}); len(after.Incidents) != 0 {
		t.Fatalf("after resolve: %+v, want no incidents", after)
	}
	tasksText, isErr := toolText(t, result(t, run(t, atlas, callTool(6, "atlas_list_tasks", map[string]any{}))[0]))
	if isErr {
		t.Fatal("list_tasks failed")
	}
	var page2 struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal([]byte(tasksText), &page2); err != nil || len(page2.Items) != 1 {
		t.Fatalf("tasks after resolve: err=%v page=%q, want the task back", err, tasksText)
	}
}

// TestListIncidentsEmpty returns an empty page (not an error) when nothing is
// stuck, with the truncation flag false.
func TestListIncidentsEmpty(t *testing.T) {
	atlas := newAtlas(t)
	page := listIncidents(t, atlas, map[string]any{})
	if page.Truncated || len(page.Incidents) != 0 {
		t.Fatalf("empty incidents page = %+v, want [] and not truncated", page)
	}
}

// TestListIncidentsTruncated raises two incidents and lists with limit 1, so the
// page is capped and the folded truncation flag is true.
func TestListIncidentsTruncated(t *testing.T) {
	atlas := newAtlas(t)
	if _, isErr := toolText(t, result(t, run(t, atlas, callTool(1, "atlas_deploy", map[string]any{"xml": userTaskBPMN}))[0])); isErr {
		t.Fatal("deploy failed")
	}
	for i := 0; i < 2; i++ {
		if _, isErr := toolText(t, result(t, run(t, atlas, callTool(2, "atlas_create_instance", map[string]any{"key": 1}))[0])); isErr {
			t.Fatalf("create_instance %d failed", i)
		}
	}
	// Fail every open task's job to exhaustion, raising two incidents.
	tasksText, _ := toolText(t, result(t, run(t, atlas, callTool(3, "atlas_list_tasks", map[string]any{}))[0]))
	var page struct {
		Items []struct {
			Key uint64 `json:"key"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(tasksText), &page); err != nil || len(page.Items) != 2 {
		t.Fatalf("expected 2 tasks, got err=%v page=%q", err, tasksText)
	}
	for _, it := range page.Items {
		if _, isErr := toolText(t, result(t, run(t, atlas, callTool(4, "atlas_fail_job", map[string]any{"key": it.Key, "retries": 0}))[0])); isErr {
			t.Fatalf("fail_job %d failed", it.Key)
		}
	}

	capped := listIncidents(t, atlas, map[string]any{"limit": 1})
	if !capped.Truncated || len(capped.Incidents) != 1 {
		t.Fatalf("capped page = %+v, want 1 incident and truncated=true", capped)
	}
}

// TestListIncidentsRejectsBadLimit covers the limit argument-validation branch.
func TestListIncidentsRejectsBadLimit(t *testing.T) {
	atlas := newAtlas(t)
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(1, "atlas_list_incidents", map[string]any{"limit": "many"}))[0]))
	if !isErr || !strings.Contains(text, "integer") {
		t.Fatalf("list_incidents bad limit = (%q, isErr=%v), want a tool error", text, isErr)
	}
}

// TestResolveIncidentMissingIsToolError resolves an element-instance key with no
// incident, surfacing the server's 404 as a tool error.
func TestResolveIncidentMissingIsToolError(t *testing.T) {
	atlas := newAtlas(t)
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(1, "atlas_resolve_incident", map[string]any{"key": 999999}))[0]))
	if !isErr || !strings.Contains(text, "no incident") {
		t.Fatalf("resolve missing incident = (%q, isErr=%v), want a not-found tool error", text, isErr)
	}
}

// TestResolveIncidentRejectsBadRetries covers the retries argument-validation
// branch of the resolve tool.
func TestResolveIncidentRejectsBadRetries(t *testing.T) {
	atlas := newAtlas(t)
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(1, "atlas_resolve_incident", map[string]any{"key": 1, "retries": "lots"}))[0]))
	if !isErr || !strings.Contains(text, "integer") {
		t.Fatalf("resolve bad retries = (%q, isErr=%v), want a tool error", text, isErr)
	}
}
