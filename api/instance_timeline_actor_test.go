package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestInstanceTimelineSurfacesOverrideActor covers the inline attribution (ADR-0098):
// when an operator overrides a variable on a running instance, the value carries the
// actor in the timeline steps that follow the override — so the replay shows not just
// what a corrected value is, but who set it. Auth is on so the override has a real
// actor; the instance is driven to completion so a step lands after the override and
// folds it in.
func TestInstanceTimelineSurfacesOverrideActor(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "supersecret")
	admin := newClient(t)
	if code := login(t, admin, ts, "root", "supersecret"); code != http.StatusOK {
		t.Fatalf("admin login: %d, want 200", code)
	}

	// Deploy start → serviceTask → end; start an instance that parks at the task.
	code, dep := cReqCT(t, admin, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, dep)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(dep, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	if code, _ := cReq(t, admin, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), `{"variables":{"amount":100}}`); code != http.StatusOK {
		t.Fatalf("start: %d", code)
	}

	key := activeInstanceKey(t, admin, ts)

	// Override amount as the admin, then complete the parked job so the token moves to
	// the end event — a step that lands after the override and carries it.
	if code, sv := cReq(t, admin, ts, http.MethodPost, fmt.Sprintf("/api/v1/instances/%d/variables", key), `{"variables":{"amount":200}}`); code != http.StatusOK {
		t.Fatalf("override: %d (%s)", code, sv)
	}
	jobKey := firstActivatableJob(t, admin, ts, key)
	if code, cj := cReq(t, admin, ts, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/complete", jobKey), `{"reason":"test: operator completed the parked job by hand"}`); code != http.StatusOK {
		t.Fatalf("complete job: %d (%s)", code, cj)
	}

	code, body := cReq(t, admin, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/timeline", key), "")
	if code != http.StatusOK {
		t.Fatalf("timeline: %d (%s)", code, body)
	}
	var tl instanceTimeline
	if err := json.Unmarshal(body, &tl); err != nil {
		t.Fatalf("decode timeline: %v (%s)", err, body)
	}

	// Some step (the end event, after the override) must show amount=200 attributed to
	// root. A model-written variable would carry no actor.
	found := false
	for _, step := range tl.Steps {
		for _, v := range step.Variables {
			if v.Name == "amount" && v.Value == "200" {
				if v.Actor != "root" {
					continue
				}
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("no timeline step shows amount=200 attributed to root; timeline=%s", body)
	}
}

// activeInstanceKey returns the key of the single active instance, via client c.
func activeInstanceKey(t *testing.T, c *http.Client, ts *httptest.Server) uint64 {
	t.Helper()
	code, list := cReq(t, c, ts, http.MethodGet, "/api/v1/instances", "")
	if code != http.StatusOK {
		t.Fatalf("list instances: %d", code)
	}
	var instances []struct {
		Key   uint64 `json:"key"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(list, &instances); err != nil {
		t.Fatalf("decode instances: %v", err)
	}
	for _, in := range instances {
		if in.State == "active" {
			return in.Key
		}
	}
	t.Fatal("no active instance found")
	return 0
}

// firstActivatableJob returns the key of the instance's single parked job, via client c.
func firstActivatableJob(t *testing.T, c *http.Client, ts *httptest.Server, instanceKey uint64) uint64 {
	t.Helper()
	code, body := cReq(t, c, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/jobs", instanceKey), "")
	if code != http.StatusOK {
		t.Fatalf("list jobs: %d (%s)", code, body)
	}
	var jobs []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &jobs); err != nil {
		t.Fatalf("decode jobs: %v (%s)", err, body)
	}
	if len(jobs) == 0 {
		t.Fatal("no activatable job found")
	}
	return jobs[0].Key
}
