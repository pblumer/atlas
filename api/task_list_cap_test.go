package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// TestListTasksCap proves the task inbox is bounded: with more parked user tasks than
// the page cap, GET /api/v1/tasks?limit= returns at most that many and says truncated
// in the body, so the inbox loads even when a definition has hundreds of thousands of
// instances parked on a user task (the reported flood).
func TestListTasksCap(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", userTaskBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	const n = 3
	for i := 0; i < n; i++ {
		if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "{}", "application/json"); code != http.StatusOK {
			t.Fatalf("create instance %d: status=%d body=%s", i, code, b)
		}
	}

	// A capped page returns at most ?limit rows and says so.
	res, err := http.Get(ts.URL + "/api/v1/tasks?limit=2")
	if err != nil {
		t.Fatalf("GET tasks?limit=2: %v", err)
	}
	page := readPage(t, res)
	res.Body.Close()
	if page.Total != 2 || !page.Truncated {
		t.Fatalf("capped task page = %d rows truncated=%v, want 2 + true", page.Total, page.Truncated)
	}

	// The default (uncapped-by-the-caller) page returns all three, not truncated.
	res, err = http.Get(ts.URL + "/api/v1/tasks")
	if err != nil {
		t.Fatalf("GET tasks: %v", err)
	}
	page = readPage(t, res)
	res.Body.Close()
	if page.Total != n || page.Truncated {
		t.Fatalf("default page = %d rows, truncated=%v; want %d rows, not truncated", page.Total, page.Truncated, n)
	}

	// An over-ceiling limit is clamped, not rejected; a bad limit is a 400.
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/tasks?limit=999999", "", ""); code != http.StatusOK {
		t.Fatalf("?limit over max status=%d, want 200 (clamped)", code)
	}
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/tasks?limit=nope", "", ""); code != http.StatusBadRequest {
		t.Fatalf("?limit=nope status=%d, want 400", code)
	}
}

// TestListIncidentsCap proves the incidents list is bounded the same way (a ?limit=
// cap and a truncated flag in the body), so a flood of failures cannot make the
// "what's stuck" view unbounded.
func TestListIncidentsCap(t *testing.T) {
	ts := newTestServer(t)

	// The empty list is a clean, unflagged 200; a bad limit is a 400.
	if res, err := http.Get(ts.URL + "/api/v1/incidents?limit=10"); err != nil {
		t.Fatalf("GET incidents: %v", err)
	} else {
		ok := res.StatusCode == http.StatusOK
		empty := readPage(t, res)
		res.Body.Close()
		if !ok || empty.Truncated || empty.Total != 0 {
			t.Fatalf("empty incidents: status=%d total=%d truncated=%v; want 200, 0, false", res.StatusCode, empty.Total, empty.Truncated)
		}
	}
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/incidents?limit=0", "", ""); code != http.StatusBadRequest {
		t.Fatalf("?limit=0 status=%d, want 400", code)
	}

	// Raise two incidents (two parked jobs failed to exhaustion), then a ?limit=1 page
	// returns one and flags truncation.
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", incidentUserTaskBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	_ = json.Unmarshal(body, &deploy)
	for i := 0; i < 2; i++ {
		if c, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "{}", "application/json"); c != http.StatusOK {
			t.Fatalf("create instance %d: status=%d body=%s", i, c, b)
		}
	}
	_, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	var tasks []struct {
		Key uint64 `json:"key"`
	}
	_ = json.Unmarshal(listRows(t, body), &tasks)
	if len(tasks) != 2 {
		t.Fatalf("want 2 parked tasks, got %d", len(tasks))
	}
	for _, tk := range tasks {
		if c, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/fail", tk.Key), `{"retries":0,"message":"boom"}`, "application/json"); c != http.StatusOK {
			t.Fatalf("fail job %d: status=%d body=%s", tk.Key, c, b)
		}
	}

	res, err := http.Get(ts.URL + "/api/v1/incidents?limit=1")
	if err != nil {
		t.Fatalf("GET incidents?limit=1: %v", err)
	}
	capped := readPage(t, res)
	res.Body.Close()
	if capped.Total != 1 || !capped.Truncated {
		t.Fatalf("capped incidents = %d rows, truncated=%v; want 1 + true", capped.Total, capped.Truncated)
	}
}
