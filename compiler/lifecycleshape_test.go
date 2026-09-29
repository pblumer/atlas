package compiler_test

import (
	"os"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

func parseShape(t *testing.T, xml string) *compiler.CompiledProcess {
	t.Helper()
	cp, err := compiler.Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return cp
}

// TestTheLifecycleTemplateWaitsWhereItShould: the per-position template in
// docs/planning waits for a change and a return at the gateway and for a return on
// the change step, every catch keyed on the position, and every cycle in it waits.
func TestTheLifecycleTemplateWaitsWhereItShould(t *testing.T) {
	raw, err := os.ReadFile("../docs/planning/lebenszyklus-pro-position/proc_produkt_lebenszyklus_vorlage.bpmn")
	if err != nil {
		t.Fatal(err)
	}
	cp := parseShape(t, string(raw))
	got := map[string]string{}
	for _, c := range cp.MessageCatchPoints() {
		if !c.Correlated {
			t.Errorf("%s waits for %s without a correlation key", c.Element, c.MessageName)
		}
		got[c.Element] = c.MessageName
	}
	want := map[string]string{
		"msg_change": "produkt.change", "msg_rueckgabe": "produkt.deprovision", "b_rueckgabe": "produkt.deprovision",
	}
	if len(got) != len(want) {
		t.Fatalf("catch points = %v, want %v", got, want)
	}
	for el, msg := range want {
		if got[el] != msg {
			t.Errorf("%s waits for %q, want %q", el, got[el], msg)
		}
	}
	if c := cp.WaitlessCycle(); c != nil {
		t.Errorf("the template has a cycle without a wait: %v", c)
	}
}

// TestAWaitlessCycleIsNamed: a loop through a script task and a gateway waits for
// nothing and is reported by its elements; an uncorrelated catch says so.
func TestAWaitlessCycleIsNamed(t *testing.T) {
	cp := parseShape(t, `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <message id="m" name="loop.again"/>
  <process id="loop" isExecutable="true">
    <startEvent id="s"/>
    <exclusiveGateway id="merge"/>
    <scriptTask id="work"><extensionElements><zeebe:script expression="=1" resultVariable="x"/></extensionElements></scriptTask>
    <exclusiveGateway id="again" default="out"/>
    <intermediateCatchEvent id="wait"><messageEventDefinition messageRef="m"/></intermediateCatchEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="merge"/>
    <sequenceFlow id="f2" sourceRef="merge" targetRef="work"/>
    <sequenceFlow id="f3" sourceRef="work" targetRef="again"/>
    <sequenceFlow id="back" sourceRef="again" targetRef="merge"><conditionExpression>=x &lt; 3</conditionExpression></sequenceFlow>
    <sequenceFlow id="out" sourceRef="again" targetRef="wait"/>
    <sequenceFlow id="f4" sourceRef="wait" targetRef="e"/>
  </process>
</definitions>`)
	cycle := cp.WaitlessCycle()
	if strings.Join(cycle, ",") != "merge,work,again" {
		t.Errorf("cycle = %v, want merge,work,again", cycle)
	}
	points := cp.MessageCatchPoints()
	if len(points) != 1 || points[0].Element != "wait" || points[0].Correlated {
		t.Errorf("catch points = %+v, want one uncorrelated catch at wait", points)
	}
}
