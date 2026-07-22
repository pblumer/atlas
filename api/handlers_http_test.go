package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestDeployEmptyBody rejects an empty deploy body as a client error.
func TestDeployEmptyBody(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", "", "application/xml")
	if code != http.StatusBadRequest || !strings.Contains(string(body), "empty request body") {
		t.Fatalf("status=%d body=%s, want 400 empty-body", code, body)
	}
}

// TestProcessXMLBadKey and not-found cover handleProcessXML's error exits.
func TestProcessXMLErrors(t *testing.T) {
	ts := newTestServer(t)

	if code, body := doReq(t, ts, http.MethodGet, "/api/v1/processes/abc/xml", "", ""); code != http.StatusBadRequest || !strings.Contains(string(body), "invalid definition key") {
		t.Fatalf("bad key: status=%d body=%s, want 400", code, body)
	}
	if code, body := doReq(t, ts, http.MethodGet, "/api/v1/processes/999/xml", "", ""); code != http.StatusNotFound || !strings.Contains(string(body), "no deployment") {
		t.Fatalf("missing key: status=%d body=%s, want 404", code, body)
	}
}

// TestProcessRuntimeErrors covers handleProcessRuntime's bad-key and not-found
// exits, plus a valid definition with no live instances (empty overlay).
func TestProcessRuntimeErrors(t *testing.T) {
	ts := newTestServer(t)

	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/processes/abc/runtime", "", ""); code != http.StatusBadRequest {
		t.Fatalf("bad key status=%d, want 400", code)
	}
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/processes/999/runtime", "", ""); code != http.StatusNotFound {
		t.Fatalf("missing key status=%d, want 404", code)
	}

	// Deploy but do not instantiate: found, zero instances, empty elements.
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/processes/1/runtime", "", "")
	if code != http.StatusOK {
		t.Fatalf("runtime status=%d body=%s", code, body)
	}
	var rt struct {
		Instances int              `json:"instances"`
		Elements  []map[string]any `json:"elements"`
	}
	if err := json.Unmarshal(body, &rt); err != nil {
		t.Fatalf("decode runtime: %v (%s)", err, body)
	}
	if rt.Instances != 0 || len(rt.Elements) != 0 {
		t.Fatalf("runtime = %+v, want zero instances and no elements", rt)
	}
}

// TestRuntimeIgnoresOtherDefinitions deploys two definitions, instantiates both,
// and asks for one's runtime: the scan must skip the other definition's element
// instances (the v.ProcessDefKey != key branch).
func TestRuntimeIgnoresOtherDefinitions(t *testing.T) {
	ts := newTestServer(t)

	for i := 0; i < 2; i++ {
		if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml"); code != http.StatusOK {
			t.Fatalf("deploy %d status=%d body=%s", i, code, body)
		}
	}
	// Instantiate both definitions (keys 1 and 2), so the store holds element
	// instances belonging to two different definitions.
	for _, key := range []string{"1", "2"} {
		if code, body := doReq(t, ts, http.MethodPost, "/api/v1/processes/"+key+"/instances", "{}", "application/json"); code != http.StatusOK {
			t.Fatalf("instance of %s status=%d body=%s", key, code, body)
		}
	}

	code, body := doReq(t, ts, http.MethodGet, "/api/v1/processes/1/runtime", "", "")
	if code != http.StatusOK {
		t.Fatalf("runtime status=%d body=%s", code, body)
	}
	var rt struct {
		Instances int `json:"instances"`
		Elements  []struct {
			ElementID string `json:"elementId"`
			Tokens    int    `json:"tokens"`
		} `json:"elements"`
	}
	if err := json.Unmarshal(body, &rt); err != nil {
		t.Fatalf("decode runtime: %v (%s)", err, body)
	}
	// Only definition 1's single instance and its one token must be reported.
	if rt.Instances != 1 || len(rt.Elements) != 1 || rt.Elements[0].Tokens != 1 {
		t.Fatalf("runtime = %+v, want exactly definition 1's one instance/token", rt)
	}
}

// TestRuntimeFilterByInstance starts two instances of one definition and asks for
// the runtime of a single one via ?instance=<key>: only that instance's token is
// reported (Instances=1), so the live view can isolate one instance on the diagram.
func TestRuntimeFilterByInstance(t *testing.T) {
	ts := newTestServer(t)

	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	for i := 0; i < 2; i++ {
		if code, body := doReq(t, ts, http.MethodPost, "/api/v1/processes/1/instances", "{}", "application/json"); code != http.StatusOK {
			t.Fatalf("instance %d status=%d body=%s", i, code, body)
		}
	}

	// Grab the two live instance keys.
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	if code != http.StatusOK {
		t.Fatalf("instances status=%d body=%s", code, body)
	}
	var insts []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &insts); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, body)
	}
	if len(insts) != 2 {
		t.Fatalf("want 2 instances, got %d (%s)", len(insts), body)
	}

	// Unfiltered: both instances, two tokens on the one service task.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/processes/1/runtime", "", "")
	if code != http.StatusOK {
		t.Fatalf("runtime status=%d body=%s", code, body)
	}
	var all struct {
		Instances int `json:"instances"`
		Tokens    int `json:"tokens"`
	}
	if err := json.Unmarshal(body, &all); err != nil {
		t.Fatalf("decode runtime: %v (%s)", err, body)
	}
	if all.Instances != 2 || all.Tokens != 2 {
		t.Fatalf("unfiltered runtime = %+v, want 2 instances / 2 tokens", all)
	}

	// Filtered to one instance: exactly its single token, reported as one instance.
	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/processes/1/runtime?instance=%d", insts[0].Key), "", "")
	if code != http.StatusOK {
		t.Fatalf("filtered runtime status=%d body=%s", code, body)
	}
	var one struct {
		Instances int `json:"instances"`
		Tokens    int `json:"tokens"`
		Elements  []struct {
			ElementID string `json:"elementId"`
			Tokens    int    `json:"tokens"`
		} `json:"elements"`
	}
	if err := json.Unmarshal(body, &one); err != nil {
		t.Fatalf("decode filtered runtime: %v (%s)", err, body)
	}
	if one.Instances != 1 || one.Tokens != 1 || len(one.Elements) != 1 || one.Elements[0].Tokens != 1 {
		t.Fatalf("filtered runtime = %+v, want exactly one instance's single token", one)
	}

	// A well-formed key that is not an instance of this definition yields nothing.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/processes/1/runtime?instance=999999", "", "")
	if code != http.StatusOK {
		t.Fatalf("unknown-instance runtime status=%d body=%s", code, body)
	}
	if err := json.Unmarshal(body, &one); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if one.Instances != 0 || one.Tokens != 0 {
		t.Fatalf("unknown instance runtime = %+v, want zero", one)
	}

	// A non-numeric instance filter is a client error.
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/processes/1/runtime?instance=abc", "", ""); code != http.StatusBadRequest {
		t.Fatalf("bad instance filter status=%d, want 400", code)
	}
}

// TestCreateInstanceErrors covers handleCreateInstance's bad-key and
// bad-variables-body error exits.
func TestCreateInstanceErrors(t *testing.T) {
	ts := newTestServer(t)

	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/processes/abc/instances", "{}", "application/json"); code != http.StatusBadRequest {
		t.Fatalf("bad key status=%d, want 400", code)
	}

	// Deploy so the key exists, then send a malformed variables body.
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/processes/1/instances", `{not json`, "application/json")
	if code != http.StatusBadRequest || !strings.Contains(string(body), "invalid JSON") {
		t.Fatalf("bad variables: status=%d body=%s, want 400 invalid JSON", code, body)
	}
}

// TestStatsEndpoint covers handleStats over HTTP.
func TestStatsEndpoint(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/stats", "", "")
	if code != http.StatusOK {
		t.Fatalf("stats status=%d body=%s", code, body)
	}
	var stats struct {
		ActiveProcessInstances int `json:"activeProcessInstances"`
		ActiveElementInstances int `json:"activeElementInstances"`
	}
	if err := json.Unmarshal(body, &stats); err != nil {
		t.Fatalf("decode stats: %v (%s)", err, body)
	}
	if stats.ActiveProcessInstances != 0 || stats.ActiveElementInstances != 0 {
		t.Fatalf("fresh server stats = %+v, want zeroes", stats)
	}
}

// TestCreateInstanceWithVariables seeds an instance with variables of every
// scalar kind and confirms they round-trip through the instances list, which
// exercises the variable rendering path end to end.
func TestCreateInstanceWithVariables(t *testing.T) {
	ts := newTestServer(t)

	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}

	vars := `{"variables": {"flag": true, "amount": 100, "label": "abc", "empty": null}}`
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/processes/1/instances", vars, "application/json"); code != http.StatusOK {
		t.Fatalf("create instance status=%d body=%s", code, body)
	}

	code, body := doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	if code != http.StatusOK {
		t.Fatalf("instances status=%d body=%s", code, body)
	}
	var insts []struct {
		Variables []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
			Kind  string `json:"kind"`
		} `json:"variables"`
	}
	if err := json.Unmarshal(body, &insts); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, body)
	}
	if len(insts) != 1 {
		t.Fatalf("got %d instances, want 1", len(insts))
	}
	byName := map[string]string{}
	kinds := map[string]string{}
	for _, v := range insts[0].Variables {
		byName[v.Name] = v.Value
		kinds[v.Name] = v.Kind
	}
	want := []struct{ name, kind, value string }{
		{"flag", "boolean", "true"},
		{"amount", "number", "100"},
		{"label", "string", "abc"},
		{"empty", "null", "null"},
	}
	for _, w := range want {
		if kinds[w.name] != w.kind || byName[w.name] != w.value {
			t.Errorf("variable %q = (kind %q, value %q), want (%q, %q)", w.name, kinds[w.name], byName[w.name], w.kind, w.value)
		}
	}
}
