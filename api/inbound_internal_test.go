package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/clio"
)

// messageStartBridgeBPMN: a message start event named "orderEvent" begins an
// instance that parks at a keyless "never" catch, so an instance the bridge starts
// stays observable.
const messageStartBridgeBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
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

// fakeClioReader serves a fixed event list, honoring the exclusive AfterID cursor.
type fakeClioReader struct{ events []clio.InboundEvent }

func (f *fakeClioReader) WriteEvent(context.Context, clio.Event) error { return nil }
func (f *fakeClioReader) GetState(context.Context, string, string) (map[string]any, error) {
	return nil, nil
}
func (f *fakeClioReader) Query(context.Context, string) (any, error) { return nil, nil }
func (f *fakeClioReader) ReadEvents(_ context.Context, r clio.ReadEventsRequest) ([]clio.InboundEvent, error) {
	seen := r.AfterID == ""
	var out []clio.InboundEvent
	for _, e := range f.events {
		if seen {
			out = append(out, e)
			continue
		}
		if e.ID == r.AfterID {
			seen = true // exclusive: the boundary event is not re-delivered
		}
	}
	return out, nil
}

func activeInstances(t *testing.T, srv *Server) int {
	t.Helper()
	var n int
	srv.do(func() { n, _ = srv.store.ActiveProcessInstanceCount() })
	return n
}

// TestInboundBridgeStartsAndDedupes is the ADR-0074 vertical slice at the server
// level: the bridge reads a new clio event, publishes it as an Atlas message that
// starts a message-start process, advances its resume cursor, and — on a re-poll —
// does not re-deliver the same event (cursor) nor start a duplicate even if it did
// (engine high-water); a genuinely new event starts another instance.
func TestInboundBridgeStartsAndDedupes(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0)) // drive the bridge manually
	x := deployTestHarness{t, srv.Handler()}

	pid := x.mkProject("Onboard")
	x.saveDraft(pid, messageStartBridgeBPMN)
	if code, b := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", ""); code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, b)
	}

	code, cb := x.do(http.MethodPost, "/api/v1/connectors", `{"name":"events","kind":"clio","endpoint":"http://x"}`)
	if code != http.StatusOK {
		t.Fatalf("create connector: %d %s", code, cb)
	}
	var conn connector
	_ = json.Unmarshal(cb, &conn)

	if code, sb := x.do(http.MethodPost, "/api/v1/connectors/"+conn.ID+"/inbound-subscriptions",
		`{"watchedSubject":"orders/new","messageName":"orderEvent","correlationKey":"= orderId"}`); code != http.StatusOK {
		t.Fatalf("create subscription: %d %s", code, sb)
	}

	// Inject a fake clio client so the bridge reads canned events (no live clio).
	fake := &fakeClioReader{events: []clio.InboundEvent{
		{ID: "e1", Seq: 1, Subject: "orders/new", Type: "OrderPlaced", Data: map[string]any{"orderId": "o-1"}},
	}}
	srv.do(func() { srv.clioRegistry.Replace(map[string]clio.Client{"events": fake}) })

	srv.pollInbound(context.Background())
	if n := activeInstances(t, srv); n != 1 {
		t.Fatalf("after first poll: active=%d, want 1 (event started a process)", n)
	}

	// Re-poll with no new events: the cursor skips e1, so nothing new is delivered.
	srv.pollInbound(context.Background())
	if n := activeInstances(t, srv); n != 1 {
		t.Fatalf("after re-poll: active=%d, want 1 (no duplicate)", n)
	}

	// A brand-new event starts another instance.
	fake.events = append(fake.events, clio.InboundEvent{ID: "e2", Seq: 2, Subject: "orders/new", Type: "OrderPlaced", Data: map[string]any{"orderId": "o-2"}})
	srv.pollInbound(context.Background())
	if n := activeInstances(t, srv); n != 2 {
		t.Fatalf("after new event: active=%d, want 2", n)
	}
}

// TestInboundBridgeDedupesLostCursor proves the engine high-water — not the cursor —
// is the correctness authority: even when the resume cursor is reset (simulating a
// lost/stale sidecar), re-delivering already-applied events starts no duplicates.
func TestInboundBridgeDedupesLostCursor(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	x := deployTestHarness{t, srv.Handler()}

	pid := x.mkProject("Onboard")
	x.saveDraft(pid, messageStartBridgeBPMN)
	if code, b := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", ""); code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, b)
	}
	code, cb := x.do(http.MethodPost, "/api/v1/connectors", `{"name":"events","kind":"clio","endpoint":"http://x"}`)
	if code != http.StatusOK {
		t.Fatalf("create connector: %d %s", code, cb)
	}
	var conn connector
	_ = json.Unmarshal(cb, &conn)
	code, sb := x.do(http.MethodPost, "/api/v1/connectors/"+conn.ID+"/inbound-subscriptions",
		`{"watchedSubject":"orders/new","messageName":"orderEvent"}`)
	if code != http.StatusOK {
		t.Fatalf("create subscription: %d %s", code, sb)
	}
	var sub inboundSubscription
	_ = json.Unmarshal(sb, &sub)

	fake := &fakeClioReader{events: []clio.InboundEvent{
		{ID: "e1", Seq: 1, Subject: "orders/new", Type: "T", Data: map[string]any{"orderId": "o-1"}},
	}}
	srv.do(func() { srv.clioRegistry.Replace(map[string]clio.Client{"events": fake}) })

	srv.pollInbound(context.Background())
	if n := activeInstances(t, srv); n != 1 {
		t.Fatalf("after first poll: active=%d, want 1", n)
	}

	// Simulate a lost/stale cursor: reset it to empty so the bridge re-reads e1.
	srv.do(func() {
		rec, _, _ := srv.inboundSubs.get(sub.ID)
		rec.LastEventID = ""
		_ = srv.inboundSubs.save(rec)
	})
	srv.pollInbound(context.Background())
	if n := activeInstances(t, srv); n != 1 {
		t.Fatalf("after re-reading a re-delivered event: active=%d, want 1 (engine high-water dedupes)", n)
	}
}

// TestInboundSubscriptionHandlers covers the CRUD endpoints' validation and error
// paths.
func TestInboundSubscriptionHandlers(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	h := srv.Handler()
	do := func(method, path, body string) (int, []byte) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}

	// A clio connector to attach subscriptions to, and a temis one to reject against.
	_, cb := do(http.MethodPost, "/api/v1/connectors", `{"name":"ev","kind":"clio","endpoint":"http://x"}`)
	var clioConn connector
	_ = json.Unmarshal(cb, &clioConn)
	_, tb := do(http.MethodPost, "/api/v1/connectors", `{"name":"dec","kind":"temis","endpoint":"http://y"}`)
	var temisConn connector
	_ = json.Unmarshal(tb, &temisConn)

	base := "/api/v1/connectors/" + clioConn.ID + "/inbound-subscriptions"

	// Missing message name / subject → 400.
	if code, _ := do(http.MethodPost, base, `{"watchedSubject":"s"}`); code != http.StatusBadRequest {
		t.Error("missing messageName: want 400")
	}
	// Bad FEEL correlation key → 400.
	if code, _ := do(http.MethodPost, base, `{"watchedSubject":"s","messageName":"m","correlationKey":"= ("}`); code != http.StatusBadRequest {
		t.Error("bad FEEL: want 400")
	}
	// A subscription on a non-clio connector → 400.
	if code, _ := do(http.MethodPost, "/api/v1/connectors/"+temisConn.ID+"/inbound-subscriptions", `{"watchedSubject":"s","messageName":"m"}`); code != http.StatusBadRequest {
		t.Error("subscription on a temis connector: want 400")
	}
	// Invalid JSON → 400.
	if code, _ := do(http.MethodPost, base, `{not json`); code != http.StatusBadRequest {
		t.Error("invalid JSON: want 400")
	}

	// A valid create, then list, patch, and delete.
	code, sb := do(http.MethodPost, base, `{"watchedSubject":"orders/new","messageName":"orderEvent","recursive":true}`)
	if code != http.StatusOK {
		t.Fatalf("create: %d %s", code, sb)
	}
	var sub inboundSubscription
	_ = json.Unmarshal(sb, &sub)

	if code, lb := do(http.MethodGet, base, ""); code != http.StatusOK || !strings.Contains(string(lb), `"messageName":"orderEvent"`) {
		t.Fatalf("list: %d %s", code, lb)
	}
	if code, _ := do(http.MethodPatch, "/api/v1/inbound-subscriptions/"+sub.ID, `{"enabled":false,"correlationKey":"= orderId"}`); code != http.StatusOK {
		t.Error("patch: want 200")
	}
	if code, _ := do(http.MethodPatch, "/api/v1/inbound-subscriptions/"+sub.ID, `{"correlationKey":"= ("}`); code != http.StatusBadRequest {
		t.Error("patch with bad FEEL: want 400")
	}
	if code, _ := do(http.MethodPatch, "/api/v1/inbound-subscriptions/missing", `{"enabled":true}`); code != http.StatusNotFound {
		t.Error("patch missing: want 404")
	}
	if code, _ := do(http.MethodDelete, "/api/v1/inbound-subscriptions/"+sub.ID, ""); code != http.StatusNoContent {
		t.Error("delete: want 204")
	}
}

// TestInboundSubStore covers the durable store directly (save/get/loadAll/delete and
// the load ordering).
func TestInboundSubStore(t *testing.T) {
	st, err := newInboundSubStore(filepath.Join(t.TempDir(), "subs"))
	if err != nil {
		t.Fatalf("newInboundSubStore: %v", err)
	}
	a := inboundSubscription{ID: "a", ConnectorID: "c", WatchedSubject: "s", MessageName: "m", Enabled: true, CreatedAt: 2}
	b := inboundSubscription{ID: "b", ConnectorID: "c", WatchedSubject: "s2", MessageName: "m2", Enabled: true, CreatedAt: 1}
	for _, rec := range []inboundSubscription{a, b} {
		if err := st.save(rec); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	got, ok, err := st.get("a")
	if err != nil || !ok || got.MessageName != "m" {
		t.Fatalf("get = %+v, %v, %v", got, ok, err)
	}
	all, err := st.loadAll()
	if err != nil || len(all) != 2 || all[0].ID != "b" { // earliest CreatedAt first
		t.Fatalf("loadAll = %+v, %v", all, err)
	}
	if err := st.delete("a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := st.get("a"); ok {
		t.Fatal("get after delete: still present")
	}
	if err := st.delete("a"); err != nil {
		t.Fatalf("delete idempotent: %v", err)
	}
}
