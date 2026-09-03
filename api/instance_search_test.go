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

// TestSearchInstancesByKey covers the exact-key lookup: an instance key pasted
// into the search box is answered as a point read of that one instance, not as a
// scan. The row carries the instance's *whole* variable set — the operator asked
// for the instance, not for the variables some needle matched — which is also how
// this result is told apart from a content match.
func TestSearchInstancesByKey(t *testing.T) {
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
	for _, vars := range []string{
		`{"variables":{"customerType":"Business","city":"Köniz"}}`,
		`{"variables":{"customerType":"Consumer","city":"Bern"}}`,
	} {
		code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), vars, "application/json")
		if code != http.StatusOK {
			t.Fatalf("create instance %s: status=%d body=%s", vars, code, body)
		}
	}

	code, body = doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	if code != http.StatusOK {
		t.Fatalf("list instances: status=%d body=%s", code, body)
	}
	var listed []searchRow
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, body)
	}
	if len(listed) != 2 {
		t.Fatalf("listed %d instances, want 2", len(listed))
	}
	want := listed[0].Key

	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/search?q=%d", want), "", "")
	if code != http.StatusOK {
		t.Fatalf("search by key: status=%d body=%s", code, body)
	}
	var rows []searchRow
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(rows) != 1 {
		t.Fatalf("search by key matched %d rows, want exactly 1", len(rows))
	}
	if rows[0].Key != want {
		t.Errorf("key = %d, want %d", rows[0].Key, want)
	}
	if rows[0].State != "active" {
		t.Errorf("state = %q, want active", rows[0].State)
	}
	if rows[0].ProcessID == "" {
		t.Error("processId is empty: the row was not enriched with its definition")
	}
	// The whole scope, not a needle's subset: both start variables are there.
	if len(rows[0].Variables) != 2 {
		t.Errorf("variables = %+v, want both of the instance's variables", rows[0].Variables)
	}
}

// TestSearchInstancesByKeyFinished: the exact-key lookup reaches the history
// record too, so an operator can paste the key of an instance that has since
// finished and still land on it.
func TestSearchInstancesByKeyFinished(t *testing.T) {
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
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	if code != http.StatusOK {
		t.Fatalf("list instances: status=%d body=%s", code, body)
	}
	var listed []searchRow
	if err := json.Unmarshal(body, &listed); err != nil || len(listed) != 1 {
		t.Fatalf("expected 1 instance, got %v (%s)", err, body)
	}
	key := listed[0].Key

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

	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/search?q=%d", key), "", "")
	if code != http.StatusOK {
		t.Fatalf("search by key: status=%d body=%s", code, body)
	}
	var rows []searchRow
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(rows) != 1 || rows[0].Key != key {
		t.Fatalf("search by key = %+v, want the finished instance %d", rows, key)
	}
	if rows[0].State == "active" {
		t.Errorf("state = %q, want a finished state", rows[0].State)
	}
}

// TestSearchInstancesNumericQueryStillSearchesContent guards the risk the
// exact-key lookup introduces: a digits-only query that is *not* an instance key
// must still search variable content rather than come back empty.
func TestSearchInstancesNumericQueryStillSearchesContent(t *testing.T) {
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
	code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), `{"variables":{"zip":3098}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}

	code, body = doReq(t, ts, http.MethodGet, "/api/v1/instances/search?q=3098", "", "")
	if code != http.StatusOK {
		t.Fatalf("search: status=%d body=%s", code, body)
	}
	var rows []searchRow
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(rows) != 1 {
		t.Fatalf("numeric free-text '3098' matched %d rows, want 1 (zip=3098)", len(rows))
	}
	if len(rows[0].Variables) != 1 || rows[0].Variables[0].Name != "zip" {
		t.Errorf("variables = %+v, want only the matched 'zip'", rows[0].Variables)
	}
}

// TestSearchInstancesScopedToProcess: narrowing a content search to one
// definition reads that definition's index instead of every instance in the
// engine, and returns only its matches. Scoping is what keeps a search on a busy
// engine proportional to the version being looked at.
func TestSearchInstancesScopedToProcess(t *testing.T) {
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
	defA := deploy(userTaskBPMN)
	defB := deploy(timerWaitBPMN)
	start := func(def uint64, vars string) {
		t.Helper()
		if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", def), vars, "application/json"); code != http.StatusOK {
			t.Fatalf("create instance: status=%d body=%s", code, b)
		}
	}
	start(defA, `{"variables":{"customerType":"Business"}}`)
	start(defB, `{"variables":{"customerType":"Business"}}`)

	do := func(query string) []searchRow {
		t.Helper()
		code, body := doReq(t, ts, http.MethodGet, "/api/v1/instances/search"+query, "", "")
		if code != http.StatusOK {
			t.Fatalf("search %s: status=%d body=%s", query, code, body)
		}
		var rows []searchRow
		if err := json.Unmarshal(body, &rows); err != nil {
			t.Fatalf("decode search %s: %v (%s)", query, err, body)
		}
		return rows
	}

	if rows := do("?q=" + url.QueryEscape("customerType=Business")); len(rows) != 2 {
		t.Errorf("unscoped search matched %d rows, want 2", len(rows))
	}
	rows := do(fmt.Sprintf("?process=%d&q=%s", defA, url.QueryEscape("customerType=Business")))
	if len(rows) != 1 {
		t.Fatalf("search scoped to def A matched %d rows, want 1", len(rows))
	}
	if rows[0].ProcessID == "" {
		t.Error("scoped row is not labelled with its definition")
	}
	if rows := do(fmt.Sprintf("?process=%d&q=%s", defB, url.QueryEscape("customerType=Business"))); len(rows) != 1 {
		t.Errorf("search scoped to def B matched %d rows, want 1", len(rows))
	}
	// A key lookup is still a key lookup, but scoping it must not hand back an
	// instance of a different definition.
	all := do("?q=" + url.QueryEscape("customerType=Business"))
	var keyOfB uint64
	for _, r := range all {
		if r.Key != rows[0].Key {
			keyOfB = r.Key
		}
	}
	if keyOfB == 0 {
		t.Fatal("could not identify def B's instance")
	}
	if got := do(fmt.Sprintf("?process=%d&q=%d", defA, keyOfB)); len(got) != 0 {
		t.Errorf("key of another definition under ?process= matched %d rows, want 0", len(got))
	}
	if got := do(fmt.Sprintf("?process=%d&q=%d", defB, keyOfB)); len(got) != 1 {
		t.Errorf("key under its own ?process= matched %d rows, want 1", len(got))
	}
	// A bad process key is refused rather than silently searching everything.
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/instances/search?process=nope&q=x", "", ""); code != http.StatusBadRequest {
		t.Errorf("bad process key = %d, want 400", code)
	}
}

// TestSearchInstancesScopedStopsAtTheCap: a scoped search reads that version's
// indexes, which are newest-first, so it stops walking the moment the cap is met
// rather than reading the rest of the version to throw it away. The rows it keeps
// are the newest — the same ones reading on would have left standing.
func TestSearchInstancesScopedStopsAtTheCap(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", timerWaitBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	const n = 205 // more than maxInstanceSearchResults
	for i := 0; i < n; i++ {
		if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), `{"variables":{"tenant":"acme"}}`, "application/json"); code != http.StatusOK {
			t.Fatalf("create instance %d: status=%d body=%s", i, code, b)
		}
	}

	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/search?process=%d&q=%s", dep.Key, url.QueryEscape("tenant=acme")), "", "")
	if code != http.StatusOK {
		t.Fatalf("search: status=%d body=%s", code, body)
	}
	var rows []searchRow
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 200 {
		t.Fatalf("scoped search returned %d rows, want the 200-row cap", len(rows))
	}
	// Newest first, and the oldest five are the ones left out.
	for i := 1; i < len(rows); i++ {
		if rows[i-1].Key <= rows[i].Key {
			t.Fatalf("rows are not newest-first: %d before %d", rows[i-1].Key, rows[i].Key)
		}
	}
	// Every row still carries the variable that matched.
	for _, r := range rows {
		if len(r.Variables) != 1 || r.Variables[0].Name != "tenant" {
			t.Fatalf("row %d variables = %+v, want the matched tenant", r.Key, r.Variables)
		}
	}
}
