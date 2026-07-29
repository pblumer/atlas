package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

type searchRow struct {
	Key       uint64 `json:"key"`
	ProcessID string `json:"processId"`
	State     string `json:"state"`
	Variables []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
		Kind  string `json:"kind"`
	} `json:"variables"`
}

// TestSearchInstancesByVariable deploys userTaskBPMN (id "approval") and starts
// several instances that park on the user task — so their start variables stay
// observable — then exercises the free-text and structured name=value search.
func TestSearchInstancesByVariable(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", userTaskBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	start := func(vars string) {
		code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), vars, "application/json")
		if code != http.StatusOK {
			t.Fatalf("create instance %s: status=%d body=%s", vars, code, body)
		}
	}
	start(`{"variables":{"customerType":"Business","city":"Köniz"}}`)
	start(`{"variables":{"customerType":"Consumer","applicant":{"score":72,"segment":"retail"}}}`)
	start(`{"variables":{"lastName":"Blumer","zip":3098}}`)

	do := func(q string) []searchRow {
		code, body := doReq(t, ts, http.MethodGet, "/api/v1/instances/search?q="+url.QueryEscape(q), "", "")
		if code != http.StatusOK {
			t.Fatalf("search %q: status=%d body=%s", q, code, body)
		}
		var rows []searchRow
		if err := json.Unmarshal(body, &rows); err != nil {
			t.Fatalf("decode search %q: %v (%s)", q, err, body)
		}
		return rows
	}

	// Free-text over values, case-insensitive substring.
	if rows := do("business"); len(rows) != 1 || rows[0].Variables[0].Value != "Business" {
		t.Errorf("free-text 'business' = %+v, want 1 row matching customerType=Business", rows)
	}
	// Free-text over variable names.
	if rows := do("lastname"); len(rows) != 1 {
		t.Errorf("free-text 'lastname' matched %d rows, want 1", len(rows))
	}
	// Free-text inside a JSON variable's canonical value.
	if rows := do("retail"); len(rows) != 1 {
		t.Errorf("free-text 'retail' (JSON value) matched %d rows, want 1", len(rows))
	}
	// Structured name=value: exact name, substring value.
	if rows := do("customerType=Cons"); len(rows) != 1 || rows[0].Variables[0].Value != "Consumer" {
		t.Errorf("structured 'customerType=Cons' = %+v, want the Consumer instance", rows)
	}
	// Structured matches every instance that has the named variable with the value.
	if rows := do("customerType=e"); len(rows) != 2 {
		t.Errorf("structured 'customerType=e' matched %d rows, want 2 (Business + Consumer)", len(rows))
	}
	// Structured name is exact: a substring of the name must not match.
	if rows := do("customer=Business"); len(rows) != 0 {
		t.Errorf("structured 'customer=Business' matched %d rows, want 0 (name is exact)", len(rows))
	}
	// A term that appears in no variable yields no rows.
	if rows := do("nonexistent-zzz"); len(rows) != 0 {
		t.Errorf("free-text 'nonexistent-zzz' matched %d rows, want 0", len(rows))
	}
	// Only the matching variables are returned on a row, not the whole scope.
	rows := do("Köniz")
	if len(rows) != 1 {
		t.Fatalf("free-text 'Köniz' matched %d rows, want 1", len(rows))
	}
	if len(rows[0].Variables) != 1 || rows[0].Variables[0].Name != "city" {
		t.Errorf("row variables = %+v, want only the matched 'city'", rows[0].Variables)
	}
}

// TestSearchInstancesFinished covers the completed-instance branch: an instance
// that ran to completion is still searchable by its retained variables, and
// comes back with its finished state (not "active").
func TestSearchInstancesFinished(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", userTaskBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), `{"variables":{"customerType":"Wholesale"}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}

	// Complete the single user task so the instance runs to the end event.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: status=%d body=%s", code, body)
	}
	var tasks []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &tasks); err != nil || len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %v (%s)", err, body)
	}
	code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/complete", tasks[0].Key), "{}", "application/json")
	if code != http.StatusOK {
		t.Fatalf("complete task: status=%d body=%s", code, body)
	}

	code, body = doReq(t, ts, http.MethodGet, "/api/v1/instances/search?q="+url.QueryEscape("customerType=Wholesale"), "", "")
	if code != http.StatusOK {
		t.Fatalf("search: status=%d body=%s", code, body)
	}
	var rows []searchRow
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(rows) != 1 {
		t.Fatalf("finished search matched %d rows, want 1", len(rows))
	}
	if rows[0].State == "active" {
		t.Errorf("state = %q, want a finished state", rows[0].State)
	}
	if len(rows[0].Variables) != 1 || rows[0].Variables[0].Value != "Wholesale" {
		t.Errorf("variables = %+v, want the matched customerType=Wholesale", rows[0].Variables)
	}
}

// TestSearchInstancesEmptyQuery: a blank query returns an empty array, not an
// error and not every instance.
func TestSearchInstancesEmptyQuery(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/instances/search?q=", "", "")
	if code != http.StatusOK {
		t.Fatalf("empty query: status=%d body=%s", code, body)
	}
	var rows []searchRow
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(rows) != 0 {
		t.Errorf("empty query returned %d rows, want 0", len(rows))
	}
}
