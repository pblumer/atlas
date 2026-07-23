package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

const userTaskBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="approval" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="review" name="Review order">
      <extensionElements>
        <zeebe:assignmentDefinition assignee="editor" candidateGroups="reviewers"/>
      </extensionElements>
    </userTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="review"/>
    <sequenceFlow id="f2" sourceRef="review" targetRef="end"/>
  </process>
</definitions>`

func TestUserTaskListAndComplete(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", userTaskBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}

	code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "{}", "application/json")
	if code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}

	code, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: status=%d body=%s", code, body)
	}
	var tasks []struct {
		Key                uint64 `json:"key"`
		ProcessInstanceKey uint64 `json:"processInstanceKey"`
		ProcessID          string `json:"processId"`
		ElementID          string `json:"elementId"`
		Name               string `json:"name"`
		Assignee           string `json:"assignee"`
		CandidateGroups    string `json:"candidateGroups"`
	}
	if err := json.Unmarshal(body, &tasks); err != nil {
		t.Fatalf("decode tasks: %v (%s)", err, body)
	}

	if len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(tasks))
	}
	task := tasks[0]
	if task.ProcessID != "approval" {
		t.Errorf("processId = %q, want \"approval\"", task.ProcessID)
	}
	if task.ElementID != "review" {
		t.Errorf("elementId = %q, want \"review\"", task.ElementID)
	}
	if task.Name != "Review order" {
		t.Errorf("name = %q, want \"Review order\"", task.Name)
	}
	if task.Assignee != "editor" {
		t.Errorf("assignee = %q, want \"editor\"", task.Assignee)
	}
	if task.CandidateGroups != "reviewers" {
		t.Errorf("candidateGroups = %q, want \"reviewers\"", task.CandidateGroups)
	}

	code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/complete", task.Key), "", "")
	if code != http.StatusOK {
		t.Fatalf("complete task: status=%d body=%s", code, body)
	}

	code, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks after complete: status=%d body=%s", code, body)
	}
	var after []struct{ Key uint64 }
	if err := json.Unmarshal(body, &after); err != nil {
		t.Fatalf("decode tasks after: %v (%s)", err, body)
	}
	if len(after) != 0 {
		t.Errorf("tasks after complete = %d, want 0", len(after))
	}
}

// listOneTask deploys the user-task process, starts an instance, and returns the
// single open task, failing otherwise. Shared by the claim/unclaim tests.
func listOneTask(t *testing.T, ts *httptest.Server) struct {
	Key             uint64 `json:"key"`
	Assignee        string `json:"assignee"`
	CandidateGroups string `json:"candidateGroups"`
} {
	t.Helper()
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", userTaskBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "{}", "application/json")
	if code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: status=%d body=%s", code, body)
	}
	var tasks []struct {
		Key             uint64 `json:"key"`
		Assignee        string `json:"assignee"`
		CandidateGroups string `json:"candidateGroups"`
	}
	if err := json.Unmarshal(body, &tasks); err != nil {
		t.Fatalf("decode tasks: %v (%s)", err, body)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(tasks))
	}
	return tasks[0]
}

func TestClaimAndUnclaimTask(t *testing.T) {
	ts := newTestServer(t)
	task := listOneTask(t, ts)
	// The model default assignee surfaces on the open task.
	if task.Assignee != "editor" {
		t.Fatalf("initial assignee = %q, want \"editor\"", task.Assignee)
	}

	// Claim reassigns to the caller.
	code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/claim", task.Key), `{"assignee":"alice"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("claim: status=%d body=%s", code, body)
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	if code != http.StatusOK {
		t.Fatalf("list after claim: status=%d body=%s", code, body)
	}
	var afterClaim []struct {
		Key      uint64 `json:"key"`
		Assignee string `json:"assignee"`
	}
	if err := json.Unmarshal(body, &afterClaim); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	var claimed string
	for _, tk := range afterClaim {
		if tk.Key == task.Key {
			claimed = tk.Assignee
		}
	}
	if claimed != "alice" {
		t.Fatalf("after claim: assignee = %q, want \"alice\"", claimed)
	}

	// Unclaim clears the assignee; the task stays open.
	code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/unclaim", task.Key), "", "")
	if code != http.StatusOK {
		t.Fatalf("unclaim: status=%d body=%s", code, body)
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	if code != http.StatusOK {
		t.Fatalf("list after unclaim: status=%d body=%s", code, body)
	}
	var afterUnclaim []struct {
		Key      uint64 `json:"key"`
		Assignee string `json:"assignee"`
	}
	if err := json.Unmarshal(body, &afterUnclaim); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	found := false
	for _, tk := range afterUnclaim {
		if tk.Key == task.Key {
			found = true
			if tk.Assignee != "" {
				t.Fatalf("after unclaim: assignee = %q, want \"\"", tk.Assignee)
			}
		}
	}
	if !found {
		t.Fatalf("task %d gone after unclaim, want still open", task.Key)
	}
}

func TestClaimTaskValidation(t *testing.T) {
	ts := newTestServer(t)
	// Missing assignee → 400.
	code, _ := doReq(t, ts, http.MethodPost, "/api/v1/tasks/1/claim", `{"assignee":""}`, "application/json")
	if code != http.StatusBadRequest {
		t.Errorf("claim with empty assignee: %d, want 400", code)
	}
	// Malformed body → 400.
	code, _ = doReq(t, ts, http.MethodPost, "/api/v1/tasks/1/claim", `not-json`, "application/json")
	if code != http.StatusBadRequest {
		t.Errorf("claim with bad body: %d, want 400", code)
	}
	// Non-existent job → 404.
	code, _ = doReq(t, ts, http.MethodPost, "/api/v1/tasks/999/claim", `{"assignee":"alice"}`, "application/json")
	if code != http.StatusNotFound {
		t.Errorf("claim non-existent task: %d, want 404", code)
	}
	// Unclaim a non-existent job → 404.
	code, _ = doReq(t, ts, http.MethodPost, "/api/v1/tasks/999/unclaim", "", "")
	if code != http.StatusNotFound {
		t.Errorf("unclaim non-existent task: %d, want 404", code)
	}
	// Invalid (non-numeric) key → 400.
	code, _ = doReq(t, ts, http.MethodPost, "/api/v1/tasks/not-a-number/claim", `{"assignee":"alice"}`, "application/json")
	if code != http.StatusBadRequest {
		t.Errorf("claim with non-numeric key: %d, want 400", code)
	}
}

func TestCompleteTaskNotFound(t *testing.T) {
	ts := newTestServer(t)
	code, _ := doReq(t, ts, http.MethodPost, "/api/v1/tasks/999/complete", "", "")
	if code != http.StatusNotFound {
		t.Errorf("complete non-existent task: %d, want 404", code)
	}
}

func TestCompleteTaskInvalidKey(t *testing.T) {
	ts := newTestServer(t)
	code, _ := doReq(t, ts, http.MethodPost, "/api/v1/tasks/not-a-number/complete", "", "")
	if code != http.StatusBadRequest {
		t.Errorf("complete task with non-numeric key: %d, want 400", code)
	}
}
