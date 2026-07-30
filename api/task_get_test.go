package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// TestGetTaskByKey fetches a single open user task by its job key — the primitive a
// deep link (…/tasks/t/{key}, e.g. from the Operations live view) relies on so a task
// stays reachable even when it falls outside the capped task-list page during a flood.
// The single-task response carries the same enriched fields as a list row.
func TestGetTaskByKey(t *testing.T) {
	ts := newTestServer(t)
	def := deployAndStart(t, ts, userTaskBPMN, "{}")

	// Grab the parked task's key from the list.
	_, body := doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	var list []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &list); err != nil || len(list) != 1 {
		t.Fatalf("list tasks: err=%v body=%s", err, body)
	}
	key := list[0].Key

	// Fetch it directly by key — same enriched shape as the list row.
	code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/tasks/%d", key), "", "")
	if code != http.StatusOK {
		t.Fatalf("get task: status=%d body=%s", code, body)
	}
	var task struct {
		Key                uint64 `json:"key"`
		ProcessInstanceKey uint64 `json:"processInstanceKey"`
		ProcessDefKey      uint64 `json:"processDefKey"`
		ProcessID          string `json:"processId"`
		ElementID          string `json:"elementId"`
		Name               string `json:"name"`
		Assignee           string `json:"assignee"`
		CandidateGroups    string `json:"candidateGroups"`
		Priority           int32  `json:"priority"`
	}
	if err := json.Unmarshal(body, &task); err != nil {
		t.Fatalf("decode task: %v (%s)", err, body)
	}
	if task.Key != key {
		t.Errorf("key = %d, want %d", task.Key, key)
	}
	if task.ProcessDefKey != def || task.ProcessID != "approval" {
		t.Errorf("def = %d/%q, want %d/approval", task.ProcessDefKey, task.ProcessID, def)
	}
	if task.ElementID != "review" || task.Name != "Review order" {
		t.Errorf("element/name = %q/%q, want review/Review order", task.ElementID, task.Name)
	}
	if task.Assignee != "editor" || task.CandidateGroups != "reviewers" || task.Priority != 80 {
		t.Errorf("assignment = %q/%q/%d, want editor/reviewers/80", task.Assignee, task.CandidateGroups, task.Priority)
	}
}

// TestGetTaskByKeyErrors rejects a non-numeric key and 404s a key with no open task
// (never existed, or already completed).
func TestGetTaskByKeyErrors(t *testing.T) {
	ts := newTestServer(t)
	def := deployAndStart(t, ts, userTaskBPMN, "{}")

	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/tasks/not-a-number", "", ""); code != http.StatusBadRequest {
		t.Errorf("bad key: status=%d, want 400", code)
	}
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/tasks/999999", "", ""); code != http.StatusNotFound {
		t.Errorf("unknown key: status=%d, want 404", code)
	}

	// Complete the task, then fetching it by key is a 404 (no open task).
	_, body := doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	var list []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &list); err != nil || len(list) != 1 {
		t.Fatalf("list tasks: err=%v body=%s", err, body)
	}
	if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/complete", list[0].Key), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("complete: status=%d body=%s", code, b)
	}
	if code, _ := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/tasks/%d", list[0].Key), "", ""); code != http.StatusNotFound {
		t.Errorf("completed task: status=%d, want 404", code)
	}
	_ = def

	// A job that exists but is not a user task (a service-task job) is not addressable
	// here: the endpoint is the inbox's read side, not a generic job peek, so it 404s.
	sdef := deployAndStart(t, ts, boundaryTimerBPMN, "{}")
	instKey := activeInstanceKeys(t, ts)[0]
	_, jb := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/jobs", instKey), "", "")
	var jobs []struct {
		Key     uint64 `json:"key"`
		JobType string `json:"jobType"`
	}
	if err := json.Unmarshal(jb, &jobs); err != nil || len(jobs) == 0 {
		t.Fatalf("list instance jobs: err=%v body=%s", err, jb)
	}
	if code, _ := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/tasks/%d", jobs[0].Key), "", ""); code != http.StatusNotFound {
		t.Errorf("service-task job via /tasks/{key}: status=%d, want 404", code)
	}
	_ = sdef
}
