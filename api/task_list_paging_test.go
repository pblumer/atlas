package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// taskRow is the subset of a task-list row these tests assert on.
type taskRow struct {
	Key                uint64 `json:"key"`
	ProcessInstanceKey uint64 `json:"processInstanceKey"`
}

// deployUserTaskDefWithN deploys the one-user-task definition and starts n instances,
// each of which parks on the "review" user task. Returns the definition key.
func deployUserTaskDefWithN(t *testing.T, ts *httptest.Server, n int) uint64 {
	t.Helper()
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
	for i := 0; i < n; i++ {
		if c, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "{}", "application/json"); c != http.StatusOK {
			t.Fatalf("create instance %d: status=%d body=%s", i, c, b)
		}
	}
	return deploy.Key
}

// TestListTasksByProcessInstance proves ?processInstance= resolves an instance's own
// open user tasks directly — so a client (the order-to-cash demo) always finds its
// instance's task even when the global, capped /tasks page (newest-first) has pushed
// it off the front. This is the fix for "no tasks displayed" under a backlog.
func TestListTasksByProcessInstance(t *testing.T) {
	ts := newTestServer(t)
	deployUserTaskDefWithN(t, ts, 3)

	// The full list has all three tasks; capture their instance keys.
	_, body := doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	var all []taskRow
	if err := json.Unmarshal(body, &all); err != nil {
		t.Fatalf("decode tasks: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("full list = %d rows, want 3", len(all))
	}

	// The oldest task (smallest key) is the one a newest-first page cap drops first.
	oldest := all[0]
	for _, r := range all {
		if r.Key < oldest.Key {
			oldest = r
		}
	}

	// A newest-first cap of 1 excludes the oldest instance's task entirely...
	res, err := http.Get(ts.URL + "/api/v1/tasks?limit=1")
	if err != nil {
		t.Fatalf("GET tasks?limit=1: %v", err)
	}
	var page []taskRow
	_ = json.NewDecoder(res.Body).Decode(&page)
	trunc := res.Header.Get("X-Tasks-Truncated")
	res.Body.Close()
	if len(page) != 1 || trunc != "true" {
		t.Fatalf("capped page = %d rows, truncated=%q; want 1 + true", len(page), trunc)
	}
	if page[0].ProcessInstanceKey == oldest.ProcessInstanceKey {
		t.Fatalf("newest-first cap returned the oldest instance's task; ordering is wrong")
	}

	// ...but scoping to that instance still finds it, regardless of the cap.
	_, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/tasks?processInstance=%d", oldest.ProcessInstanceKey), "", "")
	var scoped []taskRow
	if err := json.Unmarshal(body, &scoped); err != nil {
		t.Fatalf("decode scoped: %v", err)
	}
	if len(scoped) != 1 {
		t.Fatalf("scoped list = %d rows, want exactly the instance's 1 task", len(scoped))
	}
	if scoped[0].Key != oldest.Key || scoped[0].ProcessInstanceKey != oldest.ProcessInstanceKey {
		t.Fatalf("scoped task = %+v, want key=%d instance=%d", scoped[0], oldest.Key, oldest.ProcessInstanceKey)
	}

	// An unknown instance is an empty list, not an error; a bad key is a 400.
	code, b := doReq(t, ts, http.MethodGet, "/api/v1/tasks?processInstance=999999", "", "")
	var empty []taskRow
	if code != http.StatusOK || json.Unmarshal(b, &empty) != nil || len(empty) != 0 {
		t.Fatalf("unknown instance: status=%d body=%s, want 200 empty list", code, b)
	}
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/tasks?processInstance=nope", "", ""); code != http.StatusBadRequest {
		t.Fatalf("bad processInstance status=%d, want 400", code)
	}
}

// TestListTasksByProcessInstanceExcludesIncident proves the per-instance path returns
// only OPEN user tasks — a task parked behind an incident (retries exhausted) is off
// the activatable index, so it must not appear, matching the global list's semantics.
func TestListTasksByProcessInstanceExcludesIncident(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", incidentUserTaskBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	_ = json.Unmarshal(body, &deploy)
	if c, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "{}", "application/json"); c != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", c, b)
	}

	_, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	var tasks []taskRow
	if err := json.Unmarshal(body, &tasks); err != nil || len(tasks) != 1 {
		t.Fatalf("want 1 open task, got %d (err=%v)", len(tasks), err)
	}
	inst := tasks[0].ProcessInstanceKey

	// Scoped to the instance while the task is open: one row.
	_, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/tasks?processInstance=%d", inst), "", "")
	var scoped []taskRow
	if err := json.Unmarshal(body, &scoped); err != nil || len(scoped) != 1 {
		t.Fatalf("open scoped list = %d, want 1 (err=%v)", len(scoped), err)
	}

	// Exhaust the job's retries → an incident; the task is no longer open.
	if c, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/fail", tasks[0].Key), `{"retries":0,"message":"boom"}`, "application/json"); c != http.StatusOK {
		t.Fatalf("fail job: status=%d body=%s", c, b)
	}
	_, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/tasks?processInstance=%d", inst), "", "")
	scoped = scoped[:0]
	if err := json.Unmarshal(body, &scoped); err != nil || len(scoped) != 0 {
		t.Fatalf("incident-blocked scoped list = %d, want 0 (err=%v)", len(scoped), err)
	}
}

// twoParallelUserTasksBPMN forks on a parallel gateway into two user tasks, so a
// single instance parks on both at once — used to exercise the per-instance list's
// own page cap.
const twoParallelUserTasksBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="twopar" isExecutable="true">
    <startEvent id="s"/>
    <parallelGateway id="fork"/>
    <userTask id="ua" name="Task A"/>
    <userTask id="ub" name="Task B"/>
    <sequenceFlow id="f0" sourceRef="s" targetRef="fork"/>
    <sequenceFlow id="f1" sourceRef="fork" targetRef="ua"/>
    <sequenceFlow id="f2" sourceRef="fork" targetRef="ub"/>
  </process>
</definitions>`

// userTaskAndTimerBPMN forks into a user task and a timer catch event, so the
// instance parks on both: one active element carries a job (the user task), the
// other is active with no job (the waiting timer). The per-instance scan must skip
// the job-less element and return only the user task.
const userTaskAndTimerBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="mix" isExecutable="true">
    <startEvent id="s"/>
    <parallelGateway id="fork"/>
    <userTask id="ut" name="Human step"/>
    <intermediateCatchEvent id="wait"><timerEventDefinition><timeDuration>PT3600S</timeDuration></timerEventDefinition></intermediateCatchEvent>
    <sequenceFlow id="f0" sourceRef="s" targetRef="fork"/>
    <sequenceFlow id="f1" sourceRef="fork" targetRef="ut"/>
    <sequenceFlow id="f2" sourceRef="fork" targetRef="wait"/>
  </process>
</definitions>`

// TestListTasksForInstanceSkipsJoblessElement proves the per-instance scan returns
// only elements that hold an open user-task job: an instance parked on a user task
// AND a waiting timer (an active element with no job) yields exactly the one task.
func TestListTasksForInstanceSkipsJoblessElement(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", userTaskAndTimerBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	_ = json.Unmarshal(body, &deploy)
	if c, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "{}", "application/json"); c != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", c, b)
	}

	_, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	var all []taskRow
	if err := json.Unmarshal(body, &all); err != nil || len(all) != 1 {
		t.Fatalf("want 1 user task (timer carries no job), got %d (err=%v)", len(all), err)
	}
	inst := all[0].ProcessInstanceKey

	// Scoped to the instance: the job-less timer element is skipped; only the task.
	_, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/tasks?processInstance=%d", inst), "", "")
	var scoped []taskRow
	if err := json.Unmarshal(body, &scoped); err != nil || len(scoped) != 1 || scoped[0].Key != all[0].Key {
		t.Fatalf("scoped list = %+v, want the single user task %d (err=%v)", scoped, all[0].Key, err)
	}
}

// TestListTasksForInstanceCap proves the per-instance list is itself bounded: an
// instance parked on two user tasks, queried with ?limit=1, returns one row and flags
// X-Tasks-Truncated — so even a scoped query can't return an unbounded page.
func TestListTasksForInstanceCap(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", twoParallelUserTasksBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	_ = json.Unmarshal(body, &deploy)
	if c, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "{}", "application/json"); c != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", c, b)
	}

	// Both user tasks are parked on the one instance.
	_, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	var all []taskRow
	if err := json.Unmarshal(body, &all); err != nil || len(all) != 2 {
		t.Fatalf("want 2 parked tasks, got %d (err=%v)", len(all), err)
	}
	inst := all[0].ProcessInstanceKey

	// Scoped and capped at 1 → one row, flagged truncated.
	res, err := http.Get(ts.URL + fmt.Sprintf("/api/v1/tasks?processInstance=%d&limit=1", inst))
	if err != nil {
		t.Fatalf("GET scoped?limit=1: %v", err)
	}
	var page []taskRow
	_ = json.NewDecoder(res.Body).Decode(&page)
	trunc := res.Header.Get("X-Tasks-Truncated")
	res.Body.Close()
	if len(page) != 1 || trunc != "true" {
		t.Fatalf("scoped capped page = %d rows, truncated=%q; want 1 + true", len(page), trunc)
	}

	// Uncapped, the scoped list returns both tasks of the instance.
	_, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/tasks?processInstance=%d", inst), "", "")
	var full []taskRow
	if err := json.Unmarshal(body, &full); err != nil || len(full) != 2 {
		t.Fatalf("scoped full list = %d, want 2 (err=%v)", len(full), err)
	}
}

// TestListTasksNewestFirstPaging proves the list is newest-first and that the
// X-Tasks-Next-Cursor / ?before= cursor pages through every task exactly once, in
// strictly descending key order — the pagination the inbox's "Load older" uses.
func TestListTasksNewestFirstPaging(t *testing.T) {
	ts := newTestServer(t)
	deployUserTaskDefWithN(t, ts, 3)

	var got []uint64
	before := ""
	for pages := 0; pages < 10; pages++ {
		url := ts.URL + "/api/v1/tasks?limit=1"
		if before != "" {
			url += "&before=" + before
		}
		res, err := http.Get(url)
		if err != nil {
			t.Fatalf("GET %s: %v", url, err)
		}
		var page []taskRow
		_ = json.NewDecoder(res.Body).Decode(&page)
		trunc := res.Header.Get("X-Tasks-Truncated")
		cursor := res.Header.Get("X-Tasks-Next-Cursor")
		res.Body.Close()
		if len(page) != 1 {
			t.Fatalf("page = %d rows, want 1", len(page))
		}
		got = append(got, page[0].Key)
		if trunc != "true" {
			// Terminal page: no truncation flag and no cursor.
			if cursor != "" {
				t.Fatalf("terminal page carried a cursor %q", cursor)
			}
			break
		}
		if cursor == "" {
			t.Fatal("truncated page missing X-Tasks-Next-Cursor")
		}
		before = cursor
	}

	if len(got) != 3 {
		t.Fatalf("paged %d tasks, want 3 (%v)", len(got), got)
	}
	for i := 1; i < len(got); i++ {
		if got[i] >= got[i-1] {
			t.Fatalf("keys not strictly descending (newest first): %v", got)
		}
	}

	// A malformed cursor is a 400, not a silent full scan.
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/tasks?before=nope", "", ""); code != http.StatusBadRequest {
		t.Fatalf("bad before status=%d, want 400", code)
	}
}
