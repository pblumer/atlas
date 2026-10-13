package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// comparedOrder is a minimal executable process the comparison can be held against.
const comparedOrder = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:process id="order" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements><zeebe:taskDefinition type="work" retries="3"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

// TestDraftComparisonServerErrors drives the two 500s a normal HTTP flow cannot
// reach: a draft store that cannot be read, and a stored draft that is not XML —
// which a save refuses, so only something writing the store around it could leave.
func TestDraftComparisonServerErrors(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()
	do := func(method, path, body string) (int, string) {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	if code, body := do(http.MethodPost, "/api/v1/deployments", comparedOrder); code != http.StatusOK {
		t.Fatalf("deploy = %d %s", code, body)
	}
	srv.do(func() {
		if err := srv.drafts.Save(draft{ProcessID: "order", XML: "<definitions>"}); err != nil {
			t.Errorf("plant a broken draft: %v", err)
		}
	})
	code, body := do(http.MethodGet, "/api/v1/drafts/order/comparison", "")
	if code != http.StatusInternalServerError || !strings.Contains(body, "compare the draft with version 1") {
		t.Fatalf("comparison of an unreadable draft = %d %s, want a 500 naming the version", code, body)
	}

	// A record that cannot be read: its path is a directory.
	ds, err := newDraftStore(filepath.Join(t.TempDir(), "drafts"))
	if err != nil {
		t.Fatalf("newDraftStore: %v", err)
	}
	srv.drafts = ds
	if err := os.MkdirAll(filepath.Join(ds.FileFor("x"), "child"), 0o755); err != nil {
		t.Fatalf("make record dir: %v", err)
	}
	if code, body := do(http.MethodGet, "/api/v1/drafts/x/comparison", ""); code != http.StatusInternalServerError {
		t.Fatalf("comparison of an unreadable record = %d %s, want 500", code, body)
	}
}
