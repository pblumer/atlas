package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// serviceJobBPMN is Start → ServiceTask(payment) → End. No worker is registered
// for the "payment" job type in the API server's runner, so a started instance
// parks at the service task with an activatable job — exactly the state the
// operator complete-job endpoint exists to unstick.
const serviceJobBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="order" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="charge">
      <extensionElements><zeebe:taskDefinition type="payment" retries="5"/></extensionElements>
    </serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="charge"/>
    <sequenceFlow id="f2" sourceRef="charge" targetRef="end"/>
  </process>
</definitions>`

// serveInternal runs one request against the server's real mux and returns the
// status and body — the white-box equivalent of doReq for package api tests.
func serveInternal(t *testing.T, srv *Server, method, path, body, contentType string) (int, []byte) {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	return rec.Code, rec.Body.Bytes()
}

// parkedServiceJob returns the key of the single parked service-task job and the
// process instance it belongs to. The scan runs on the run-loop goroutine (the
// sole toucher of state) via do, honoring the single-writer invariant.
func parkedServiceJob(t *testing.T, srv *Server, defKey uint64) (jobKey, instKey uint64) {
	t.Helper()
	srv.do(func() {
		cp := srv.processLookup(defKey)
		if cp == nil {
			return
		}
		_ = srv.store.ActiveElementInstances(func(_ uint64, v *model.ElementInstanceValue) error {
			if v.ProcessDefKey != defKey || cp.Node(v.ElementId).Type != compiler.TypeServiceTask {
				return nil
			}
			instKey = v.ProcessInstanceKey
			jobType := cp.ServiceTask(cp.Node(v.ElementId).Detail).JobType
			return srv.store.ActivatableJobs(jobType, func(k uint64) error {
				jobKey = k
				return nil
			})
		})
	})
	if jobKey == 0 || instKey == 0 {
		t.Fatalf("no parked service-task job found (job=%d inst=%d)", jobKey, instKey)
	}
	return jobKey, instKey
}

// TestCompleteServiceJobOverHTTP is the motivating case: an instance parks at a
// service task with no worker, and an operator completes its job by hand over
// HTTP — carrying the outputs the (absent) worker would have produced — which
// flows the token to the end event and completes the instance.
func TestCompleteServiceJobOverHTTP(t *testing.T) {
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

	// Complete the parked job with the outputs a worker would have returned.
	code, body = serveInternal(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/complete", jobKey), `{"variables":{"paid":true}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("complete job: status=%d body=%s", code, body)
	}
	var resp struct {
		JobKey uint64 `json:"jobKey"`
	}
	if err := json.Unmarshal(body, &resp); err != nil || resp.JobKey != jobKey {
		t.Fatalf("complete resp = %s (err %v), want jobKey %d", body, err, jobKey)
	}

	// The job is gone and the instance ran to completion.
	var jobGone, completed bool
	srv.do(func() {
		if _, ok, err := srv.store.GetJob(jobKey); err == nil && !ok {
			jobGone = true
		}
		if pi, ok, err := srv.store.ProcessInstance(instKey); err == nil && ok && pi.State == model.PICompleted {
			completed = true
		}
	})
	if !jobGone {
		t.Errorf("job %d still present after completion", jobKey)
	}
	if !completed {
		t.Errorf("instance %d did not complete after the job was completed", instKey)
	}
}

// TestCompleteJobErrors covers the handler's rejection paths, mirroring the
// bad-key / bad-body / missing-job shape of the fail-job endpoint.
func TestCompleteJobErrors(t *testing.T) {
	srv := newServerForErrors(t)

	// Non-numeric key → 400 (before any state touch).
	if code, _ := serveInternal(t, srv, http.MethodPost, "/api/v1/jobs/notakey/complete", `{}`, "application/json"); code != http.StatusBadRequest {
		t.Errorf("complete bad key: status=%d, want 400", code)
	}
	// Malformed JSON body → 400 (variables are parsed before the job is looked up).
	if code, _ := serveInternal(t, srv, http.MethodPost, "/api/v1/jobs/5/complete", `{bad`, "application/json"); code != http.StatusBadRequest {
		t.Errorf("complete bad body: status=%d, want 400", code)
	}
	// Well-formed request for a job that does not exist → 404.
	if code, _ := serveInternal(t, srv, http.MethodPost, "/api/v1/jobs/999999/complete", `{}`, "application/json"); code != http.StatusNotFound {
		t.Errorf("complete missing job: status=%d, want 404", code)
	}
}
