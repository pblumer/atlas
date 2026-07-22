package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// collabBPMN is a semantic-only (no diagram) two-pool collaboration: the Buyer
// pool throws an "order" message correlated on orderId, the Seller pool catches
// it and finishes. There is no BPMN-DI, so the XML endpoint must auto-lay-out the
// pools for the viewer.
const collabBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <collaboration id="Collab_1">
    <participant id="P_buyer" name="Buyer" processRef="buyer"/>
    <participant id="P_seller" name="Seller" processRef="seller"/>
  </collaboration>
  <message id="Msg_order" name="order">
    <extensionElements><zeebe:subscription correlationKey="= orderId"/></extensionElements>
  </message>
  <process id="buyer" isExecutable="true">
    <startEvent id="b_start"/>
    <intermediateThrowEvent id="b_throw"><messageEventDefinition messageRef="Msg_order"/></intermediateThrowEvent>
    <endEvent id="b_end"/>
    <sequenceFlow id="bf1" sourceRef="b_start" targetRef="b_throw"/>
    <sequenceFlow id="bf2" sourceRef="b_throw" targetRef="b_end"/>
  </process>
  <process id="seller" isExecutable="true">
    <startEvent id="s_start"/>
    <intermediateCatchEvent id="s_catch"><messageEventDefinition messageRef="Msg_order"/></intermediateCatchEvent>
    <endEvent id="s_end"/>
    <sequenceFlow id="sf1" sourceRef="s_start" targetRef="s_catch"/>
    <sequenceFlow id="sf2" sourceRef="s_catch" targetRef="s_end"/>
  </process>
</definitions>`

type collabDeployResp struct {
	Deployments []struct {
		Key       uint64 `json:"key"`
		ProcessID string `json:"processId"`
		Name      string `json:"name"`
		Version   int32  `json:"version"`
	} `json:"deployments"`
}

// TestDeployCollaborationRunsBothPools deploys a collaboration (two pools),
// confirms both processes register with their pool names, and drives the message
// flow between them: the Seller instance parks at its catch event and the Buyer
// instance's throw correlates it to completion.
func TestDeployCollaborationRunsBothPools(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", collabBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep collabDeployResp
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	if len(dep.Deployments) != 2 {
		t.Fatalf("deployments = %d, want 2 (one per pool): %s", len(dep.Deployments), body)
	}
	keyByProc := map[string]uint64{}
	nameByProc := map[string]string{}
	for _, d := range dep.Deployments {
		keyByProc[d.ProcessID] = d.Key
		nameByProc[d.ProcessID] = d.Name
	}
	if nameByProc["buyer"] != "Buyer" || nameByProc["seller"] != "Seller" {
		t.Fatalf("pool names = %v, want buyer=Buyer seller=Seller", nameByProc)
	}

	start := func(procKey uint64) {
		code, body := doReq(t, ts, http.MethodPost,
			fmt.Sprintf("/api/v1/processes/%d/instances", procKey),
			`{"variables":{"orderId":"order-1"}}`, "application/json")
		if code != http.StatusOK {
			t.Fatalf("start %d: status=%d body=%s", procKey, code, body)
		}
	}

	// Seller first: it subscribes and waits at its message catch event.
	start(keyByProc["seller"])
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/stats", "", "")
	var st struct {
		ActiveProcessInstances int `json:"activeProcessInstances"`
	}
	_ = json.Unmarshal(body, &st)
	if st.ActiveProcessInstances != 1 {
		t.Fatalf("after seller start: active=%d, want 1 (waiting at catch)", st.ActiveProcessInstances)
	}

	// Buyer next: its throw publishes "order"/order-1, correlating the seller; both
	// instances run to completion.
	start(keyByProc["buyer"])
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/stats", "", "")
	_ = json.Unmarshal(body, &st)
	if st.ActiveProcessInstances != 0 {
		t.Fatalf("after buyer start: active=%d, want 0 (message flow correlated both pools)", st.ActiveProcessInstances)
	}
}

// TestCollaborationXMLGetsPoolLayout confirms the XML endpoint injects diagram
// interchange for a DI-less collaboration: a plane bound to the collaboration and
// a horizontal pool shape per participant, so the viewer can render it.
func TestCollaborationXMLGetsPoolLayout(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", collabBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep collabDeployResp
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode: %v", err)
	}

	code, xmlBody := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/processes/%d/xml", dep.Deployments[0].Key), "", "")
	if code != http.StatusOK {
		t.Fatalf("xml status=%d", code)
	}
	x := string(xmlBody)
	for _, want := range []string{
		"BPMNDiagram",
		`bpmnElement="Collab_1"`, // plane bound to the collaboration
		`bpmnElement="P_buyer"`,  // a pool shape per participant
		`bpmnElement="P_seller"`,
		`isHorizontal="true"`,
	} {
		if !strings.Contains(x, want) {
			t.Fatalf("generated collaboration XML missing %q:\n%s", want, x)
		}
	}
}
