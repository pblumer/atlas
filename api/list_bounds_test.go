package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestCancelInstancesScopedToDefinition checks the bulk-cancel scan cancels only
// instances of the targeted definition and steps over instances of every other one.
// With a live instance of a *different* definition present, a drain of the target
// reports zero cancelled and leaves the bystander running — the per-definition scope
// the flood drain depends on.
func TestCancelInstancesScopedToDefinition(t *testing.T) {
	ts := newTestServer(t)

	// Two independent deployments of the parking process → two distinct definition keys.
	deploy := func() uint64 {
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
		return dep.Key
	}
	defA := deploy()
	defB := deploy()

	// One live instance of B only.
	if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", defB), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create B instance: status=%d body=%s", code, b)
	}

	// Draining A must skip B's instance and cancel nothing.
	code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/cancel-instances", defA), "", "")
	if code != http.StatusOK {
		t.Fatalf("cancel A status=%d body=%s", code, body)
	}
	var cr struct {
		Canceled  int  `json:"canceled"`
		Remaining bool `json:"remaining"`
	}
	if err := json.Unmarshal(body, &cr); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if cr.Canceled != 0 || cr.Remaining {
		t.Fatalf("cancel A: canceled=%d remaining=%v, want 0/false (B's instance is out of scope)", cr.Canceled, cr.Remaining)
	}

	// B's instance is untouched: still one active instance in the list.
	_, body = doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	var insts []struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(body, &insts); err != nil {
		t.Fatalf("decode instances: %v", err)
	}
	active := 0
	for _, in := range insts {
		if in.State == "active" {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("active instances=%d, want 1 (B untouched)", active)
	}
}

// TestListIncidentsLimit exercises the incidents list's ?limit= handling: a rejected
// non-positive limit, a valid in-range limit, and an over-ceiling limit that is clamped
// rather than refused. The list itself is empty here — the point is the bound parsing,
// which the operator "what's stuck" view relies on to stay bounded under a flood.
func TestListIncidentsLimit(t *testing.T) {
	ts := newTestServer(t)

	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/incidents?limit=0", "", ""); code != http.StatusBadRequest {
		t.Fatalf("incidents limit=0: status=%d, want 400", code)
	}
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/incidents?limit=nope", "", ""); code != http.StatusBadRequest {
		t.Fatalf("incidents limit=nope: status=%d, want 400", code)
	}
	if code, body := doReq(t, ts, http.MethodGet, "/api/v1/incidents?limit=5", "", ""); code != http.StatusOK || !strings.Contains(string(body), `"incidents"`) {
		t.Fatalf("incidents limit=5: status=%d body=%s", code, body)
	}
	// Over the ceiling → clamped to the max, still a clean 200.
	if code, body := doReq(t, ts, http.MethodGet, "/api/v1/incidents?limit=99999", "", ""); code != http.StatusOK {
		t.Fatalf("incidents limit=99999: status=%d body=%s", code, body)
	}
}
