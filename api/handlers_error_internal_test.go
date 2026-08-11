package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// errReader always fails, to drive the io.ReadAll error branches of the deploy
// and create-instance handlers.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

// newServerForErrors builds a real engine-backed Server for white-box handler
// tests. The engine is single-writer; the Server owns it through its run loop,
// so the test drives it only via ServeHTTP.
func newServerForErrors(t *testing.T) *Server {
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
	srv, err := New(proc, store, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		srv.Close()
		_ = store.Close()
		_ = log.Close()
	})
	return srv
}

// TestReadBodyErrors covers the io.ReadAll failure branch in handleDeploy and
// handleCreateInstance by supplying a request body that errors on read.
func TestReadBodyErrors(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()

	cases := []struct {
		name string
		path string
	}{
		{"deploy", "/api/v1/deployments"},
		{"save draft", "/api/v1/drafts"},
		{"create instance", "/api/v1/processes/1/instances"},
		{"publish message", "/api/v1/messages"},
		{"complete job", "/api/v1/jobs/1/complete"},
		{"set instance variables", "/api/v1/instances/1/variables"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, errReader{})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestScanHandlersReportDecodeErrors injects an undecodable process-instance record so
// the instance-scan handlers (list, summary, bulk-cancel) surface a 500 instead of
// silently returning a partial result. It covers the store-scan error branch each of
// them shares.
func TestScanHandlersReportDecodeErrors(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()

	// A deployment so the bulk-cancel path passes its existence check and reaches the
	// scan; minimalBPMN is a trivial one-flow process.
	const minimalBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" name="P" isExecutable="true">
    <startEvent id="s"/><endEvent id="e"/><sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`
	depRec := httptest.NewRecorder()
	depReq := httptest.NewRequest(http.MethodPost, "/api/v1/deployments", strings.NewReader(minimalBPMN))
	depReq.Header.Set("Content-Type", "application/xml")
	h.ServeHTTP(depRec, depReq)
	if depRec.Code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", depRec.Code, depRec.Body.String())
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(depRec.Body.Bytes(), &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}

	// Plant an undecodable record under the active process-instance column family, and
	// one under the incident column family, so both scans hit a decode error.
	srv.do(func() {
		if err := srv.store.InjectCorruptProcessInstance(99); err != nil {
			t.Fatalf("inject corrupt record: %v", err)
		}
		if err := srv.store.InjectCorruptIncident(99); err != nil {
			t.Fatalf("inject corrupt incident: %v", err)
		}
	})

	for _, tc := range []struct {
		method, path, body string
	}{
		{http.MethodGet, "/api/v1/instances", ""},
		// /api/v1/instances/summary is not listed: it reads O(1) per-definition counters
		// (ADR-0083), not the instance records, so an undecodable instance does not
		// affect it — that decoupling is the point of the change.
		{http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/cancel-instances", dep.Key), ""},
		// The single-instance runtime overlay scans the process instances (ADR-0080),
		// so the undecodable record surfaces as a 500 there too.
		{http.MethodGet, fmt.Sprintf("/api/v1/processes/%d/runtime?instance=99", dep.Key), ""},
		// Bulk terminate: filter mode scans the process instances, and keys mode point-
		// reads one — both hit the undecodable record and surface a 500.
		{http.MethodPost, "/api/v1/instances/terminate", fmt.Sprintf(`{"processDefKey":%d}`, dep.Key)},
		{http.MethodPost, "/api/v1/instances/terminate", `{"keys":[99]}`},
		// Setting variables point-reads the target instance to confirm it is running
		// (ADR-0095); the undecodable record surfaces as a 500 rather than a silent
		// success.
		{http.MethodPost, "/api/v1/instances/99/variables", `{"variables":{"x":1}}`},
		// The incident list scans the incident column family (ADR-0061), so the
		// undecodable incident record surfaces as a 500 there too.
		{http.MethodGet, "/api/v1/incidents", ""},
	} {
		rec := httptest.NewRecorder()
		var body io.Reader
		if tc.body != "" {
			body = strings.NewReader(tc.body)
		}
		h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, body))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s %s status=%d, want 500 (undecodable record)", tc.method, tc.path, rec.Code)
		}
	}
}

// TestSetInstanceVariablesScopeReadError covers the set-variables handler's
// scope-validation error branch (ADR-0095): a valid running instance but an
// undecodable element instance at the requested scopeKey surfaces as a 500 rather
// than a silent success. It deploys and starts a waiting process for a real active
// instance, then plants a corrupt element record and targets it as the scope.
func TestSetInstanceVariablesScopeReadError(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()

	const waitBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="wait" isExecutable="true">
    <startEvent id="s"/>
    <serviceTask id="t"><extensionElements><zeebe:taskDefinition type="work"/></extensionElements></serviceTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </process>
</definitions>`
	depRec := httptest.NewRecorder()
	depReq := httptest.NewRequest(http.MethodPost, "/api/v1/deployments", strings.NewReader(waitBPMN))
	depReq.Header.Set("Content-Type", "application/xml")
	h.ServeHTTP(depRec, depReq)
	if depRec.Code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", depRec.Code, depRec.Body.String())
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(depRec.Body.Bytes(), &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	startRec := httptest.NewRecorder()
	h.ServeHTTP(startRec, httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), nil))
	if startRec.Code != http.StatusOK {
		t.Fatalf("start status=%d body=%s", startRec.Code, startRec.Body.String())
	}

	var instKey uint64
	srv.do(func() {
		if err := srv.store.ActiveProcessInstances(func(k uint64, _ *model.ProcessInstanceValue) error {
			instKey = k
			return nil
		}); err != nil {
			t.Fatalf("scan instances: %v", err)
		}
		if err := srv.store.InjectCorruptElementInstance(777); err != nil {
			t.Fatalf("inject corrupt element: %v", err)
		}
	})
	if instKey == 0 {
		t.Fatal("no active instance found")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/instances/%d/variables", instKey),
		strings.NewReader(`{"variables":{"x":1},"scopeKey":777}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("corrupt scope: status=%d, want 500 (%s)", rec.Code, rec.Body.String())
	}
}

// TestDoAfterCloseIsNoop covers the do() closing branch: once the run loop has
// stopped, a submitted closure never runs and the result stays zero-valued. It
// builds its own server so it can Close exactly once (no cleanup double-close).
func TestDoAfterCloseIsNoop(t *testing.T) {
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
	t.Cleanup(func() {
		_ = store.Close()
		_ = log.Close()
	})

	srv, err := New(proc, store, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv.Close() // stop the loop

	ran := false
	srv.do(func() { ran = true })
	if ran {
		t.Fatal("do ran a closure after the loop was stopped")
	}
}
