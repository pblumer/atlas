package compiler

import (
	"strings"
	"testing"
)

// ADR-0547: the sequence-flow shapes BPMN forbids are refused by stage 5, on every
// start- and end-event kind and on both data-based gateway kinds, and nothing else is.

func forbiddenProblems(t *testing.T, xml string) []Problem {
	t.Helper()
	ps, err := ValidateModel(strings.NewReader(xml))
	if err != nil {
		t.Fatalf("ValidateModel: %v", err)
	}
	return ps
}

func hasForbidden(ps []Problem, rule, element string) bool {
	for _, p := range ps {
		if p.Rule == rule && p.Element == element && p.Severity == SeverityError {
			return true
		}
	}
	return false
}

const forbiddenHead = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <message id="M" name="m"/><signal id="S" name="sig"/><error id="E" errorCode="E1"/>
  <escalation id="X" escalationCode="X1"/>
  <process id="p" isExecutable="true">`

const forbiddenTail = `</process></definitions>`

func TestStartEventWithIncomingFlowIsRefused(t *testing.T) {
	starts := map[string]string{
		"none":    `<startEvent id="st"/>`,
		"message": `<startEvent id="st"><messageEventDefinition messageRef="M"/></startEvent>`,
		"signal":  `<startEvent id="st"><signalEventDefinition signalRef="S"/></startEvent>`,
		"timer":   `<startEvent id="st"><timerEventDefinition><timeDuration>PT1M</timeDuration></timerEventDefinition></startEvent>`,
	}
	for kind, start := range starts {
		t.Run(kind, func(t *testing.T) {
			xml := forbiddenHead + start + `<task id="a"/><endEvent id="e"/>` +
				`<sequenceFlow id="f1" sourceRef="st" targetRef="a"/><sequenceFlow id="f2" sourceRef="a" targetRef="e"/>` +
				`<sequenceFlow id="back" sourceRef="a" targetRef="st"/>` + forbiddenTail
			if ps := forbiddenProblems(t, xml); !hasForbidden(ps, RuleStartIncomingFlow, "st") {
				t.Fatalf("problems %+v lack %s on st", ps, RuleStartIncomingFlow)
			}
			if _, err := Parse(1, 1, strings.NewReader(xml)); err == nil || !strings.Contains(err.Error(), RuleStartIncomingFlow) {
				t.Fatalf("Parse = %v, want refusal naming %s", err, RuleStartIncomingFlow)
			}
		})
	}
	t.Run("embedded-subprocess", func(t *testing.T) {
		xml := forbiddenHead + `<startEvent id="s"/><subProcess id="sub"><startEvent id="ss"/><task id="a"/><endEvent id="se"/>` +
			`<sequenceFlow id="i1" sourceRef="ss" targetRef="a"/><sequenceFlow id="i2" sourceRef="a" targetRef="se"/>` +
			`<sequenceFlow id="i3" sourceRef="a" targetRef="ss"/></subProcess><endEvent id="e"/>` +
			`<sequenceFlow id="f1" sourceRef="s" targetRef="sub"/><sequenceFlow id="f2" sourceRef="sub" targetRef="e"/>` + forbiddenTail
		if ps := forbiddenProblems(t, xml); !hasForbidden(ps, RuleStartIncomingFlow, "ss") {
			t.Fatalf("problems %+v lack %s on ss", ps, RuleStartIncomingFlow)
		}
	})
}

func TestEndEventWithOutgoingFlowIsRefused(t *testing.T) {
	ends := map[string]string{
		"none":       `<endEvent id="en"/>`,
		"message":    `<endEvent id="en"><messageEventDefinition messageRef="M"/></endEvent>`,
		"signal":     `<endEvent id="en"><signalEventDefinition signalRef="S"/></endEvent>`,
		"error":      `<endEvent id="en"><errorEventDefinition errorRef="E"/></endEvent>`,
		"escalation": `<endEvent id="en"><escalationEventDefinition escalationRef="X"/></endEvent>`,
		"terminate":  `<endEvent id="en"><terminateEventDefinition/></endEvent>`,
	}
	for kind, end := range ends {
		t.Run(kind, func(t *testing.T) {
			xml := forbiddenHead + `<startEvent id="s"/>` + end + `<task id="a"/><endEvent id="e2"/>` +
				`<sequenceFlow id="f1" sourceRef="s" targetRef="en"/><sequenceFlow id="f2" sourceRef="en" targetRef="a"/>` +
				`<sequenceFlow id="f3" sourceRef="a" targetRef="e2"/>` + forbiddenTail
			if ps := forbiddenProblems(t, xml); !hasForbidden(ps, RuleEndOutgoingFlow, "en") {
				t.Fatalf("problems %+v lack %s on en", ps, RuleEndOutgoingFlow)
			}
		})
	}
}

func TestGatewayDefaultMustBeItsOwnOutgoingFlow(t *testing.T) {
	for _, kind := range []string{"exclusiveGateway", "inclusiveGateway"} {
		t.Run(kind+"/foreign-gateway-flow", func(t *testing.T) {
			xml := forbiddenHead + `<startEvent id="s"/><` + kind + ` id="g1" default="other_default"/>` +
				`<exclusiveGateway id="g2"/><endEvent id="e1"/><endEvent id="e2"/><endEvent id="e3"/>` +
				`<sequenceFlow id="f1" sourceRef="s" targetRef="g1"/>` +
				`<sequenceFlow id="g1a" sourceRef="g1" targetRef="g2"><conditionExpression>= true</conditionExpression></sequenceFlow>` +
				`<sequenceFlow id="other_default" sourceRef="g2" targetRef="e1"><conditionExpression>= false</conditionExpression></sequenceFlow>` +
				`<sequenceFlow id="g2b" sourceRef="g2" targetRef="e2"><conditionExpression>= false</conditionExpression></sequenceFlow>` +
				`<sequenceFlow id="g1b" sourceRef="g1" targetRef="e3"><conditionExpression>= false</conditionExpression></sequenceFlow>` + forbiddenTail
			ps := forbiddenProblems(t, xml)
			if !hasForbidden(ps, RuleGatewayDefaultNotOutgoing, "g1") {
				t.Fatalf("problems %+v lack %s on g1", ps, RuleGatewayDefaultNotOutgoing)
			}
			for _, p := range ps {
				if p.Rule == RuleGatewayDefaultNotOutgoing && p.Element != "g1" {
					t.Fatalf("the rule names %q; it belongs to the declaring gateway g1", p.Element)
				}
			}
		})
	}
	t.Run("own-outgoing-default-is-fine", func(t *testing.T) {
		xml := forbiddenHead + `<startEvent id="s"/><exclusiveGateway id="g" default="d"/><endEvent id="e1"/><endEvent id="e2"/>` +
			`<sequenceFlow id="f1" sourceRef="s" targetRef="g"/><sequenceFlow id="d" sourceRef="g" targetRef="e1"/>` +
			`<sequenceFlow id="c" sourceRef="g" targetRef="e2"><conditionExpression>= false</conditionExpression></sequenceFlow>` + forbiddenTail
		for _, p := range forbiddenProblems(t, xml) {
			if p.Severity == SeverityError {
				t.Fatalf("a correct default is refused: %+v", p)
			}
		}
		if _, err := Parse(1, 1, strings.NewReader(xml)); err != nil {
			t.Fatalf("Parse: %v", err)
		}
	})
}

// TestReloadKeepsForbiddenShapesAsCompiled pins the reload half: the definition comes
// back with the problem beside it, and the leaked default is still marked exactly as an
// older build compiled it, so a restart does not change routing (ADR-0177, ADR-0393).
func TestReloadKeepsForbiddenShapesAsCompiled(t *testing.T) {
	xml := forbiddenHead + `<startEvent id="s"/><exclusiveGateway id="g1" default="leak"/>` +
		`<exclusiveGateway id="g2"/><endEvent id="e1"/><endEvent id="e2"/>` +
		`<sequenceFlow id="f1" sourceRef="s" targetRef="g1"/><sequenceFlow id="g1a" sourceRef="g1" targetRef="g2"/>` +
		`<sequenceFlow id="leak" sourceRef="g2" targetRef="e1"><conditionExpression>= false</conditionExpression></sequenceFlow>` +
		`<sequenceFlow id="g2b" sourceRef="g2" targetRef="e2"><conditionExpression>= false</conditionExpression></sequenceFlow>` + forbiddenTail
	cp, problems, err := ReloadNamed(1, 1, strings.NewReader(xml), "p")
	if err != nil || cp == nil {
		t.Fatalf("ReloadNamed: cp=%v err=%v", cp, err)
	}
	if !hasForbidden(problems, RuleGatewayDefaultNotOutgoing, "g1") {
		t.Fatalf("reload problems %+v lack %s", problems, RuleGatewayDefaultNotOutgoing)
	}
	leak := int32(-1)
	for i := int32(0); i < int32(cp.FlowCount()); i++ {
		if cp.FlowBpmnId(i) == "leak" {
			leak = i
		}
	}
	if leak < 0 || !cp.Flow(leak).Default {
		t.Fatalf("flow leak must stay marked default on reload, as an older build compiled it")
	}
}
