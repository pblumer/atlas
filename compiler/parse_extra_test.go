package compiler

import (
	"strings"
	"testing"
)

// TestParseRegisterErrors covers the register() legs each element category can
// fail on: an empty element id, and a duplicate id detected while registering a
// service task, script task, business rule task, and exclusive gateway (each is
// registered in its own loop, so each needs its own case). The unknown-sourceRef
// flow leg and the exclusive-gateway default-references-unknown-flow leg round it
// out — all are deploy-time modeling errors that must fail Parse.
func TestParseRegisterErrors(t *testing.T) {
	tests := []struct {
		name string
		xml  string
	}{
		{
			name: "empty start event id",
			xml: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
				<startEvent id=""/></process></definitions>`,
		},
		{
			name: "duplicate service task id",
			xml: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
				<startEvent id="dup"/>
				<serviceTask id="dup"><extensionElements><taskDefinition type="w"/></extensionElements></serviceTask>
				</process></definitions>`,
		},
		{
			name: "duplicate script task id",
			xml: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
			      xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"><process id="p">
				<startEvent id="dup"/>
				<scriptTask id="dup"><extensionElements><zeebe:script expression="= 1 + 1" resultVariable="r"/></extensionElements></scriptTask>
				</process></definitions>`,
		},
		{
			name: "duplicate business rule task id",
			xml: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
			      xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"><process id="p">
				<startEvent id="dup"/>
				<businessRuleTask id="dup"><extensionElements><zeebe:calledDecision decisionId="D"/></extensionElements></businessRuleTask>
				</process></definitions>`,
		},
		{
			name: "duplicate exclusive gateway id",
			xml: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
				<startEvent id="dup"/><exclusiveGateway id="dup"/></process></definitions>`,
		},
		{
			name: "flow unknown sourceRef",
			xml: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
				<startEvent id="s"/>
				<sequenceFlow id="f" sourceRef="ghost" targetRef="s"/></process></definitions>`,
		},
		{
			name: "exclusive gateway default references unknown flow",
			xml: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
				<startEvent id="s"/><exclusiveGateway id="gw" default="ghost"/><endEvent id="e"/>
				<sequenceFlow id="a" sourceRef="s" targetRef="gw"/>
				<sequenceFlow id="b" sourceRef="gw" targetRef="e"/></process></definitions>`,
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

// TestParseExclusiveGatewayNoDefault covers the default-marking loop's skip leg:
// an exclusive gateway with no default attribute parses fine, and its lone
// outgoing flow is unconditional (no condition, not marked default).
func TestParseExclusiveGatewayNoDefault(t *testing.T) {
	const gwBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <exclusiveGateway id="gw"/>
	    <endEvent id="e"/>
	    <sequenceFlow id="a" sourceRef="s" targetRef="gw"/>
	    <sequenceFlow id="b" sourceRef="gw" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(gwBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	gw := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	if cp.Node(gw).Type != TypeExclusiveGateway {
		t.Fatalf("expected exclusive gateway, got %v", cp.Node(gw).Type)
	}
	out := cp.Outgoing(gw)
	if len(out) != 1 {
		t.Fatalf("gateway outgoing = %d, want 1", len(out))
	}
	if f := cp.Flow(out[0]); f.Condition != nil || f.Default {
		t.Errorf("flow = {cond:%v default:%v}, want unconditional non-default", f.Condition != nil, f.Default)
	}
}
