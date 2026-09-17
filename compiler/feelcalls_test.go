package compiler

import (
	"strings"
	"testing"
)

// callModel is one process whose script expression a test can choose, so the
// deploy's answer to that expression is the only thing that varies.
func callModel(expression string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                 xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="p" isExecutable="true">
    <startEvent id="s"><outgoing>f1</outgoing></startEvent>
    <scriptTask id="Kunden_daten_pruefen" name="Kunden daten prüfen">
      <incoming>f1</incoming><outgoing>f2</outgoing>
      <extensionElements>
        <zeebe:script expression="` + expression + `" resultVariable="ok"/>
      </extensionElements>
    </scriptTask>
    <endEvent id="e"><incoming>f2</incoming></endEvent>
    <sequenceFlow id="f1" sourceRef="s" targetRef="Kunden_daten_pruefen"/>
    <sequenceFlow id="f2" sourceRef="Kunden_daten_pruefen" targetRef="e"/>
  </process>
</definitions>`
}

// deployed reports what a deploy of this model says: the empty string when it is
// accepted, and the refusal otherwise.
func deployed(t *testing.T, model string) string {
	t.Helper()
	if _, err := Parse(1, 1, strings.NewReader(model)); err != nil {
		return err.Error()
	}
	return ""
}

// The model from the field. It deployed clean, the expression evaluated to null,
// and the gateway reading that null took its default flow — three steps, no error
// anywhere, and a customer set INACTIV who should have been ACTIVE.
func TestACallThatCanOnlyBeNullIsRefusedAtDeploy(t *testing.T) {
	said := deployed(t, callModel("= is defined(kunde.geburtsdatum)"))
	if said == "" {
		t.Fatal("a model whose expression can only ever be null deployed clean")
	}
	// The element and the field come from the caller's own wrapping, and they are
	// what makes the refusal findable in a diagram of thirty shapes.
	for _, want := range []string{"Kunden_daten_pruefen", "is defined", "x != null"} {
		if !strings.Contains(said, want) {
			t.Errorf("refusal does not mention %q:\n%s", want, said)
		}
	}
}

// The other silent null from the same model: a real function called with no
// argument binds exactly as an unknown name does.
func TestAKnownFunctionWithTheWrongArgumentCountIsRefusedAtDeploy(t *testing.T) {
	said := deployed(t, callModel("= date()"))
	if said == "" {
		t.Fatal("date() deployed clean")
	}
	if !strings.Contains(said, "date") {
		t.Errorf("refusal does not name the call:\n%s", said)
	}
}

// The corrected expression, which is the whole point: the refusal has to let the
// fix through, or it is just a wall.
func TestTheStandardWayToWriteItDeploys(t *testing.T) {
	if said := deployed(t, callModel("= kunde.geburtsdatum != null")); said != "" {
		t.Fatalf("the corrected expression was refused: %s", said)
	}
}

// A model may legitimately call something the check cannot resolve statically, and
// refusing it would block a model that runs.
func TestAnExpressionCallingSomethingItBindsItselfDeploys(t *testing.T) {
	if said := deployed(t, callModel("= for f in [1] return count([f])")); said != "" {
		t.Fatalf("a self-bound call was refused: %s", said)
	}
}

// Every expression a model carries goes through the same door, not only a script
// task's. A gateway condition is where a null does the most damage, because it
// routes.
func TestAConditionIsHeldToTheSameRule(t *testing.T) {
	model := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <startEvent id="s"><outgoing>f1</outgoing></startEvent>
    <exclusiveGateway id="g" default="f3"><incoming>f1</incoming><outgoing>f2</outgoing><outgoing>f3</outgoing></exclusiveGateway>
    <endEvent id="ok"><incoming>f2</incoming></endEvent>
    <endEvent id="nok"><incoming>f3</incoming></endEvent>
    <sequenceFlow id="f1" sourceRef="s" targetRef="g"/>
    <sequenceFlow id="f2" sourceRef="g" targetRef="ok">
      <conditionExpression>= is defined(x)</conditionExpression>
    </sequenceFlow>
    <sequenceFlow id="f3" sourceRef="g" targetRef="nok"/>
  </process>
</definitions>`
	said := deployed(t, model)
	if said == "" {
		t.Fatal("a gateway condition that can only be null deployed clean")
	}
	if !strings.Contains(said, "is defined") {
		t.Errorf("refusal does not name the call:\n%s", said)
	}
}
