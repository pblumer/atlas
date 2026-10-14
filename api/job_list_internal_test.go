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

// scopedJobsBPMN parks one instance on two jobs at once: a service task at the top
// level and another inside an embedded subprocess, so one of the instance's jobs
// sits on a token whose flow scope is not the instance itself.
const scopedJobsBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="fulfil" isExecutable="true">
    <startEvent id="start"/>
    <parallelGateway id="split"/>
    <serviceTask id="charge">
      <extensionElements><zeebe:taskDefinition type="payment" retries="5"/></extensionElements>
    </serviceTask>
    <subProcess id="packing">
      <startEvent id="subStart"/>
      <serviceTask id="ship">
        <extensionElements><zeebe:taskDefinition type="shipping" retries="5"/></extensionElements>
      </serviceTask>
      <endEvent id="subEnd"/>
      <sequenceFlow id="s1" sourceRef="subStart" targetRef="ship"/>
      <sequenceFlow id="s2" sourceRef="ship" targetRef="subEnd"/>
    </subProcess>
    <parallelGateway id="join"/>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="split"/>
    <sequenceFlow id="f2" sourceRef="split" targetRef="charge"/>
    <sequenceFlow id="f3" sourceRef="split" targetRef="packing"/>
    <sequenceFlow id="f4" sourceRef="charge" targetRef="join"/>
    <sequenceFlow id="f5" sourceRef="packing" targetRef="join"/>
    <sequenceFlow id="f6" sourceRef="join" targetRef="end"/>
  </process>
</definitions>`

// TestListInstanceJobsScopedToInstance pins what the listing returns now that it
// reads through the instance's own element index instead of the server-wide
// activatable index: every activatable job of *this* instance — a subprocess's
// included — and nothing of another instance, with a leased job left out exactly as
// the index leaves it out.
func TestListInstanceJobsScopedToInstance(t *testing.T) {
	srv := newServerForErrors(t)

	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", scopedJobsBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	start := func() uint64 {
		t.Helper()
		code, body := serveInternal(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "{}", "application/json")
		if code != http.StatusOK {
			t.Fatalf("create instance: status=%d body=%s", code, body)
		}
		var created struct {
			InstanceKey uint64 `json:"instanceKey"`
		}
		if err := json.Unmarshal(body, &created); err != nil || created.InstanceKey == 0 {
			t.Fatalf("decode create: %v (%s)", err, body)
		}
		return created.InstanceKey
	}
	type row struct {
		Key                uint64 `json:"key"`
		ProcessInstanceKey uint64 `json:"processInstanceKey"`
		ProcessDefKey      uint64 `json:"processDefKey"`
		ElementID          string `json:"elementId"`
		JobType            string `json:"jobType"`
		Retries            int32  `json:"retries"`
	}
	list := func(inst uint64) map[string]row {
		t.Helper()
		code, body := serveInternal(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/jobs", inst), "", "")
		if code != http.StatusOK {
			t.Fatalf("list jobs of %d: status=%d body=%s", inst, code, body)
		}
		var rows []row
		if err := json.Unmarshal(body, &rows); err != nil {
			t.Fatalf("decode jobs: %v (%s)", err, body)
		}
		byElement := map[string]row{}
		for _, r := range rows {
			if r.ProcessInstanceKey != inst {
				t.Errorf("instance %d lists job %d of instance %d", inst, r.Key, r.ProcessInstanceKey)
			}
			byElement[r.ElementID] = r
		}
		if len(byElement) != len(rows) {
			t.Errorf("instance %d lists %d rows over %d elements: %s", inst, len(rows), len(byElement), body)
		}
		return byElement
	}

	a, b := start(), start()
	jobsA, jobsB := list(a), list(b)
	for inst, jobs := range map[uint64]map[string]row{a: jobsA, b: jobsB} {
		if len(jobs) != 2 {
			t.Fatalf("instance %d lists %d jobs, want 2 (charge, ship): %+v", inst, len(jobs), jobs)
		}
		for element, jobType := range map[string]string{"charge": "payment", "ship": "shipping"} {
			j, ok := jobs[element]
			if !ok {
				t.Fatalf("instance %d does not list its %s job: %+v", inst, element, jobs)
			}
			if j.JobType != jobType || j.ProcessDefKey != deploy.Key || j.Retries != 5 {
				t.Errorf("instance %d %s job = %+v, want type %s, definition %d, retries 5", inst, element, j, jobType, deploy.Key)
			}
		}
	}
	if jobsA["charge"].Key == jobsB["charge"].Key || jobsA["ship"].Key == jobsB["ship"].Key {
		t.Fatalf("the two instances list the same job: a=%+v b=%+v", jobsA, jobsB)
	}

	// A worker leases one payment job. It is off the activatable index while leased,
	// so its instance no longer lists it; everything else is unchanged.
	code, body = serveInternal(t, srv, http.MethodPost, "/api/v1/jobs/activate",
		`{"type":"payment","worker":"w1","maxJobs":1,"leaseMs":60000}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("lease: status=%d body=%s", code, body)
	}
	var leased struct {
		Jobs []struct {
			Key uint64 `json:"key"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(body, &leased); err != nil || len(leased.Jobs) != 1 {
		t.Fatalf("lease: want one job, got %v (%s)", err, body)
	}
	leasedKey := leased.Jobs[0].Key
	after := map[uint64]map[string]row{a: list(a), b: list(b)}
	listed := 0
	for inst, jobs := range after {
		listed += len(jobs)
		for _, j := range jobs {
			if j.Key == leasedKey {
				t.Errorf("instance %d still lists the leased job %d", inst, leasedKey)
			}
		}
	}
	if listed != 3 {
		t.Errorf("after one lease the two instances list %d jobs, want 3: %+v", listed, after)
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
