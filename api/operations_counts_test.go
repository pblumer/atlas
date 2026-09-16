package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The numbers the Operations views state, and where each one comes from
// (ADR-0365).
//
// Both cases below share one shape: a list is fetched with a page cap, its rows are
// counted, and the count is rendered as a fact about the population. That agrees with
// the truth until the population outgrows the page — and because every capped list
// here is ordered, what falls off is a contiguous slice rather than a sample, so a
// whole class of subject goes missing together and the count reads zero rather than
// low. Zero is not a floor; it is a claim that nothing is there.

// parkEveryInstance starts n instances of defKey, giving the last `marked` of them a
// variable the search can find, and parks every one. The marking matters: the incident
// listing is ordered by key (oldest first) and an unscoped search walks in the same
// direction, so a fixture where every instance matches would hand back only hits the
// capped incident page already covered — and the case this is about is the instance it
// does not.
func parkEveryInstance(t *testing.T, ts *httptest.Server, defKey uint64, n, marked int) {
	t.Helper()
	for i := 0; i < n; i++ {
		body := "{}"
		if i >= n-marked {
			body = `{"variables":{"tenant":"acme"}}`
		}
		if code, out := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", defKey), body, "application/json"); code != http.StatusOK {
			t.Fatalf("create instance %d: status=%d body=%s", i, code, out)
		}
	}
	for round := 0; round < 5; round++ {
		_, body := doReq(t, ts, http.MethodGet, "/api/v1/tasks?limit=5000", "", "")
		var tasks []struct {
			Key uint64 `json:"key"`
		}
		_ = json.Unmarshal(listRows(t, body), &tasks)
		if len(tasks) == 0 {
			return
		}
		for _, task := range tasks {
			if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/fail", task.Key),
				`{"retries":0,"message":"mockup task simulated failure"}`, "application/json"); code != http.StatusOK {
				t.Fatalf("fail job: status=%d body=%s", code, body)
			}
		}
	}
	t.Fatalf("could not park all %d instances", n)
}

// TestSearchHitSaysWhetherItIsStuck. A running instance parked behind an incident has
// to say so on the row, because "active" beside it is what an operator reads as
// healthy — and that flag used to come from a 5 000-row page of the server's whole
// incident list, bucketed by instance in the browser. An instance past the page
// carried no flag and rendered as plainly active.
func TestSearchHitSaysWhetherItIsStuck(t *testing.T) {
	ts := newTestServer(t)
	defKey := deployTaskProcess(t, ts, "flooded")
	const flood = 5200 // > the incident listing's 5000-row ceiling
	const marked = 50  // the newest ones, the ones the incident page cannot reach
	parkEveryInstance(t, ts, defKey, flood, marked)

	// The page the console used to count. It is short of the population, and it says so
	// — the fact this test exists to outlive.
	_, listBody := doReq(t, ts, http.MethodGet, "/api/v1/incidents", "", "")
	facts := decodePage(t, listBody)
	var listed []struct {
		ProcessInstanceKey uint64 `json:"processInstanceKey"`
	}
	if err := json.Unmarshal(facts.Items, &listed); err != nil {
		t.Fatalf("decode incidents: %v", err)
	}
	if len(listed) >= flood || !facts.Truncated {
		t.Fatalf("expected a capped incident page under a flood of %d; got %d rows, truncated=%v",
			flood, len(listed), facts.Truncated)
	}
	inThePage := map[uint64]bool{}
	for _, r := range listed {
		inThePage[r.ProcessInstanceKey] = true
	}

	// Every search hit now carries its own count, so it is right for the instances the
	// page never reached as much as for the ones it did.
	_, searchBody := doReq(t, ts, http.MethodGet, "/api/v1/instances/search?q=acme", "", "")
	var hits []struct {
		Key       uint64 `json:"key"`
		State     string `json:"state"`
		Incidents *int   `json:"incidents"`
	}
	if err := json.Unmarshal(listRows(t, searchBody), &hits); err != nil {
		t.Fatalf("decode search: %v (%s)", err, truncateForLog(searchBody))
	}
	if len(hits) == 0 {
		t.Fatalf("the search matched nothing; the fixture cannot show anything")
	}
	beyondThePage := 0
	for _, h := range hits {
		if h.State != "active" {
			continue
		}
		if h.Incidents == nil {
			t.Fatalf("instance %d: no incident count on the row, so the console cannot tell stuck from healthy", h.Key)
		}
		if *h.Incidents != 1 {
			t.Errorf("instance %d: incidents = %d, want 1 — every instance in this fixture is parked", h.Key, *h.Incidents)
		}
		if !inThePage[h.Key] {
			beyondThePage++
		}
	}
	t.Logf("%d of %d search hits lie beyond the incident page and are flagged anyway", beyondThePage, len(hits))
	if beyondThePage == 0 {
		t.Errorf("no search hit landed beyond the capped incident page, so the case this guards went untested")
	}
}

// TestHealthySearchHitSaysZeroRatherThanNothing. The other half: a count has to be
// able to say "none". A row carrying no count at all would put the console back where
// it started, guessing from the absence of a flag.
func TestHealthySearchHitSaysZeroRatherThanNothing(t *testing.T) {
	ts := newTestServer(t)
	defKey := deployTaskProcess(t, ts, "quiet")
	if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", defKey), `{"variables":{"tenant":"acme"}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}
	_, body := doReq(t, ts, http.MethodGet, "/api/v1/instances/search?q=acme", "", "")
	var hits []struct {
		Key       uint64 `json:"key"`
		State     string `json:"state"`
		Incidents *int   `json:"incidents"`
	}
	if err := json.Unmarshal(listRows(t, body), &hits); err != nil {
		t.Fatalf("decode search: %v (%s)", err, truncateForLog(body))
	}
	if len(hits) == 0 {
		t.Fatalf("the search matched nothing; the fixture proves nothing")
	}
	for _, h := range hits {
		if h.State != "active" {
			continue
		}
		if h.Incidents == nil {
			t.Errorf("instance %d: a running row carries no incident count", h.Key)
			continue
		}
		if *h.Incidents != 0 {
			t.Errorf("instance %d: incidents = %d on an engine holding none", h.Key, *h.Incidents)
		}
	}
}

// TestBuiltinFolderCountsTheInboxNotThePage. The four fixed folders are counted by the
// server over the whole open-task population. Counted in the browser off the
// newest-first page instead, a task claimed by somebody and sitting past that page left
// their "Assigned to me" badge reading zero while the task was in their inbox.
func TestBuiltinFolderCountsTheInboxNotThePage(t *testing.T) {
	ts := newTestServer(t)
	defKey := deployTaskProcess(t, ts, "inbox")
	const waiting = 700 // > maxTaskListDefault (500)
	for i := 0; i < waiting; i++ {
		if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", defKey), "{}", "application/json"); code != http.StatusOK {
			t.Fatalf("create instance %d: status=%d body=%s", i, code, body)
		}
	}

	// Claim the oldest task — the one certain to fall outside a newest-first page.
	_, body := doReq(t, ts, http.MethodGet, "/api/v1/tasks?limit=5000", "", "")
	var every []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(listRows(t, body), &every); err != nil || len(every) != waiting {
		t.Fatalf("expected %d waiting tasks, got %d (%v)", waiting, len(every), err)
	}
	oldest := every[len(every)-1].Key
	if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/claim", oldest), `{"assignee":"anna"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("claim: status=%d body=%s", code, body)
	}

	// The page the badges used to be counted from does not contain that task.
	_, pageBody := doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	taskPage := decodePage(t, pageBody)
	var page []struct {
		Key      uint64 `json:"key"`
		Assignee string `json:"assignee"`
	}
	_ = json.Unmarshal(taskPage.Items, &page)
	if !taskPage.Truncated {
		t.Fatalf("the default task page was not capped; the fixture proves nothing")
	}
	for _, r := range page {
		if r.Key == oldest {
			t.Fatalf("the claimed task is inside the page, so the hard case went untested")
		}
	}

	// The badges themselves.
	code, countsBody := doReq(t, ts, http.MethodGet, "/api/v1/task-folders/counts?me=anna", "", "")
	if code != http.StatusOK {
		t.Fatalf("counts: status=%d body=%s", code, countsBody)
	}
	var counts struct {
		Builtin   map[string]int `json:"builtin"`
		Total     int            `json:"total"`
		Truncated bool           `json:"truncated"`
	}
	if err := json.Unmarshal(countsBody, &counts); err != nil {
		t.Fatalf("decode counts: %v (%s)", err, countsBody)
	}
	switch {
	case counts.Builtin["mine"] != 1:
		t.Errorf("\"Assigned to me\" = %d, want 1 — the task is anna's and outside the page", counts.Builtin["mine"])
	case counts.Builtin["all"] != waiting:
		t.Errorf("\"All tasks\" = %d, want %d", counts.Builtin["all"], waiting)
	case counts.Builtin["unassigned"] != waiting-1:
		t.Errorf("\"Unassigned\" = %d, want %d", counts.Builtin["unassigned"], waiting-1)
	case counts.Truncated:
		t.Errorf("the counting scan reported a floor at %d open tasks, well inside its budget", waiting)
	}
}

// TestFixedFolderCountsAreServedWithoutSavedFolders. The counting scan used to be
// skipped for a viewer who had saved none, which is exactly the viewer whose badges
// were then counted from a page.
func TestFixedFolderCountsAreServedWithoutSavedFolders(t *testing.T) {
	ts := newTestServer(t)
	defKey := deployTaskProcess(t, ts, "inbox")
	for i := 0; i < 3; i++ {
		if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", defKey), "{}", "application/json"); code != http.StatusOK {
			t.Fatalf("create instance: status=%d body=%s", code, body)
		}
	}
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/task-folders/counts", "", "")
	if code != http.StatusOK {
		t.Fatalf("counts: status=%d body=%s", code, body)
	}
	var counts struct {
		Builtin map[string]int `json:"builtin"`
		Folders map[string]int `json:"folders"`
	}
	if err := json.Unmarshal(body, &counts); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(counts.Folders) != 0 {
		t.Errorf("saved folders = %v on a server with none", counts.Folders)
	}
	// Every fixed folder answers, zero included: an absent entry reads as "not counted"
	// and the console draws that as an em dash rather than as a number.
	for _, id := range []string{"all", "mine", "unassigned", "group"} {
		if _, ok := counts.Builtin[id]; !ok {
			t.Errorf("fixed folder %q went uncounted", id)
		}
	}
	if counts.Builtin["all"] != 3 {
		t.Errorf("\"All tasks\" = %d, want 3", counts.Builtin["all"])
	}
	if counts.Builtin["mine"] != 0 {
		t.Errorf("\"Assigned to me\" = %d with no viewer identity, want 0", counts.Builtin["mine"])
	}
}

func truncateForLog(b []byte) string {
	if len(b) > 300 {
		return string(b[:300]) + "…"
	}
	return string(b)
}
