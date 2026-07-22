package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

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
}
