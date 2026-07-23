package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
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
