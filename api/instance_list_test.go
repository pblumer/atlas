package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// listPage issues a GET against the instances list and returns the decoded rows
// with the pagination headers, which is where the newest-first cursor rides.
func listPage(t *testing.T, ts *httptest.Server, query string) ([]listRow, http.Header) {
	t.Helper()
	res, err := http.Get(ts.URL + "/api/v1/instances" + query)
	if err != nil {
		t.Fatalf("GET instances%s: %v", query, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET instances%s: status=%d", query, res.StatusCode)
	}
	var rows []listRow
	if err := json.NewDecoder(res.Body).Decode(&rows); err != nil {
		t.Fatalf("decode instances%s: %v", query, err)
	}
	return rows, res.Header
}

type listRow struct {
	Key           uint64 `json:"key"`
	ProcessDefKey uint64 `json:"processDefKey"`
	State         string `json:"state"`
	CompletedAt   int64  `json:"completedAt"`
}

// TestListInstancesByProcessIsScopedAndNewestFirst: narrowing to one definition
// yields only that definition's instances, newest first — the order a paged list
// has to have for the first page to be the useful one.
func TestListInstancesByProcessIsScopedAndNewestFirst(t *testing.T) {
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
	defA := deploy(timerWaitBPMN)
	defB := deploy(userTaskBPMN)
	start := func(def uint64, n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", def), "{}", "application/json"); code != http.StatusOK {
				t.Fatalf("create instance: status=%d body=%s", code, b)
			}
		}
	}
	start(defA, 3)
	start(defB, 2)

	rows, _ := listPage(t, ts, fmt.Sprintf("?process=%d", defA))
	if len(rows) != 3 {
		t.Fatalf("def A page = %d rows, want 3", len(rows))
	}
	for _, r := range rows {
		if r.ProcessDefKey != defA {
			t.Fatalf("row %d belongs to def %d, want %d", r.Key, r.ProcessDefKey, defA)
		}
	}
	for i := 1; i < len(rows); i++ {
		if rows[i-1].Key <= rows[i].Key {
			t.Errorf("rows are not newest-first: %d before %d", rows[i-1].Key, rows[i].Key)
		}
	}
	if rows, _ := listPage(t, ts, fmt.Sprintf("?process=%d", defB)); len(rows) != 2 {
		t.Errorf("def B page = %d rows, want 2", len(rows))
	}
}

// TestListInstancesActiveStatePaging walks a definition's live instances through
// the cursor, which is what keeps the cost of the first page independent of how
// many instances the definition has.
func TestListInstancesActiveStatePaging(t *testing.T) {
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
	const n = 5
	for i := 0; i < n; i++ {
		if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "{}", "application/json"); code != http.StatusOK {
			t.Fatalf("create instance %d: status=%d body=%s", i, code, b)
		}
	}

	seen := map[uint64]bool{}
	cursor, pages := "", 0
	for {
		q := fmt.Sprintf("?process=%d&state=active&limit=2", dep.Key)
		if cursor != "" {
			q += "&before=" + cursor
		}
		rows, hdr := listPage(t, ts, q)
		pages++
		for _, r := range rows {
			if r.State != "active" {
				t.Errorf("row %d state = %q, want active", r.Key, r.State)
			}
			if seen[r.Key] {
				t.Fatalf("instance %d appeared on two pages", r.Key)
			}
			seen[r.Key] = true
		}
		if hdr.Get("X-Instances-Truncated") != "true" {
			if len(rows) > 2 {
				t.Fatalf("uncapped page = %d rows, want at most 2", len(rows))
			}
			break
		}
		cursor = hdr.Get("X-Instances-Next-Cursor")
		if cursor == "" {
			t.Fatal("a truncated page carried no cursor")
		}
		if pages > n+2 {
			t.Fatal("paging did not terminate")
		}
	}
	if len(seen) != n {
		t.Errorf("paged over %d instances, want %d", len(seen), n)
	}
}

// TestListInstancesFinishedStatePaging is the history half: finished instances
// page most-recently-completed first, through a cursor that carries the completion
// time as well as the key — because an instance started first can finish last.
func TestListInstancesFinishedStatePaging(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", userTaskBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	const n = 4
	for i := 0; i < n; i++ {
		if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "{}", "application/json"); code != http.StatusOK {
			t.Fatalf("create instance %d: status=%d body=%s", i, code, b)
		}
	}
	// Complete the tasks in reverse order of creation, so completion order is not
	// key order and the cursor has to carry both.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: status=%d body=%s", code, body)
	}
	var tasks []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &tasks); err != nil || len(tasks) != n {
		t.Fatalf("expected %d tasks, got %v (%s)", n, err, body)
	}
	for _, task := range tasks {
		if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/complete", task.Key), "{}", "application/json"); code != http.StatusOK {
			t.Fatalf("complete task %d: status=%d body=%s", task.Key, code, b)
		}
	}

	seen := map[uint64]bool{}
	var lastCompletedAt int64
	cursor, pages := "", 0
	for {
		q := fmt.Sprintf("?process=%d&state=finished&limit=2", dep.Key)
		if cursor != "" {
			q += "&before=" + cursor
		}
		rows, hdr := listPage(t, ts, q)
		pages++
		for _, r := range rows {
			if r.State == "active" {
				t.Errorf("row %d state = %q, want a finished state", r.Key, r.State)
			}
			if seen[r.Key] {
				t.Fatalf("instance %d appeared on two pages", r.Key)
			}
			seen[r.Key] = true
			if lastCompletedAt != 0 && r.CompletedAt > lastCompletedAt {
				t.Errorf("completion order broke across the cursor: %d after %d", r.CompletedAt, lastCompletedAt)
			}
			lastCompletedAt = r.CompletedAt
		}
		if hdr.Get("X-Instances-Truncated") != "true" {
			break
		}
		cursor = hdr.Get("X-Instances-Next-Cursor")
		if cursor == "" {
			t.Fatal("a truncated page carried no cursor")
		}
		if pages > n+2 {
			t.Fatal("paging did not terminate")
		}
	}
	if len(seen) != n {
		t.Errorf("paged over %d finished instances, want %d", len(seen), n)
	}

	// Unscoped, the finished half has no index to read in completion order, so it is
	// the capped family scan sorted afterwards. The order it returns must still be
	// most-recently-finished first — that is what the sort is for.
	rows, _ := listPage(t, ts, "?state=finished")
	if len(rows) != n {
		t.Fatalf("unscoped finished = %d rows, want %d", len(rows), n)
	}
	for i := 1; i < len(rows); i++ {
		if rows[i-1].CompletedAt < rows[i].CompletedAt {
			t.Errorf("unscoped finished is not newest-completion-first: %d before %d", rows[i-1].CompletedAt, rows[i].CompletedAt)
		}
	}
}

// TestListInstancesQueryContract pins what the listing accepts and what it
// refuses. A cursor addresses a position in one definition's index, so it is
// refused without that definition rather than quietly ignored — silently dropping
// a paging parameter is how a client loops over the same page forever.
func TestListInstancesQueryContract(t *testing.T) {
	ts := newTestServer(t)
	for _, q := range []string{
		"?before=123",                // no state: the two halves order differently
		"?state=active&before=1",     // no process: no index to address
		"?state=finished&before=1.2", // likewise
		"?process=1&state=bogus",     // not a half
		"?process=1&state=active&before=nope",
		"?process=1&state=finished&before=nope",
		"?process=1&state=finished&before=1",   // missing the key half of the pair
		"?process=1&state=finished&before=x.1", // unparseable completion time
		"?process=1&state=finished&before=1.x", // unparseable key
		"?process=nope",
		"?limit=0",
		"?limit=nope",
	} {
		code, body := doReq(t, ts, http.MethodGet, "/api/v1/instances"+q, "", "")
		if code != http.StatusBadRequest {
			t.Errorf("GET instances%s = %d, want 400 (%s)", q, code, body)
		}
	}

	// A single half unscoped is served (capped, unpaged) rather than refused: it is
	// the same family scan the unscoped listing already does, narrowed. "all" and
	// "completed" are the spellings callers already wrote back when the parameter
	// was ignored, and they keep working.
	for _, q := range []string{"?state=active", "?state=finished", "?state=completed", "?state=all"} {
		code, body := doReq(t, ts, http.MethodGet, "/api/v1/instances"+q, "", "")
		if code != http.StatusOK {
			t.Errorf("GET instances%s = %d, want 200 (%s)", q, code, body)
		}
	}
}

// TestListInstancesStateFiltersHalves: asking for one half returns that half and
// nothing else — including under "completed", the spelling callers already used
// back when the parameter was ignored.
func TestListInstancesStateFiltersHalves(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", userTaskBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	for i := 0; i < 2; i++ {
		if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "{}", "application/json"); code != http.StatusOK {
			t.Fatalf("create instance %d: status=%d body=%s", i, code, b)
		}
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: status=%d body=%s", code, body)
	}
	var tasks []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &tasks); err != nil || len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %v (%s)", err, body)
	}
	if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/complete", tasks[0].Key), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("complete task: status=%d body=%s", code, b)
	}

	for _, tc := range []struct {
		query  string
		want   int
		active bool
	}{
		{"?state=active", 1, true},
		{"?state=finished", 1, false},
		{"?state=completed", 1, false},
		{"?state=all", 2, true}, // both halves, running first — the default

		{fmt.Sprintf("?process=%d&state=active", dep.Key), 1, true},
		{fmt.Sprintf("?process=%d&state=finished", dep.Key), 1, false},
	} {
		rows, _ := listPage(t, ts, tc.query)
		if len(rows) != tc.want {
			t.Errorf("GET instances%s = %d rows, want %d", tc.query, len(rows), tc.want)
			continue
		}
		if isActive := rows[0].State == "active"; isActive != tc.active {
			t.Errorf("GET instances%s returned state %q, want active=%v", tc.query, rows[0].State, tc.active)
		}
	}
}

// secondWaiterBPMN parks like timerWaitBPMN but under a second process id, so a test
// can hold live instances of two definitions at once.
const secondWaiterBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="waiter-two" name="Waiter Two" isExecutable="true">
    <startEvent id="s"/>
    <intermediateCatchEvent id="w"><timerEventDefinition><timeDuration>PT3600S</timeDuration></timerEventDefinition></intermediateCatchEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="w"/>
    <sequenceFlow id="f2" sourceRef="w" targetRef="e"/>
  </process>
</definitions>`

// TestANewInstanceIsOffTheCappedListingButOnItsOwnDefinitionsPage pins the contract
// the hosted apps under api/web depend on to find the instance they just started.
//
// The unscoped listing is a page, not the set. It is capped, and its active half is
// scanned in ascending instance-key order — oldest first — so the newest instance is
// the first row the cap drops. A client that starts an instance and then looks for it
// on that page therefore finds nothing on any engine holding more active instances
// than the cap, and reports an instance that is running perfectly well as missing.
//
// That is a production failure rather than a hypothesis: reisebuchung-kunde.html
// located its instance by diffing the unscoped listing, and stopped working the day a
// load test left tens of thousands of active instances in front of it — without a
// deploy, and with the page's own code unchanged for weeks.
//
// Two reads answer the question without a scan, and this pins both: the listing
// scoped with ?process=, which reads one definition's own index newest-first, and
// /instances/search?q=<key>, which is a point read of one instance.
//
// The cap is driven with ?limit= rather than by creating maxInstanceListDefault
// instances. The ordering is the property under test; how many rows it takes to reach
// the cap is not, and a thousand instances would buy nothing but a slow test.
func TestANewInstanceIsOffTheCappedListingButOnItsOwnDefinitionsPage(t *testing.T) {
	ts := newTestServer(t)

	deploy := func(bpmn string) uint64 {
		t.Helper()
		code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", bpmn, "application/xml")
		if code != http.StatusOK {
			t.Fatalf("deploy status=%d body=%s", code, body)
		}
		var dep struct {
			Key uint64 `json:"key"`
		}
		if err := json.Unmarshal(body, &dep); err != nil {
			t.Fatalf("decode deploy: %v", err)
		}
		return dep.Key
	}
	start := func(defKey uint64) {
		t.Helper()
		path := fmt.Sprintf("/api/v1/processes/%d/instances", defKey)
		if code, b := doReq(t, ts, http.MethodPost, path, "{}", "application/json"); code != http.StatusOK {
			t.Fatalf("create instance of %d: status=%d body=%s", defKey, code, b)
		}
	}
	list := func(query string) []instanceRow {
		t.Helper()
		_, body := doReq(t, ts, http.MethodGet, "/api/v1/instances"+query, "", "")
		var rows []instanceRow
		if err := json.Unmarshal(body, &rows); err != nil {
			t.Fatalf("decode /instances%s: %v (%s)", query, err, body)
		}
		return rows
	}

	// Three instances of one definition stand in for the engine's existing population,
	// then one instance of another is the one a client just started. It is the newest,
	// so it carries the highest key.
	noise, mine := deploy(timerWaitBPMN), deploy(secondWaiterBPMN)
	for i := 0; i < 3; i++ {
		start(noise)
	}
	start(mine)

	// Scoped to its definition, the new instance is simply there — this is the read a
	// client should be making.
	scoped := list(fmt.Sprintf("?process=%d", mine))
	if len(scoped) != 1 {
		t.Fatalf("?process=%d returned %d rows, want exactly the one instance started", mine, len(scoped))
	}
	fresh := scoped[0]
	if fresh.ProcessDefKey != mine || fresh.State != "active" {
		t.Fatalf("scoped row = %+v, want defKey %d in state active", fresh, mine)
	}

	// Unscoped and capped, it is not — the page is filled by the older instances of the
	// other definition, and the caller is given no row for the instance it just made.
	res, err := http.Get(ts.URL + "/api/v1/instances?limit=3")
	if err != nil {
		t.Fatalf("GET instances?limit=3: %v", err)
	}
	var page []instanceRow
	if err := json.NewDecoder(res.Body).Decode(&page); err != nil {
		res.Body.Close()
		t.Fatalf("decode capped page: %v", err)
	}
	truncated := res.Header.Get("X-Instances-Truncated")
	res.Body.Close()
	if len(page) != 3 {
		t.Fatalf("capped page = %d rows, want 3", len(page))
	}
	if truncated != "true" {
		t.Fatalf("truncation header = %q, want true — a client cannot even tell the page is partial", truncated)
	}
	for _, r := range page {
		if r.Key == fresh.Key {
			t.Fatalf("the capped page still carries the newest instance %d; the active half is "+
				"supposed to be scanned oldest-key-first, and this test no longer reproduces "+
				"the failure it exists for", fresh.Key)
		}
	}

	// And the point read answers for one key without reference to any page at all.
	_, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/search?q=%d", fresh.Key), "", "")
	var found []instanceRow
	if err := json.Unmarshal(body, &found); err != nil {
		t.Fatalf("decode search: %v (%s)", err, body)
	}
	if len(found) != 1 || found[0].Key != fresh.Key || found[0].State != "active" {
		t.Fatalf("search?q=%d = %+v, want the one live instance", fresh.Key, found)
	}
}

// instanceRow is the part of an instance listing row these assertions read.
type instanceRow struct {
	Key           uint64 `json:"key"`
	ProcessDefKey uint64 `json:"processDefKey"`
	State         string `json:"state"`
}
