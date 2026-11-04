package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// deployVersion deploys a model and answers the key of the definition it registered.
func deployVersion(t *testing.T, ts *httptest.Server, xml string) uint64 {
	t.Helper()
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", xml, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	return dep.Key
}

// startVersionTask starts an instance of a one-user-task definition and answers the key of
// the task it parked on — the newest open task, since earlier instances' tasks may
// still be open beside it.
func startVersionTask(t *testing.T, ts *httptest.Server, defKey uint64) uint64 {
	t.Helper()
	if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", defKey), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}
	_, body := doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	var tasks []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(listRows(t, body), &tasks); err != nil || len(tasks) == 0 {
		t.Fatalf("list tasks: %v (%s)", err, body)
	}
	newest := tasks[0].Key
	for _, tk := range tasks {
		newest = max(newest, tk.Key)
	}
	return newest
}

// TestProcessVersionsCountEveryVersion is the reading behind the live view's version
// picker: for every deployed version of the process a definition belongs to, how many
// of its instances are running, how many have finished and how many tokens are parked
// behind an incident — so an operator can see which old versions still hold work
// without opening each one in turn. Newest first, every version listed (a version
// with nothing in it is a zero, not an absence), and another process's versions never.
func TestProcessVersionsCountEveryVersion(t *testing.T) {
	ts := newTestServer(t)
	v1 := deployVersion(t, ts, incidentUserTaskBPMN)
	v2 := deployVersion(t, ts, strings.Replace(incidentUserTaskBPMN, `name="Review"`, `name="Review v2"`, 1))
	v3 := deployVersion(t, ts, strings.Replace(incidentUserTaskBPMN, `name="Review"`, `name="Review v3"`, 1))
	other := deployVersion(t, ts, strings.Replace(incidentUserTaskBPMN, `id="approval"`, `id="other"`, 1))

	// v1: one instance finished, one running behind an incident.
	done := startVersionTask(t, ts, v1)
	if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/complete", done), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("complete task: status=%d body=%s", code, body)
	}
	stuck := startVersionTask(t, ts, v1)
	if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/fail", stuck),
		`{"retries":0,"message":"boom"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("fail job: status=%d body=%s", code, body)
	}
	// v2: two running. v3: nothing. other: one running, which no version of approval may count.
	startVersionTask(t, ts, v2)
	startVersionTask(t, ts, v2)
	startVersionTask(t, ts, other)

	type version struct {
		Key       uint64 `json:"key"`
		Version   int32  `json:"version"`
		Active    bool   `json:"active"`
		Running   int    `json:"running"`
		Finished  int    `json:"finished"`
		Incidents int    `json:"incidents"`
	}
	type answer struct {
		ProcessID           string    `json:"processId"`
		IncidentCountsExact bool      `json:"incidentCountsExact"`
		Versions            []version `json:"versions"`
	}
	want := []version{
		{Key: v3, Version: 3, Active: true},
		{Key: v2, Version: 2, Active: true, Running: 2},
		{Key: v1, Version: 1, Active: true, Running: 1, Finished: 1, Incidents: 1},
	}
	// Any version of the process answers for all of them: the picker asks from
	// whichever version it is showing.
	for _, key := range []uint64{v1, v3} {
		code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/processes/%d/versions", key), "", "")
		if code != http.StatusOK {
			t.Fatalf("versions of %d: status=%d body=%s", key, code, body)
		}
		var got answer
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decode versions: %v (%s)", err, body)
		}
		if got.ProcessID != "approval" || !got.IncidentCountsExact {
			t.Errorf("versions of %d: processId=%q exact=%v, want approval and exact", key, got.ProcessID, got.IncidentCountsExact)
		}
		if len(got.Versions) != len(want) {
			t.Fatalf("versions of %d: %+v, want %+v", key, got.Versions, want)
		}
		for i := range want {
			if got.Versions[i] != want[i] {
				t.Errorf("versions of %d [%d] = %+v, want %+v", key, i, got.Versions[i], want[i])
			}
		}
	}

	// A deactivated version says so: it still holds its instances, but starts no new
	// ones by itself, which is part of what "where is this process still alive" asks.
	if code, body := doReq(t, ts, http.MethodPut, fmt.Sprintf("/api/v1/processes/%d/active", v2), `{"active":false}`, "application/json"); code != http.StatusOK {
		t.Fatalf("deactivate: status=%d body=%s", code, body)
	}
	_, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/processes/%d/versions", v2), "", "")
	var got answer
	if err := json.Unmarshal(body, &got); err != nil || len(got.Versions) != 3 {
		t.Fatalf("decode versions: %v (%s)", err, body)
	}
	if got.Versions[1].Active || !got.Versions[0].Active {
		t.Errorf("after deactivating v2: %+v, want v2 alone inactive", got.Versions)
	}

	for path, status := range map[string]int{
		"/api/v1/processes/abc/versions": http.StatusBadRequest,
		"/api/v1/processes/999/versions": http.StatusNotFound,
	} {
		if code, body := doReq(t, ts, http.MethodGet, path, "", ""); code != status {
			t.Errorf("GET %s = %d %s, want %d", path, code, body, status)
		}
	}
}
