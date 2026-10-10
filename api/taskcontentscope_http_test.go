package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// openReviewBPMN is reviewBPMN's task addressed to nobody: open work, which every
// signed-in account sees in its inbox and may pick up (ADR-0042, ADR-0421).
const openReviewBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="open-review" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="check" name="Check">
      <extensionElements>
        <zeebe:formDefinition formId="review-form"/>
      </extensionElements>
    </userTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="check"/>
    <sequenceFlow id="f2" sourceRef="check" targetRef="end"/>
  </process>
</definitions>`

// openFormlessBPMN is the same open task with no form at all.
const openFormlessBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="open-formless" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="check" name="Check"/>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="check"/>
    <sequenceFlow id="f2" sourceRef="check" targetRef="end"/>
  </process>
</definitions>`

// startOpenTask deploys bpmn and starts one instance of it with vars, as admin.
func startOpenTask(t *testing.T, admin *http.Client, ts *httptest.Server, bpmn, vars string) {
	t.Helper()
	code, body := cReq(t, admin, ts, "POST", "/api/v1/deployments", bpmn)
	if code != http.StatusOK {
		t.Fatalf("deploy = %d: %s", code, body)
	}
	var dep struct{ Key uint64 }
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deployment: %v", err)
	}
	if code, body := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), vars); code != http.StatusOK {
		t.Fatalf("start instance = %d: %s", code, body)
	}
}

// taskContentAs reads the inbox listing with content as one client and returns the
// content of its only row.
func taskContentAs(t *testing.T, c *http.Client, ts *httptest.Server, path string) []string {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", path, "")
	if code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, code, body)
	}
	var rows []struct {
		Content []string `json:"content"`
	}
	if err := json.Unmarshal(listRows(t, body), &rows); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if len(rows) != 1 {
		t.Fatalf("GET %s = %d rows, want 1", path, len(rows))
	}
	return rows[0].Content
}

// TestTaskContentCarriesOnlyWhatTheTasksFormAsksFor is the regression the status
// report of 2026-10-10 found (ADR-0275, audit F11): a list row's content was every
// short value at the task's scope, so an open task — which every signed-in account
// sees — handed an unrelated account values the instance's variables endpoint
// refuses it. The row may say what the task's form shows, and nothing more; the
// rule is the same for every viewer, because it is a rule about what a row is.
func TestTaskContentCarriesOnlyWhatTheTasksFormAsksFor(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")
	createUserWithRoles(t, admin, ts.URL, "outsider", `["user"]`)
	outsider := signInAs(t, ts.URL, "outsider", "a-password-that-is-long")

	if code, body := cReq(t, admin, ts, "POST", "/api/v1/forms", reviewForm); code != http.StatusOK {
		t.Fatalf("save form = %d: %s", code, body)
	}
	startOpenTask(t, admin, ts, openReviewBPMN,
		`{"variables":{"comment":"bitte-pruefen","secret":"only-for-the-project"}}`)
	code, body := cReq(t, admin, ts, "POST", "/api/v1/task-folders",
		`{"name":"Checks","rule":{"match":"all","conditions":[{"field":"process","op":"is","value":"open-review"}]}}`)
	if code != http.StatusOK {
		t.Fatalf("create folder = %d: %s", code, body)
	}
	var folder struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &folder); err != nil {
		t.Fatalf("decode folder: %v", err)
	}

	for _, tc := range []struct {
		who  string
		c    *http.Client
		path string
	}{
		// Each listing reaches the rows by its own walk: the outsider's skips what is
		// not theirs, the admin's sees everything, and a folder applies its rule.
		{"outsider", outsider, "/api/v1/tasks?content=1"},
		{"admin", admin, "/api/v1/tasks?content=1"},
		{"admin, folder", admin, "/api/v1/tasks?content=1&folder=" + folder.ID},
	} {
		t.Run(tc.who, func(t *testing.T) {
			got := taskContentAs(t, tc.c, ts, tc.path)
			if !slices.Contains(got, "bitte-pruefen") {
				t.Errorf("content = %v, want the form's own field `comment`", got)
			}
			if slices.Contains(got, "only-for-the-project") {
				t.Errorf("content = %v carries `secret`, which no form asks for", got)
			}
		})
	}
}

// TestATaskWithoutAFormCarriesNoContent: a task with no form has no declared set of
// fields, and guessing one is how an allowlist becomes a formality — the same
// answer the variables endpoint gives a holder of such a task.
func TestATaskWithoutAFormCarriesNoContent(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")
	startOpenTask(t, admin, ts, openFormlessBPMN, `{"variables":{"secret":"only-for-the-project"}}`)

	if got := taskContentAs(t, admin, ts, "/api/v1/tasks?content=1"); len(got) != 0 {
		t.Errorf("content = %v, want none for a task without a form", got)
	}
}
