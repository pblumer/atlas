package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// waitForOrderBPMN parks at a message catch on "order", keyed by orderId.
const waitForOrderBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <message id="Msg_order" name="order">
    <extensionElements><zeebe:subscription correlationKey="= orderId"/></extensionElements>
  </message>
  <process id="waiter" isExecutable="true">
    <startEvent id="s"/>
    <intermediateCatchEvent id="c"><messageEventDefinition messageRef="Msg_order"/></intermediateCatchEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="c"/>
    <sequenceFlow id="f2" sourceRef="c" targetRef="e"/>
  </process>
</definitions>`

// newServerWith is newServerForErrors with server options.
func newServerWith(t *testing.T, opts ...Option) *Server {
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

func deployInternal(t *testing.T, srv *Server, bpmn string) uint64 {
	t.Helper()
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", bpmn, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var d struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	return d.Key
}

func bufferedCount(t *testing.T, srv *Server) int {
	t.Helper()
	n := 0
	srv.do(func() {
		if err := srv.store.BufferedMessages(func(uint64, *model.MessageValue) error { n++; return nil }); err != nil {
			t.Errorf("BufferedMessages: %v", err)
		}
	})
	return n
}

// A message published over HTTP with timeToLiveMs before anything waits for it is
// kept, and the instance that starts waiting afterwards receives it and finishes
// (ADR-0370). The answer echoes what was asked for.
func TestAPublishWithATimeToLiveWaitsForItsInstance(t *testing.T) {
	srv := newServerForErrors(t)
	defKey := deployInternal(t, srv, waitForOrderBPMN)

	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/messages",
		`{"name":"order","correlationKey":"42","timeToLiveMs":60000,"messageId":"m-1","variables":{"note":"early"}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("publish: status=%d body=%s", code, body)
	}
	var resp publishMessageResp
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if resp.TimeToLiveMs != 60000 || resp.MessageId != "m-1" {
		t.Fatalf("answer = %+v, want the time to live and id echoed", resp)
	}
	if n := bufferedCount(t, srv); n != 1 {
		t.Fatalf("buffered after publish = %d, want 1", n)
	}

	path := fmt.Sprintf("/api/v1/processes/%d/instances", defKey)
	if code, body = serveInternal(t, srv, http.MethodPost, path, `{"variables":{"orderId":42}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}
	var running int
	srv.do(func() {
		_ = srv.store.ActiveProcessInstances(func(uint64, *model.ProcessInstanceValue) error { running++; return nil })
	})
	if running != 0 {
		t.Fatalf("instances still running = %d, want 0 (the buffered message reached the catch)", running)
	}
	if n := bufferedCount(t, srv); n != 0 {
		t.Fatalf("buffered after delivery = %d, want 0", n)
	}
}

// What a publish may ask for is checked before anything reaches the engine.
func TestAPublishsTimeToLiveIsChecked(t *testing.T) {
	srv := newServerWith(t, WithMessageTTLMax(time.Minute))
	for _, c := range []struct {
		body string
		code int
		says string
	}{
		{`{"name":"order","timeToLiveMs":-1}`, http.StatusBadRequest, "negative"},
		{`{"name":"order","timeToLiveMs":60001}`, http.StatusBadRequest, "--message-ttl-max"},
		{`{"name":"order","messageId":"m-1"}`, http.StatusBadRequest, "messageId needs a timeToLiveMs"},
		{`{"name":"order","timeToLiveMs":60000}`, http.StatusOK, ""},
	} {
		code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/messages", c.body, "application/json")
		if code != c.code || !strings.Contains(string(body), c.says) {
			t.Errorf("%s: status=%d body=%s, want %d naming %q", c.body, code, body, c.code, c.says)
		}
	}
	if n := bufferedCount(t, srv); n != 1 {
		t.Fatalf("buffered = %d, want only the accepted publish", n)
	}
}

// A message that may be kept must not carry a value any deployed process declares
// personal: nothing on the message path can seal it, so it would wait readable in the
// buffer (ADR-0314, ADR-0370 amended 2026-10-13). Without a time to live nothing is
// kept, and the publish is answered as before.
func TestAKeptMessageMayNotCarryPersonalData(t *testing.T) {
	srv := newServerForErrors(t)
	deployInternal(t, srv, personalBPMN)

	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/messages",
		`{"name":"order","timeToLiveMs":60000,"variables":{"vorname":"Ida"}}`, "application/json")
	if code != http.StatusUnprocessableEntity || !strings.Contains(string(body), "vorname") || !strings.Contains(string(body), "order") {
		t.Fatalf("kept message with a personal value: status=%d body=%s, want 422 naming vorname and the process", code, body)
	}
	if n := bufferedCount(t, srv); n != 0 {
		t.Fatalf("buffered = %d, want nothing kept", n)
	}

	code, body = serveInternal(t, srv, http.MethodPost, "/api/v1/messages",
		`{"name":"order","timeToLiveMs":60000,"variables":{"note":"not personal"}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("kept message without a personal value: status=%d body=%s, want 200", code, body)
	}
	if code, body = serveInternal(t, srv, http.MethodPost, "/api/v1/messages",
		`{"name":"order","variables":{"vorname":"Ida"}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("a publish without a time to live: status=%d body=%s, want 200 as before", code, body)
	}
}
