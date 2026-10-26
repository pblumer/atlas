package api_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestRuntimeCountsCarryNoIncidentDetails is the counts-only form of the overlay
// (ADR-0493): what a system that shows a process elsewhere reads, and all a
// `diagrams` token reaches. It answers the same numbers as the overlay, field for
// field, and nothing behind them — not an incident's message, which can carry
// business data, nor the instance, job or Worker it names. An instance cannot be
// isolated on it either: a refusal, not a quietly aggregated answer.
func TestRuntimeCountsCarryNoIncidentDetails(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", incidentUserTaskBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	if code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}
	_, body = doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	var instances []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(listRows(t, body), &instances); err != nil || len(instances) != 1 {
		t.Fatalf("list instances: %v (%s)", err, body)
	}
	_, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	var tasks []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(listRows(t, body), &tasks); err != nil || len(tasks) != 1 {
		t.Fatalf("list tasks: %v (%s)", err, body)
	}
	const secret = "customer Paul Muster, IBAN CH93 0076 2011 6238 5295 7"
	if code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/fail", tasks[0].Key),
		`{"retries":0,"message":"`+secret+`"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("fail job: status=%d body=%s", code, body)
	}

	code, overlay := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/processes/%d/runtime", dep.Key), "", "")
	if code != http.StatusOK || !strings.Contains(string(overlay), secret) {
		t.Fatalf("the overlay should carry the incident's message: status=%d body=%s", code, overlay)
	}
	code, counts := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/processes/%d/runtime/counts", dep.Key), "", "")
	if code != http.StatusOK {
		t.Fatalf("counts: status=%d body=%s", code, counts)
	}
	for _, leak := range []string{secret, strconv.FormatUint(instances[0].Key, 10), strconv.FormatUint(tasks[0].Key, 10)} {
		if strings.Contains(string(counts), leak) {
			t.Errorf("the counts carry %q: %s", leak, counts)
		}
	}

	// The answer is an allowlist of fields: one added to the overlay later reaches a
	// diagrams token only when somebody decides it should, here.
	var got, whole map[string]any
	if err := json.Unmarshal(counts, &got); err != nil {
		t.Fatalf("decode counts: %v (%s)", err, counts)
	}
	if err := json.Unmarshal(overlay, &whole); err != nil {
		t.Fatalf("decode overlay: %v (%s)", err, overlay)
	}
	want := []string{"elements", "finished", "incidentCountsExact", "incidentTotal", "instances", "tokens"}
	if keys := slices.Sorted(maps.Keys(got)); !slices.Equal(keys, want) {
		t.Errorf("counts answer %v, want exactly %v", keys, want)
	}
	for _, k := range want {
		if !reflect.DeepEqual(got[k], whole[k]) {
			t.Errorf("%s: counts %v, overlay %v — the two must say the same", k, got[k], whole[k])
		}
	}
	if got["incidentTotal"] != float64(1) {
		t.Errorf("incidentTotal = %v, want the parked token counted", got["incidentTotal"])
	}
	for _, e := range got["elements"].([]any) {
		el := e.(map[string]any)
		for k := range el {
			if !slices.Contains([]string{"elementId", "type", "tokens", "visits", "incidents", "terminated"}, k) {
				t.Errorf("an element of the counts carries %q: %v", k, el)
			}
		}
	}

	for path, status := range map[string]int{
		fmt.Sprintf("/api/v1/processes/%d/runtime/counts?instance=%d", dep.Key, instances[0].Key): http.StatusBadRequest,
		fmt.Sprintf("/api/v1/processes/%d/runtime/counts?instance=", dep.Key):                     http.StatusBadRequest,
		"/api/v1/processes/abc/runtime/counts":                                                    http.StatusBadRequest,
		"/api/v1/processes/999/runtime/counts":                                                    http.StatusNotFound,
	} {
		if code, body := doReq(t, ts, http.MethodGet, path, "", ""); code != status || strings.Contains(string(body), secret) {
			t.Errorf("GET %s = %d %s, want %d", path, code, body, status)
		}
	}
}
