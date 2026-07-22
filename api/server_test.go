package api_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

const sampleBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="order" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="task">
      <extensionElements><zeebe:taskDefinition type="payment" retries="5"/></extensionElements>
    </serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="task"/>
    <sequenceFlow id="f2" sourceRef="task" targetRef="end"/>
  </process>
</definitions>`

// newTestServer wires a real wal+state+engine over a temp dir behind the API and
// returns an httptest server plus a cleanup.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv := api.New(proc, store)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Close()
		_ = store.Close()
		_ = log.Close()
	})
	return ts
}

func doReq(t *testing.T, ts *httptest.Server, method, path, body, contentType string) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, ts.URL+path, r)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, data
}

// TestDeployRunAndStats drives the full skeleton path over HTTP: deploy a model,
// see it listed, start an instance, and observe it waiting at the service task.
func TestDeployRunAndStats(t *testing.T) {
	ts := newTestServer(t)

	// Deploy.
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status = %d, body = %s", code, body)
	}
	var dep struct {
		Key       uint64 `json:"key"`
		ProcessID string `json:"processId"`
		Version   int32  `json:"version"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	if dep.ProcessID != "order" || dep.Version != 1 {
		t.Fatalf("deploy = %+v, want processId=order version=1", dep)
	}

	// List shows it.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/processes", "", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"processId":"order"`) {
		t.Fatalf("list status=%d body=%s", code, body)
	}

	// XML round-trips.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/processes/1/xml", "", "")
	if code != http.StatusOK || !strings.Contains(string(body), `id="order"`) {
		t.Fatalf("xml status=%d body=%s", code, body)
	}

	// Start an instance; it parks at the service task (one process, one element).
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/processes/1/instances", "{}", "application/json")
	if code != http.StatusOK {
		t.Fatalf("create instance status=%d body=%s", code, body)
	}
	var ci struct {
		Stats struct {
			ActiveProcessInstances int `json:"activeProcessInstances"`
			ActiveElementInstances int `json:"activeElementInstances"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(body, &ci); err != nil {
		t.Fatalf("decode create: %v (%s)", err, body)
	}
	if ci.Stats.ActiveProcessInstances != 1 || ci.Stats.ActiveElementInstances != 1 {
		t.Fatalf("stats = %+v, want 1 and 1", ci.Stats)
	}

	// The running instance shows up in the operations list with its process.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	if code != http.StatusOK {
		t.Fatalf("instances status=%d body=%s", code, body)
	}
	var insts []struct {
		Key              uint64 `json:"key"`
		ProcessID        string `json:"processId"`
		ElementInstances int    `json:"elementInstances"`
		State            string `json:"state"`
	}
	if err := json.Unmarshal(body, &insts); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, body)
	}
	if len(insts) != 1 || insts[0].ProcessID != "order" || insts[0].ElementInstances != 1 || insts[0].State != "active" {
		t.Fatalf("instances = %+v, want one active order instance with 1 token", insts)
	}

	// The live overlay data: the token sits on the service task "task".
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/processes/1/runtime", "", "")
	if code != http.StatusOK {
		t.Fatalf("runtime status=%d body=%s", code, body)
	}
	var rt struct {
		Instances int `json:"instances"`
		Tokens    int `json:"tokens"`
		Elements  []struct {
			ElementID string `json:"elementId"`
			Type      string `json:"type"`
			Tokens    int    `json:"tokens"`
		} `json:"elements"`
	}
	if err := json.Unmarshal(body, &rt); err != nil {
		t.Fatalf("decode runtime: %v (%s)", err, body)
	}
	if rt.Instances != 1 || rt.Tokens != 1 || len(rt.Elements) != 1 ||
		rt.Elements[0].ElementID != "task" || rt.Elements[0].Type != "ServiceTask" || rt.Elements[0].Tokens != 1 {
		t.Fatalf("runtime = %+v, want 1 instance with 1 token total on service task \"task\"", rt)
	}
}

// TestDeleteProcess removes a deployment, and refuses while an instance runs.
func TestDeleteProcess(t *testing.T) {
	ts := newTestServer(t)
	// Deploy two definitions.
	for i := 0; i < 2; i++ {
		if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml"); code != http.StatusOK {
			t.Fatalf("deploy %d: status=%d body=%s", i, code, body)
		}
	}
	// Delete definition 2 (no instances) → 204, then it's gone from the list.
	if code, body := doReq(t, ts, http.MethodDelete, "/api/v1/processes/2", "", ""); code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", code, body)
	}
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/processes", "", "")
	if code != http.StatusOK || strings.Contains(string(body), `"key":2`) {
		t.Fatalf("definition 2 still listed: %s", body)
	}

	// Start an instance of definition 1, then deletion must be refused (409).
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/processes/1/instances", "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance failed")
	}
	if code, _ := doReq(t, ts, http.MethodDelete, "/api/v1/processes/1", "", ""); code != http.StatusConflict {
		t.Fatalf("delete with running instance: status=%d, want 409", code)
	}
}

// TestListInstancesIncludesCompleted deploys a process that runs straight to the
// end (no wait point), starts it, and checks the instance list now carries it as
// a finished instance with a completion time — the history view (ADR-0017).
func TestListInstancesIncludesCompleted(t *testing.T) {
	ts := newTestServer(t)

	const straightThrough = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="passthrough" isExecutable="true">
    <startEvent id="start"/>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="end"/>
  </process>
</definitions>`

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", straightThrough, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}

	// Starting the instance runs it to completion synchronously.
	code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "{}", "application/json")
	if code != http.StatusOK {
		t.Fatalf("create instance status=%d body=%s", code, body)
	}

	code, body = doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	if code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", code, body)
	}
	var insts []struct {
		ProcessID        string `json:"processId"`
		State            string `json:"state"`
		CompletedAt      int64  `json:"completedAt"`
		ElementInstances int    `json:"elementInstances"`
	}
	if err := json.Unmarshal(body, &insts); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, body)
	}
	if len(insts) != 1 {
		t.Fatalf("instances = %d, want 1 (%s)", len(insts), body)
	}
	if insts[0].ProcessID != "passthrough" || insts[0].State != "completed" ||
		insts[0].CompletedAt == 0 || insts[0].ElementInstances != 0 {
		t.Fatalf("instance = %+v, want completed passthrough with a completion time and 0 tokens", insts[0])
	}
}

// TestHistorySweepPurgesViaServer wires retention through the server: with a
// tiny window and a background sweep, a finished instance is dropped from the
// instances list without any manual purge call (ADR-0018).
func TestHistorySweepPurgesViaServer(t *testing.T) {
	dir := t.TempDir()
	wl, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, wl, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	proc.SetHistoryRetention(time.Nanosecond) // anything already completed is expired
	srv := api.New(proc, store)
	srv.StartHistorySweep(5 * time.Millisecond)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Close()
		_ = store.Close()
		_ = wl.Close()
	})

	const passthrough = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="passthrough" isExecutable="true">
    <startEvent id="start"/>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="end"/>
  </process>
</definitions>`

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", passthrough, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance status=%d body=%s", code, body)
	}

	// The sweep runs every 5ms; poll generously for the completed instance to
	// disappear from the list.
	deadline := time.Now().Add(3 * time.Second)
	for {
		code, body := doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
		if code != http.StatusOK {
			t.Fatalf("list status=%d body=%s", code, body)
		}
		var insts []struct{}
		if err := json.Unmarshal(body, &insts); err != nil {
			t.Fatalf("decode instances: %v (%s)", err, body)
		}
		if len(insts) == 0 {
			return // purged
		}
		if time.Now().After(deadline) {
			t.Fatalf("instance not purged within deadline; still listed: %s", body)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestDeployInvalidModel rejects a model with no start event as a client error.
func TestDeployInvalidModel(t *testing.T) {
	ts := newTestServer(t)
	const bad = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p"><endEvent id="e"/></process></definitions>`
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", bad, "application/xml")
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", code, body)
	}
}

// TestInstanceUnknownDefinition returns 404 for a definition that was never deployed.
func TestInstanceUnknownDefinition(t *testing.T) {
	ts := newTestServer(t)
	code, _ := doReq(t, ts, http.MethodPost, "/api/v1/processes/999/instances", "{}", "application/json")
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
}

// TestHealthAndUI checks the health endpoint and that the embedded UI is served.
func TestHealthAndUI(t *testing.T) {
	ts := newTestServer(t)
	if code, body := doReq(t, ts, http.MethodGet, "/healthz", "", ""); code != http.StatusOK || !strings.Contains(string(body), "ok") {
		t.Fatalf("healthz status=%d body=%s", code, body)
	}
	if code, body := doReq(t, ts, http.MethodGet, "/", "", ""); code != http.StatusOK || !strings.Contains(string(body), "<title>Atlas</title>") {
		t.Fatalf("index status=%d body=%s", code, body)
	}
	if code, body := doReq(t, ts, http.MethodGet, "/api/v1/info", "", ""); code != http.StatusOK || !strings.Contains(string(body), `"product":"Atlas"`) {
		t.Fatalf("info status=%d body=%s", code, body)
	}
}

// demoBPMN mirrors DEMO_BPMN in api/web/app.js: the one-click demo the UI
// deploys. Kept in sync here so a compiler change that would reject the model
// (or stop it parking a token) fails a test instead of a user's button click.
const demoBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
             targetNamespace="http://atlas/demo">
  <process id="order-review" isExecutable="true">
    <startEvent id="start" name="Order received"/>
    <serviceTask id="review" name="Review order">
      <extensionElements><zeebe:taskDefinition type="review" retries="3"/></extensionElements>
    </serviceTask>
    <serviceTask id="charge" name="Charge payment">
      <extensionElements><zeebe:taskDefinition type="charge" retries="3"/></extensionElements>
    </serviceTask>
    <endEvent id="end" name="Done"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="review"/>
    <sequenceFlow id="f2" sourceRef="review" targetRef="charge"/>
    <sequenceFlow id="f3" sourceRef="charge" targetRef="end"/>
  </process>
</definitions>`

// TestDeployDemoParksToken deploys the UI's demo model, starts an instance, and
// confirms one token parks on the "review" service task — the wait point that
// keeps the instance visible and gives the live token total something to show.
func TestDeployDemoParksToken(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", demoBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy demo status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}

	code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "{}", "application/json")
	if code != http.StatusOK {
		t.Fatalf("create instance status=%d body=%s", code, body)
	}

	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/processes/%d/runtime", dep.Key), "", "")
	if code != http.StatusOK {
		t.Fatalf("runtime status=%d body=%s", code, body)
	}
	var rt struct {
		Instances int `json:"instances"`
		Tokens    int `json:"tokens"`
		Elements  []struct {
			ElementID string `json:"elementId"`
			Tokens    int    `json:"tokens"`
		} `json:"elements"`
	}
	if err := json.Unmarshal(body, &rt); err != nil {
		t.Fatalf("decode runtime: %v (%s)", err, body)
	}
	if rt.Instances != 1 || rt.Tokens != 1 || len(rt.Elements) != 1 ||
		rt.Elements[0].ElementID != "review" || rt.Elements[0].Tokens != 1 {
		t.Fatalf("runtime = %+v, want 1 instance with 1 token parked on \"review\"", rt)
	}
}

// TestServesVendoredModeler confirms the embedded bpmn-js asset is served, so the
// editor is genuinely self-contained (ADR-0013).
func TestServesVendoredModeler(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodGet, "/vendor/bpmn/bpmn-modeler.js", "", "")
	if code != http.StatusOK || len(body) < 100_000 {
		t.Fatalf("modeler asset status=%d size=%d, want 200 and a sizable bundle", code, len(body))
	}
	// The zeebe moddle must be served too, so the editor can author zeebe extensions.
	if code, body := doReq(t, ts, http.MethodGet, "/vendor/bpmn/zeebe.json", "", ""); code != http.StatusOK || !strings.Contains(string(body), `"prefix": "zeebe"`) {
		t.Fatalf("zeebe moddle status=%d body=%.60s", code, body)
	}
}
