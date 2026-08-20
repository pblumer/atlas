package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestListInstanceJobsOverHTTP covers the read side of the operator job affordance:
// an instance parked on a service task exposes its activatable job (key, element,
// type) via GET /api/v1/instances/{key}/jobs, so the job key can be discovered —
// and then completed — over HTTP without any direct state access. It is what makes
// POST /jobs/{key}/complete usable from a client that only speaks the HTTP API.
func TestListInstanceJobsOverHTTP(t *testing.T) {
	srv := newServerForErrors(t)

	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", serviceJobBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	if code, body = serveInternal(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}

	jobKey, instKey := parkedServiceJob(t, srv, deploy.Key)

	// The endpoint lists exactly the one parked service-task job, with its element
	// id and interned job type — enough to complete it.
	code, body = serveInternal(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/jobs", instKey), "", "")
	if code != http.StatusOK {
		t.Fatalf("list jobs: status=%d body=%s", code, body)
	}
	var jobs []struct {
		Key                uint64 `json:"key"`
		ProcessInstanceKey uint64 `json:"processInstanceKey"`
		ElementID          string `json:"elementId"`
		Name               string `json:"name"`
		JobType            string `json:"jobType"`
	}
	if err := json.Unmarshal(body, &jobs); err != nil {
		t.Fatalf("decode jobs: %v (%s)", err, body)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1 (%s)", len(jobs), body)
	}
	if j := jobs[0]; j.Key != jobKey || j.ProcessInstanceKey != instKey || j.ElementID != "charge" || j.JobType != "payment" {
		t.Fatalf("job = %+v, want key=%d inst=%d element=charge type=payment", j, jobKey, instKey)
	}

	// The discovered key completes the job over HTTP → the instance advances and no
	// activatable job remains.
	if code, body = serveInternal(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/complete", jobs[0].Key), `{"reason":"test: operator completed the parked job by hand"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("complete discovered job: status=%d body=%s", code, body)
	}
	code, body = serveInternal(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/jobs", instKey), "", "")
	if code != http.StatusOK || strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("list jobs after complete: status=%d body=%s, want 200 []", code, body)
	}
}

// TestListInstanceJobsErrors covers the rejection/edge paths.
func TestListInstanceJobsErrors(t *testing.T) {
	srv := newServerForErrors(t)
	// Non-numeric instance key → 400, before any state touch.
	if code, _ := serveInternal(t, srv, http.MethodGet, "/api/v1/instances/notakey/jobs", "", ""); code != http.StatusBadRequest {
		t.Errorf("list jobs bad key: status=%d, want 400", code)
	}
	// An unknown instance simply has no jobs → 200 with an empty array.
	if code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/instances/999999/jobs", "", ""); code != http.StatusOK || strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("list jobs unknown instance: status=%d body=%s, want 200 []", code, body)
	}
}
