package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// TestListInstancesCapAndSummary covers the read-path hardening that keeps the
// operations page reachable under a large instance count: the list endpoint caps its
// page (and marks a capped page with X-Instances-Truncated), filters to one
// definition with ?process=, and the lean summary endpoint reports per-definition
// counts without enriching every instance.
func TestListInstancesCapAndSummary(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", timerWaitBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	const n = 3
	for i := 0; i < n; i++ {
		if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "{}", "application/json"); code != http.StatusOK {
			t.Fatalf("create instance %d: status=%d body=%s", i, code, b)
		}
	}

	// A capped page returns at most ?limit rows and flags truncation in a header.
	res, err := http.Get(ts.URL + "/api/v1/instances?limit=2")
	if err != nil {
		t.Fatalf("GET instances?limit=2: %v", err)
	}
	var page []struct {
		Key   uint64 `json:"key"`
		State string `json:"state"`
	}
	_ = json.NewDecoder(res.Body).Decode(&page)
	res.Body.Close()
	if len(page) != 2 {
		t.Fatalf("capped page = %d rows, want 2", len(page))
	}
	if res.Header.Get("X-Instances-Truncated") != "true" {
		t.Fatalf("truncation header = %q, want true", res.Header.Get("X-Instances-Truncated"))
	}

	// The summary reports the full active count without shipping every instance.
	_, body = doReq(t, ts, http.MethodGet, "/api/v1/instances/summary", "", "")
	var summary []struct {
		ProcessDefKey uint64 `json:"processDefKey"`
		Active        int    `json:"active"`
		Completed     int    `json:"completed"`
	}
	if err := json.Unmarshal(body, &summary); err != nil {
		t.Fatalf("decode summary: %v (%s)", err, body)
	}
	if len(summary) != 1 || summary[0].ProcessDefKey != dep.Key || summary[0].Active != n {
		t.Fatalf("summary = %+v, want one row with active=%d", summary, n)
	}

	// ?process= narrows to one definition; an unmatched key yields an empty list.
	_, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances?process=%d", dep.Key), "", "")
	var filtered []json.RawMessage
	_ = json.Unmarshal(body, &filtered)
	if len(filtered) != n {
		t.Fatalf("?process filter returned %d, want %d", len(filtered), n)
	}
	_, body = doReq(t, ts, http.MethodGet, "/api/v1/instances?process=424242", "", "")
	if string(body) != "[]\n" && string(body) != "[]" {
		t.Fatalf("?process=<unknown> = %s, want empty list", body)
	}

	// Malformed query params are rejected.
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/instances?limit=nope", "", ""); code != http.StatusBadRequest {
		t.Fatalf("?limit=nope status=%d, want 400", code)
	}
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/instances?process=nope", "", ""); code != http.StatusBadRequest {
		t.Fatalf("?process=nope status=%d, want 400", code)
	}
	// A limit above the ceiling is clamped, not rejected.
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/instances?limit=999999", "", ""); code != http.StatusOK {
		t.Fatalf("?limit over max status=%d, want 200 (clamped)", code)
	}
}

// autoCompleteBPMN runs straight from start to end, so an instance completes the
// moment it is created and lands in the finished (history) set.
const autoCompleteBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="quick" name="Quick" isExecutable="true">
    <startEvent id="s"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`

// TestListInstancesCompletedCounts exercises the finished-instance paths: the summary
// counts completed instances (with the newest completion time) and the list returns
// them, both narrowable with ?process=.
func TestListInstancesCompletedCounts(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", autoCompleteBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	const n = 3
	for i := 0; i < n; i++ {
		if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "{}", "application/json"); code != http.StatusOK {
			t.Fatalf("create instance %d: status=%d body=%s", i, code, b)
		}
	}

	// The finished side of the list is capped and flagged just like the active side.
	res, err := http.Get(ts.URL + "/api/v1/instances?limit=2")
	if err != nil {
		t.Fatalf("GET instances?limit=2: %v", err)
	}
	var page []json.RawMessage
	_ = json.NewDecoder(res.Body).Decode(&page)
	res.Body.Close()
	if len(page) != 2 || res.Header.Get("X-Instances-Truncated") != "true" {
		t.Fatalf("capped finished page = %d rows, truncated=%q; want 2 rows + true", len(page), res.Header.Get("X-Instances-Truncated"))
	}

	// A second definition, also completing, so the summary has more than one row
	// (exercising its ordering) and a ?process= filter has other-definition instances
	// to skip on the finished side.
	code, body2 := doReq(t, ts, http.MethodPost, "/api/v1/deployments", autoCompleteBPMN2, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy 2 status=%d body=%s", code, body2)
	}
	var dep2 struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body2, &dep2); err != nil {
		t.Fatalf("decode deploy 2: %v", err)
	}
	if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep2.Key), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance (def2): status=%d body=%s", code, b)
	}

	_, body = doReq(t, ts, http.MethodGet, "/api/v1/instances/summary", "", "")
	var summary []struct {
		ProcessDefKey     uint64 `json:"processDefKey"`
		Active            int    `json:"active"`
		Completed         int    `json:"completed"`
		LatestCompletedAt int64  `json:"latestCompletedAt"`
	}
	if err := json.Unmarshal(body, &summary); err != nil {
		t.Fatalf("decode summary: %v (%s)", err, body)
	}
	if len(summary) != 2 || summary[0].ProcessDefKey != dep.Key || summary[0].Completed != n || summary[0].LatestCompletedAt == 0 {
		t.Fatalf("summary = %+v, want two rows, first = def %d completed=%d", summary, dep.Key, n)
	}

	// The list returns this definition's finished instances only, skipping the other
	// definition's completed instances during the scan.
	_, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances?process=%d", dep.Key), "", "")
	var rows []struct {
		ProcessDefKey uint64 `json:"processDefKey"`
		State         string `json:"state"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, body)
	}
	if len(rows) != n {
		t.Fatalf("filtered finished list = %d rows, want %d", len(rows), n)
	}
	for _, r := range rows {
		if r.ProcessDefKey != dep.Key || r.State == "active" {
			t.Fatalf("filtered row = %+v, want finished rows of def %d only", r, dep.Key)
		}
	}
}

// autoCompleteBPMN2 is a second immediately-completing definition (distinct id) used
// to give the summary/list tests more than one definition.
const autoCompleteBPMN2 = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="quick2" name="Quick2" isExecutable="true">
    <startEvent id="s"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`
