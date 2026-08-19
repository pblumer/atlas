package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/clio"
)

// TestInboundBridgeLive runs the bridge's ticker goroutine (not pollInbound
// directly): with a short poll interval a configured subscription's clio event is
// picked up and starts a process without any manual poll, exercising the
// inboundBridge loop and its shutdown on Close.
func TestInboundBridgeLive(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(15*time.Millisecond))
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
		`{"watchedSubject":"orders/new","messageName":"orderEvent","startFromTip":false}`); code != http.StatusOK {
		t.Fatalf("create subscription: %d %s", code, sb)
	}
	srv.do(func() {
		srv.clioRegistry.Replace(map[string]clio.Client{"events": &fakeClioReader{events: []clio.InboundEvent{
			{ID: "1", Subject: "orders/new", Type: "OrderPlaced", Data: map[string]any{"orderId": "o-1"}},
		}}})
	})
	// The ticker fires within a few intervals; wait until the bridge starts the process.
	deadline := time.Now().Add(3 * time.Second)
	for activeInstances(t, srv) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("bridge did not start a process within the deadline")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

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

// fakeClioReader serves a fixed event list, honoring the exclusive AfterID cursor,
// and records the last read request so a test can assert the recursive/subject flags.
type fakeClioReader struct {
	events  []clio.InboundEvent
	lastReq clio.ReadEventsRequest
	err     error // when set, ReadEvents fails (simulating a transient clio read error)
}

func (f *fakeClioReader) WriteEvent(context.Context, clio.Event) error { return nil }
func (f *fakeClioReader) GetState(context.Context, string, string) (map[string]any, error) {
	return nil, nil
}
func (f *fakeClioReader) Query(context.Context, string, string) (any, error) { return nil, nil }
func (f *fakeClioReader) ReadEvents(_ context.Context, r clio.ReadEventsRequest) ([]clio.InboundEvent, error) {
	f.lastReq = r
	if f.err != nil {
		return nil, f.err
	}
	seen := r.AfterID == ""
	var out []clio.InboundEvent
	for _, e := range f.events {
		if seen {
			out = append(out, e)
			if r.Limit > 0 && len(out) >= r.Limit {
				break // honor clio's Limit: a poll reads at most this many events
			}
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

// TestInboundBridgeStartsAndDedupes is the ADR-0075 vertical slice at the server
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
		`{"watchedSubject":"orders/new","messageName":"orderEvent","correlationKey":"= orderId","startFromTip":false}`); code != http.StatusOK {
		t.Fatalf("create subscription: %d %s", code, sb)
	}

	// Inject a fake clio client so the bridge reads canned events (no live clio).
	fake := &fakeClioReader{events: []clio.InboundEvent{
		{ID: "1", Subject: "orders/new", Type: "OrderPlaced", Data: map[string]any{"orderId": "o-1"}},
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
	fake.events = append(fake.events, clio.InboundEvent{ID: "2", Subject: "orders/new", Type: "OrderPlaced", Data: map[string]any{"orderId": "o-2"}})
	srv.pollInbound(context.Background())
	if n := activeInstances(t, srv); n != 2 {
		t.Fatalf("after new event: active=%d, want 2", n)
	}
}

// TestInboundBridgeBatchLimitBoundsCatchUp proves the per-poll ReadEvents cap
// (WithInboundBatchLimit) bounds how many backlogged events one poll republishes, so
// a watch pointed at a subject with a large backlog drains as bounded catch-up across
// ticks instead of starting every backlogged event's process in one run-loop batch —
// the reported /employees flood (ADR-0075).
func TestInboundBridgeBatchLimitBoundsCatchUp(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0), WithInboundBatchLimit(2))
	x := deployTestHarness{t, srv.Handler()}

	pid := x.mkProject("Onboard")
	x.saveDraft(pid, employeeStartBPMN)
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
		`{"watchedSubject":"/employees","recursive":true,"messageName":"employee.created","startFromTip":false}`); code != http.StatusOK {
		t.Fatalf("create subscription: %d %s", code, sb)
	}

	// Five backlogged events on the watched subtree.
	fake := &fakeClioReader{}
	for i := 1; i <= 5; i++ {
		fake.events = append(fake.events, clio.InboundEvent{
			ID: strconv.Itoa(i), Subject: "/employees/E-" + strconv.Itoa(i), Type: "employee.created",
		})
	}
	srv.do(func() { srv.clioRegistry.Replace(map[string]clio.Client{"events": fake}) })

	// Each poll republishes at most the cap (2), advancing the cursor; the backlog
	// drains 2, 2, 1 across ticks — never all five in one batch.
	for _, want := range []int{2, 4, 5, 5} {
		srv.pollInbound(context.Background())
		if n := activeInstances(t, srv); n != want {
			t.Fatalf("active after poll = %d, want %d (bounded catch-up)", n, want)
		}
	}
	if fake.lastReq.Limit != 2 {
		t.Fatalf("ReadEvents Limit = %d, want 2 (per-poll cap forwarded to clio)", fake.lastReq.Limit)
	}
}

// TestInboundBridgeStartFromTipSkipsBacklog proves a new subscription defaults to
// forward-only: the bridge primes its cursor past the existing backlog WITHOUT
// republishing it, so connecting a watch to a subject that already has history does
// not start a process per historical event (the reported /employees flood). Only
// events that arrive after priming start processes.
func TestInboundBridgeStartFromTipSkipsBacklog(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	x := deployTestHarness{t, srv.Handler()}

	pid := x.mkProject("Onboard")
	x.saveDraft(pid, employeeStartBPMN)
	if code, b := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", ""); code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, b)
	}
	code, cb := x.do(http.MethodPost, "/api/v1/connectors", `{"name":"events","kind":"clio","endpoint":"http://x"}`)
	if code != http.StatusOK {
		t.Fatalf("create connector: %d %s", code, cb)
	}
	var conn connector
	_ = json.Unmarshal(cb, &conn)
	// No startFromTip in the body → the forward-only default applies.
	code, sb := x.do(http.MethodPost, "/api/v1/connectors/"+conn.ID+"/inbound-subscriptions",
		`{"watchedSubject":"/employees","recursive":true,"messageName":"employee.created"}`)
	if code != http.StatusOK {
		t.Fatalf("create subscription: %d %s", code, sb)
	}
	var sub inboundSubscription
	_ = json.Unmarshal(sb, &sub)
	if !sub.StartFromTip {
		t.Fatalf("new subscription StartFromTip = false, want true (forward-only default)")
	}

	// A pre-existing backlog on the watched subtree.
	fake := &fakeClioReader{}
	for i := 1; i <= 3; i++ {
		fake.events = append(fake.events, clio.InboundEvent{
			ID: strconv.Itoa(i), Subject: "/employees/E-" + strconv.Itoa(i), Type: "employee.created",
		})
	}
	srv.do(func() { srv.clioRegistry.Replace(map[string]clio.Client{"events": fake}) })

	// Priming jumps the cursor to the tip, starting nothing.
	srv.pollInbound(context.Background())
	if n := activeInstances(t, srv); n != 0 {
		t.Fatalf("after priming: active=%d, want 0 (backlog skipped, not replayed)", n)
	}
	primed, cursor := inboundPrimeState(t, srv, sub.ID)
	if !primed || cursor != "3" {
		t.Fatalf("after priming: primed=%v cursor=%q, want true/\"3\"", primed, cursor)
	}

	// A genuinely new event (after the tip) now starts a process.
	fake.events = append(fake.events, clio.InboundEvent{ID: "4", Subject: "/employees/E-4", Type: "employee.created"})
	srv.pollInbound(context.Background())
	if n := activeInstances(t, srv); n != 1 {
		t.Fatalf("after new event: active=%d, want 1 (forward events still start)", n)
	}
}

// TestInboundBridgePrimesAcrossPolls proves a backlog larger than one priming page
// is skipped over several polls: each poll advances the cursor by a page and only a
// short page flips the subscription to primed — still starting nothing.
func TestInboundBridgePrimesAcrossPolls(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	x := deployTestHarness{t, srv.Handler()}

	pid := x.mkProject("Onboard")
	x.saveDraft(pid, employeeStartBPMN)
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
		`{"watchedSubject":"/employees","recursive":true,"messageName":"employee.created"}`)
	if code != http.StatusOK {
		t.Fatalf("create subscription: %d %s", code, sb)
	}
	var sub inboundSubscription
	_ = json.Unmarshal(sb, &sub)

	// Serve exactly one full priming page on the first poll, then the tail: the
	// first poll must not flip primed, the second must.
	full := make([]clio.InboundEvent, inboundPrimeBatch)
	for i := range full {
		full[i] = clio.InboundEvent{ID: strconv.Itoa(i + 1), Subject: "/employees/E", Type: "employee.created"}
	}
	fake := &fakeClioReader{events: full}
	srv.do(func() { srv.clioRegistry.Replace(map[string]clio.Client{"events": fake}) })

	srv.pollInbound(context.Background())
	if primed, _ := inboundPrimeState(t, srv, sub.ID); primed {
		t.Fatalf("after a full page: primed=true, want false (tip not yet reached)")
	}
	// Add a short tail past the full page; the next poll reaches the tip.
	fake.events = append(fake.events, clio.InboundEvent{ID: strconv.Itoa(inboundPrimeBatch + 1), Subject: "/employees/E", Type: "employee.created"})
	srv.pollInbound(context.Background())
	if primed, _ := inboundPrimeState(t, srv, sub.ID); !primed {
		t.Fatalf("after the short tail: primed=false, want true (tip reached)")
	}
	if n := activeInstances(t, srv); n != 0 {
		t.Fatalf("priming a large backlog started %d instances, want 0", n)
	}
}

// TestInboundBridgePrimeResilience covers priming's failure paths: a transient read
// error leaves the subscription un-primed (retried next tick), and a priming step
// for a since-deleted subscription is a harmless no-op.
func TestInboundBridgePrimeResilience(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	x := deployTestHarness{t, srv.Handler()}

	code, cb := x.do(http.MethodPost, "/api/v1/connectors", `{"name":"events","kind":"clio","endpoint":"http://x"}`)
	if code != http.StatusOK {
		t.Fatalf("create connector: %d %s", code, cb)
	}
	var conn connector
	_ = json.Unmarshal(cb, &conn)
	code, sb := x.do(http.MethodPost, "/api/v1/connectors/"+conn.ID+"/inbound-subscriptions",
		`{"watchedSubject":"/employees","recursive":true,"messageName":"employee.created"}`)
	if code != http.StatusOK {
		t.Fatalf("create subscription: %d %s", code, sb)
	}
	var sub inboundSubscription
	_ = json.Unmarshal(sb, &sub)

	fake := &fakeClioReader{err: errClioRead}
	srv.do(func() { srv.clioRegistry.Replace(map[string]clio.Client{"events": fake}) })

	srv.pollInbound(context.Background()) // read errors: nothing advances
	if primed, cursor := inboundPrimeState(t, srv, sub.ID); primed || cursor != "" {
		t.Fatalf("after a failed priming read: primed=%v cursor=%q, want false/\"\"", primed, cursor)
	}

	// A priming step targeting a deleted subscription is a no-op (does not panic).
	srv.do(func() { srv.markInboundPrimed("does-not-exist", "9", true) })
}

var errClioRead = errClio("clio read failed")

type errClio string

func (e errClio) Error() string { return string(e) }

// inboundPrimeState reads a subscription's Primed flag and resume cursor off the run
// loop for assertions.
func inboundPrimeState(t *testing.T, srv *Server, id string) (bool, string) {
	t.Helper()
	var rec inboundSubscription
	srv.do(func() { rec, _, _ = srv.inboundSubs.Get(id) })
	return rec.Primed, rec.LastEventID
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
		`{"watchedSubject":"orders/new","messageName":"orderEvent","startFromTip":false}`)
	if code != http.StatusOK {
		t.Fatalf("create subscription: %d %s", code, sb)
	}
	var sub inboundSubscription
	_ = json.Unmarshal(sb, &sub)

	fake := &fakeClioReader{events: []clio.InboundEvent{
		{ID: "1", Subject: "orders/new", Type: "T", Data: map[string]any{"orderId": "o-1"}},
	}}
	srv.do(func() { srv.clioRegistry.Replace(map[string]clio.Client{"events": fake}) })

	srv.pollInbound(context.Background())
	if n := activeInstances(t, srv); n != 1 {
		t.Fatalf("after first poll: active=%d, want 1", n)
	}

	// Simulate a lost/stale cursor: reset it to empty so the bridge re-reads e1.
	srv.do(func() {
		rec, _, _ := srv.inboundSubs.Get(sub.ID)
		rec.LastEventID = ""
		_ = srv.inboundSubs.Save(rec)
	})
	srv.pollInbound(context.Background())
	if n := activeInstances(t, srv); n != 1 {
		t.Fatalf("after re-reading a re-delivered event: active=%d, want 1 (engine high-water dedupes)", n)
	}
}

// employeeStartBPMN: a message start event named "employee.created" — the reported
// scenario — that begins an instance parking at a keyless catch so it stays observable.
const employeeStartBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <message id="Mstart" name="employee.created"/>
  <message id="Mnever" name="never"/>
  <process id="onEmployee" isExecutable="true">
    <startEvent id="s"><messageEventDefinition messageRef="Mstart"/></startEvent>
    <intermediateCatchEvent id="park"><messageEventDefinition messageRef="Mnever"/></intermediateCatchEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="park"/>
    <sequenceFlow id="f2" sourceRef="park" targetRef="e"/>
  </process>
</definitions>`

// TestInboundBridgeRecursiveSubjectKey is the reported use case end to end: a
// recursive watch on /employees, correlating on the subject's leaf. An event written
// to the child subject /employees/E-123456 (with no employeeId in its body) starts the
// employee.created process; the bridge reads recursively and the started instance
// carries subjectTail = E-123456.
func TestInboundBridgeRecursiveSubjectKey(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	x := deployTestHarness{t, srv.Handler()}

	pid := x.mkProject("Onboard")
	x.saveDraft(pid, employeeStartBPMN)
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
		`{"watchedSubject":"/employees","recursive":true,"messageName":"employee.created","correlationKey":"= subjectTail","startFromTip":false}`); code != http.StatusOK {
		t.Fatalf("create subscription: %d %s", code, sb)
	}

	fake := &fakeClioReader{events: []clio.InboundEvent{
		{ID: "1", Subject: "/employees/E-123456", Type: "employee.created", Data: map[string]any{"firstName": "Ada"}},
	}}
	srv.do(func() { srv.clioRegistry.Replace(map[string]clio.Client{"events": fake}) })

	srv.pollInbound(context.Background())
	if n := activeInstances(t, srv); n != 1 {
		t.Fatalf("active=%d, want 1 (a subtree event started the process)", n)
	}
	if !fake.lastReq.Recursive || fake.lastReq.Subject != "/employees" {
		t.Errorf("read request = %+v, want recursive on subject /employees", fake.lastReq)
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

	// A subscription on a second clio connector must be filtered out of this
	// connector's list (the list is scoped by connector id).
	_, ob := do(http.MethodPost, "/api/v1/connectors", `{"name":"other","kind":"clio","endpoint":"http://z"}`)
	var otherConn connector
	_ = json.Unmarshal(ob, &otherConn)
	_, _ = do(http.MethodPost, "/api/v1/connectors/"+otherConn.ID+"/inbound-subscriptions", `{"watchedSubject":"x","messageName":"otherEvent"}`)

	if code, lb := do(http.MethodGet, base, ""); code != http.StatusOK || !strings.Contains(string(lb), `"messageName":"orderEvent"`) || strings.Contains(string(lb), "otherEvent") {
		t.Fatalf("list (should be scoped to this connector): %d %s", code, lb)
	}
	// A create defaults StartFromTip on (forward-only).
	if !sub.StartFromTip {
		t.Errorf("created subscription StartFromTip = false, want true (default)")
	}
	if code, pb := do(http.MethodPatch, "/api/v1/inbound-subscriptions/"+sub.ID,
		`{"enabled":false,"correlationKey":"= orderId","watchedSubject":"orders/all","messageName":"orderChanged","recursive":false,"startFromTip":false}`); code != http.StatusOK {
		t.Fatalf("patch: %d %s", code, pb)
	} else {
		var up inboundSubscription
		_ = json.Unmarshal(pb, &up)
		if up.WatchedSubject != "orders/all" || up.MessageName != "orderChanged" || up.CorrelationKey != "orderId" || up.Enabled || up.StartFromTip {
			t.Errorf("patch result = %+v, want all fields updated (incl. startFromTip off)", up)
		}
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
		if err := st.Save(rec); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	got, ok, err := st.Get("a")
	if err != nil || !ok || got.MessageName != "m" {
		t.Fatalf("get = %+v, %v, %v", got, ok, err)
	}
	all, err := st.LoadAll()
	if err != nil || len(all) != 2 || all[0].ID != "b" { // earliest CreatedAt first
		t.Fatalf("loadAll = %+v, %v", all, err)
	}
	if err := st.Delete("a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := st.Get("a"); ok {
		t.Fatal("get after delete: still present")
	}
	if err := st.Delete("a"); err != nil {
		t.Fatalf("delete idempotent: %v", err)
	}
}
