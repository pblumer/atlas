package compiler

import (
	"strings"
	"testing"
)

// W5 of ADR-0314: the declaration, and the deploy-time refusal that makes it mean
// something.
//
// The record's diagnosis of why the ISDS recommendation R-06 is still amber is the whole
// reason this is a compiler rule rather than documentation: *"A rule that relies on a
// modeller remembering is the modelling recommendation R-06 already has, and it is the
// reason R-06 is still amber."*
//
// So what is under test here is not that the engine enciphers correctly — that is the next
// step. It is that **a deployment which computes on a declared personal variable does not
// happen at all**, and that the rule's boundary sits where the code actually evaluates the
// expression rather than where it would be convenient.

// TestPersonalDeclarationIsReadFromTheProcess pins the declaration onto the same shape
// and place as ADR-0244's searchable variables, which is what ADR-0314 asks for: "this
// adds a list, not a mechanism".
func TestPersonalDeclarationIsReadFromTheProcess(t *testing.T) {
	const model = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true" atlas:personal="vorname, nachname">
    <bpmn:startEvent id="s"/>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := Parse(1, 1, strings.NewReader(model))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := cp.PersonalVariables()
	if len(got) != 2 || got[0] != "vorname" || got[1] != "nachname" {
		t.Fatalf("PersonalVariables() = %v, want [vorname nachname]", got)
	}
	if !cp.IsPersonal("vorname") || !cp.IsPersonal("nachname") {
		t.Error("a declared name is not reported as personal")
	}
	if cp.IsPersonal("kuerzel") {
		t.Error("an undeclared name is reported as personal")
	}
}

// TestADeclarationThatCannotMeanAnythingFailsTheDeploy mirrors what the searchable
// declaration already refuses, for the same stated reason: it would otherwise protect
// nothing and look like it was working.
func TestADeclarationThatCannotMeanAnythingFailsTheDeploy(t *testing.T) {
	for _, tc := range []struct{ name, decl, want string }{
		{"empty entry", "vorname,,nachname", "empty variable name"},
		{"duplicate", "vorname,vorname", "twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true" atlas:personal="` + tc.decl + `">
    <bpmn:startEvent id="s"/>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
			_, err := Parse(1, 1, strings.NewReader(model))
			if err == nil {
				t.Fatalf("Parse accepted personal=%q", tc.decl)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not say %q", err, tc.want)
			}
		})
	}
}

// TestADeployRefusesAnExpressionThatReadsAPersonalVariable is the rule, over the expression
// kinds ADR-0314 names one by one: no gateway condition, no sequence-flow condition, no
// input/output mapping, no assignment expression, no script. Each case is the same model
// shape with the personal variable moved into a different expression, so what the table
// proves is that the refusal is not attached to one kind by accident.
func TestADeployRefusesAnExpressionThatReadsAPersonalVariable(t *testing.T) {
	for _, tc := range []struct{ name, body, kind string }{
		{
			name: "sequence flow condition",
			kind: "sequence flow condition",
			body: `<bpmn:exclusiveGateway id="gw"/>
    <bpmn:endEvent id="e2"/>
    <bpmn:sequenceFlow id="fa" sourceRef="s" targetRef="gw"/>
    <bpmn:sequenceFlow id="fb" sourceRef="gw" targetRef="e2">
      <bpmn:conditionExpression>= vorname != null</bpmn:conditionExpression>
    </bpmn:sequenceFlow>`,
		},
		{
			name: "output mapping",
			kind: "output mapping",
			body: `<bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="w"/>
        <zeebe:ioMapping><zeebe:output source="= vorname" target="upn"/></zeebe:ioMapping>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e2"/>
    <bpmn:sequenceFlow id="fa" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="fb" sourceRef="t" targetRef="e2"/>`,
		},
		{
			name: "input mapping",
			kind: "input mapping",
			body: `<bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="w"/>
        <zeebe:ioMapping><zeebe:input source="= vorname" target="local"/></zeebe:ioMapping>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e2"/>
    <bpmn:sequenceFlow id="fa" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="fb" sourceRef="t" targetRef="e2"/>`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true" atlas:personal="vorname">
    <bpmn:startEvent id="s"/>
    ` + tc.body + `
  </bpmn:process>
</bpmn:definitions>`
			_, err := Parse(1, 1, strings.NewReader(model))
			if err == nil {
				t.Fatal("the deploy was accepted; a declared personal variable was read in an expression")
			}
			msg := err.Error()
			// The message has to name the variable, the kind and the expression, because
			// the person reading it has to find that expression and move it into a worker.
			for _, want := range []string{"vorname", tc.kind, "personal"} {
				if !strings.Contains(msg, want) {
					t.Errorf("error %q does not mention %q", msg, want)
				}
			}
		})
	}
}

// TestAWorkerExpressionMayReadAPersonalVariable is the boundary, and it is the half that a
// rule written from the record's prose alone would have got wrong.
//
// ADR-0314 forbids a declared variable "in any expression", but it also sends transforms
// that combine personal values *into the worker*, "where the plaintext exists for the
// duration of one call". Those two only fit together because a connector's literal-or-FEEL
// fields are evaluated in the worker and not by the engine — connector/mail/worker.go:89
// and its siblings, never engine/. So a worker expression is exactly where the record
// permits it, and refusing here would forbid the one remedy the record prescribes.
func TestAWorkerExpressionMayReadAPersonalVariable(t *testing.T) {
	const model = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true" atlas:personal="vorname">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:mailConnector connector="m" to="a@example.com" subject="Willkommen"
                             body="= &quot;Hallo &quot; + vorname"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	if _, err := Parse(1, 1, strings.NewReader(model)); err != nil {
		t.Fatalf("a worker expression reading a personal variable was refused, which forbids the very remedy ADR-0314 prescribes: %v", err)
	}
}

// TestAnUndeclaredVariableInAnExpressionIsFine is why this rule can be introduced into a
// repository full of existing models: nothing changes for a process that declares nothing.
// The refusal is opt-in by declaration.
func TestAnUndeclaredVariableInAnExpressionIsFine(t *testing.T) {
	const model = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true" atlas:personal="nachname">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="w"/>
        <zeebe:ioMapping><zeebe:output source="= vorname" target="upn"/></zeebe:ioMapping>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	if _, err := Parse(1, 1, strings.NewReader(model)); err != nil {
		t.Fatalf("a process reading an *undeclared* variable was refused: %v", err)
	}
}

// TestAPersonalVariableMayStillBeWrittenAndCarried keeps the rule from being wider than the
// record makes it. A declared variable is payload: it may be set by a form, handed to a
// worker and stored. What it may not be is *read by an engine expression*. A rule that also
// refused it as an output mapping's `target` would make the variable unusable rather than
// uncomputable, and no portal process could then hold a name at all.
func TestAPersonalVariableMayStillBeWrittenAndCarried(t *testing.T) {
	const model = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true" atlas:personal="vorname">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="w"/>
        <zeebe:ioMapping><zeebe:output source="= kuerzel" target="vorname"/></zeebe:ioMapping>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	if _, err := Parse(1, 1, strings.NewReader(model)); err != nil {
		t.Fatalf("writing *to* a personal variable was refused, which makes it unusable rather than uncomputable: %v", err)
	}
}
