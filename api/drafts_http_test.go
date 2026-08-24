package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// A minimal, deliberately NON-executable diagram: a lone start event with no job
// type or end. Deploy would reject it; saving it as a draft must still work.
const draftBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="wip-order" name="Order fulfillment">
    <bpmn:startEvent id="StartEvent_1" name="Start"/>
  </bpmn:process>
</bpmn:definitions>`

// TestDraftSaveListReopenDelete drives the full draft lifecycle over HTTP.
func TestDraftSaveListReopenDelete(t *testing.T) {
	ts := newTestServer(t)

	// Save a draft of a not-yet-executable diagram.
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts", draftBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("save draft status=%d body=%s", code, body)
	}
	var saved struct {
		ProcessID string `json:"processId"`
		Name      string `json:"name"`
		SavedAt   int64  `json:"savedAt"`
	}
	if err := json.Unmarshal(body, &saved); err != nil {
		t.Fatalf("decode save: %v (%s)", err, body)
	}
	if saved.ProcessID != "wip-order" || saved.Name != "Order fulfillment" {
		t.Fatalf("saved = %+v, want wip-order/Order fulfillment", saved)
	}

	// It appears in the list.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/drafts", "", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"processId":"wip-order"`) {
		t.Fatalf("list drafts status=%d body=%s", code, body)
	}

	// Its XML round-trips for reopening.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/drafts/wip-order/xml", "", "")
	if code != http.StatusOK || !strings.Contains(string(body), `id="wip-order"`) {
		t.Fatalf("draft xml status=%d body=%s", code, body)
	}

	// Re-saving overwrites rather than duplicating.
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/drafts", draftBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("re-save status=%d", code)
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/drafts", "", "")
	var list []map[string]any
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 draft after overwrite, got %d", len(list))
	}

	// Delete removes it.
	if code, _ := doReq(t, ts, http.MethodDelete, "/api/v1/drafts/wip-order", "", ""); code != http.StatusNoContent {
		t.Fatalf("delete draft status=%d", code)
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/drafts/wip-order/xml", "", "")
	if code != http.StatusNotFound {
		t.Fatalf("draft after delete status=%d body=%s, want 404", code, body)
	}
}

// TestDraftSaveRejectsBadInput covers the empty-body and no-process-id branches.
func TestDraftSaveRejectsBadInput(t *testing.T) {
	ts := newTestServer(t)
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/drafts", "", "application/xml"); code != http.StatusBadRequest {
		t.Fatalf("empty body status=%d, want 400", code)
	}
	noID := `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"><bpmn:process/></bpmn:definitions>`
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts", noID, "application/xml"); code != http.StatusBadRequest {
		t.Fatalf("no process id status=%d body=%s, want 400", code, body)
	}
	// Malformed XML can't be parsed for a process id → 400, not a 500.
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts", "<not closed", "application/xml"); code != http.StatusBadRequest {
		t.Fatalf("malformed xml status=%d body=%s, want 400", code, body)
	}
	// A draft that was never saved is a 404 on reopen, not a 500.
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/drafts/never/xml", "", ""); code != http.StatusNotFound {
		t.Fatalf("missing draft xml status=%d, want 404", code)
	}
}

// TestDraftResavePreservesProject proves that re-saving a draft (as the editor
// does) without an explicit projectId query parameter preserves the existing
// project assignment — the bug that caused drafts to reset to Ungrouped.
func TestDraftResavePreservesProject(t *testing.T) {
	ts := newTestServer(t)

	// Create a project.
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/projects", `{"name":"Sticky"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create project status=%d body=%s", code, body)
	}
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}

	// Save a draft into the project.
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/drafts?projectId="+p.ID, draftBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("save into project status=%d body=%s", code, body)
	}
	if !strings.Contains(string(body), `"projectId":"`+p.ID+`"`) {
		t.Fatalf("initial save missing projectId: %s", body)
	}

	// Re-save the same draft WITHOUT a projectId parameter (what the editor does).
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/drafts", draftBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("re-save status=%d body=%s", code, body)
	}
	if !strings.Contains(string(body), `"projectId":"`+p.ID+`"`) {
		t.Fatalf("project assignment lost after re-save: %s", body)
	}

	// Explicitly passing projectId="" (empty) should clear the assignment.
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/drafts?projectId=", draftBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("clear project status=%d body=%s", code, body)
	}
	if strings.Contains(string(body), `"projectId"`) {
		t.Fatalf("projectId should be cleared: %s", body)
	}
}

// TestDraftSurvivesRestart proves drafts are durable across a restart, the whole
// point of saving.
func TestDraftSurvivesRestart(t *testing.T) {
	dir := t.TempDir()

	first := boot(t, dir)
	if code, body := doReq(t, first.ts, http.MethodPost, "/api/v1/drafts", draftBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("save draft status=%d body=%s", code, body)
	}
	first.shutdown()

	second := boot(t, dir)
	defer second.shutdown()

	code, body := doReq(t, second.ts, http.MethodGet, "/api/v1/drafts/wip-order/xml", "", "")
	if code != http.StatusOK || !strings.Contains(string(body), `id="wip-order"`) {
		t.Fatalf("draft after restart status=%d body=%s", code, body)
	}
}

// diLessBPMN is a runnable model with no <bpmndi:BPMNDiagram> at all. BPMN-DI is
// optional in the standard, so this is what a generator, an export from another tool,
// or a hand-written file routinely looks like.
const diLessBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="no-layout" name="Ohne Darstellung" isExecutable="true">
    <bpmn:startEvent id="Start"><bpmn:outgoing>f1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:sequenceFlow id="f1" sourceRef="Start" targetRef="Ende"/>
    <bpmn:endEvent id="Ende"><bpmn:incoming>f1</bpmn:incoming></bpmn:endEvent>
  </bpmn:process>
</bpmn:definitions>`

// TestDraftWithoutLayoutIsLaidOutOnTheWayIn: a deployed model with no diagram
// interchange renders, because the deployed-XML read generates a layout for it
// (ADR-0124) — a draft did not, so the same file imported instead of deployed opened
// onto an empty canvas. It is now laid out as it is stored, and the save says so, which
// is what lets the importer tell the author whose arrangement they are looking at.
func TestDraftWithoutLayoutIsLaidOutOnTheWayIn(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts", diLessBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("save draft status=%d body=%s", code, body)
	}
	var saved struct {
		ProcessID       string `json:"processId"`
		LayoutGenerated bool   `json:"layoutGenerated"`
	}
	if err := json.Unmarshal(body, &saved); err != nil {
		t.Fatalf("decode save: %v (%s)", err, body)
	}
	if !saved.LayoutGenerated {
		t.Errorf("layoutGenerated = false, want true — the file carried no diagram interchange")
	}

	code, body = doReq(t, ts, http.MethodGet, "/api/v1/drafts/no-layout/xml", "", "")
	if code != http.StatusOK {
		t.Fatalf("read draft status=%d body=%s", code, body)
	}
	for _, want := range []string{"BPMNDiagram", `bpmnElement="Start"`, `bpmnElement="Ende"`, `bpmnElement="f1"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("reopened draft has no %s — the editor would draw an empty canvas:\n%s", want, body)
		}
	}
}

// TestDraftKeepsTheLayoutItCameWith is the other half: a model that carries its own
// diagram interchange is stored byte for byte. Generating over an author's layout would
// throw away the arrangement they made, and every save from the Modeler carries one.
func TestDraftKeepsTheLayoutItCameWith(t *testing.T) {
	ts := newTestServer(t)

	const laidOut = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" xmlns:dc="http://www.omg.org/spec/DD/20100524/DC">
  <bpmn:process id="has-layout" name="Mit Darstellung" isExecutable="true">
    <bpmn:startEvent id="Start"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="D"><bpmndi:BPMNPlane id="P" bpmnElement="has-layout">
    <bpmndi:BPMNShape id="Start_di" bpmnElement="Start"><dc:Bounds x="999" y="888" width="36" height="36"/></bpmndi:BPMNShape>
  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</bpmn:definitions>`

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts", laidOut, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("save draft status=%d body=%s", code, body)
	}
	if strings.Contains(string(body), `"layoutGenerated":true`) {
		t.Errorf("layoutGenerated = true for a model that brought its own layout: %s", body)
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/drafts/has-layout/xml", "", "")
	if code != http.StatusOK {
		t.Fatalf("read draft status=%d body=%s", code, body)
	}
	if string(body) != laidOut {
		t.Errorf("the stored draft is not what was saved:\n%s", body)
	}
	if !strings.Contains(string(body), `x="999"`) {
		t.Errorf("the author's own coordinates were replaced:\n%s", body)
	}
}
