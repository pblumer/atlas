package api_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// foreignDefaultBPMN is a model an older build deployed: gw1's default attribute names
// gw2's flow b_no. That build marked b_no as gw2's default, so for x = 1 — where both of
// gw2's conditions are false and gw2 should park with an incident — gw2 routes to "no"
// and the instance completes. ADR-0547 refuses the shape at deploy
// (gateway.default-not-outgoing) and keeps the stored definition exactly as it ran.
const foreignDefaultBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="foreign-default" isExecutable="true">
    <startEvent id="s"/>
    <scriptTask id="x">
      <extensionElements><zeebe:script expression="= 1" resultVariable="x"/></extensionElements>
    </scriptTask>
    <exclusiveGateway id="gw1" default="b_no"/>
    <exclusiveGateway id="gw2"/>
    <endEvent id="yes"/>
    <endEvent id="no"/>
    <endEvent id="other"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="x"/>
    <sequenceFlow id="f2" sourceRef="x" targetRef="gw1"/>
    <sequenceFlow id="a1" sourceRef="gw1" targetRef="gw2"><conditionExpression>= x = 1</conditionExpression></sequenceFlow>
    <sequenceFlow id="a2" sourceRef="gw1" targetRef="other"><conditionExpression>= x = 5</conditionExpression></sequenceFlow>
    <sequenceFlow id="b_no" sourceRef="gw2" targetRef="no"><conditionExpression>= x = 2</conditionExpression></sequenceFlow>
    <sequenceFlow id="b_yes" sourceRef="gw2" targetRef="yes"><conditionExpression>= x = 3</conditionExpression></sequenceFlow>
  </process>
</definitions>`

// TestStoredForbiddenFlowShapeReloadsAndRunsAsBefore: a server holding a definition
// with a shape ADR-0547 now refuses still starts, lists it, and runs it the way the
// build that stored it did. The instance completing is the proof: with the default
// confined to gw1, gw2 would have no flow to take and would park the instance with an
// incident instead. Repairing that on restart would change what running instances do.
func TestStoredForbiddenFlowShapeReloadsAndRunsAsBefore(t *testing.T) {
	dir := t.TempDir()
	seedDeployment(t, dir, 1, "foreign-default", foreignDefaultBPMN)

	s := boot(t, dir) // must not fail: the rule is a deploy gate, not a load condition
	defer s.shutdown()

	code, body := doReq(t, s.ts, "GET", "/api/v1/processes", "", "")
	if code != 200 || !strings.Contains(string(body), `"processId":"foreign-default"`) {
		t.Fatalf("processes after reload: status=%d body=%s", code, body)
	}
	code, body = doReq(t, s.ts, "POST", "/api/v1/processes/1/instances", "", "application/json")
	if code != 200 {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}
	var insts []struct {
		ProcessID string `json:"processId"`
		State     string `json:"state"`
	}
	code, body = doReq(t, s.ts, "GET", "/api/v1/instances", "", "")
	if code != 200 {
		t.Fatalf("list instances: status=%d body=%s", code, body)
	}
	if err := json.Unmarshal(listRows(t, body), &insts); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, body)
	}
	if len(insts) != 1 || insts[0].ProcessID != "foreign-default" || insts[0].State != "completed" {
		t.Fatalf("instance = %+v, want one completed foreign-default instance routed as before (%s)", insts, body)
	}
}

// TestDeployRefusesForbiddenFlowShapes is the deploy half: each shape is refused
// through the API with its rule named.
func TestDeployRefusesForbiddenFlowShapes(t *testing.T) {
	const head = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p" isExecutable="true">`
	const tail = `</process></definitions>`
	cases := []struct{ rule, xml string }{
		{"gateway.default-not-outgoing", foreignDefaultBPMN},
		{"start.incoming-flow", head + `<startEvent id="s"/><task id="a"/><endEvent id="e"/>` +
			`<sequenceFlow id="f1" sourceRef="s" targetRef="a"/><sequenceFlow id="f2" sourceRef="a" targetRef="e"/>` +
			`<sequenceFlow id="f3" sourceRef="a" targetRef="s"/>` + tail},
		{"end.outgoing-flow", head + `<startEvent id="s"/><endEvent id="e"/><task id="a"/><endEvent id="e2"/>` +
			`<sequenceFlow id="f1" sourceRef="s" targetRef="e"/><sequenceFlow id="f2" sourceRef="e" targetRef="a"/>` +
			`<sequenceFlow id="f3" sourceRef="a" targetRef="e2"/>` + tail},
	}
	ts := newTestServer(t)
	for _, tc := range cases {
		code, body := doReq(t, ts, "POST", "/api/v1/deployments", tc.xml, "application/xml")
		if code == 200 {
			t.Fatalf("deploy of a %s violation succeeded: %s", tc.rule, body)
		}
		if !strings.Contains(string(body), tc.rule) {
			t.Fatalf("deploy rejection does not name %s: status=%d body=%s", tc.rule, code, body)
		}
	}
}
