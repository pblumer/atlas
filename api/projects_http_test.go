package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// projectDraftXML is a minimal, non-executable BPMN draft used to exercise
// artifact grouping — a lone start event, exactly what a project holds as
// work-in-progress.
func projectDraftXML(id, name string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="%s" name="%s">
    <bpmn:startEvent id="StartEvent_1"/>
  </bpmn:process>
</bpmn:definitions>`, id, name)
}

// TestProjectLifecycle drives create → list-with-count → file a draft into it →
// rename → delete, and checks that deleting a project leaves its artifact intact.
func TestProjectLifecycle(t *testing.T) {
	ts := newTestServer(t)

	// Create a project.
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/projects", `{"name":"Onboarding"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create project status=%d body=%s", code, body)
	}
	var p struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Artifacts int    `json:"artifacts"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if p.ID == "" || p.Name != "Onboarding" {
		t.Fatalf("project = %+v, want a non-empty id and name Onboarding", p)
	}

	// It appears in the list.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/projects", "", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"name":"Onboarding"`) {
		t.Fatalf("list status=%d body=%s", code, body)
	}

	// File a BPMN draft into the project; the response echoes the project id.
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/drafts?projectId="+p.ID, projectDraftXML("wip-1", "WIP 1"), "application/xml")
	if code != http.StatusOK {
		t.Fatalf("save draft status=%d body=%s", code, body)
	}
	if !strings.Contains(string(body), `"projectId":"`+p.ID+`"`) {
		t.Fatalf("saved draft missing projectId: %s", body)
	}

	// Listing drafts filtered by the project returns exactly that one.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/drafts?projectId="+p.ID, "", "")
	if code != http.StatusOK {
		t.Fatalf("list drafts status=%d body=%s", code, body)
	}
	var drafts []map[string]any
	if err := json.Unmarshal(body, &drafts); err != nil {
		t.Fatalf("decode drafts: %v (%s)", err, body)
	}
	if len(drafts) != 1 || drafts[0]["processId"] != "wip-1" {
		t.Fatalf("filtered drafts = %v, want just wip-1", drafts)
	}

	// The project list now reports one artifact.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/projects", "", "")
	var projs []struct {
		ID        string `json:"id"`
		Artifacts int    `json:"artifacts"`
	}
	if err := json.Unmarshal(body, &projs); err != nil {
		t.Fatalf("decode projects: %v (%s)", err, body)
	}
	if len(projs) != 1 || projs[0].Artifacts != 1 {
		t.Fatalf("projects = %+v, want one project with 1 artifact", projs)
	}

	// Rename, and see the new name plus the preserved artifact count.
	code, body = doReq(t, ts, http.MethodPatch, "/api/v1/projects/"+p.ID, `{"name":"Onboarding v2"}`, "application/json")
	if code != http.StatusOK || !strings.Contains(string(body), `"name":"Onboarding v2"`) || !strings.Contains(string(body), `"artifacts":1`) {
		t.Fatalf("rename status=%d body=%s", code, body)
	}

	// Delete the project.
	if code, _ := doReq(t, ts, http.MethodDelete, "/api/v1/projects/"+p.ID, "", ""); code != http.StatusNoContent {
		t.Fatalf("delete project status=%d", code)
	}
	// Its draft still exists (deleting a project does not delete artifacts).
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/drafts/wip-1/xml", "", ""); code != http.StatusOK {
		t.Fatalf("draft gone after project delete: status=%d", code)
	}
	// And the project is no longer listed.
	if _, body := doReq(t, ts, http.MethodGet, "/api/v1/projects", "", ""); strings.Contains(string(body), p.ID) {
		t.Fatalf("deleted project still listed: %s", body)
	}
}

// TestMoveDraftBetweenProjects proves a draft can be reassigned between projects
// and back to Ungrouped, and that referential rules hold.
func TestMoveDraftBetweenProjects(t *testing.T) {
	ts := newTestServer(t)

	makeProject := func(name string) string {
		code, body := doReq(t, ts, http.MethodPost, "/api/v1/projects", `{"name":"`+name+`"}`, "application/json")
		if code != http.StatusOK {
			t.Fatalf("create %s status=%d body=%s", name, code, body)
		}
		var p struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		return p.ID
	}
	a := makeProject("A")
	b := makeProject("B")

	// Save an ungrouped draft.
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts", projectDraftXML("d", "D"), "application/xml"); code != http.StatusOK {
		t.Fatalf("save ungrouped draft status=%d body=%s", code, body)
	}

	countIn := func(pid string) int {
		_, body := doReq(t, ts, http.MethodGet, "/api/v1/drafts?projectId="+pid, "", "")
		var ds []map[string]any
		if err := json.Unmarshal(body, &ds); err != nil {
			t.Fatalf("decode drafts for %s: %v", pid, err)
		}
		return len(ds)
	}

	// Move it to A.
	if code, body := doReq(t, ts, http.MethodPatch, "/api/v1/drafts/d", `{"projectId":"`+a+`"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("move to A status=%d body=%s", code, body)
	}
	if countIn(a) != 1 || countIn(b) != 0 {
		t.Fatalf("after move to A: A=%d B=%d, want 1 and 0", countIn(a), countIn(b))
	}

	// Move it to B.
	if code, _ := doReq(t, ts, http.MethodPatch, "/api/v1/drafts/d", `{"projectId":"`+b+`"}`, "application/json"); code != http.StatusOK {
		t.Fatal("move to B failed")
	}
	if countIn(a) != 0 || countIn(b) != 1 {
		t.Fatalf("after move to B: A=%d B=%d, want 0 and 1", countIn(a), countIn(b))
	}

	// Move it to Ungrouped (empty projectId).
	if code, _ := doReq(t, ts, http.MethodPatch, "/api/v1/drafts/d", `{"projectId":""}`, "application/json"); code != http.StatusOK {
		t.Fatal("move to ungrouped failed")
	}
	if countIn(b) != 0 {
		t.Fatalf("after move to ungrouped: B=%d, want 0", countIn(b))
	}

	// Saving into an unknown project is rejected.
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/drafts?projectId=nope", projectDraftXML("e", "E"), "application/xml"); code != http.StatusBadRequest {
		t.Fatal("save into unknown project: want 400")
	}
	// Moving into an unknown project is rejected.
	if code, _ := doReq(t, ts, http.MethodPatch, "/api/v1/drafts/d", `{"projectId":"nope"}`, "application/json"); code != http.StatusBadRequest {
		t.Fatal("move into unknown project: want 400")
	}
	// Moving a missing draft is 404.
	if code, _ := doReq(t, ts, http.MethodPatch, "/api/v1/drafts/ghost", `{"projectId":"`+a+`"}`, "application/json"); code != http.StatusNotFound {
		t.Fatal("move missing draft: want 404")
	}
}

// TestProjectBadInput covers the create/rename/delete validation branches.
func TestProjectBadInput(t *testing.T) {
	ts := newTestServer(t)

	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/projects", `{"name":"   "}`, "application/json"); code != http.StatusBadRequest {
		t.Fatal("blank name: want 400")
	}
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/projects", `{`, "application/json"); code != http.StatusBadRequest {
		t.Fatal("bad json: want 400")
	}
	// Rename an unknown project → 404.
	if code, _ := doReq(t, ts, http.MethodPatch, "/api/v1/projects/nope", `{"name":"X"}`, "application/json"); code != http.StatusNotFound {
		t.Fatal("rename unknown: want 404")
	}
	// Rename with a blank name → 400, and with bad json → 400.
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/projects", `{"name":"Real"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create: %d", code)
	}
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if code, _ := doReq(t, ts, http.MethodPatch, "/api/v1/projects/"+p.ID, `{"name":""}`, "application/json"); code != http.StatusBadRequest {
		t.Fatal("rename blank: want 400")
	}
	if code, _ := doReq(t, ts, http.MethodPatch, "/api/v1/projects/"+p.ID, `{`, "application/json"); code != http.StatusBadRequest {
		t.Fatal("rename bad json: want 400")
	}
	// Move with bad json → 400.
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/drafts", projectDraftXML("d", "D"), "application/xml"); code != http.StatusOK {
		t.Fatal("save draft")
	}
	if code, _ := doReq(t, ts, http.MethodPatch, "/api/v1/drafts/d", `{`, "application/json"); code != http.StatusBadRequest {
		t.Fatal("move bad json: want 400")
	}
	// Delete an unknown project is idempotent → 204.
	if code, _ := doReq(t, ts, http.MethodDelete, "/api/v1/projects/nope", "", ""); code != http.StatusNoContent {
		t.Fatal("delete unknown: want 204 (idempotent)")
	}
}

// TestProjectSurvivesRestart proves projects and their artifact counts are
// durable across a restart.
func TestProjectSurvivesRestart(t *testing.T) {
	dir := t.TempDir()

	first := boot(t, dir)
	code, body := doReq(t, first.ts, http.MethodPost, "/api/v1/projects", `{"name":"Persistent"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", code, body)
	}
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if code, _ := doReq(t, first.ts, http.MethodPost, "/api/v1/drafts?projectId="+p.ID, projectDraftXML("wip", "WIP"), "application/xml"); code != http.StatusOK {
		t.Fatal("save draft into project")
	}
	first.shutdown()

	second := boot(t, dir)
	defer second.shutdown()

	code, body = doReq(t, second.ts, http.MethodGet, "/api/v1/projects", "", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"name":"Persistent"`) {
		t.Fatalf("project after restart status=%d body=%s", code, body)
	}
	if !strings.Contains(string(body), `"artifacts":1`) {
		t.Fatalf("artifact count not restored after restart: %s", body)
	}
}
