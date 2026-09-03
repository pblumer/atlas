package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/clio"
	"github.com/pblumer/atlas/connector/jira"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// TestEventVarsAndKeys covers the pure bridge helpers: payload conversion across
// every value kind and the reserved envelope fields, correlation-key evaluation
// (keyless, a missing field, a present body field, and a subject-derived key), the
// event-id → dedup-seq parse, and the source-id format.
func TestEventVarsAndKeys(t *testing.T) {
	// eventVars seeds every body field plus the reserved envelope fields.
	vars := eventVars(clioFields(clio.InboundEvent{
		ID: "7", Subject: "/employees/E-123456", Type: "employee.created",
		Data: map[string]any{"s": "hi", "n": 42.0, "b": true, "obj": map[string]any{"a": 1}, "z": nil},
	}))
	kinds := map[string]model.VarKind{}
	text := map[string]string{}
	for _, v := range vars {
		kinds[v.Name] = v.Kind
		text[v.Name] = v.Text
	}
	for name, want := range map[string]model.VarKind{
		"s": model.VarString, "n": model.VarNumber, "b": model.VarBool, "obj": model.VarJSON, "z": model.VarNull,
	} {
		if kinds[name] != want {
			t.Errorf("var %q kind = %v, want %v", name, kinds[name], want)
		}
	}
	if text["subject"] != "/employees/E-123456" || text["subjectTail"] != "E-123456" {
		t.Errorf("envelope subject/subjectTail = %q/%q, want /employees/E-123456 and E-123456", text["subject"], text["subjectTail"])
	}
	if text["eventType"] != "employee.created" || text["eventId"] != "7" {
		t.Errorf("envelope eventType/eventId = %q/%q, want employee.created and 7", text["eventType"], text["eventId"])
	}

	if got := correlationKeyOf(nil, clioFields(clio.InboundEvent{})); got != "" {
		t.Errorf("keyless correlationKeyOf = %q, want empty", got)
	}
	orderKey, err := expr.CompileAuto("orderId")
	if err != nil {
		t.Fatalf("CompileAuto: %v", err)
	}
	if got := correlationKeyOf(orderKey, clioFields(clio.InboundEvent{Data: map[string]any{}})); got != "" {
		t.Errorf("missing-field correlationKeyOf = %q, want empty (null propagates)", got)
	}
	if got := correlationKeyOf(orderKey, clioFields(clio.InboundEvent{Data: map[string]any{"orderId": "o-9"}})); got != "o-9" {
		t.Errorf("correlationKeyOf = %q, want o-9", got)
	}
	// The reported use case: a correlation key of subjectTail derives E-123456 from
	// the subject /employees/E-123456, with no such field in the event body.
	tailKey, err := expr.CompileAuto("subjectTail")
	if err != nil {
		t.Fatalf("CompileAuto: %v", err)
	}
	if got := correlationKeyOf(tailKey, clioFields(clio.InboundEvent{Subject: "/employees/E-123456"})); got != "E-123456" {
		t.Errorf("subjectTail correlationKeyOf = %q, want E-123456", got)
	}

	// A clio event id is a decimal per-partition sequence → the engine dedup seq.
	if got := clioSeq("42"); got != 42 {
		t.Errorf("clioSeq(42) = %d, want 42", got)
	}
	if got := clioSeq("not-a-number"); got != 0 {
		t.Errorf("clioSeq(garbage) = %d, want 0 (skipped)", got)
	}

	// A clio watch keeps one mark for the whole subject, and the string must stay
	// byte-identical: reshaping it would reset every existing watch's high-water mark
	// and replay its backlog as new process starts.
	sub := inboundSubscription{ID: "w1", ConnectorID: "c1", WatchedSubject: "orders/new"}
	if got := inboundSourceID("clio", sub, ""); got != "clio:c1:orders/new" {
		t.Errorf("inboundSourceID = %q, want clio:c1:orders/new", got)
	}
	// A source that hands over an event-level mark key gets one mark per event
	// subject instead — for Jira, per issue (ADR-0214).
	if got := inboundSourceID("jira", sub, "10042"); got != "jira:c1:w1:10042" {
		t.Errorf("inboundSourceID = %q, want jira:c1:w1:10042", got)
	}
}

// TestResolveInboundSubsSkips proves resolveInboundSubs skips subscriptions that are
// disabled, reference a non-clio or disabled worker, or whose worker has no
// live client — only a fully-live clio subscription is returned.
func TestResolveInboundSubsSkips(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	srv.do(func() {
		_ = srv.connectors.Save(connector{ID: "clio-on", Name: "on", Kind: "clio", Endpoint: "http://x", Enabled: true, CreatedAt: 1})
		_ = srv.connectors.Save(connector{ID: "clio-off", Name: "off", Kind: "clio", Endpoint: "http://x", Enabled: false, CreatedAt: 2})
		_ = srv.connectors.Save(connector{ID: "temis", Name: "t", Kind: "temis", Endpoint: "http://x", Enabled: true, CreatedAt: 3})
		_ = srv.inboundSubs.Save(inboundSubscription{ID: "s-live", ConnectorID: "clio-on", WatchedSubject: "a", MessageName: "m", Enabled: true, CreatedAt: 1})
		_ = srv.inboundSubs.Save(inboundSubscription{ID: "s-disabled", ConnectorID: "clio-on", WatchedSubject: "a", MessageName: "m", Enabled: false, CreatedAt: 2})
		_ = srv.inboundSubs.Save(inboundSubscription{ID: "s-offconn", ConnectorID: "clio-off", WatchedSubject: "a", MessageName: "m", Enabled: true, CreatedAt: 3})
		_ = srv.inboundSubs.Save(inboundSubscription{ID: "s-temis", ConnectorID: "temis", WatchedSubject: "a", MessageName: "m", Enabled: true, CreatedAt: 4})
		_ = srv.inboundSubs.Save(inboundSubscription{ID: "s-noconn", ConnectorID: "gone", WatchedSubject: "a", MessageName: "m", Enabled: true, CreatedAt: 5})
		// Only clio-on has a live client registered.
		srv.clioRegistry.Replace(map[string]clio.Client{"on": &fakeClioReader{}})
	})
	var got []pendingSub
	srv.do(func() { got = srv.resolveInboundSubs() })
	if len(got) != 1 || got[0].rec.ID != "s-live" {
		t.Fatalf("resolveInboundSubs = %d subs, want only s-live", len(got))
	}
}

// TestResolveInboundSubsBadKey covers resolveInboundSubs' compile-error branch: a
// subscription stored with an uncompilable correlation key (bypassing the API's
// config-time validation) is skipped rather than crashing the poll.
func TestResolveInboundSubsBadKey(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	var got []pendingSub
	srv.do(func() {
		_ = srv.connectors.Save(connector{ID: "c", Name: "on", Kind: "clio", Endpoint: "http://x", Enabled: true, CreatedAt: 1})
		_ = srv.inboundSubs.Save(inboundSubscription{ID: "s", ConnectorID: "c", WatchedSubject: "a", MessageName: "m", CorrelationKey: "( unbalanced", Enabled: true, CreatedAt: 1})
		srv.clioRegistry.Replace(map[string]clio.Client{"on": &fakeClioReader{}})
		got = srv.resolveInboundSubs()
	})
	if len(got) != 0 {
		t.Fatalf("resolveInboundSubs with an uncompilable key = %d, want 0 (skipped)", len(got))
	}
}

// TestAdvanceInboundCursorMissing covers the guard for advancing a vanished
// subscription's cursor: it is a no-op, not a panic.
func TestAdvanceInboundCursorMissing(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	srv.do(func() { srv.advanceInboundCursor("missing", "e1") }) // must not panic
}

// TestPollInboundReadError covers the transient-read-failure branch: a client whose
// ReadEvents fails leaves nothing published (the poll continues to the next tick).
func TestPollInboundReadError(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	srv.do(func() {
		_ = srv.connectors.Save(connector{ID: "c", Name: "on", Kind: "clio", Endpoint: "http://x", Enabled: true, CreatedAt: 1})
		_ = srv.inboundSubs.Save(inboundSubscription{ID: "s", ConnectorID: "c", WatchedSubject: "a", MessageName: "m", Enabled: true, CreatedAt: 1})
		srv.clioRegistry.Replace(map[string]clio.Client{"on": errReader{}})
	})
	srv.pollInbound(context.Background()) // read fails → no publish, no panic
	if n := activeInstances(t, srv); n != 0 {
		t.Fatalf("active=%d, want 0 (read failed)", n)
	}
}

// errReader (defined in handlers_error_internal_test.go as a failing io.Reader) is
// extended here to also be a clio.Client whose ReadEvents always errors, exercising
// the bridge's transient-read-failure branch.
func (errReader) WriteEvent(context.Context, clio.Event) error                     { return nil }
func (errReader) GetState(context.Context, string, string) (map[string]any, error) { return nil, nil }
func (errReader) Query(context.Context, string, string) (any, error)               { return nil, nil }
func (errReader) ReadEvents(context.Context, clio.ReadEventsRequest) ([]clio.InboundEvent, error) {
	return nil, context.DeadlineExceeded
}

// TestInboundSubStoreErrors covers the store's non-happy paths (mirrors the
// worker store error tests).
func TestInboundSubStoreErrors(t *testing.T) {
	// loadAll ignores foreign files (subdir, non-json, non-hex name).
	st, _ := newInboundSubStore(filepath.Join(t.TempDir(), "s"))
	_ = os.Mkdir(filepath.Join(st.Dir(), "sub"), 0o755)
	_ = os.WriteFile(filepath.Join(st.Dir(), "notes.txt"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(st.Dir(), "zz.json"), []byte("{}"), 0o644) // non-hex name → skipped
	_ = st.Save(inboundSubscription{ID: "a", ConnectorID: "c", MessageName: "m", CreatedAt: 1})
	if all, err := st.LoadAll(); err != nil || len(all) != 1 {
		t.Fatalf("loadAll = %v, %v, want 1 record", all, err)
	}

	// get / loadAll over a corrupt record error.
	_ = os.WriteFile(st.FileFor("bad"), []byte("{not json"), 0o644)
	if _, _, err := st.Get("bad"); err == nil {
		t.Error("get of corrupt record: want error")
	}
	if _, err := st.LoadAll(); err == nil {
		t.Error("loadAll over a corrupt record: want error")
	}

	// newInboundSubStore under a file (can't create dir) errors.
	f := filepath.Join(t.TempDir(), "afile")
	_ = os.WriteFile(f, []byte("x"), 0o644)
	if _, err := newInboundSubStore(filepath.Join(f, "sub")); err == nil {
		t.Error("newInboundSubStore under a file: want error")
	}

	// loadAll on a missing dir errors.
	gone, _ := newInboundSubStore(filepath.Join(t.TempDir(), "gone"))
	_ = os.RemoveAll(gone.Dir())
	if _, err := gone.LoadAll(); err == nil {
		t.Error("loadAll of a missing dir: want error")
	}

	// A record path that is a non-empty directory triggers get/delete read errors.
	fresh, _ := newInboundSubStore(filepath.Join(t.TempDir(), "d"))
	dirRec := fresh.FileFor("dir")
	_ = os.Mkdir(dirRec, 0o755)
	_ = os.WriteFile(filepath.Join(dirRec, "x"), []byte("y"), 0o644)
	if _, _, err := fresh.Get("dir"); err == nil {
		t.Error("get on a directory record: want error")
	}
	if err := fresh.Delete("dir"); err == nil {
		t.Error("delete of a non-empty directory record: want error")
	}
}

// TestInboundHandlerStoreAndBodyErrors covers the endpoints' store-failure (500) and
// read-body/JSON (400) branches.
func TestInboundHandlerStoreAndBodyErrors(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	// A clio worker exists so create passes its kind check and reaches the save.
	srv.do(func() {
		_ = srv.connectors.Save(connector{ID: "c", Name: "on", Kind: "clio", Endpoint: "http://x", Enabled: true, CreatedAt: 1})
	})
	h := srv.Handler()
	do := func(method, path, body string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	doReader := func(method, path string) int {
		req := httptest.NewRequest(method, path, errReader{})
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	// Read-body errors on create and update → 400.
	if doReader(http.MethodPost, "/api/v1/connectors/c/inbound-subscriptions") != http.StatusBadRequest {
		t.Error("create read-body error: want 400")
	}
	if doReader(http.MethodPatch, "/api/v1/inbound-subscriptions/x") != http.StatusBadRequest {
		t.Error("update read-body error: want 400")
	}
	// Invalid JSON on update → 400.
	if do(http.MethodPatch, "/api/v1/inbound-subscriptions/x", "{bad") != http.StatusBadRequest {
		t.Error("update invalid JSON: want 400")
	}

	// A corrupt worker record makes the create handler's worker lookup error
	// (a decode failure, not a missing file) → 500.
	srv.do(func() {
		_ = os.WriteFile(srv.connectors.FileFor("corrupt"), []byte("{not json"), 0o644)
	})
	if do(http.MethodPost, "/api/v1/connectors/corrupt/inbound-subscriptions", `{"watchedSubject":"s","messageName":"m"}`) != http.StatusInternalServerError {
		t.Error("create with a corrupt worker record: want 500")
	}

	// Point the subscription store at a broken (missing) directory while the worker
	// store stays real. list (loadAll) and delete (fsync) fail hard → 500; create
	// passes the Worker Type check on the real "c" worker, then the save fails
	// → 500. (An update or get of a missing record is a 404, not a 500, since a
	// missing file is not-found rather than an error.)
	srv.do(func() { srv.inboundSubs = brokenStore(newInboundSubStore(filepath.Join(t.TempDir(), "gone"))) })
	if do(http.MethodGet, "/api/v1/connectors/c/inbound-subscriptions", "") != http.StatusInternalServerError {
		t.Error("list with a broken store: want 500")
	}
	if do(http.MethodPost, "/api/v1/connectors/c/inbound-subscriptions", `{"watchedSubject":"s","messageName":"m"}`) != http.StatusInternalServerError {
		t.Error("create with a broken store: want 500")
	}
	if do(http.MethodDelete, "/api/v1/inbound-subscriptions/x", "") != http.StatusInternalServerError {
		t.Error("delete with a broken store: want 500")
	}
}

// TestResolveInboundSubsLoadErrors covers resolveInboundSubs' store-read failure
// branches: a broken subscription store or worker store yields no subscriptions.
func TestResolveInboundSubsLoadErrors(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	var got []pendingSub
	srv.do(func() {
		srv.inboundSubs = brokenStore(newInboundSubStore(filepath.Join(t.TempDir(), "gone")))
		got = srv.resolveInboundSubs()
	})
	if got != nil {
		t.Errorf("resolveInboundSubs with a broken sub store = %v, want nil", got)
	}
	srv.do(func() {
		srv.inboundSubs, _ = newInboundSubStore(filepath.Join(t.TempDir(), "subs"))
		_ = srv.inboundSubs.Save(inboundSubscription{ID: "s", ConnectorID: "c", MessageName: "m", Enabled: true})
		srv.connectors = brokenStore(newConnectorStore(filepath.Join(t.TempDir(), "gone")))
		got = srv.resolveInboundSubs()
	})
	if got != nil {
		t.Errorf("resolveInboundSubs with a broken worker store = %v, want nil", got)
	}
}

// TestInboundSubStoreLoadAllReadError covers loadAll's per-file read-error branch: a
// dangling symlink named like a record passes the hex/.json filter but fails to read.
func TestInboundSubStoreLoadAllReadError(t *testing.T) {
	st, _ := newInboundSubStore(filepath.Join(t.TempDir(), "s"))
	if err := os.Symlink(filepath.Join(st.Dir(), "missing"), st.FileFor("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LoadAll(); err == nil {
		t.Error("loadAll over an unreadable record: want error")
	}
}

// A jira watch resolves through the same path as a clio one: the worker's kind is
// the discriminator, so what changes is which reader the watch gets, not the record's
// shape (ADR-0214). A kind with no inbound half is skipped rather than handed a reader
// it has no use for.
func TestResolveInboundSubsPicksTheReaderByConnectorKind(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	srv.jiraRegistry.Replace(map[string]jira.Client{"acme": &fakeJiraClient{}})
	srv.do(func() {
		_ = srv.connectors.Save(connector{ID: "j", Name: "acme", Kind: connectorKindJira, Endpoint: "https://acme.atlassian.net", Enabled: true, CreatedAt: 1})
		_ = srv.connectors.Save(connector{ID: "j-unheld", Name: "other", Kind: connectorKindJira, Endpoint: "https://x", Enabled: true, CreatedAt: 2})
		_ = srv.connectors.Save(connector{ID: "m", Name: "smtp", Kind: connectorKindMail, Endpoint: "smtp:587", Enabled: true, CreatedAt: 3})
		_ = srv.inboundSubs.Save(inboundSubscription{ID: "w-jira", ConnectorID: "j", JQL: "project = OPS", CursorField: "created", MessageName: "m", Enabled: true, CreatedAt: 1})
		_ = srv.inboundSubs.Save(inboundSubscription{ID: "w-unheld", ConnectorID: "j-unheld", JQL: "project = X", MessageName: "m", Enabled: true, CreatedAt: 2})
		_ = srv.inboundSubs.Save(inboundSubscription{ID: "w-mail", ConnectorID: "m", JQL: "project = Y", MessageName: "m", Enabled: true, CreatedAt: 3})
	})
	var subs []pendingSub
	srv.do(func() { subs = srv.resolveInboundSubs() })

	if len(subs) != 1 {
		t.Fatalf("resolved %d watches, want only the one whose jira worker has a live client: %+v", len(subs), subs)
	}
	if subs[0].rec.ID != "w-jira" || subs[0].kind != connectorKindJira {
		t.Errorf("resolved %q of kind %q, want w-jira of kind jira", subs[0].rec.ID, subs[0].kind)
	}
	if _, ok := subs[0].source.(jiraSource); !ok {
		t.Errorf("reader = %T, want a jiraSource", subs[0].source)
	}
}
