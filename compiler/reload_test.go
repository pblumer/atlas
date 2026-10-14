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

// nullCallReloadModel calls a FEEL function this build does not have. Today's
// deploy refuses it (ADR-0388) — the call can only ever evaluate to null, so it
// cannot be doing what its author meant. But the refusal arrived after models
// carrying such a call had already been deployed, and one of those took a server
// down on upgrade: the reload compiled it, the refusal came back as a plain error
// rather than as a gate refusal, and every other definition and every running
// instance sat behind a definition that would not load.
const nullCallReloadModel = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                 xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="probe" isExecutable="true">
    <startEvent id="start"/>
    <scriptTask id="s_keys">
      <extensionElements><zeebe:script expression="= get keys(kunde)" resultVariable="keys"/></extensionElements>
    </scriptTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="s_keys"/>
    <sequenceFlow id="f2" sourceRef="s_keys" targetRef="end"/>
  </process>
</definitions>`

// TestReloadNamedKeepsAModelWhoseCallCanOnlyBeNull is the regression: a rule the
// compiler gained after a definition was stored decides whether that definition
// may be *deployed*, never whether the server may start (ADR-0177).
func TestReloadNamedKeepsAModelWhoseCallCanOnlyBeNull(t *testing.T) {
	cp, problems, err := ReloadNamed(379, 1, strings.NewReader(nullCallReloadModel), "probe")
	if err != nil {
		t.Fatalf("ReloadNamed: %v", err)
	}
	if cp == nil {
		t.Fatal("ReloadNamed returned no compiled process")
	}
	if cp.Key != 379 || cp.ProcessId() != "probe" {
		t.Fatalf("reloaded key=%d id=%q, want 379/probe", cp.Key, cp.ProcessId())
	}
	if !HasErrors(problems) {
		t.Fatalf("problems = %v, want the null-only call reported", problems)
	}
	var said string
	for _, p := range problems {
		if p.Rule == RuleNullCall {
			said = p.Message
		}
	}
	if said == "" {
		t.Fatalf("no %s problem in %v", RuleNullCall, problems)
	}
	// The operator has to be able to find the model and the name to fix. There is no
	// element anchor here, so the expression itself has to be in the message.
	for _, want := range []string{"get keys", "get keys(kunde)"} {
		if !strings.Contains(said, want) {
			t.Errorf("problem does not mention %q:\n%s", want, said)
		}
	}
}

// TestDeployStillRefusesACallThatCanOnlyBeNull keeps the two halves apart: the
// reload's tolerance is for definitions that are already running, and must not
// soften the deploy, which is the moment ADR-0388 exists for.
func TestDeployStillRefusesACallThatCanOnlyBeNull(t *testing.T) {
	if _, err := ParseNamed(379, 1, strings.NewReader(nullCallReloadModel), "probe"); err == nil {
		t.Fatal("a model whose call can only ever be null deployed clean")
	} else if !strings.Contains(err.Error(), "s_keys") || !strings.Contains(err.Error(), "get keys") {
		t.Fatalf("refusal names neither the element nor the call: %v", err)
	}
}

// TestACleanModelReloadsWithNoNullCallProblem keeps the report meaningful: a
// definition with nothing wrong reloads with an empty list, so a caller can log on
// len(problems) > 0 without filtering.
func TestACleanModelReloadsWithNoNullCallProblem(t *testing.T) {
	_, problems, err := ReloadNamed(1, 1, strings.NewReader(cleanReloadModel), "dotted")
	if err != nil {
		t.Fatalf("ReloadNamed: %v", err)
	}
	for _, p := range problems {
		if p.Rule == RuleNullCall {
			t.Fatalf("clean model reported a null call: %+v", p)
		}
	}
}
