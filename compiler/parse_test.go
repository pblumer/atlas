package compiler

import (
	"encoding/json"
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

const businessRuleBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <bpmn:process id="dinner" isExecutable="true">
    <bpmn:startEvent id="start"/>
    <bpmn:businessRuleTask id="decide" name="Pick dish">
      <bpmn:extensionElements>
        <zeebe:calledDecision decisionId="Dish" resultVariable="dish" retries="5"/>
        <atlas:decisionInput name="Season" value="Winter"/>
        <atlas:decisionInput name="Guests" value="8"/>
        <atlas:decisionInput name="Mood" variable="currentMood"/>
      </bpmn:extensionElements>
    </bpmn:businessRuleTask>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="decide"/>
    <bpmn:sequenceFlow id="f2" sourceRef="decide" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseBusinessRuleTask(t *testing.T) {
	cp, err := Parse(7, 1, strings.NewReader(businessRuleBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	if cp.Node(task).Type != TypeBusinessRuleTask {
		t.Fatalf("expected business rule task after start, got %v", cp.Node(task).Type)
	}
	detail := cp.BusinessRuleTask(cp.Node(task).Detail)
	if got := cp.Intern(detail.DecisionId); got != "Dish" {
		t.Errorf("decisionId = %q, want Dish", got)
	}
	if cp.Intern(detail.JobType) != DMNJobType {
		t.Errorf("jobType = %q, want %q", cp.Intern(detail.JobType), DMNJobType)
	}
	if detail.Retries != 5 {
		t.Errorf("retries = %d, want 5", detail.Retries)
	}
	if got := cp.Intern(detail.ResultVariable); got != "dish" {
		t.Errorf("resultVariable = %q, want dish", got)
	}
	// Static inputs are stored as a JSON object; the string value stays a string
	// and the numeric value keeps its JSON number type.
	var inputs map[string]any
	if err := json.Unmarshal([]byte(cp.Intern(detail.Inputs)), &inputs); err != nil {
		t.Fatalf("inputs not valid JSON: %v", err)
	}
	if inputs["Season"] != "Winter" {
		t.Errorf("Season = %#v, want \"Winter\"", inputs["Season"])
	}
	if inputs["Guests"] != float64(8) {
		t.Errorf("Guests = %#v, want 8", inputs["Guests"])
	}
	if _, isStatic := inputs["Mood"]; isStatic {
		t.Errorf("Mood should be a variable mapping, not a static input")
	}
	// The variable-bound input becomes an input mapping (Mood ← currentMood).
	if len(detail.InputMappings) != 1 {
		t.Fatalf("input mappings = %d, want 1", len(detail.InputMappings))
	}
	m := detail.InputMappings[0]
	if cp.Intern(m.Target) != "Mood" || cp.Intern(m.Source) != "currentMood" {
		t.Errorf("mapping = %q ← %q, want Mood ← currentMood", cp.Intern(m.Target), cp.Intern(m.Source))
	}
	if out := cp.Outgoing(task); len(out) != 1 || cp.Node(cp.Flow(out[0]).Target).Type != TypeEndEvent {
		t.Errorf("expected end event after business rule task")
	}
}

func TestParseBusinessRuleTaskDefaults(t *testing.T) {
	const noRetries = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p">
    <startEvent id="s"/>
    <businessRuleTask id="t"><extensionElements><calledDecision decisionId="D"/></extensionElements></businessRuleTask>
    <sequenceFlow id="f" sourceRef="s" targetRef="t"/>
  </process>
</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(noRetries))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	detail := cp.BusinessRuleTask(cp.Node(task).Detail)
	if detail.Retries != defaultRetries {
		t.Errorf("retries = %d, want default %d", detail.Retries, defaultRetries)
	}
	if detail.Inputs != -1 {
		t.Errorf("Inputs index = %d, want -1 (no inputs)", detail.Inputs)
	}
}

func TestParseBusinessRuleTaskWithoutDecision(t *testing.T) {
	const missing = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p"><startEvent id="s"/><businessRuleTask id="t"/></process></definitions>`
	if _, err := Parse(1, 1, strings.NewReader(missing)); err == nil {
		t.Fatal("Parse of business rule task without decisionId = nil error, want error")
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
