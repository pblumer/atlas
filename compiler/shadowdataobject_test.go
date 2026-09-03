package compiler

import (
	"strings"
	"testing"
)

// shadowModel is Patrick's example reduced to its bones: a task fetches a customer
// into a variable, a data-output association lifts that value into the data object
// the diagram draws, and a second task reads the object back out. Which names the
// three of them get is the parameter, because the name is the whole question.
func shadowModel(resultVar, objectName, readInto string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                 xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
                 xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <process id="p" isExecutable="true">
    <startEvent id="s"><outgoing>f1</outgoing></startEvent>
    <dataObject id="DO" name="` + objectName + `"/>
    <dataObjectReference id="Ref" name="` + objectName + `" dataObjectRef="DO">
      <dataState name="received"/>
    </dataObjectReference>
    <scriptTask id="Einlesen" name="Kunde einlesen">
      <incoming>f1</incoming><outgoing>f2</outgoing>
      <extensionElements><zeebe:script expression="=1" resultVariable="` + resultVar + `"/></extensionElements>
      <dataOutputAssociation id="doa">
        <targetRef>Ref</targetRef>
        <assignment><from xsi:type="tFormalExpression">=` + resultVar + `</from></assignment>
      </dataOutputAssociation>
    </scriptTask>
    <scriptTask id="Pruefen" name="Kunde prüfen">
      <incoming>f2</incoming><outgoing>f3</outgoing>
      <extensionElements><zeebe:script expression="=1" resultVariable="ok"/></extensionElements>
      <property id="Prop" name="__targetRef_placeholder"/>
      <dataInputAssociation id="dia">
        <sourceRef>Ref</sourceRef>
        <targetRef>Prop</targetRef>
        <assignment><to xsi:type="tFormalExpression">` + readInto + `</to></assignment>
      </dataInputAssociation>
    </scriptTask>
    <endEvent id="e"><incoming>f3</incoming></endEvent>
    <sequenceFlow id="f1" sourceRef="s" targetRef="Einlesen"/>
    <sequenceFlow id="f2" sourceRef="Einlesen" targetRef="Pruefen"/>
    <sequenceFlow id="f3" sourceRef="Pruefen" targetRef="e"/>
  </process>
</definitions>`
}

// shadowProblems compiles the model and returns its shadowing findings.
func shadowProblems(t *testing.T, model string) []Problem {
	t.Helper()
	cp, err := Parse(1, 1, strings.NewReader(model))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var out []Problem
	for _, p := range Validate(cp) {
		if p.Rule == RuleVariableShadowsDataObject {
			out = append(out, p)
		}
	}
	return out
}

// TestReadingAnObjectIntoAVariableOfItsOwnNameIsWarnedAbout covers the case that
// prompted the rule, and the one a modeller reaches for first: the data object is
// called Kunde, so the variable it is read into is called Kunde too.
//
// It leaves two Kunde in the instance and nothing linking them — the object with its
// type, state and history, and a copy of its value taken when the read ran. Only the
// variable answers to the name in FEEL, so every expression after this means the copy
// while the diagram shows one box.
func TestReadingAnObjectIntoAVariableOfItsOwnNameIsWarnedAbout(t *testing.T) {
	ps := shadowProblems(t, shadowModel("customer", "Kunde", "Kunde"))
	if len(ps) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(ps), ps)
	}
	if ps[0].Severity != SeverityWarning {
		t.Errorf("severity = %v, want a warning — it is legal, just misleading", ps[0].Severity)
	}
	if ps[0].Element != "Pruefen" {
		t.Errorf("anchored to %q, want the task that does the read", ps[0].Element)
	}
	// The message has to carry why it matters, not just that it happened.
	for _, want := range []string{"Kunde", "FEEL", "data state"} {
		if !strings.Contains(ps[0].Message, want) {
			t.Errorf("message does not mention %q: %s", want, ps[0].Message)
		}
	}
}

// TestAResultVariableNamedAfterADataObjectIsWarnedAbout is the same collision arriving
// from the other side: the task's own result is called what the object is called, so
// the value exists twice from the moment it is produced.
func TestAResultVariableNamedAfterADataObjectIsWarnedAbout(t *testing.T) {
	ps := shadowProblems(t, shadowModel("Kunde", "Kunde", "kundeDaten"))
	if len(ps) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(ps), ps)
	}
	if ps[0].Element != "Einlesen" {
		t.Errorf("anchored to %q, want the task whose result carries the name", ps[0].Element)
	}
	if !strings.Contains(ps[0].Message, "its result") {
		t.Errorf("message does not say which write it is: %s", ps[0].Message)
	}
}

// TestDistinctNamesRaiseNothing: the rule has to stay quiet on the model somebody
// gets right, or it teaches people to ignore the panel.
func TestDistinctNamesRaiseNothing(t *testing.T) {
	if ps := shadowProblems(t, shadowModel("customer", "Kunde", "kundeDaten")); len(ps) != 0 {
		t.Fatalf("findings = %+v, want none", ps)
	}
}

// TestAProcessWithoutDataObjectsIsNotWalked: a variable called anything at all is
// nobody's business when the process has no data objects to collide with.
func TestAProcessWithoutDataObjectsIsNotWalked(t *testing.T) {
	const noObjects = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                 xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="p" isExecutable="true">
    <startEvent id="s"><outgoing>f1</outgoing></startEvent>
    <scriptTask id="t">
      <incoming>f1</incoming><outgoing>f2</outgoing>
      <extensionElements><zeebe:script expression="=1" resultVariable="Kunde"/></extensionElements>
    </scriptTask>
    <endEvent id="e"><incoming>f2</incoming></endEvent>
    <sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </process>
</definitions>`
	if ps := shadowProblems(t, noObjects); len(ps) != 0 {
		t.Fatalf("findings = %+v, want none", ps)
	}
}
