package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

const incidentUserTaskBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="approval" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="review" name="Review"/>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="review"/>
    <sequenceFlow id="f2" sourceRef="review" targetRef="end"/>
  </process>
</definitions>`

// deployAndStartTask deploys a one-user-task process, starts an instance, and
// returns the parked task's job key.
func deployAndStartTask(t *testing.T, ts *httptest.Server) uint64 {
	t.Helper()
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", incidentUserTaskBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	if code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: status=%d body=%s", code, body)
	}
	var tasks []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &tasks); err != nil || len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %v (%s)", err, body)
	}
	return tasks[0].Key
}

func listIncidents(t *testing.T, ts *httptest.Server) []struct {
	ElementInstanceKey uint64 `json:"elementInstanceKey"`
	ProcessInstanceKey uint64 `json:"processInstanceKey"`
	JobKey             uint64 `json:"jobKey"`
	Message            string `json:"message"`
} {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/incidents", "", "")
	if code != http.StatusOK {
		t.Fatalf("list incidents: status=%d body=%s", code, body)
	}
	var resp struct {
		Incidents []struct {
			ElementInstanceKey uint64 `json:"elementInstanceKey"`
			ProcessInstanceKey uint64 `json:"processInstanceKey"`
			JobKey             uint64 `json:"jobKey"`
			Message            string `json:"message"`
		} `json:"incidents"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode incidents: %v (%s)", err, body)
	}
	return resp.Incidents
}

// TestFailJobRaisesAndResolveIncident drives the whole operator loop over HTTP:
// fail a job to exhaustion → an incident appears → resolve it → the task is back.
func TestFailJobRaisesAndResolveIncident(t *testing.T) {
	ts := newTestServer(t)
	jobKey := deployAndStartTask(t, ts)

	// Fail with no retries left → an incident.
	code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/fail", jobKey), `{"retries":0,"message":"boom"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("fail job: status=%d body=%s", code, body)
	}

	inc := listIncidents(t, ts)
	if len(inc) != 1 || inc[0].JobKey != jobKey || inc[0].Message != "boom" {
		t.Fatalf("incidents = %+v, want one pointing at job %d with message", inc, jobKey)
	}

	// The task is gone from the inbox while blocked.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	var tasks []json.RawMessage
	_ = json.Unmarshal(body, &tasks)
	if code != http.StatusOK || len(tasks) != 0 {
		t.Fatalf("blocked task still listed: status=%d body=%s", code, body)
	}

	// Resolve with an empty body → retries default to 1; the job is re-activated
	// and the task returns.
	elKey := inc[0].ElementInstanceKey
	if code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/incidents/%d/resolve", elKey), `{}`, "application/json"); code != http.StatusOK {
		t.Fatalf("resolve: status=%d body=%s", code, body)
	}
	if got := listIncidents(t, ts); len(got) != 0 {
		t.Fatalf("after resolve: %d incidents remain", len(got))
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	_ = json.Unmarshal(body, &tasks)
	if code != http.StatusOK || len(tasks) != 1 {
		t.Fatalf("task did not return after resolve: status=%d body=%s", code, body)
	}
}

func TestFailJobAndResolveErrors(t *testing.T) {
	ts := newTestServer(t)

	// Bad keys → 400.
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/jobs/notakey/fail", `{}`, "application/json"); code != http.StatusBadRequest {
		t.Errorf("fail bad key: status=%d, want 400", code)
	}
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/incidents/notakey/resolve", `{}`, "application/json"); code != http.StatusBadRequest {
		t.Errorf("resolve bad key: status=%d, want 400", code)
	}
	// Invalid JSON body → 400.
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/jobs/5/fail", `{bad`, "application/json"); code != http.StatusBadRequest {
		t.Errorf("fail bad body: status=%d, want 400", code)
	}
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/incidents/5/resolve", `{bad`, "application/json"); code != http.StatusBadRequest {
		t.Errorf("resolve bad body: status=%d, want 400", code)
	}
	// Nonexistent job / incident → 404.
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/jobs/999999/fail", `{"retries":0}`, "application/json"); code != http.StatusNotFound {
		t.Errorf("fail missing job: status=%d, want 404", code)
	}
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/incidents/999999/resolve", `{"retries":1}`, "application/json"); code != http.StatusNotFound {
		t.Errorf("resolve missing incident: status=%d, want 404", code)
	}
	// No incidents initially.
	if got := listIncidents(t, ts); len(got) != 0 {
		t.Errorf("fresh server has %d incidents, want 0", len(got))
	}
}
