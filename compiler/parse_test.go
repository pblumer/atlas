package compiler

import (
	"strings"
	"testing"
)

const linearBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:process id="order" isExecutable="true">
    <bpmn:startEvent id="start"/>
    <bpmn:serviceTask id="task" name="Charge">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="payment" retries="5"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="task"/>
    <bpmn:sequenceFlow id="f2" sourceRef="task" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseLinearProcess(t *testing.T) {
	cp, err := Parse(99, 1, strings.NewReader(linearBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cp.Key != 99 || cp.Version != 1 {
		t.Errorf("key/version = %d/%d, want 99/1", cp.Key, cp.Version)
	}
	if got := cp.Intern(cp.BpmnProcessId); got != "order" {
		t.Errorf("BpmnProcessId = %q, want \"order\"", got)
	}

	starts := cp.StartEvents()
	if len(starts) != 1 {
		t.Fatalf("start events = %d, want 1", len(starts))
	}
	start := starts[0]
	if cp.Node(start).Type != TypeStartEvent {
		t.Fatalf("start node type = %v", cp.Node(start).Type)
	}

	// start → task → end
	out := cp.Outgoing(start)
	if len(out) != 1 {
		t.Fatalf("start outgoing = %d, want 1", len(out))
	}
	task := cp.Flow(out[0]).Target
	if cp.Node(task).Type != TypeServiceTask {
		t.Fatalf("expected service task after start, got %v", cp.Node(task).Type)
	}
	detail := cp.ServiceTask(cp.Node(task).Detail)
	if cp.Intern(detail.JobType) != "payment" || detail.Retries != 5 {
		t.Errorf("task detail jobType=%q retries=%d, want payment/5", cp.Intern(detail.JobType), detail.Retries)
	}

	out = cp.Outgoing(task)
	if len(out) != 1 || cp.Node(cp.Flow(out[0]).Target).Type != TypeEndEvent {
		t.Errorf("expected end event after task")
	}
}

func TestParseDefaultRetries(t *testing.T) {
	const noRetries = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p">
    <startEvent id="s"/>
    <serviceTask id="t">
      <extensionElements><taskDefinition type="work"/></extensionElements>
    </serviceTask>
    <sequenceFlow id="f" sourceRef="s" targetRef="t"/>
  </process>
</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(noRetries))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// the service task is element 1 (after the start event)
	var task int32 = -1
	for _, fid := range cp.Outgoing(cp.StartEvents()[0]) {
		task = cp.Flow(fid).Target
	}
	if detail := cp.ServiceTask(cp.Node(task).Detail); detail.Retries != defaultRetries {
		t.Errorf("retries = %d, want default %d", detail.Retries, defaultRetries)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		xml  string
	}{
		{
			name: "no process",
			xml:  `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"></definitions>`,
		},
		{
			name: "no start event",
			xml: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
				<endEvent id="e"/></process></definitions>`,
		},
		{
			name: "service task without type",
			xml: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
				<startEvent id="s"/><serviceTask id="t"/></process></definitions>`,
		},
		{
			name: "dangling flow",
			xml: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
				<startEvent id="s"/><sequenceFlow id="f" sourceRef="s" targetRef="missing"/></process></definitions>`,
		},
		{
			name: "duplicate id",
			xml: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
				<startEvent id="s"/><endEvent id="s"/></process></definitions>`,
		},
		{
			name: "unsupported plain task",
			xml: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
				<startEvent id="s"/><task id="t"/><endEvent id="e"/>
				<sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
				<sequenceFlow id="f2" sourceRef="t" targetRef="e"/></process></definitions>`,
		},
		{
			name: "unsupported gateway",
			xml: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
				<startEvent id="s"/><exclusiveGateway id="g"/></process></definitions>`,
		},
		{
			name: "malformed xml",
			xml:  `<definitions><process`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse(1, 1, strings.NewReader(tt.xml)); err == nil {
				t.Errorf("Parse(%s) = nil error, want error", tt.name)
			}
		})
	}
}

// TestParseScriptTask compiles a Zeebe script task and checks its detail: a
// compiled FEEL expression and a result variable.
func TestParseScriptTask(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <scriptTask id="calc">
	      <extensionElements><zeebe:script expression="= 6 * 7" resultVariable="answer"/></extensionElements>
	    </scriptTask>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="calc"/>
	    <sequenceFlow id="f2" sourceRef="calc" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Registration order: start(0), scriptTask(1), end(2).
	node := cp.Node(1)
	if node.Type != TypeScriptTask {
		t.Fatalf("node 1 type = %v, want ScriptTask", node.Type)
	}
	detail := cp.ScriptTask(node.Detail)
	if detail.ResultVar != "answer" {
		t.Errorf("result var = %q, want answer", detail.ResultVar)
	}
	if detail.Expr == nil {
		t.Error("script task has no compiled expression")
	}
}

// TestParseScriptTaskRejectsBadExpression fails deploy when the FEEL is invalid.
func TestParseScriptTaskRejectsBadExpression(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p"><startEvent id="s"/>
	    <scriptTask id="calc"><extensionElements>
	      <zeebe:script expression="= 6 * " resultVariable="answer"/></extensionElements>
	    </scriptTask></process></definitions>`
	if _, err := Parse(1, 1, strings.NewReader(xml)); err == nil {
		t.Fatal("want a compile error for a malformed FEEL expression")
	}
}

// TestParseUnsupportedElementMessage locks in the actionable error text for an
// unsupported element (a plain task) rather than a confusing "unknown targetRef".
func TestParseUnsupportedElementMessage(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
		<startEvent id="s"/><task id="Activity_1"/><endEvent id="e"/>
		<sequenceFlow id="f1" sourceRef="s" targetRef="Activity_1"/>
		<sequenceFlow id="f2" sourceRef="Activity_1" targetRef="e"/></process></definitions>`
	_, err := Parse(1, 1, strings.NewReader(xml))
	if err == nil {
		t.Fatal("want error for a plain <task>")
	}
	for _, want := range []string{"Activity_1", "task", "service task"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err.Error(), want)
		}
	}
}
