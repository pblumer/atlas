package compiler

import (
	"strings"
	"testing"
)

// TestValidateUnboundedStandardLoopWarns: a standard loop bounded only by its FEEL
// condition is legal and deploys, but nothing in the model says how often it may run —
// a condition that never turns false loops until the instance is cancelled. The author
// gets a warning naming the engine's safety ceiling, so the bound is a decision rather
// than a surprise (ADR-0133, amended).
func TestValidateUnboundedStandardLoopWarns(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <serviceTask id="work">
	      <extensionElements><zeebe:taskDefinition type="worker"/></extensionElements>
	      <standardLoopCharacteristics><loopCondition>=again</loopCondition></standardLoopCharacteristics>
	    </serviceTask>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="work"/>
	    <sequenceFlow id="f2" sourceRef="work" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err) // a warning must never block the deploy
	}
	ps := Validate(cp)
	var got *Problem
	for i := range ps {
		if ps[i].Rule == RuleLoopUnbounded {
			got = &ps[i]
		}
	}
	if got == nil {
		t.Fatalf("no %s problem; got %v", RuleLoopUnbounded, ps)
	}
	if got.Severity != SeverityWarning {
		t.Errorf("severity = %q, want warning (an unbounded loop still deploys)", got.Severity)
	}
	if got.Element != "work" {
		t.Errorf("element = %q, want work", got.Element)
	}
	if HasErrors(ps) {
		t.Errorf("HasErrors = true, want false — the warning must not refuse the deploy")
	}
}

// TestValidateBoundedStandardLoopIsQuiet: a loop that states its own bound — a
// loopMaximum, with or without a condition — is exactly what the warning asks for, so
// it must not warn.
func TestValidateBoundedStandardLoopIsQuiet(t *testing.T) {
	for name, marker := range map[string]string{
		"condition and maximum": `<standardLoopCharacteristics loopMaximum="20"><loopCondition>=again</loopCondition></standardLoopCharacteristics>`,
		"maximum only":          `<standardLoopCharacteristics loopMaximum="3"/>`,
	} {
		t.Run(name, func(t *testing.T) {
			xml := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
			        xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
			  <process id="p" isExecutable="true">
			    <startEvent id="s"/>
			    <serviceTask id="work">
			      <extensionElements><zeebe:taskDefinition type="worker"/></extensionElements>
			      ` + marker + `
			    </serviceTask>
			    <endEvent id="e"/>
			    <sequenceFlow id="f1" sourceRef="s" targetRef="work"/>
			    <sequenceFlow id="f2" sourceRef="work" targetRef="e"/>
			  </process>
			</definitions>`
			cp, err := Parse(1, 1, strings.NewReader(xml))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			for _, p := range Validate(cp) {
				if p.Rule == RuleLoopUnbounded {
					t.Errorf("warned on a loop that states its own bound: %v", p)
				}
			}
		})
	}
}

// TestValidateMultiInstanceIsQuiet: a multi-instance is bounded by its collection or
// cardinality, so the unbounded-loop warning must not fire on it.
func TestValidateMultiInstanceIsQuiet(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <serviceTask id="work">
	      <extensionElements><zeebe:taskDefinition type="worker"/></extensionElements>
	      <multiInstanceLoopCharacteristics><extensionElements>
	        <zeebe:loopCharacteristics inputCollection="=items" inputElement="item"/>
	      </extensionElements></multiInstanceLoopCharacteristics>
	    </serviceTask>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="work"/>
	    <sequenceFlow id="f2" sourceRef="work" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for _, p := range Validate(cp) {
		if p.Rule == RuleLoopUnbounded {
			t.Errorf("warned on a multi-instance, which its collection bounds: %v", p)
		}
	}
}
