package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// deactivateMsgStartBPMN: a message start event named "orderEvent" begins an
// instance that parks at a keyless "never" catch, so a started instance stays
// active and countable.
const deactivateMsgStartBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <message id="Mstart" name="orderEvent"/>
  <message id="Mnever" name="never"/>
  <process id="onOrder" isExecutable="true">
    <startEvent id="s"><messageEventDefinition messageRef="Mstart"/></startEvent>
    <intermediateCatchEvent id="park"><messageEventDefinition messageRef="Mnever"/></intermediateCatchEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="park"/>
    <sequenceFlow id="f2" sourceRef="park" targetRef="e"/>
  </process>
</definitions>`

// listActive returns the "active" flag the process listing reports for key.
func listActive(t *testing.T, ts *httptest.Server, key uint64) bool {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/processes", "", "")
	if code != http.StatusOK {
		t.Fatalf("list processes status=%d body=%s", code, body)
	}
	var procs []struct {
		Key    uint64 `json:"key"`
		Active bool   `json:"active"`
	}
	if err := json.Unmarshal(body, &procs); err != nil {
		t.Fatalf("decode processes: %v (%s)", err, body)
	}
	for _, p := range procs {
		if p.Key == key {
			return p.Active
		}
	}
	t.Fatalf("no process with key %d in listing %s", key, body)
	return false
}

// countInstances returns how many process instances the instances list reports.
func countInstances(t *testing.T, ts *httptest.Server) int {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	if code != http.StatusOK {
		t.Fatalf("list instances status=%d body=%s", code, body)
	}
	var insts []json.RawMessage
	if err := json.Unmarshal(body, &insts); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, body)
	}
	return len(insts)
}

// TestSetProcessActiveListingAndPersistence proves the toggle endpoint flips the
// listing's active flag, is idempotent, and that the deactivation survives a
// restart over the same data dir (ADR-0119).
func TestSetProcessActiveListingAndPersistence(t *testing.T) {
	dir := t.TempDir()
	first := boot(t, dir)

	if code, body := doReq(t, first.ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	// A fresh deployment is active by default.
	if !listActive(t, first.ts, 1) {
		t.Fatal("fresh deployment listed inactive, want active")
	}

	// Deactivate.
	if code, body := doReq(t, first.ts, http.MethodPut, "/api/v1/processes/1/active", `{"active":false}`, "application/json"); code != http.StatusOK {
		t.Fatalf("deactivate status=%d body=%s", code, body)
	}
	if listActive(t, first.ts, 1) {
		t.Fatal("after deactivate: listed active, want inactive")
	}
	// Idempotent: deactivating again is still 200 and still inactive.
	if code, body := doReq(t, first.ts, http.MethodPut, "/api/v1/processes/1/active", `{"active":false}`, "application/json"); code != http.StatusOK {
		t.Fatalf("re-deactivate status=%d body=%s", code, body)
	}
	if listActive(t, first.ts, 1) {
		t.Fatal("after re-deactivate: listed active, want inactive")
	}

	first.shutdown()

	// Restart over the same dir: the deactivation is restored from the sidecar.
	second := boot(t, dir)
	defer second.shutdown()
	if listActive(t, second.ts, 1) {
		t.Fatal("after restart: listed active, want inactive (deactivation must persist)")
	}

	// Reactivate and confirm it sticks.
	if code, body := doReq(t, second.ts, http.MethodPut, "/api/v1/processes/1/active", `{"active":true}`, "application/json"); code != http.StatusOK {
		t.Fatalf("reactivate status=%d body=%s", code, body)
	}
	if !listActive(t, second.ts, 1) {
		t.Fatal("after reactivate: listed inactive, want active")
	}
}

// TestSetProcessActiveErrors covers the handler's bad-key, not-found, and bad-body
// exits.
func TestSetProcessActiveErrors(t *testing.T) {
	ts := newTestServer(t)
	if code, _ := doReq(t, ts, http.MethodPut, "/api/v1/processes/abc/active", `{"active":false}`, "application/json"); code != http.StatusBadRequest {
		t.Fatalf("bad key status=%d, want 400", code)
	}
	if code, _ := doReq(t, ts, http.MethodPut, "/api/v1/processes/999/active", `{"active":false}`, "application/json"); code != http.StatusNotFound {
		t.Fatalf("missing key status=%d, want 404", code)
	}
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	if code, _ := doReq(t, ts, http.MethodPut, "/api/v1/processes/1/active", `not json`, "application/json"); code != http.StatusBadRequest {
		t.Fatalf("bad body status=%d, want 400", code)
	}
}

// TestSetProcessActiveSidecarCorrupt covers the handler's read-failure path: if the
// on-disk deployment record can no longer be decoded when the toggle re-reads it to
// preserve the XML/DMN models, the change is refused with a 500 rather than applied
// unpersisted (ADR-0119).
func TestSetProcessActiveSidecarCorrupt(t *testing.T) {
	dir := t.TempDir()
	s := boot(t, dir)
	defer s.shutdown()
	if code, body := doReq(t, s.ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	if err := os.WriteFile(filepath.Join(dir, "deployments", "1.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("corrupt record: %v", err)
	}
	if code, body := doReq(t, s.ts, http.MethodPut, "/api/v1/processes/1/active", `{"active":false}`, "application/json"); code != http.StatusInternalServerError {
		t.Fatalf("corrupt-record toggle status=%d body=%s, want 500", code, body)
	}
}

// TestSetProcessActiveSidecarUnreadable covers load's read-failure branch (distinct
// from a missing file): the record path exists but cannot be read as a file, so the
// toggle refuses with a 500 (ADR-0119).
func TestSetProcessActiveSidecarUnreadable(t *testing.T) {
	dir := t.TempDir()
	s := boot(t, dir)
	defer s.shutdown()
	if code, body := doReq(t, s.ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	recPath := filepath.Join(dir, "deployments", "1.json")
	if err := os.Remove(recPath); err != nil {
		t.Fatalf("remove record: %v", err)
	}
	// Replace the record file with a directory of the same name: os.ReadFile then fails
	// with a non-"not exist" error.
	if err := os.Mkdir(recPath, 0o755); err != nil {
		t.Fatalf("mkdir record path: %v", err)
	}
	if code, body := doReq(t, s.ts, http.MethodPut, "/api/v1/processes/1/active", `{"active":false}`, "application/json"); code != http.StatusInternalServerError {
		t.Fatalf("unreadable-record toggle status=%d body=%s, want 500", code, body)
	}
}

// TestSetProcessActiveSidecarMissing covers the divergence path: the definition is in
// the in-memory registry but its sidecar record is gone, so the toggle reports 404
// rather than applying a change it cannot persist (ADR-0119).
func TestSetProcessActiveSidecarMissing(t *testing.T) {
	dir := t.TempDir()
	s := boot(t, dir)
	defer s.shutdown()
	if code, body := doReq(t, s.ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	if err := os.Remove(filepath.Join(dir, "deployments", "1.json")); err != nil {
		t.Fatalf("remove record: %v", err)
	}
	if code, body := doReq(t, s.ts, http.MethodPut, "/api/v1/processes/1/active", `{"active":false}`, "application/json"); code != http.StatusNotFound {
		t.Fatalf("missing-record toggle status=%d body=%s, want 404", code, body)
	}
}

// TestSetProcessActiveGatesMessageStart proves the toggle reaches the engine, not
// just the listing: a deactivated message-start process does not instantiate on a
// correlating message, and starts again once reactivated (ADR-0119).
func TestSetProcessActiveGatesMessageStart(t *testing.T) {
	ts := newTestServer(t)
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", deactivateMsgStartBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}

	// Active: a message starts an instance.
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/messages", `{"name":"orderEvent"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("publish (active) status=%d body=%s", code, body)
	}
	if got := countInstances(t, ts); got != 1 {
		t.Fatalf("after message while active: instances=%d, want 1", got)
	}

	// Deactivate: a message starts nothing.
	if code, body := doReq(t, ts, http.MethodPut, "/api/v1/processes/1/active", `{"active":false}`, "application/json"); code != http.StatusOK {
		t.Fatalf("deactivate status=%d body=%s", code, body)
	}
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/messages", `{"name":"orderEvent"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("publish (inactive) status=%d body=%s", code, body)
	}
	if got := countInstances(t, ts); got != 1 {
		t.Fatalf("after message while inactive: instances=%d, want 1 (no new start)", got)
	}

	// Reactivate: a message starts an instance again.
	if code, body := doReq(t, ts, http.MethodPut, "/api/v1/processes/1/active", `{"active":true}`, "application/json"); code != http.StatusOK {
		t.Fatalf("reactivate status=%d body=%s", code, body)
	}
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/messages", `{"name":"orderEvent"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("publish (reactivated) status=%d body=%s", code, body)
	}
	if got := countInstances(t, ts); got != 2 {
		t.Fatalf("after message while reactivated: instances=%d, want 2", got)
	}
}
