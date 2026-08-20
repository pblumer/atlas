package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// parkedJob deploys the sample process (start → serviceTask → end), starts an
// instance, and returns the key of the job it parks on — the situation an operator
// faces when a task cannot succeed on its own.
func parkedJob(t *testing.T, c *http.Client, ts *httptest.Server) uint64 {
	t.Helper()
	code, dep := cReqCT(t, c, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, dep)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(dep, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	if code, raw := cReq(t, c, ts, http.MethodPost,
		fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), `{"variables":{"amount":100}}`); code != http.StatusOK {
		t.Fatalf("start: %d (%s)", code, raw)
	}
	key := activeInstanceKey(t, c, ts)
	code, raw := cReq(t, c, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/jobs", key), "")
	if code != http.StatusOK {
		t.Fatalf("list jobs: %d (%s)", code, raw)
	}
	var jobs []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(raw, &jobs); err != nil {
		t.Fatalf("decode jobs: %v", err)
	}
	if len(jobs) == 0 {
		t.Fatal("no job parked on the instance")
	}
	return jobs[0].Key
}

// TestManualCompletionIsRefusedWithoutReason covers the gate that makes ADR-0159 worth
// having: completing a parked job by hand is an operator intervention, so the route
// refuses one that does not say why. Without it the audit record would exist but carry
// nothing worth reading.
func TestManualCompletionIsRefusedWithoutReason(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "supersecret")
	c := newClient(t)
	if code := login(t, c, ts, "root", "supersecret"); code != http.StatusOK {
		t.Fatalf("login: %d", code)
	}

	jobKey := parkedJob(t, c, ts)
	for _, body := range []string{``, `{}`, `{"variables":{"ok":true}}`, `{"reason":"   "}`} {
		code, raw := cReq(t, c, ts, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/complete", jobKey), body)
		if code != http.StatusBadRequest {
			t.Fatalf("complete with body %q = %d (%s), want 400", body, code, raw)
		}
		if !strings.Contains(string(raw), "reason is required") {
			t.Fatalf("complete with body %q: error = %s, want it to name the missing reason", body, raw)
		}
	}
	// Every refusal left the job alone: the token never moved, so it still completes.
	if code, raw := cReq(t, c, ts, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/complete", jobKey),
		`{"reason":"the work was carried out out of band"}`); code != http.StatusOK {
		t.Fatalf("complete with a reason = %d (%s), want 200 — the job should still have been there", code, raw)
	}
}

// TestManualCompletionIsAudited is the point of ADR-0159: a job an operator completes
// by hand carries who did it and why on its step in the instance timeline, so a replay
// can never present a forced step as one the engine drove. Auth is on, so the record
// has a real actor.
func TestManualCompletionIsAudited(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "supersecret")
	admin := newClient(t)
	if code := login(t, admin, ts, "root", "supersecret"); code != http.StatusOK {
		t.Fatalf("admin login: %d, want 200", code)
	}

	jobKey := parkedJob(t, admin, ts)
	key := activeInstanceKey(t, admin, ts)
	const reason = "account created by hand in AD, ticket INC-4711"
	body := fmt.Sprintf(`{"reason":%q,"variables":{"kontoAngelegt":true}}`, reason)
	if code, raw := cReq(t, admin, ts, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/complete", jobKey), body); code != http.StatusOK {
		t.Fatalf("manual complete: %d (%s)", code, raw)
	}

	code, raw := cReq(t, admin, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/timeline", key), "")
	if code != http.StatusOK {
		t.Fatalf("timeline: %d (%s)", code, raw)
	}
	var tl struct {
		Steps []struct {
			ElementID string `json:"elementId"`
			Manual    *struct {
				Action string `json:"action"`
				Actor  string `json:"actor"`
				Reason string `json:"reason"`
				At     int64  `json:"at"`
			} `json:"manual"`
			VariablesAfter []struct {
				Name string `json:"name"`
			} `json:"variablesAfter"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(raw, &tl); err != nil {
		t.Fatalf("decode timeline: %v", err)
	}

	marked := 0
	for _, s := range tl.Steps {
		if s.Manual == nil {
			continue
		}
		marked++
		if s.Manual.Action != "completeJob" {
			t.Errorf("step %s: action = %q, want completeJob", s.ElementID, s.Manual.Action)
		}
		if s.Manual.Actor != "root" {
			t.Errorf("step %s: actor = %q, want the authenticated operator", s.ElementID, s.Manual.Actor)
		}
		if s.Manual.Reason != reason {
			t.Errorf("step %s: reason = %q, want %q", s.ElementID, s.Manual.Reason, reason)
		}
		if s.Manual.At == 0 {
			t.Errorf("step %s: the intervention carries no timestamp", s.ElementID)
		}
		// The outputs the operator supplied are what the step left behind, so the replay
		// shows what was asserted, not merely that something was forced (ADR-0159).
		var names []string
		for _, v := range s.VariablesAfter {
			names = append(names, v.Name)
		}
		found := false
		for _, n := range names {
			if n == "kontoAngelegt" {
				found = true
			}
		}
		if !found {
			t.Errorf("step %s: variablesAfter = %v, want the operator's output among them", s.ElementID, names)
		}
	}
	if marked != 1 {
		t.Fatalf("%d steps marked manual, want exactly the one that was forced", marked)
	}
}

// TestUserTaskCompletionIsNotAudited guards the other direction: a person doing their
// assigned work through the Tasks app is the modelled behaviour, not an operator
// forcing a step, so it must not mint an operator-action record — otherwise ordinary
// steps would read as interventions and the marker would mean nothing.
func TestUserTaskCompletionIsNotAudited(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "supersecret")
	c := newClient(t)
	if code := login(t, c, ts, "root", "supersecret"); code != http.StatusOK {
		t.Fatalf("login: %d", code)
	}

	code, dep := cReqCT(t, c, ts, http.MethodPost, "/api/v1/deployments", userTaskBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, dep)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(dep, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	if code, raw := cReq(t, c, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), `{}`); code != http.StatusOK {
		t.Fatalf("start: %d (%s)", code, raw)
	}
	key := activeInstanceKey(t, c, ts)
	code, raw := cReq(t, c, ts, http.MethodGet, "/api/v1/tasks", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: %d (%s)", code, raw)
	}
	var tasks []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(raw, &tasks); err != nil {
		t.Fatalf("decode tasks: %v", err)
	}
	if len(tasks) == 0 {
		t.Skip("no user task surfaced; the tasks route is covered elsewhere")
	}
	if code, raw := cReq(t, c, ts, http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/complete", tasks[0].Key), `{}`); code != http.StatusOK {
		t.Fatalf("complete task: %d (%s)", code, raw)
	}
	code, raw = cReq(t, c, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/timeline", key), "")
	if code != http.StatusOK {
		t.Fatalf("timeline: %d (%s)", code, raw)
	}
	if strings.Contains(string(raw), `"manual"`) {
		t.Fatalf("an ordinary user-task completion produced an operator-action record: %s", raw)
	}
}
