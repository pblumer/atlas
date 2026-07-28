package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// runtimeElem mirrors the runtime view's per-element entry, including the waiting
// jobs an operator can act on.
type runtimeElem struct {
	ElementID string `json:"elementId"`
	Type      string `json:"type"`
	Tokens    int    `json:"tokens"`
	Jobs      []struct {
		JobKey             uint64 `json:"jobKey"`
		ProcessInstanceKey uint64 `json:"processInstanceKey"`
	} `json:"jobs"`
}

func getRuntime(t *testing.T, ts *httptest.Server, defKey uint64) []runtimeElem {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/processes/%d/runtime", defKey), "", "")
	if code != http.StatusOK {
		t.Fatalf("runtime: status=%d body=%s", code, body)
	}
	var rt struct {
		Elements []runtimeElem `json:"elements"`
	}
	if err := json.Unmarshal(body, &rt); err != nil {
		t.Fatalf("decode runtime: %v (%s)", err, body)
	}
	return rt.Elements
}

func findElem(elems []runtimeElem, id string) (runtimeElem, bool) {
	for _, e := range elems {
		if e.ElementID == id {
			return e, true
		}
	}
	return runtimeElem{}, false
}

// TestRuntimeExposesServiceJobKey is the discovery→completion loop over HTTP: a
// token parks at a service task with no worker, the runtime view surfaces that
// token's job key, and completing that key by hand clears the token — so an
// operator can find and finish a stuck service job entirely over HTTP.
func TestRuntimeExposesServiceJobKey(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	if code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}

	// The parked service task exposes its waiting job in the runtime view.
	task, ok := findElem(getRuntime(t, ts, deploy.Key), "task")
	if !ok {
		t.Fatalf("service task 'task' not in runtime view")
	}
	if task.Tokens != 1 || len(task.Jobs) != 1 {
		t.Fatalf("task = %+v, want 1 token and 1 job", task)
	}
	jobKey := task.Jobs[0].JobKey
	if jobKey == 0 || task.Jobs[0].ProcessInstanceKey == 0 {
		t.Fatalf("job entry = %+v, want non-zero job and instance keys", task.Jobs[0])
	}

	// Completing the discovered job clears the token from the service task.
	if code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/complete", jobKey), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("complete job: status=%d body=%s", code, body)
	}
	task, _ = findElem(getRuntime(t, ts, deploy.Key), "task")
	if task.Tokens != 0 || len(task.Jobs) != 0 {
		t.Fatalf("after completion task = %+v, want no live token and no jobs", task)
	}
}

// TestRuntimeUserTaskAlsoExposesJobKey confirms the discovery is general: a
// user-task token likewise surfaces its job key (the same key /tasks lists),
// not only service tasks.
func TestRuntimeUserTaskAlsoExposesJobKey(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", incidentUserTaskBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	if code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}

	review, ok := findElem(getRuntime(t, ts, deploy.Key), "review")
	if !ok || len(review.Jobs) != 1 || review.Jobs[0].JobKey == 0 {
		t.Fatalf("user task 'review' = %+v (found %v), want one job with a key", review, ok)
	}

	// The runtime-surfaced key is the same one the Tasks list reports.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: status=%d body=%s", code, body)
	}
	var tasks []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &tasks); err != nil || len(tasks) != 1 {
		t.Fatalf("tasks = %v (%s), want 1", err, body)
	}
	if tasks[0].Key != review.Jobs[0].JobKey {
		t.Fatalf("runtime job %d != task key %d", review.Jobs[0].JobKey, tasks[0].Key)
	}
}
