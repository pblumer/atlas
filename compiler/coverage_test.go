package compiler

import (
	"strings"
	"testing"
)

// TestBpmnTypeString covers the String rendering for every known type and the
// default.
func TestBpmnTypeString(t *testing.T) {
	tests := []struct {
		in   BpmnType
		want string
	}{
		{TypeStartEvent, "StartEvent"},
		{TypeEndEvent, "EndEvent"},
		{TypeServiceTask, "ServiceTask"},
		{TypeScriptTask, "ScriptTask"},
		{TypeBusinessRuleTask, "BusinessRuleTask"},
		{TypeExclusiveGateway, "ExclusiveGateway"},
		{TypeUnspecified, "Unspecified"},
		{BpmnType(200), "Unspecified"},
	}
	for _, tt := range tests {
		if got := tt.in.String(); got != tt.want {
			t.Errorf("BpmnType(%d).String() = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestInternOutOfRange: an interned index outside the string table yields "".
func TestInternOutOfRange(t *testing.T) {
	b := NewBuilder(1, "p", 1)
	b.AddStartEvent()
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := cp.Intern(-1); got != "" {
		t.Errorf("Intern(-1) = %q, want \"\"", got)
	}
	if got := cp.Intern(int32(len(cp.strings))); got != "" {
		t.Errorf("Intern(out of range) = %q, want \"\"", got)
	}
	// A valid index round-trips.
	if got := cp.Intern(cp.BpmnProcessId); got != "p" {
		t.Errorf("Intern(BpmnProcessId) = %q, want \"p\"", got)
	}
}

// TestElementBpmnId returns the recorded source id for a node, "" for a node with
// none, and "" for an out-of-range node id.
func TestElementBpmnId(t *testing.T) {
	b := NewBuilder(1, "p", 1)
	start := b.AddStartEvent()
	b.SetElementBpmnId(start, "StartEvent_1")
	end := b.AddEndEvent() // no bpmn id recorded
	b.Connect(start, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := cp.ElementBpmnId(start); got != "StartEvent_1" {
		t.Errorf("ElementBpmnId(start) = %q, want StartEvent_1", got)
	}
	if got := cp.ElementBpmnId(end); got != "" {
		t.Errorf("ElementBpmnId(end) = %q, want \"\"", got)
	}
	if got := cp.ElementBpmnId(-1); got != "" {
		t.Errorf("ElementBpmnId(-1) = %q, want \"\"", got)
	}
	if got := cp.ElementBpmnId(int32(len(cp.nodes))); got != "" {
		t.Errorf("ElementBpmnId(out of range) = %q, want \"\"", got)
	}
}

// TestInternEmptyAndReuse: interning "" returns -1, and interning the same string
// twice returns the same index (the map-hit branch).
func TestInternEmptyAndReuse(t *testing.T) {
	b := NewBuilder(1, "p", 1)
	if idx := b.intern(""); idx != -1 {
		t.Errorf("intern(\"\") = %d, want -1", idx)
	}
	first := b.intern("dup")
	second := b.intern("dup")
	if first != second {
		t.Errorf("intern(\"dup\") = %d then %d, want equal", first, second)
	}
}

// TestSetElementBpmnIdInvalidNode: recording an id for an out-of-range node is a
// silent no-op (validNode guards it).
func TestSetElementBpmnIdInvalidNode(t *testing.T) {
	b := NewBuilder(1, "p", 1)
	b.SetElementBpmnId(999, "nope") // must not panic
	start := b.AddStartEvent()
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := cp.ElementBpmnId(start); got != "" {
		t.Errorf("ElementBpmnId(start) = %q, want \"\"", got)
	}
}

// TestAddBusinessRuleTaskUnmarshalableInputs: an input value JSON cannot encode
// (a channel) surfaces as an error from AddBusinessRuleTask.
func TestAddBusinessRuleTaskUnmarshalableInputs(t *testing.T) {
	b := NewBuilder(1, "p", 1)
	_, err := b.AddBusinessRuleTask("D", map[string]any{"bad": make(chan int)}, 3)
	if err == nil {
		t.Fatal("AddBusinessRuleTask with unmarshalable inputs = nil error, want error")
	}
	if !strings.Contains(err.Error(), "D") {
		t.Errorf("error %q should name the decision", err.Error())
	}
}

// TestDecisionInputsErrors covers the empty-name and duplicate-name paths, and
// the nil (no inputs) short-circuit.
func TestDecisionInputsErrors(t *testing.T) {
	if m, err := decisionInputs(nil); m != nil || err != nil {
		t.Errorf("decisionInputs(nil) = %v, %v, want nil, nil", m, err)
	}
	if _, err := decisionInputs([]xmlDecisionInput{{Name: "", Value: "x"}}); err == nil {
		t.Error("decisionInputs with empty name = nil error, want error")
	}
	if _, err := decisionInputs([]xmlDecisionInput{{Name: "a", Value: "1"}, {Name: "a", Value: "2"}}); err == nil {
		t.Error("decisionInputs with duplicate name = nil error, want error")
	}
	// A non-JSON value is kept verbatim as a string; a JSON value keeps its type.
	m, err := decisionInputs([]xmlDecisionInput{{Name: "s", Value: "plain"}, {Name: "n", Value: "3"}})
	if err != nil {
		t.Fatalf("decisionInputs: %v", err)
	}
	if m["s"] != "plain" {
		t.Errorf("s = %#v, want \"plain\"", m["s"])
	}
	if m["n"] != float64(3) {
		t.Errorf("n = %#v, want 3", m["n"])
	}
}

// TestParseMoreErrors covers Parse error legs the existing suite misses: invalid
// service-task retries, script task without a result variable, business rule task
// with invalid retries, and a business rule task whose decisionInput has an empty
// name.
func TestParseMoreErrors(t *testing.T) {
	tests := []struct {
		name string
		xml  string
	}{
		{
			name: "service task invalid retries",
			xml: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
				<startEvent id="s"/>
				<serviceTask id="t"><extensionElements><taskDefinition type="work" retries="lots"/></extensionElements></serviceTask>
				<sequenceFlow id="f" sourceRef="s" targetRef="t"/></process></definitions>`,
		},
		{
			name: "script task without result variable",
			xml: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
			      xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"><process id="p">
				<startEvent id="s"/>
				<scriptTask id="t"><extensionElements><zeebe:script expression="= 1 + 1"/></extensionElements></scriptTask>
				<sequenceFlow id="f" sourceRef="s" targetRef="t"/></process></definitions>`,
		},
		{
			name: "script task without expression",
			xml: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
			      xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"><process id="p">
				<startEvent id="s"/>
				<scriptTask id="t"><extensionElements><zeebe:script expression="=  " resultVariable="r"/></extensionElements></scriptTask>
				<sequenceFlow id="f" sourceRef="s" targetRef="t"/></process></definitions>`,
		},
		{
			name: "business rule task invalid retries",
			xml: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
			      xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"><process id="p">
				<startEvent id="s"/>
				<businessRuleTask id="t"><extensionElements><zeebe:calledDecision decisionId="D" retries="lots"/></extensionElements></businessRuleTask>
				<sequenceFlow id="f" sourceRef="s" targetRef="t"/></process></definitions>`,
		},
		{
			name: "business rule task empty decisionInput name",
			xml: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
			      xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
			      xmlns:atlas="http://atlas/schema/1.0"><process id="p">
				<startEvent id="s"/>
				<businessRuleTask id="t"><extensionElements>
				  <zeebe:calledDecision decisionId="D"/>
				  <atlas:decisionInput value="x"/>
				</extensionElements></businessRuleTask>
				<sequenceFlow id="f" sourceRef="s" targetRef="t"/></process></definitions>`,
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
