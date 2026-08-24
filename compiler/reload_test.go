package compiler

import (
	"strings"
	"testing"
)

// dottedReloadModel writes a script task's result to a dotted target. Today's
// deploy gate refuses it (variable.dotted-target), but a definition deployed
// before that rule existed is sitting in a deployment store somewhere, and the
// reload path has to be able to bring it back.
const dottedReloadModel = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                 xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="dotted" isExecutable="true">
    <startEvent id="start"/>
    <scriptTask id="total">
      <extensionElements><zeebe:script expression="= 1" resultVariable="customers.gesamtumsatz"/></extensionElements>
    </scriptTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="total"/>
    <sequenceFlow id="f2" sourceRef="total" targetRef="end"/>
  </process>
</definitions>`

// cleanReloadModel is the same shape with a name the gate is happy with, so a
// reload of an ordinary definition can be told apart from a reload of one a newer
// rule would refuse.
const cleanReloadModel = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                 xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="dotted" isExecutable="true">
    <startEvent id="start"/>
    <scriptTask id="total">
      <extensionElements><zeebe:script expression="= 1" resultVariable="gesamtumsatz"/></extensionElements>
    </scriptTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="total"/>
    <sequenceFlow id="f2" sourceRef="total" targetRef="end"/>
  </process>
</definitions>`

// TestReloadNamedKeepsAModelTodaysGateWouldRefuse is the point of the reload
// entry point: validation is a deploy-time gate, so a definition that passed it
// once comes back compiled, with what today's rules say about it reported
// alongside rather than in place of it.
func TestReloadNamedKeepsAModelTodaysGateWouldRefuse(t *testing.T) {
	cp, problems, err := ReloadNamed(7, 3, strings.NewReader(dottedReloadModel), "dotted")
	if err != nil {
		t.Fatalf("ReloadNamed: %v", err)
	}
	if cp == nil {
		t.Fatal("ReloadNamed returned no compiled process")
	}
	if cp.Key != 7 || cp.Version != 3 || cp.ProcessId() != "dotted" {
		t.Fatalf("reloaded key=%d version=%d id=%q, want 7/3/dotted", cp.Key, cp.Version, cp.ProcessId())
	}
	if !HasErrors(problems) {
		t.Fatalf("problems = %v, want the dotted target reported as an error", problems)
	}
	for _, p := range problems {
		if p.Severity == SeverityError && p.Rule != RuleDottedTarget {
			t.Fatalf("unexpected error problem %+v", p)
		}
	}
}

// TestReloadNamedReportsNothingForACleanModel keeps the reported problems
// meaningful: an ordinary definition reloads with an empty list, so a caller can
// log on len(problems) > 0 without filtering.
func TestReloadNamedReportsNothingForACleanModel(t *testing.T) {
	cp, problems, err := ReloadNamed(1, 1, strings.NewReader(cleanReloadModel), "dotted")
	if err != nil {
		t.Fatalf("ReloadNamed: %v", err)
	}
	if cp == nil {
		t.Fatal("ReloadNamed returned no compiled process")
	}
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
}

// TestReloadNamedStillFailsWhenThereIsNothingToRun draws the line: skipping the
// gate is not the same as tolerating anything. A model that cannot be turned into
// a compiled process has no definition to bring back, so it stays an error.
func TestReloadNamedStillFailsWhenThereIsNothingToRun(t *testing.T) {
	cases := []struct{ name, model, processId string }{
		{"no such process", cleanReloadModel, "absent"},
		{"not a model", "not a model at all", "p"},
		{"unparsable expression", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"><process id="p"><startEvent id="s"/><scriptTask id="t"><extensionElements><zeebe:script expression="= (" resultVariable="x"/></extensionElements></scriptTask><sequenceFlow id="f1" sourceRef="s" targetRef="t"/></process></definitions>`, "p"},
	}
	for _, c := range cases {
		if _, _, err := ReloadNamed(1, 1, strings.NewReader(c.model), c.processId); err == nil {
			t.Fatalf("ReloadNamed(%s): want an error, got nil", c.name)
		}
	}
}

// TestParseNamedStillRefusesADottedTarget guards the other half of the split: the
// deploy path keeps its gate, so the rule still does its job where it was meant to
// — at deploy, with the author watching.
func TestParseNamedStillRefusesADottedTarget(t *testing.T) {
	if _, err := ParseNamed(1, 1, strings.NewReader(dottedReloadModel), "dotted"); err == nil {
		t.Fatal("ParseNamed with a dotted target: want the deploy gate to refuse it, got nil")
	} else if !strings.Contains(err.Error(), RuleDottedTarget) {
		t.Fatalf("ParseNamed error = %v, want the %s rule", err, RuleDottedTarget)
	}
}
