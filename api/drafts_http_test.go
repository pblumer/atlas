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
