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
		{"layout", "/api/v1/layout"},
		{"complete task", "/api/v1/tasks/1/complete"},
		// The live-session actions share decodeSessionBody, which reads before it
		// looks the draft up — so an unreadable body is a 400 even for a draft that
		// does not exist.
		{"session presence", "/api/v1/drafts/ghost/session/presence"},
		{"session lock", "/api/v1/drafts/ghost/session/lock"},
		{"session change", "/api/v1/drafts/ghost/session/change"},
		// The user endpoints share readJSONBody, which reports an unreadable body
		// before it can know whether the request was even well-formed.
		{"login", "/api/v1/auth/login"},
		{"create user", "/api/v1/users"},
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
		// The content search walks the same instance family, so it reports the
		// undecodable record rather than quietly returning the instances it could read
		// — a search that silently omits a match is worse than one that fails.
		{http.MethodGet, "/api/v1/instances/search?q=anything", ""},
		{http.MethodGet, "/api/v1/instances?state=active", ""},
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

// nonFlusher is a ResponseWriter that deliberately does not implement
// http.Flusher, standing in for a middleware or proxy wrapper that hides it.
type nonFlusher struct{ h http.Header }

func (n *nonFlusher) Header() http.Header       { return n.h }
func (*nonFlusher) Write(b []byte) (int, error) { return len(b), nil }
func (*nonFlusher) WriteHeader(int)             {}

// TestDraftSessionRefusesUnflushableWriter pins the guard the SSE endpoint needs
// before it commits to streaming: without a Flusher every frame would sit in the
// buffer, so a co-editing client would join a session that never delivers an
// event. Refusing up front with a 500 is the honest answer; net/http's own
// writer always flushes, so only a wrapper can produce this.
func TestDraftSessionRefusesUnflushableWriter(t *testing.T) {
	srv := newServerForErrors(t)
	const draftXML = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="wip" isExecutable="true"><startEvent id="s"/></process>
	</definitions>`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/drafts", strings.NewReader(draftXML))
	req.Header.Set("Content-Type", "application/xml")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("save draft = %d, body %s", rec.Code, rec.Body.String())
	}

	// Called directly rather than through the mux, so the writer is ours.
	sess := httptest.NewRequest(http.MethodGet, "/api/v1/drafts/wip/session", nil)
	sess.SetPathValue("id", "wip")
	w := &nonFlusher{h: http.Header{}}
	srv.handleDraftSession(w, sess)
	if got := w.Header().Get("Content-Type"); strings.Contains(got, "event-stream") {
		t.Errorf("Content-Type = %q, want the stream never to have been opened", got)
	}
}

// newServerWithClock is newServerForErrors with the engine clock injected, for tests
// that must make time pass (a worker's lease elapsing) without waiting for it.
func newServerWithClock(t *testing.T, clk engine.Clock) *Server {
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
	proc := engine.New(1, log, store, clk)
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

// newServerWithOptions is newServerForErrors with Options applied, for tests that
// need a differently configured server.
func newServerWithOptions(t *testing.T, opts ...Option) *Server {
	t.Helper()
	srv, err := newServerWithOptionsErr(t, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

// newServerWithOptionsErr is newServerWithOptions returning the construction error,
// for tests about configuration that must be refused.
func newServerWithOptionsErr(t *testing.T, opts ...Option) (*Server, error) {
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
	srv, err := New(proc, store, dir, opts...)
	t.Cleanup(func() {
		if srv != nil {
			srv.Close()
		}
		_ = store.Close()
		_ = log.Close()
	})
	return srv, err
}

// TestInstanceDataObjectsReportsScanErrors covers the read-failure branches of the
// instance Data view. Three reads stand between a request and the answer — the
// objects themselves, the instance record that says which definition declared them,
// and the retained state trail — and each must surface as a 500 rather than a
// half-annotated list. A datum whose trail is missing its middle reads as a value
// that never passed through a state it did, which is worse than no answer at all.
func TestInstanceDataObjectsReportsScanErrors(t *testing.T) {
	const scope = uint64(4242)
	h500 := func(t *testing.T, srv *Server, what string) {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/data-objects", scope), nil))
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s: status=%d body=%s, want 500", what, rec.Code, rec.Body.String())
		}
	}

	t.Run("undecodable data object", func(t *testing.T) {
		srv := newServerForErrors(t)
		srv.do(func() {
			if err := srv.store.InjectCorruptDataObject(scope, "order"); err != nil {
				t.Fatalf("inject: %v", err)
			}
		})
		h500(t, srv, "corrupt data object")
	})

	t.Run("undecodable instance record", func(t *testing.T) {
		srv := newServerForErrors(t)
		srv.do(func() {
			// A readable object, so the handler gets past its own scan and reaches the
			// annotation — where the instance record it needs is the broken one.
			if err := putDataObjectForTest(srv, scope, "order"); err != nil {
				t.Fatalf("seed: %v", err)
			}
			if err := srv.store.InjectCorruptProcessInstance(scope); err != nil {
				t.Fatalf("inject: %v", err)
			}
		})
		h500(t, srv, "corrupt instance record")
	})

	t.Run("undecodable state trail", func(t *testing.T) {
		srv := newServerForErrors(t)
		srv.do(func() {
			if err := putDataObjectForTest(srv, scope, "order"); err != nil {
				t.Fatalf("seed: %v", err)
			}
			if err := putProcessInstanceForTest(srv, scope, 1); err != nil {
				t.Fatalf("seed instance: %v", err)
			}
			if err := srv.store.InjectCorruptDataObjectSnapshot(scope, 1, 1); err != nil {
				t.Fatalf("inject: %v", err)
			}
		})
		h500(t, srv, "corrupt state trail")
	})
}

// dataObjectPairBPMN declares two data objects, so a test can give an instance only
// one of them.
const dataObjectPairBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="pair" isExecutable="true">
    <dataObject id="DO_order" name="order" itemSubjectRef="Order"/>
    <dataObject id="DO_invoice" name="invoice" itemSubjectRef="Invoice"/>
    <startEvent id="s"/><endEvent id="e"/><sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`

// TestInstanceDataObjectsListsWhatTheInstanceCarries pins that the Data view lists the
// objects the *instance* holds and annotates them from the definition — not the other
// way round. An instance that carries only one of its definition's two data objects
// gets one row, with its declared class; the other is not invented from the model. It
// is how a migrated instance reads: a version that declares a data object the instance
// was never seeded with does not conjure a datum that has no value and no history.
func TestInstanceDataObjectsListsWhatTheInstanceCarries(t *testing.T) {
	const scope = uint64(4244)
	srv := newServerForErrors(t)
	h := srv.Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/deployments", strings.NewReader(dataObjectPairBPMN))
	req.Header.Set("Content-Type", "application/xml")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", rec.Code, rec.Body.String())
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	srv.do(func() {
		if err := putDataObjectForTest(srv, scope, "order"); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if err := putProcessInstanceForTest(srv, scope, dep.Key); err != nil {
			t.Fatalf("seed instance: %v", err)
		}
	})

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/data-objects", scope), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got []dataObjectView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if len(got) != 1 || got[0].Name != "order" {
		t.Fatalf("got %+v, want only the object the instance carries", got)
	}
	if got[0].ItemType != "Order" {
		t.Errorf("itemType = %q, want Order — annotated from the definition", got[0].ItemType)
	}
}

// TestInstanceDataObjectsWithoutInstanceRecord covers the case where a scope carries
// data objects but no instance record resolves — the values still read, with no
// declared class and no attribution, because both come from facts that are not there.
func TestInstanceDataObjectsWithoutInstanceRecord(t *testing.T) {
	const scope = uint64(4243)
	srv := newServerForErrors(t)
	srv.do(func() {
		if err := putDataObjectForTest(srv, scope, "order"); err != nil {
			t.Fatalf("seed: %v", err)
		}
	})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/data-objects", scope), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var got []dataObjectView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if len(got) != 1 || got[0].Name != "order" {
		t.Fatalf("got %+v, want the one seeded object", got)
	}
	if got[0].ItemType != "" || got[0].ProducedBy != "" || len(got[0].History) != 0 {
		t.Errorf("got %+v, want no declared class, no attribution and an empty trail", got[0])
	}
}

// putDataObjectForTest and putProcessInstanceForTest plant the two records the Data
// view reads, so a test can put one of them out of reach and see what the handler
// does. They write straight to the store under the run loop, as the injectors above
// do — the point is a store in a shape the engine would not normally produce.
func putDataObjectForTest(srv *Server, scope uint64, name string) error {
	tx := srv.store.NewTransaction()
	defer tx.Close()
	if err := tx.PutDataObject(&model.DataObjectValue{ScopeKey: scope, Name: name, State: "received", Kind: model.VarNull}); err != nil {
		return err
	}
	return tx.Commit()
}

func putProcessInstanceForTest(srv *Server, key, defKey uint64) error {
	tx := srv.store.NewTransaction()
	defer tx.Close()
	if err := tx.PutProcessInstance(key, &model.ProcessInstanceValue{ProcessDefKey: defKey, State: model.PIActive}); err != nil {
		return err
	}
	return tx.Commit()
}

// TestIndexBackedScansReportDecodeErrors is the scoped counterpart: the
// by-definition indexes point at instance records, and an undecodable record
// behind an entry must surface as a failure rather than as a version that
// silently lists one instance fewer than it has. The record is corrupted *after*
// it was really written, so its index entry stands and the scan reaches it —
// which an injected record alone could not arrange.
func TestIndexBackedScansReportDecodeErrors(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()

	const oneTask = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:atlas="http://atlas.dev/schema/bpmn">
  <process id="p" name="P" isExecutable="true">
    <startEvent id="s"/>
    <userTask id="t" name="T"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </process>
</definitions>`
	depRec := httptest.NewRecorder()
	depReq := httptest.NewRequest(http.MethodPost, "/api/v1/deployments", strings.NewReader(oneTask))
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
	startReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), strings.NewReader("{}"))
	startReq.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("create instance status=%d body=%s", startRec.Code, startRec.Body.String())
	}

	// Find the instance the definition's index now points at, then make its record
	// undecodable while leaving the entry standing.
	var key uint64
	srv.do(func() {
		if err := srv.store.ActiveInstancesOfDefDesc(dep.Key, 0, func(k uint64, _ *model.ProcessInstanceValue) error {
			key = k
			return nil
		}); err != nil {
			t.Fatalf("ActiveInstancesOfDefDesc: %v", err)
		}
	})
	if key == 0 {
		t.Fatal("the definition's index holds no instance")
	}
	srv.do(func() {
		if err := srv.store.InjectCorruptProcessInstance(key); err != nil {
			t.Fatalf("inject corrupt record: %v", err)
		}
	})

	for _, path := range []string{
		fmt.Sprintf("/api/v1/instances?process=%d", dep.Key),
		fmt.Sprintf("/api/v1/instances?process=%d&state=active", dep.Key),
		fmt.Sprintf("/api/v1/instances/search?process=%d&q=anything", dep.Key),
		fmt.Sprintf("/api/v1/instances/search?q=%d", key), // the exact-key point read
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("GET %s = %d, want 500 over an undecodable record (%s)", path, rec.Code, rec.Body.String())
		}
	}
}
