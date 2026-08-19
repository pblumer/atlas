package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// terminateResult mirrors the terminate endpoint's JSON so tests can assert the
// counts it reports back.
type terminateResult struct {
	Terminated int  `json:"terminated"`
	NotFound   int  `json:"notFound"`
	Remaining  bool `json:"remaining"`
	Stats      struct {
		ActiveProcessInstances int `json:"activeProcessInstances"`
	} `json:"stats"`
}

// startParkedInstances deploys the given BPMN and starts n instances, returning the
// deployment key. The BPMN is expected to park each instance (timer/user task) so it
// stays active and selectable.
func deployAndStart(t *testing.T, ts *httptest.Server, bpmn string, bodies ...string) uint64 {
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
	for i, b := range bodies {
		if code, rb := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), b, "application/json"); code != http.StatusOK {
			t.Fatalf("create instance %d: status=%d body=%s", i, code, rb)
		}
	}
	return dep.Key
}

// activeInstanceKeys returns the keys of the currently active instances.
func activeInstanceKeys(t *testing.T, ts *httptest.Server) []uint64 {
	t.Helper()
	_, body := doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	var insts []struct {
		Key   uint64 `json:"key"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(body, &insts); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, body)
	}
	var keys []uint64
	for _, in := range insts {
		if in.State == "active" {
			keys = append(keys, in.Key)
		}
	}
	return keys
}

func postTerminate(t *testing.T, ts *httptest.Server, body string) (int, terminateResult) {
	t.Helper()
	code, rb := doReq(t, ts, http.MethodPost, "/api/v1/instances/terminate", body, "application/json")
	var res terminateResult
	if code == http.StatusOK {
		if err := json.Unmarshal(rb, &res); err != nil {
			t.Fatalf("decode terminate: %v (%s)", err, rb)
		}
	}
	return code, res
}

// TestTerminateInstancesByKeys terminates an explicit, hand-picked set of instances
// (the operator's "check these three, terminate" surface) and checks the reported
// counts: the selected ones go, the rest stay, and re-terminating already-gone keys
// reports them as notFound rather than terminated.
func TestTerminateInstancesByKeys(t *testing.T) {
	ts := newTestServer(t)
	deployAndStart(t, ts, timerWaitBPMN, "{}", "{}", "{}")
	keys := activeInstanceKeys(t, ts)
	if len(keys) != 3 {
		t.Fatalf("want 3 active instances, got %d", len(keys))
	}

	// Terminate two of the three by key.
	code, res := postTerminate(t, ts, fmt.Sprintf(`{"keys":[%d,%d]}`, keys[0], keys[1]))
	if code != http.StatusOK {
		t.Fatalf("terminate by keys: status=%d", code)
	}
	if res.Terminated != 2 || res.NotFound != 0 || res.Remaining {
		t.Fatalf("terminate result = %+v, want terminated=2 notFound=0 remaining=false", res)
	}
	if res.Stats.ActiveProcessInstances != 1 {
		t.Fatalf("active after terminate = %d, want 1", res.Stats.ActiveProcessInstances)
	}

	// The already-terminated keys plus a bogus key all report as notFound.
	code, res = postTerminate(t, ts, fmt.Sprintf(`{"keys":[%d,%d,999999]}`, keys[0], keys[1]))
	if code != http.StatusOK {
		t.Fatalf("terminate stale keys: status=%d", code)
	}
	if res.Terminated != 0 || res.NotFound != 3 {
		t.Fatalf("stale terminate = %+v, want terminated=0 notFound=3", res)
	}

	// Duplicate keys in one request are collapsed — terminating the last live one twice
	// counts it once.
	code, res = postTerminate(t, ts, fmt.Sprintf(`{"keys":[%d,%d]}`, keys[2], keys[2]))
	if code != http.StatusOK {
		t.Fatalf("terminate dup keys: status=%d", code)
	}
	if res.Terminated != 1 || res.NotFound != 0 {
		t.Fatalf("dup terminate = %+v, want terminated=1 notFound=0", res)
	}
	if res.Stats.ActiveProcessInstances != 0 {
		t.Fatalf("active after all terminated = %d, want 0", res.Stats.ActiveProcessInstances)
	}
}

// TestTerminateInstancesByFilter terminates every active instance of one definition
// whose variables match a query — the "select all N matching, terminate" scope. Only
// the matching instances go; the others stay live.
func TestTerminateInstancesByFilter(t *testing.T) {
	ts := newTestServer(t)
	def := deployAndStart(t, ts, userTaskBPMN,
		`{"variables":{"customerType":"Business"}}`,
		`{"variables":{"customerType":"Consumer"}}`,
		`{"variables":{"customerType":"Business"}}`,
	)
	if got := len(activeInstanceKeys(t, ts)); got != 3 {
		t.Fatalf("want 3 active, got %d", got)
	}

	// Filter mode: terminate the two Business instances by variable content.
	code, res := postTerminate(t, ts, fmt.Sprintf(`{"processDefKey":%d,"q":"customerType=Business"}`, def))
	if code != http.StatusOK {
		t.Fatalf("terminate by filter: status=%d", code)
	}
	if res.Terminated != 2 || res.Remaining {
		t.Fatalf("filter terminate = %+v, want terminated=2 remaining=false", res)
	}
	if res.Stats.ActiveProcessInstances != 1 {
		t.Fatalf("active after filter terminate = %d, want 1 (Consumer)", res.Stats.ActiveProcessInstances)
	}

	// Filter with no query terminates all remaining instances of the definition.
	code, res = postTerminate(t, ts, fmt.Sprintf(`{"processDefKey":%d}`, def))
	if code != http.StatusOK {
		t.Fatalf("terminate all of def: status=%d", code)
	}
	if res.Terminated != 1 || res.Stats.ActiveProcessInstances != 0 {
		t.Fatalf("terminate-all = %+v, want terminated=1 and 0 active", res)
	}
}

// TestTerminateInstancesFilterBatched drains a definition's instances in bounded
// batches via the body limit, mirroring the per-call cap of the bulk-drain endpoint:
// the caller repeats while remaining=true.
func TestTerminateInstancesFilterBatched(t *testing.T) {
	ts := newTestServer(t)
	def := deployAndStart(t, ts, timerWaitBPMN, "{}", "{}", "{}", "{}", "{}")

	total, rounds := 0, 0
	for {
		code, res := postTerminate(t, ts, fmt.Sprintf(`{"processDefKey":%d,"limit":2}`, def))
		if code != http.StatusOK {
			t.Fatalf("round %d: status=%d", rounds, code)
		}
		if res.Terminated > 2 {
			t.Fatalf("round %d terminated=%d, want <= 2 (per-call cap)", rounds, res.Terminated)
		}
		total += res.Terminated
		if !res.Remaining {
			if res.Stats.ActiveProcessInstances != 0 {
				t.Fatalf("drain finished with %d still active", res.Stats.ActiveProcessInstances)
			}
			break
		}
		if rounds++; rounds > 7 {
			t.Fatalf("drain did not converge; total=%d", total)
		}
	}
	if total != 5 {
		t.Fatalf("total terminated=%d, want 5", total)
	}
}

// TestTerminateInstancesErrors covers the request-shape rejections and the missing
// definition case.
func TestTerminateInstancesErrors(t *testing.T) {
	ts := newTestServer(t)
	def := deployAndStart(t, ts, timerWaitBPMN)

	cases := []struct {
		name string
		body string
		want int
	}{
		{"empty body", `{}`, http.StatusBadRequest},
		{"empty keys", `{"keys":[]}`, http.StatusBadRequest},
		{"both keys and def", fmt.Sprintf(`{"keys":[1],"processDefKey":%d}`, def), http.StatusBadRequest},
		{"unknown def", `{"processDefKey":999999}`, http.StatusNotFound},
		{"bad json", `{`, http.StatusBadRequest},
		// limit:0 is indistinguishable from omitted in JSON, so it defaults rather than
		// erroring; a negative limit is the unambiguous rejection.
		{"zero limit defaults", fmt.Sprintf(`{"processDefKey":%d,"limit":0}`, def), http.StatusOK},
		{"negative limit", fmt.Sprintf(`{"processDefKey":%d,"limit":-1}`, def), http.StatusBadRequest},
	}
	for _, c := range cases {
		if code, _ := postTerminate(t, ts, c.body); code != c.want {
			t.Errorf("%s: status=%d, want %d", c.name, code, c.want)
		}
	}
}
