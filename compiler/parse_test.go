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
