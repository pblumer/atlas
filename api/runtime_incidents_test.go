package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// runtimeView mirrors the overlay payload the live view polls, including the incident
// fields it draws a parked element from.
type runtimeView struct {
	Instances int `json:"instances"`
	Tokens    int `json:"tokens"`
	Elements  []struct {
		ElementID string `json:"elementId"`
		Tokens    int    `json:"tokens"`
		Incidents int    `json:"incidents"`
	} `json:"elements"`
	Incidents []struct {
		ElementInstanceKey uint64 `json:"elementInstanceKey"`
		ProcessInstanceKey uint64 `json:"processInstanceKey"`
		JobKey             uint64 `json:"jobKey"`
		ElementID          string `json:"elementId"`
		RaisedAt           int64  `json:"raisedAt"`
		Message            string `json:"message"`
	} `json:"incidents"`
	IncidentsTruncated bool `json:"incidentsTruncated"`
}

func getRuntime(t *testing.T, ts *httptest.Server, path string) runtimeView {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, path, "", "")
	if code != http.StatusOK {
		t.Fatalf("runtime %s: status=%d body=%s", path, code, body)
	}
	var rt runtimeView
	if err := json.Unmarshal(body, &rt); err != nil {
		t.Fatalf("decode runtime: %v (%s)", err, body)
	}
	return rt
}

// TestProcessRuntimeShowsWhyATokenIsParked is the reason this data is on the overlay
// at all: an operator looking at the live diagram of a failing process used to see a
// token sitting on a task and nothing else — identical to a task that is legitimately
// waiting — while the engine had been holding the failure the whole time (ADR-0150).
// The runtime payload now carries the incident, on both the aggregate and the
// single-instance view, and stops carrying it the moment it is resolved.
func TestProcessRuntimeShowsWhyATokenIsParked(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", incidentUserTaskBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	if code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}
	// The create answers with the definition key and live stats, so the instance key
	// comes from the listing.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	var instances []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &instances); err != nil || len(instances) != 1 {
		t.Fatalf("list instances: %v (status=%d body=%s)", err, code, body)
	}
	instKey := instances[0].Key

	// Nothing is wrong yet: a token waits on the task and the overlay says so.
	rt := getRuntime(t, ts, fmt.Sprintf("/api/v1/processes/%d/runtime?instance=%d", dep.Key, instKey))
	if len(rt.Incidents) != 0 {
		t.Fatalf("a healthy instance reports %d incidents", len(rt.Incidents))
	}

	// Exhaust the job's retries the way a failing connector does.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	var tasks []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &tasks); err != nil || len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %v (%s)", err, body)
	}
	jobKey := tasks[0].Key
	failure := `{"retries":0,"message":"mail: send via mx1.example.ch: dial tcp: missing port in address"}`
	if code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/fail", jobKey), failure, "application/json"); code != http.StatusOK {
		t.Fatalf("fail job: status=%d body=%s", code, body)
	}

	for _, path := range []string{
		fmt.Sprintf("/api/v1/processes/%d/runtime?instance=%d", dep.Key, instKey),
		fmt.Sprintf("/api/v1/processes/%d/runtime", dep.Key), // the aggregate view finds it too
	} {
		rt = getRuntime(t, ts, path)
		if len(rt.Incidents) != 1 {
			t.Fatalf("%s: incidents = %d, want 1", path, len(rt.Incidents))
		}
		inc := rt.Incidents[0]
		switch {
		case inc.ElementID != "review":
			t.Errorf("%s: incident element = %q, want the BPMN id \"review\"", path, inc.ElementID)
		case inc.ProcessInstanceKey != instKey || inc.JobKey != jobKey:
			t.Errorf("%s: incident = %+v, want it to point at instance %d / job %d", path, inc, instKey, jobKey)
		case !strings.Contains(inc.Message, "missing port in address"):
			t.Errorf("%s: incident message = %q, want the provider's own words", path, inc.Message)
		case inc.RaisedAt == 0:
			t.Errorf("%s: incident has no raisedAt", path)
		}
		if rt.IncidentsTruncated {
			t.Errorf("%s: one incident reported as a truncated page", path)
		}
		// The element the diagram marks red is the one holding the parked token.
		var marked int
		for _, e := range rt.Elements {
			if e.ElementID == "review" {
				marked = e.Incidents
			}
		}
		if marked != 1 {
			t.Errorf("%s: element \"review\" reports %d incidents, want 1", path, marked)
		}
	}

	// Resolving it clears the overlay again — the token is live, not parked.
	elKey := rt.Incidents[0].ElementInstanceKey
	if code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/incidents/%d/resolve", elKey), `{"retries":1}`, "application/json"); code != http.StatusOK {
		t.Fatalf("resolve: status=%d body=%s", code, body)
	}
	rt = getRuntime(t, ts, fmt.Sprintf("/api/v1/processes/%d/runtime?instance=%d", dep.Key, instKey))
	if len(rt.Incidents) != 0 {
		t.Errorf("after resolve: incidents = %+v, want none", rt.Incidents)
	}
	if rt.Tokens != 1 {
		t.Errorf("after resolve: tokens = %d, want the token back on the task", rt.Tokens)
	}
}

// TestProcessRuntimeIgnoresAnotherDefinitionsIncidents: the aggregate view attributes
// an incident through its process instance, so a failure in one deployment must not
// appear on another's diagram — where its compiled element index would name a
// completely unrelated shape.
func TestProcessRuntimeIgnoresAnotherDefinitionsIncidents(t *testing.T) {
	ts := newTestServer(t)

	deploy := func(xml string) uint64 {
		t.Helper()
		code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", xml, "application/xml")
		if code != http.StatusOK {
			t.Fatalf("deploy: status=%d body=%s", code, body)
		}
		var dep struct {
			Key uint64 `json:"key"`
		}
		if err := json.Unmarshal(body, &dep); err != nil {
			t.Fatalf("decode deploy: %v", err)
		}
		return dep.Key
	}
	failing := deploy(incidentUserTaskBPMN)
	other := deploy(strings.ReplaceAll(incidentUserTaskBPMN, `id="approval"`, `id="unrelated"`))

	if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", failing), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}
	_, body := doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	var tasks []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &tasks); err != nil || len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %v (%s)", err, body)
	}
	if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/fail", tasks[0].Key), `{"retries":0,"message":"boom"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("fail job: status=%d body=%s", code, body)
	}

	if rt := getRuntime(t, ts, fmt.Sprintf("/api/v1/processes/%d/runtime", failing)); len(rt.Incidents) != 1 {
		t.Errorf("failing definition: incidents = %d, want 1", len(rt.Incidents))
	}
	if rt := getRuntime(t, ts, fmt.Sprintf("/api/v1/processes/%d/runtime", other)); len(rt.Incidents) != 0 {
		t.Errorf("unrelated definition: incidents = %+v, want none", rt.Incidents)
	}
}
