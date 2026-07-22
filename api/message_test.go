package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// messageBPMN is a process that waits at a message intermediate catch event whose
// correlation key is the FEEL expression "= orderId" over its start variables.
const messageBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <message id="Msg_go" name="go">
    <extensionElements><zeebe:subscription correlationKey="= orderId"/></extensionElements>
  </message>
  <process id="waiter" isExecutable="true">
    <startEvent id="start"/>
    <intermediateCatchEvent id="wait">
      <messageEventDefinition messageRef="Msg_go"/>
    </intermediateCatchEvent>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="wait"/>
    <sequenceFlow id="f2" sourceRef="wait" targetRef="end"/>
  </process>
</definitions>`

// TestPublishMessageCorrelates deploys a message-catching process, parks an
// instance at the catch event, then publishes a matching message over HTTP and
// sees the instance run to completion.
func TestPublishMessageCorrelates(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", messageBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}

	// Start an instance with orderId=order-1; it parks at the catch event.
	code, body = doReq(t, ts, http.MethodPost,
		fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key),
		`{"variables":{"orderId":"order-1"}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create instance status=%d body=%s", code, body)
	}
	var ci struct {
		Stats struct {
			ActiveProcessInstances int `json:"activeProcessInstances"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(body, &ci); err != nil {
		t.Fatalf("decode create: %v (%s)", err, body)
	}
	if ci.Stats.ActiveProcessInstances != 1 {
		t.Fatalf("after start: active=%d, want 1 (waiting at catch)", ci.Stats.ActiveProcessInstances)
	}

	// A non-matching key is accepted but correlates nothing.
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/messages",
		`{"name":"go","correlationKey":"order-999"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("publish (miss) status=%d body=%s", code, body)
	}
	var miss struct {
		Stats struct {
			ActiveProcessInstances int `json:"activeProcessInstances"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(body, &miss); err != nil {
		t.Fatalf("decode publish: %v (%s)", err, body)
	}
	if miss.Stats.ActiveProcessInstances != 1 {
		t.Fatalf("after non-matching publish: active=%d, want 1", miss.Stats.ActiveProcessInstances)
	}

	// The matching key correlates the waiting instance, which then completes.
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/messages",
		`{"name":"go","correlationKey":"order-1"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("publish status=%d body=%s", code, body)
	}
	var pub struct {
		Stats struct {
			ActiveProcessInstances int `json:"activeProcessInstances"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(body, &pub); err != nil {
		t.Fatalf("decode publish: %v (%s)", err, body)
	}
	if pub.Stats.ActiveProcessInstances != 0 {
		t.Fatalf("after matching publish: active=%d, want 0 (instance completed)", pub.Stats.ActiveProcessInstances)
	}
}

// TestPublishMessageRequiresName rejects a publish with no message name.
func TestPublishMessageRequiresName(t *testing.T) {
	ts := newTestServer(t)
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/messages", `{"correlationKey":"x"}`, "application/json"); code != http.StatusBadRequest {
		t.Fatalf("publish without name: status=%d, want 400", code)
	}
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/messages", `not json`, "application/json"); code != http.StatusBadRequest {
		t.Fatalf("publish invalid json: status=%d, want 400", code)
	}
	// A non-scalar payload variable is rejected by parseStartVariables.
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/messages", `{"name":"go","variables":{"x":[1,2]}}`, "application/json"); code != http.StatusBadRequest {
		t.Fatalf("publish with non-scalar variable: status=%d, want 400", code)
	}
}
