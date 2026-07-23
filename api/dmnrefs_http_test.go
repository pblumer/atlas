package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestDmnRefLifecycle drives create → list → move → delete for a DMN reference,
// and proves it counts as a project artifact alongside BPMN drafts.
func TestDmnRefLifecycle(t *testing.T) {
	ts := newTestServer(t)

	// Create a project to file references into.
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/projects", `{"name":"Lending"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create project status=%d body=%s", code, body)
	}
	var proj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &proj); err != nil {
		t.Fatalf("decode project: %v", err)
	}

	// Create a DMN reference in the project.
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/dmnrefs",
		`{"name":"Risk scoring","modelRef":"risk-score","projectId":"`+proj.ID+`"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create ref status=%d body=%s", code, body)
	}
	var ref struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		ModelRef  string `json:"modelRef"`
		ProjectID string `json:"projectId"`
	}
	if err := json.Unmarshal(body, &ref); err != nil {
		t.Fatalf("decode ref: %v (%s)", err, body)
	}
	if ref.ID == "" || ref.Name != "Risk scoring" || ref.ModelRef != "risk-score" || ref.ProjectID != proj.ID {
		t.Fatalf("ref = %+v, want it filed into the project with its handle", ref)
	}

	// It appears when listing the project's references.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/dmnrefs?projectId="+proj.ID, "", "")
	if code != http.StatusOK {
		t.Fatalf("list refs status=%d body=%s", code, body)
	}
	var refs []map[string]any
	if err := json.Unmarshal(body, &refs); err != nil {
		t.Fatalf("decode refs: %v", err)
	}
	if len(refs) != 1 || refs[0]["modelRef"] != "risk-score" {
		t.Fatalf("filtered refs = %v, want one risk-score", refs)
	}

	// Also file a BPMN draft into the same project, so the artifact count mixes types.
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/drafts?projectId="+proj.ID, projectDraftXML("wip", "WIP"), "application/xml"); code != http.StatusOK {
		t.Fatal("save draft into project")
	}
	// The project now reports two artifacts (1 draft + 1 DMN reference).
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/projects", "", "")
	var projs []struct {
		Artifacts int `json:"artifacts"`
	}
	if err := json.Unmarshal(body, &projs); err != nil {
		t.Fatalf("decode projects: %v", err)
	}
	if len(projs) != 1 || projs[0].Artifacts != 2 {
		t.Fatalf("projects = %+v, want one project with 2 artifacts", projs)
	}

	// Move the reference to Ungrouped.
	if code, _ := doReq(t, ts, http.MethodPatch, "/api/v1/dmnrefs/"+ref.ID, `{"projectId":""}`, "application/json"); code != http.StatusOK {
		t.Fatal("move ref to ungrouped")
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/dmnrefs?projectId="+proj.ID, "", "")
	if err := json.Unmarshal(body, &refs); err != nil {
		t.Fatalf("decode refs: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("after move to ungrouped: project has %d refs, want 0", len(refs))
	}

	// Delete the reference (idempotent).
	if code, _ := doReq(t, ts, http.MethodDelete, "/api/v1/dmnrefs/"+ref.ID, "", ""); code != http.StatusNoContent {
		t.Fatal("delete ref")
	}
	if code, _ := doReq(t, ts, http.MethodDelete, "/api/v1/dmnrefs/"+ref.ID, "", ""); code != http.StatusNoContent {
		t.Fatal("delete ref again: want 204 idempotent")
	}
}

// TestDmnRefValidation covers the create/move validation branches.
func TestDmnRefValidation(t *testing.T) {
	ts := newTestServer(t)

	// Name and modelRef are both required.
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/dmnrefs", `{"name":"","modelRef":"x"}`, "application/json"); code != http.StatusBadRequest {
		t.Fatal("blank name: want 400")
	}
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/dmnrefs", `{"name":"X","modelRef":"  "}`, "application/json"); code != http.StatusBadRequest {
		t.Fatal("blank modelRef: want 400")
	}
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/dmnrefs", `{`, "application/json"); code != http.StatusBadRequest {
		t.Fatal("bad json: want 400")
	}
	// Filing into an unknown project is rejected.
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/dmnrefs", `{"name":"X","modelRef":"m","projectId":"nope"}`, "application/json"); code != http.StatusBadRequest {
		t.Fatal("unknown project on create: want 400")
	}

	// Seed a valid ungrouped reference.
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/dmnrefs", `{"name":"D","modelRef":"m"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("seed ref: %d", code)
	}
	var ref struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &ref); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Move into an unknown project is rejected.
	if code, _ := doReq(t, ts, http.MethodPatch, "/api/v1/dmnrefs/"+ref.ID, `{"projectId":"nope"}`, "application/json"); code != http.StatusBadRequest {
		t.Fatal("move into unknown project: want 400")
	}
	// Move with bad json is rejected.
	if code, _ := doReq(t, ts, http.MethodPatch, "/api/v1/dmnrefs/"+ref.ID, `{`, "application/json"); code != http.StatusBadRequest {
		t.Fatal("move bad json: want 400")
	}
	// Moving a missing reference is 404.
	if code, _ := doReq(t, ts, http.MethodPatch, "/api/v1/dmnrefs/ghost", `{"projectId":""}`, "application/json"); code != http.StatusNotFound {
		t.Fatal("move missing ref: want 404")
	}
}

// TestDmnRefSurvivesRestart proves DMN references are durable across a restart
// and still count toward their project's artifacts.
func TestDmnRefSurvivesRestart(t *testing.T) {
	dir := t.TempDir()

	first := boot(t, dir)
	code, body := doReq(t, first.ts, http.MethodPost, "/api/v1/projects", `{"name":"Persist"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create project: %d", code)
	}
	var proj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &proj); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if code, _ := doReq(t, first.ts, http.MethodPost, "/api/v1/dmnrefs",
		`{"name":"Keep","modelRef":"keep-model","projectId":"`+proj.ID+`"}`, "application/json"); code != http.StatusOK {
		t.Fatal("create ref")
	}
	first.shutdown()

	second := boot(t, dir)
	defer second.shutdown()

	code, body = doReq(t, second.ts, http.MethodGet, "/api/v1/dmnrefs", "", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"modelRef":"keep-model"`) {
		t.Fatalf("ref after restart status=%d body=%s", code, body)
	}
	code, body = doReq(t, second.ts, http.MethodGet, "/api/v1/projects", "", "")
	if !strings.Contains(string(body), `"artifacts":1`) {
		t.Fatalf("artifact count not restored: %s", body)
	}
}
