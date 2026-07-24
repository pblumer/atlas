package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// dataObjectBPMN parks an instance on a long timer so it stays active, and
// declares two data objects — one with an initial data state, one a nameless
// collection — so the data-objects endpoint has something to surface (ADR-0053).
const dataObjectBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="withdata" name="With Data" isExecutable="true">
    <dataObject id="DataObject_order" name="order" isCollection="false">
      <dataState name="received"/>
    </dataObject>
    <dataObject id="DataObject_items" isCollection="true"/>
    <startEvent id="s"/>
    <intermediateCatchEvent id="w"><timerEventDefinition><timeDuration>PT3600S</timeDuration></timerEventDefinition></intermediateCatchEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="w"/>
    <sequenceFlow id="f2" sourceRef="w" targetRef="e"/>
  </process>
</definitions>`

// TestInstanceDataObjects deploys a process with data objects, starts an
// instance, and reads its data objects back — each with its name, declared data
// state, and (unset) value — so an operator sees the data the process carries and
// what state it is in (ADR-0053).
func TestInstanceDataObjects(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", dataObjectBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, b)
	}

	// Find the running instance's key.
	_, body = doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	var instances []struct {
		Key   uint64 `json:"key"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(body, &instances); err != nil {
		t.Fatalf("decode instances: %v", err)
	}
	var key uint64
	for _, in := range instances {
		if in.State == "active" {
			key = in.Key
		}
	}
	if key == 0 {
		t.Fatal("no active instance found")
	}

	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/data-objects", key), "", "")
	if code != http.StatusOK {
		t.Fatalf("get data objects: status=%d body=%s", code, body)
	}
	var got []struct {
		Name  string `json:"name"`
		State string `json:"state"`
		Value any    `json:"value"`
		Kind  string `json:"kind"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode data objects: %v (%s)", err, body)
	}
	// Scanned in name order: DataObject_items (id fallback), then order.
	if len(got) != 2 {
		t.Fatalf("data objects = %d, want 2 (%s)", len(got), body)
	}
	byName := map[string]struct {
		state string
		value any
		kind  string
	}{}
	for _, d := range got {
		byName[d.Name] = struct {
			state string
			value any
			kind  string
		}{d.State, d.Value, d.Kind}
	}
	order, ok := byName["order"]
	if !ok {
		t.Fatalf("no 'order' data object (%s)", body)
	}
	if order.state != "received" {
		t.Errorf("order.state = %q, want received", order.state)
	}
	if order.kind != "null" || order.value != nil {
		t.Errorf("order value = %v kind = %q, want null/nil (unset until associations)", order.value, order.kind)
	}
	if _, ok := byName["DataObject_items"]; !ok {
		t.Errorf("nameless collection not surfaced under its id fallback (%s)", body)
	}
}

// TestInstanceDataObjectsEmpty covers an instance (or unknown key) with no data
// objects: the endpoint returns an empty array, not an error — a convenience read,
// not an existence check, mirroring the variables endpoint.
func TestInstanceDataObjectsEmpty(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/instances/999/data-objects", "", "")
	if code != http.StatusOK {
		t.Fatalf("get data objects for unknown instance: status=%d body=%s", code, body)
	}
	if got := string(bytes.TrimSpace(body)); got != "[]" {
		t.Errorf("body = %s, want []", got)
	}
}

// TestInstanceDataObjectsInvalidKey rejects a non-numeric instance key.
func TestInstanceDataObjectsInvalidKey(t *testing.T) {
	ts := newTestServer(t)
	code, _ := doReq(t, ts, http.MethodGet, "/api/v1/instances/not-a-number/data-objects", "", "")
	if code != http.StatusBadRequest {
		t.Errorf("non-numeric key: %d, want 400", code)
	}
}
