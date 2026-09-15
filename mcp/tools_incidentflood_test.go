package mcp_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// The two tools an agent triaging a *flood* needs: a reading whose size is the
// number of causes, and an action that clears one (ADR-draft-incident-floods). An
// agent that can only page a list and resolve one key at a time is in exactly the
// position the operator was — reading thousands of rows of the same failure.

// parkTwoIncidents deploys the user-task model, starts two instances and exhausts
// both jobs, so the server holds one cause with two incidents behind it.
func parkTwoIncidents(t *testing.T, atlas *httptest.Server) {
	t.Helper()
	if _, isErr := toolText(t, result(t, run(t, atlas, callTool(1, "atlas_deploy", map[string]any{"xml": userTaskBPMN}))[0])); isErr {
		t.Fatal("deploy failed")
	}
	for i := 0; i < 2; i++ {
		if _, isErr := toolText(t, result(t, run(t, atlas, callTool(2, "atlas_create_instance", map[string]any{"key": 1}))[0])); isErr {
			t.Fatalf("create_instance %d failed", i)
		}
	}
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
		if _, isErr := toolText(t, result(t, run(t, atlas, callTool(4, "atlas_fail_job", map[string]any{"key": it.Key, "retries": 0, "message": "boom"}))[0])); isErr {
			t.Fatalf("fail_job %d failed", it.Key)
		}
	}
}

// TestIncidentSummaryViaTool: two parked tokens on one element are one group, and
// the group names what an agent needs to act — the definition, the element, the
// kind, and how many.
func TestIncidentSummaryViaTool(t *testing.T) {
	atlas := newAtlas(t)
	parkTwoIncidents(t, atlas)

	text, isErr := toolText(t, result(t, run(t, atlas, callTool(5, "atlas_incident_summary", map[string]any{}))[0]))
	if isErr {
		t.Fatalf("incident_summary = %q", text)
	}
	var sum struct {
		Total  int `json:"total"`
		Groups []struct {
			ProcessDefKey uint64 `json:"processDefKey"`
			ElementID     string `json:"elementId"`
			Type          string `json:"type"`
			Count         int    `json:"count"`
			Message       string `json:"message"`
		} `json:"groups"`
	}
	if err := json.Unmarshal([]byte(text), &sum); err != nil {
		t.Fatalf("decode summary %q: %v", text, err)
	}
	if sum.Total != 2 || len(sum.Groups) != 1 {
		t.Fatalf("summary = %+v, want 2 incidents in one group", sum)
	}
	g := sum.Groups[0]
	if g.Count != 2 || g.Type != "job" || g.Message != "boom" || g.ProcessDefKey != 1 || g.ElementID == "" {
		t.Errorf("group = %+v, want the shared cause named", g)
	}

	// Scoped the way the list is, and a scope that matches nothing is an empty
	// summary rather than an error.
	scoped, isErr := toolText(t, result(t, run(t, atlas, callTool(6, "atlas_incident_summary", map[string]any{"process": 999999}))[0]))
	if isErr {
		t.Fatalf("scoped summary = %q", scoped)
	}
	if !strings.Contains(scoped, `"total":0`) {
		t.Errorf("summary of an unknown definition = %q, want total 0", scoped)
	}
}

// TestResolveIncidentsByScopeViaTool clears a whole cause in one call — the action
// the flood needs — and reports honestly when nothing matches.
func TestResolveIncidentsByScopeViaTool(t *testing.T) {
	atlas := newAtlas(t)
	parkTwoIncidents(t, atlas)

	text, isErr := toolText(t, result(t, run(t, atlas, callTool(5, "atlas_resolve_incidents", map[string]any{"processDefKey": 1, "type": "job", "retries": 2}))[0]))
	if isErr {
		t.Fatalf("resolve_incidents = %q", text)
	}
	if !strings.Contains(text, `"resolved":2`) {
		t.Errorf("resolve_incidents = %q, want both incidents resolved", text)
	}
	if after := listIncidents(t, atlas, map[string]any{}); len(after.Incidents) != 0 {
		t.Fatalf("after the bulk resolve: %+v, want nothing stuck", after)
	}
	again, isErr := toolText(t, result(t, run(t, atlas, callTool(6, "atlas_resolve_incidents", map[string]any{"processDefKey": 1}))[0]))
	if isErr || !strings.Contains(again, `"resolved":0`) {
		t.Errorf("second call = (%q, isErr=%v), want an honest zero", again, isErr)
	}
}

// TestResolveIncidentsByKeysViaTool covers the explicit set an agent builds from a
// listing, including a key whose incident is already gone.
func TestResolveIncidentsByKeysViaTool(t *testing.T) {
	atlas := newAtlas(t)
	parkTwoIncidents(t, atlas)
	page := listIncidents(t, atlas, map[string]any{})
	if len(page.Incidents) != 2 {
		t.Fatalf("setup: %+v, want 2 incidents", page)
	}

	args := map[string]any{"keys": []any{page.Incidents[0].ElementInstanceKey, 999999}}
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(5, "atlas_resolve_incidents", args))[0]))
	if isErr {
		t.Fatalf("resolve_incidents = %q", text)
	}
	if !strings.Contains(text, `"resolved":1`) || !strings.Contains(text, `"notFound":1`) {
		t.Errorf("resolve_incidents = %q, want one resolved and one not found", text)
	}
}

// TestResolveIncidentsArgumentRefusals covers the argument validation: a call that
// names nothing would mean "every incident on the server", and one that names both
// modes means two different things at once.
func TestResolveIncidentsArgumentRefusals(t *testing.T) {
	atlas := newAtlas(t)
	for name, args := range map[string]map[string]any{
		"no selector":  {},
		"only retries": {"retries": 2},
		"both modes":   {"keys": []any{1}, "processDefKey": 1},
		"bad keys":     {"keys": "1,2"},
		"bad limit":    {"processDefKey": 1, "limit": "lots"},
	} {
		text, isErr := toolText(t, result(t, run(t, atlas, callTool(1, "atlas_resolve_incidents", args))[0]))
		if !isErr {
			t.Errorf("%s: resolve_incidents = %q, want a tool error", name, text)
		}
	}
	// An unknown type reaches the server, which refuses it rather than silently
	// matching nothing.
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(2, "atlas_resolve_incidents", map[string]any{"type": "nonsense"}))[0]))
	if !isErr || !strings.Contains(text, "type") {
		t.Errorf("unknown type = (%q, isErr=%v), want a refusal naming the type", text, isErr)
	}
}

// TestListIncidentsNarrowedViaTool covers the filters that turn a group back into
// its rows — the same predicate the bulk resolve takes.
func TestListIncidentsNarrowedViaTool(t *testing.T) {
	atlas := newAtlas(t)
	parkTwoIncidents(t, atlas)

	if page := listIncidents(t, atlas, map[string]any{"type": "job"}); len(page.Incidents) != 2 {
		t.Errorf("type=job = %+v, want both", page)
	}
	if page := listIncidents(t, atlas, map[string]any{"message": "BOOM"}); len(page.Incidents) != 2 {
		t.Errorf("message=BOOM = %+v, want both (the match is case-insensitive)", page)
	}
	if page := listIncidents(t, atlas, map[string]any{"element": "nosuchelement"}); len(page.Incidents) != 0 {
		t.Errorf("element=nosuchelement = %+v, want none", page)
	}
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(9, "atlas_list_incidents", map[string]any{"type": "nonsense"}))[0]))
	if !isErr || !strings.Contains(text, "type") {
		t.Errorf("type=nonsense = (%q, isErr=%v), want a refusal naming the type", text, isErr)
	}
}
