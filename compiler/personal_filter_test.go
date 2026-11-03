package compiler

import (
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/expr"
)

// A name a filter alone reads may be a key of the elements, and before CompileAuto bound
// such names that is all it ever was. A process that declares a personal variable of the
// same name, and filters a list on that key, deployed — and must keep deploying: a stored
// deployment the server cannot rebuild stops the server at startup. So the name stays out
// of the engine's bindings (the ciphertext never reaches the filter) instead of tripping
// ADR-0314's refusal.
func TestAFilterKeyNamedLikeAPersonalVariableDeploys(t *testing.T) {
	body := `<bpmn:exclusiveGateway id="gw" default="fd"/>
    <bpmn:endEvent id="e2"/>
    <bpmn:sequenceFlow id="fa" sourceRef="s" targetRef="gw"/>
    <bpmn:sequenceFlow id="fb" sourceRef="gw" targetRef="e2">
      <bpmn:conditionExpression>= count(people[vorname != null]) &gt; 0</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="fd" sourceRef="gw" targetRef="e"/>`
	cp, err := Parse(1, 1, strings.NewReader(personalModel(`atlas:personal="vorname" atlas:dataSubject="pnr"`, body)))
	if err != nil {
		t.Fatalf("Parse refused a filter on the elements' key: %v", err)
	}
	var cond *expr.Compiled
	for i := range cp.flows {
		if c := cp.flows[i].Condition; c != nil && strings.Contains(c.Source(), "people") {
			cond = c
		}
	}
	if cond == nil {
		t.Fatal("the condition was not compiled")
	}
	if slices.Contains(cond.Inputs(), "vorname") {
		t.Fatalf("inputs %v bind the personal variable vorname; the engine would hand the filter ciphertext", cond.Inputs())
	}
	if !slices.Contains(cond.Inputs(), "people") {
		t.Fatalf("inputs %v lost people", cond.Inputs())
	}
	v, err := cond.Eval(map[string]expr.Value{"people": expr.FromJSON([]any{
		map[string]any{"vorname": "Ada"}, map[string]any{"pnr": "2"},
	})})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := expr.ToJSON(v); got != "true" {
		t.Fatalf("condition = %s, want true", got)
	}
}

// The exemption is for the filter alone: the same name read outside a filter is the
// variable, and the deploy is still refused.
func TestAPersonalVariableReadBesideAFilterIsStillRefused(t *testing.T) {
	body := `<bpmn:exclusiveGateway id="gw" default="fd"/>
    <bpmn:endEvent id="e2"/>
    <bpmn:sequenceFlow id="fa" sourceRef="s" targetRef="gw"/>
    <bpmn:sequenceFlow id="fb" sourceRef="gw" targetRef="e2">
      <bpmn:conditionExpression>= vorname != null and count(people[vorname != null]) &gt; 0</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="fd" sourceRef="gw" targetRef="e"/>`
	_, err := Parse(1, 1, strings.NewReader(personalModel(`atlas:personal="vorname" atlas:dataSubject="pnr"`, body)))
	if err == nil || !strings.Contains(err.Error(), "vorname") {
		t.Fatalf("Parse = %v, want the personal-variable refusal naming vorname", err)
	}
}
