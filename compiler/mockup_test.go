package compiler

import (
	"strings"
	"testing"
)

// mockupBPMN wraps a mockup service task's extension element in a minimal process.
func mockupBPMN(ext string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>` + ext + `</bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

// A service task bearing an <atlas:mockupConnector> extension compiles to a mockup
// task the engine simulates itself (ADR-0120): a random duration, an optional FEEL
// result, and a failure probability — no reserved job type.
func TestParseMockupTask(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(mockupBPMN(
		`<atlas:mockupConnector minDuration="PT1S" maxDuration="PT5S" resultExpression="amount * 2" resultVariable="doubled" failRate="0.25" failMessage="umsystem down" errorCode="UMSYS_DOWN"/>`)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	node := cp.Node(task)
	if node.Type != TypeMockupTask {
		t.Fatalf("task node type = %v, want MockupTask", node.Type)
	}
	d := cp.MockupTask(node.Detail)
	if d.MinNanos != int64(1e9) || d.MaxNanos != int64(5e9) {
		t.Errorf("duration bounds = [%d,%d], want [1e9,5e9]", d.MinNanos, d.MaxNanos)
	}
	if d.ResultVar != "doubled" {
		t.Errorf("resultVar = %q, want doubled", d.ResultVar)
	}
	if d.Expr == nil {
		t.Error("Expr = nil, want a compiled FEEL result expression")
	}
	if d.FailPerMillion != 250_000 {
		t.Errorf("failPerMillion = %d, want 250000", d.FailPerMillion)
	}
	if d.FailMessage != "umsystem down" {
		t.Errorf("failMessage = %q, want %q", d.FailMessage, "umsystem down")
	}
	if d.ErrorCode != "UMSYS_DOWN" {
		t.Errorf("errorCode = %q, want %q", d.ErrorCode, "UMSYS_DOWN")
	}
}

// An omitted maxDuration means a fixed duration (max == min); an omitted failRate
// never fails; and a mockup task is a valid boundary-event host (it is an activity).
func TestParseMockupTaskDefaults(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(mockupBPMN(
		`<atlas:mockupConnector minDuration="PT3S"/>`)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	d := cp.MockupTask(cp.Node(task).Detail)
	if d.MinNanos != int64(3e9) || d.MaxNanos != int64(3e9) {
		t.Errorf("duration bounds = [%d,%d], want a fixed 3e9", d.MinNanos, d.MaxNanos)
	}
	if d.FailPerMillion != 0 {
		t.Errorf("failPerMillion = %d, want 0", d.FailPerMillion)
	}
	if d.Expr != nil || d.ResultVar != "" {
		t.Errorf("no result authored, want empty; got expr=%v resultVar=%q", d.Expr != nil, d.ResultVar)
	}
	if !isActivity(TypeMockupTask) {
		t.Error("isActivity(TypeMockupTask) = false, want true (a mockup task hosts boundary events)")
	}
}

// A result expression honors the fx toggle: a leading '=' is optional and stripped.
func TestParseMockupTaskFeelPrefix(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(mockupBPMN(
		`<atlas:mockupConnector minDuration="PT1S" resultExpression="=amount + 1" resultVariable="r"/>`)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	if d := cp.MockupTask(cp.Node(task).Detail); d.Expr == nil {
		t.Error("Expr = nil, want the compiled expression with the '=' stripped")
	}
}

func TestParseMockupTaskErrors(t *testing.T) {
	cases := []struct {
		name    string
		ext     string
		wantSub string
	}{
		{"max before min", `<atlas:mockupConnector minDuration="PT5S" maxDuration="PT1S"/>`, "maxDuration before minDuration"},
		{"bad min duration", `<atlas:mockupConnector minDuration="5 seconds"/>`, "minDuration"},
		{"bad max duration", `<atlas:mockupConnector minDuration="PT1S" maxDuration="soon"/>`, "maxDuration"},
		{"failRate over one", `<atlas:mockupConnector minDuration="PT1S" failRate="1.5"/>`, "between 0 and 1"},
		{"failRate not a number", `<atlas:mockupConnector minDuration="PT1S" failRate="often"/>`, "invalid failRate"},
		{"expr without variable", `<atlas:mockupConnector minDuration="PT1S" resultExpression="1 + 1"/>`, "no result variable"},
		{"variable without expr", `<atlas:mockupConnector minDuration="PT1S" resultVariable="r"/>`, "no result expression"},
		{"bad result expression", `<atlas:mockupConnector minDuration="PT1S" resultExpression="1 +" resultVariable="r"/>`, "result expression"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(1, 1, strings.NewReader(mockupBPMN(tc.ext)))
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("Parse error = %v, want one containing %q", err, tc.wantSub)
			}
		})
	}
}
