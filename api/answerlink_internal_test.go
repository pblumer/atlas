package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestALinkOpensOnlyATaskAnsweredByLink: a token is minted only for a task that opted in,
// but the check does not rely on that. A correctly signed token for a task that did not
// opt in — the version an instance was migrated to dropped the mark, say — opens nothing
// (ADR-draft-user-task-answered-by-link).
func TestALinkOpensOnlyATaskAnsweredByLink(t *testing.T) {
	srv, _ := newValidateServer(t)
	h := srv.Handler()
	do := func(method, path, auth, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	const model = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="plain" isExecutable="true">
	    <startEvent id="s"/>
	    <userTask id="review"><extensionElements><zeebe:formDefinition formId="f"/></extensionElements></userTask>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="review"/>
	    <sequenceFlow id="f2" sourceRef="review" targetRef="e"/>
	  </process>
	</definitions>`
	if rec := do("POST", "/api/v1/deployments", "", model); rec.Code != http.StatusOK {
		t.Fatalf("deploy: %d %s", rec.Code, rec.Body)
	}
	rec := do("POST", "/api/v1/instances", "", `{"processId":"plain"}`)
	var inst struct {
		Key uint64 `json:"instanceKey"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &inst) != nil || inst.Key == 0 {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	token, err := srv.answerToken(inst.Key, inst.Key, "review")
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if rec := do("GET", "/public/answer/task", token, ""); rec.Code != http.StatusNotFound {
		t.Errorf("a signed link to a task that did not opt in: %d %s, want 404", rec.Code, rec.Body)
	}
}
