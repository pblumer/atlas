package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// jobTypeModel is a one-service-task process whose BPMN process id and job type
// are both parameters, so two definitions can be deployed that differ only in
// their model-authored job type — the shape that produced the collision.
func jobTypeModel(processID, jobType string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
             targetNamespace="http://example.com/bpmn">
  <process id="%s" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="task">
      <extensionElements><zeebe:taskDefinition type="%s" retries="3"/></extensionElements>
    </serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="task"/>
    <sequenceFlow id="f2" sourceRef="task" targetRef="end"/>
  </process>
</definitions>`, processID, jobType)
}

type deployedProcess struct {
	Key       uint64 `json:"key"`
	ProcessID string `json:"processId"`
}

type instanceJob struct {
	Key     uint64 `json:"key"`
	JobType string `json:"jobType"`
}

// TestJobTypesAreDistinctAcrossDefinitions is the regression test for the defect
// the engine-wide job-type table fixes: two definitions each intern their own
// strings, so before the table both service tasks created jobs under the same
// index for different job types, and a worker subscribing by type would be handed
// the wrong work (ADR-0007).
func TestJobTypesAreDistinctAcrossDefinitions(t *testing.T) {
	ts := newTestServer(t)

	deploy := func(processID, jobType string) uint64 {
		t.Helper()
		code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", jobTypeModel(processID, jobType), "application/xml")
		if code != http.StatusOK {
			t.Fatalf("deploy %s: status=%d body=%s", processID, code, body)
		}
		var dep deployedProcess
		if err := json.Unmarshal(body, &dep); err != nil || dep.Key == 0 {
			t.Fatalf("decode deploy %s: %v (%s)", processID, err, body)
		}
		return dep.Key
	}
	start := func(defKey uint64) {
		t.Helper()
		code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", defKey), "{}", "application/json")
		if code != http.StatusOK {
			t.Fatalf("start %d: status=%d body=%s", defKey, code, body)
		}
	}
	// The start response carries stats, not the instance key, so the running
	// instance is looked up by the process id it belongs to.
	instanceOf := func(processID string) uint64 {
		t.Helper()
		code, body := doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
		if code != http.StatusOK {
			t.Fatalf("instances: status=%d body=%s", code, body)
		}
		var insts []struct {
			Key       uint64 `json:"key"`
			ProcessID string `json:"processId"`
		}
		if err := json.Unmarshal(body, &insts); err != nil {
			t.Fatalf("decode instances: %v (%s)", err, body)
		}
		for _, in := range insts {
			if in.ProcessID == processID {
				return in.Key
			}
		}
		t.Fatalf("no running instance of %q in %s", processID, body)
		return 0
	}
	jobsOf := func(instanceKey uint64) []instanceJob {
		t.Helper()
		code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/jobs", instanceKey), "", "")
		if code != http.StatusOK {
			t.Fatalf("list jobs of %d: status=%d body=%s", instanceKey, code, body)
		}
		var jobs []instanceJob
		if err := json.Unmarshal(body, &jobs); err != nil {
			t.Fatalf("decode jobs: %v (%s)", err, body)
		}
		return jobs
	}

	start(deploy("orders", "send-email"))
	start(deploy("billing", "charge-card"))
	orders, billing := instanceOf("orders"), instanceOf("billing")

	// Each instance parks on exactly one job, and the listing reports the job type
	// the model authored — the read path has to resolve the engine-wide index back
	// to a name, not through the process's own string table.
	byName := map[string]uint64{}
	for _, tc := range []struct {
		instance uint64
		want     string
	}{{orders, "send-email"}, {billing, "charge-card"}} {
		jobs := jobsOf(tc.instance)
		if len(jobs) != 1 {
			t.Fatalf("instance %d has %d activatable jobs, want 1", tc.instance, len(jobs))
		}
		if jobs[0].JobType != tc.want {
			t.Errorf("instance %d job type = %q, want %q", tc.instance, jobs[0].JobType, tc.want)
		}
		byName[tc.want] = jobs[0].Key
	}
	if len(byName) != 2 {
		t.Fatalf("the two instances reported %d distinct job types, want 2", len(byName))
	}
}
