package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// replayBPMN is the Kunde/Lieferant collaboration the replay view is built for.
// The Kunde throws "order" (which a message start event opens the Lieferant with)
// then waits for "confirm"; the Lieferant throws "confirm" back, correlated on the
// order id. It exercises both flow-recording deliveries: a message start ("order"
// into l_start) and an intermediate catch ("confirm" into k_catch).
const replayBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <collaboration id="Collab_1">
    <participant id="P_kunde" name="Kunde" processRef="kunde"/>
    <participant id="P_lieferant" name="Lieferant" processRef="lieferant"/>
  </collaboration>
  <message id="Msg_order" name="order">
    <extensionElements><zeebe:subscription correlationKey="= orderId"/></extensionElements>
  </message>
  <message id="Msg_confirm" name="confirm">
    <extensionElements><zeebe:subscription correlationKey="= orderId"/></extensionElements>
  </message>
  <process id="kunde" isExecutable="true">
    <startEvent id="k_start"/>
    <intermediateThrowEvent id="k_throw"><messageEventDefinition messageRef="Msg_order"/></intermediateThrowEvent>
    <intermediateCatchEvent id="k_catch"><messageEventDefinition messageRef="Msg_confirm"/></intermediateCatchEvent>
    <endEvent id="k_end"/>
    <sequenceFlow id="kf1" sourceRef="k_start" targetRef="k_throw"/>
    <sequenceFlow id="kf2" sourceRef="k_throw" targetRef="k_catch"/>
    <sequenceFlow id="kf3" sourceRef="k_catch" targetRef="k_end"/>
  </process>
  <process id="lieferant" isExecutable="true">
    <startEvent id="l_start"><messageEventDefinition messageRef="Msg_order"/></startEvent>
    <intermediateThrowEvent id="l_throw"><messageEventDefinition messageRef="Msg_confirm"/></intermediateThrowEvent>
    <endEvent id="l_end"/>
    <sequenceFlow id="lf1" sourceRef="l_start" targetRef="l_throw"/>
    <sequenceFlow id="lf2" sourceRef="l_throw" targetRef="l_end"/>
  </process>
</definitions>`

type replayProc struct {
	Key              uint64 `json:"key"`
	ProcessID        string `json:"processId"`
	CollaborationKey uint64 `json:"collaborationKey"`
}

type collabRuntime struct {
	Pools []struct {
		Key       uint64 `json:"key"`
		ProcessID string `json:"processId"`
		Name      string `json:"name"`
	} `json:"pools"`
	Instances int `json:"instances"`
	Tokens    int `json:"tokens"`
	Elements  []struct {
		ElementID string `json:"elementId"`
		Visits    int    `json:"visits"`
	} `json:"elements"`
	MessageFlows []struct {
		At                int64  `json:"at"`
		MessageName       string `json:"messageName"`
		CorrelationKey    string `json:"correlationKey"`
		ReceiverElementID string `json:"receiverElementId"`
		SenderInstance    uint64 `json:"senderInstance"`
		ReceiverInstance  uint64 `json:"receiverInstance"`
	} `json:"messageFlows"`
}

// TestCollaborationReplayRuntime deploys the Kunde/Lieferant collaboration, runs
// the whole exchange, and checks the collaboration runtime endpoint: both pools
// are reported, the element overlay merges both pools, and the message-flow
// timeline lists "order" then "confirm" naming their receiving diagram elements
// (ADR-0038).
func TestCollaborationReplayRuntime(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", replayBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep collabDeployResp
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	keyByProc := map[string]uint64{}
	for _, d := range dep.Deployments {
		keyByProc[d.ProcessID] = d.Key
	}
	kundeKey := keyByProc["kunde"]
	if kundeKey == 0 || keyByProc["lieferant"] == 0 {
		t.Fatalf("missing pool keys: %v", keyByProc)
	}

	// The processes list flags both pools as a collaboration, pointing at the same
	// group key (the smaller pool key).
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/processes", "", "")
	var procs []replayProc
	if err := json.Unmarshal(body, &procs); err != nil {
		t.Fatalf("decode processes: %v (%s)", err, body)
	}
	group := kundeKey
	if keyByProc["lieferant"] < group {
		group = keyByProc["lieferant"]
	}
	for _, p := range procs {
		if p.ProcessID == "kunde" || p.ProcessID == "lieferant" {
			if p.CollaborationKey != group {
				t.Errorf("%s collaborationKey = %d, want %d", p.ProcessID, p.CollaborationKey, group)
			}
		}
	}

	// Start the Kunde with an order id; its throw opens the Lieferant (message
	// start), which confirms back, and both instances finish in one drain.
	code, body = doReq(t, ts, http.MethodPost,
		fmt.Sprintf("/api/v1/processes/%d/instances", kundeKey),
		`{"variables":{"orderId":"order-1"}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("start kunde: status=%d body=%s", code, body)
	}

	// Ask for the collaboration runtime via either pool key — siblings are found
	// from the shared XML.
	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/collaborations/%d/runtime", kundeKey), "", "")
	if code != http.StatusOK {
		t.Fatalf("collaboration runtime: status=%d body=%s", code, body)
	}
	var rt collabRuntime
	if err := json.Unmarshal(body, &rt); err != nil {
		t.Fatalf("decode runtime: %v (%s)", err, body)
	}

	if len(rt.Pools) != 2 {
		t.Fatalf("pools = %d, want 2: %s", len(rt.Pools), body)
	}
	if rt.Instances != 0 || rt.Tokens != 0 {
		t.Errorf("after exchange: instances=%d tokens=%d, want 0 and 0", rt.Instances, rt.Tokens)
	}

	// The merged overlay carries elements from both pools (a Kunde element and a
	// Lieferant element both show visits).
	visits := map[string]int{}
	for _, e := range rt.Elements {
		visits[e.ElementID] = e.Visits
	}
	if visits["k_throw"] == 0 || visits["l_throw"] == 0 {
		t.Errorf("merged visits missing a pool: k_throw=%d l_throw=%d", visits["k_throw"], visits["l_throw"])
	}

	// The replay timeline: "order" into the Lieferant's start, then "confirm" into
	// the Kunde's catch, in that order, each carrying the correlation key.
	if len(rt.MessageFlows) != 2 {
		t.Fatalf("message flows = %d, want 2: %s", len(rt.MessageFlows), body)
	}
	order, confirm := rt.MessageFlows[0], rt.MessageFlows[1]
	if order.MessageName != "order" || order.ReceiverElementID != "l_start" || order.CorrelationKey != "order-1" {
		t.Errorf("first flow = %+v, want order → l_start key order-1", order)
	}
	if confirm.MessageName != "confirm" || confirm.ReceiverElementID != "k_catch" || confirm.CorrelationKey != "order-1" {
		t.Errorf("second flow = %+v, want confirm → k_catch key order-1", confirm)
	}
	if order.At > confirm.At {
		t.Errorf("timeline out of order: order.at=%d confirm.at=%d", order.At, confirm.At)
	}
	// The confirm returns to the Kunde instance that sent the order.
	if confirm.ReceiverInstance == 0 || confirm.ReceiverInstance != order.SenderInstance {
		t.Errorf("confirm.receiver=%d should equal order.sender=%d (the Kunde instance)", confirm.ReceiverInstance, order.SenderInstance)
	}
}

// TestCollaborationRuntimeStandalone confirms the endpoint degrades gracefully for
// a plain (non-collaboration) process: it reports the single definition as the
// lone pool with no message flows, and the process is not flagged as a
// collaboration in the process list.
func TestCollaborationRuntimeStandalone(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", timerWaitBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode: %v", err)
	}

	code, body = doReq(t, ts, http.MethodGet, "/api/v1/processes", "", "")
	var procs []replayProc
	if err := json.Unmarshal(body, &procs); err != nil {
		t.Fatalf("decode processes: %v", err)
	}
	for _, p := range procs {
		if p.Key == dep.Key && p.CollaborationKey != 0 {
			t.Errorf("standalone process collaborationKey = %d, want 0", p.CollaborationKey)
		}
	}

	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/collaborations/%d/runtime", dep.Key), "", "")
	if code != http.StatusOK {
		t.Fatalf("collaboration runtime: status=%d body=%s", code, body)
	}
	var rt collabRuntime
	if err := json.Unmarshal(body, &rt); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rt.Pools) != 1 || len(rt.MessageFlows) != 0 {
		t.Errorf("standalone runtime: pools=%d flows=%d, want 1 and 0", len(rt.Pools), len(rt.MessageFlows))
	}
}

// TestCollaborationRuntimeErrors covers the endpoint's request-error branches: a
// non-numeric key is a 400, and a well-formed but unknown key is a 404.
func TestCollaborationRuntimeErrors(t *testing.T) {
	ts := newTestServer(t)
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/collaborations/not-a-number/runtime", "", ""); code != http.StatusBadRequest {
		t.Errorf("bad key status = %d, want 400", code)
	}
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/collaborations/999999/runtime", "", ""); code != http.StatusNotFound {
		t.Errorf("unknown key status = %d, want 404", code)
	}
}

// TestCollaborationRuntimeActiveTokens starts one pool that parks at its message
// catch event, then checks the collaboration runtime reports the live token and
// running instance across the merged overlay (the active-token branch, distinct
// from the finished-exchange case).
func TestCollaborationRuntimeActiveTokens(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", collabBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep collabDeployResp
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var sellerKey uint64
	for _, d := range dep.Deployments {
		if d.ProcessID == "seller" {
			sellerKey = d.Key
		}
	}
	// The Seller subscribes and parks at its catch event: a live token, no message
	// has flowed yet.
	code, body = doReq(t, ts, http.MethodPost,
		fmt.Sprintf("/api/v1/processes/%d/instances", sellerKey),
		`{"variables":{"orderId":"order-1"}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("start seller: status=%d body=%s", code, body)
	}

	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/collaborations/%d/runtime", sellerKey), "", "")
	if code != http.StatusOK {
		t.Fatalf("runtime status=%d body=%s", code, body)
	}
	var rt collabRuntime
	if err := json.Unmarshal(body, &rt); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rt.Instances != 1 || rt.Tokens < 1 {
		t.Errorf("parked seller: instances=%d tokens=%d, want 1 and >=1", rt.Instances, rt.Tokens)
	}
	if len(rt.MessageFlows) != 0 {
		t.Errorf("message flows = %d, want 0 (no message has flowed yet)", len(rt.MessageFlows))
	}
}

// TestCollaborationRuntimeRedeployKeepsLatestPools redeploys the same
// collaboration and checks that the runtime resolves to the current version's
// pools only — two, not four — so a redeploy does not double the diagram.
func TestCollaborationRuntimeRedeployKeepsLatestPools(t *testing.T) {
	ts := newTestServer(t)
	deploy := func() collabDeployResp {
		code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", replayBPMN, "application/xml")
		if code != http.StatusOK {
			t.Fatalf("deploy status=%d body=%s", code, body)
		}
		var dep collabDeployResp
		if err := json.Unmarshal(body, &dep); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return dep
	}
	deploy()
	dep2 := deploy() // v2 of both pools, identical XML

	code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/collaborations/%d/runtime", dep2.Deployments[0].Key), "", "")
	if code != http.StatusOK {
		t.Fatalf("runtime status=%d body=%s", code, body)
	}
	var rt collabRuntime
	if err := json.Unmarshal(body, &rt); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rt.Pools) != 2 {
		t.Errorf("pools after redeploy = %d, want 2 (latest version only): %s", len(rt.Pools), body)
	}
}
