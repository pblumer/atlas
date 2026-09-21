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
  <bpmn:process id="p" isExecutable="true" atlas:personal="vorname, nachname" atlas:dataSubject="pnr">
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
  <bpmn:process id="p" isExecutable="true" atlas:personal="` + tc.decl + `" atlas:dataSubject="pnr">
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
  <bpmn:process id="p" isExecutable="true" atlas:personal="vorname" atlas:dataSubject="pnr">
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
  <bpmn:process id="p" isExecutable="true" atlas:personal="vorname" atlas:dataSubject="pnr">
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
  <bpmn:process id="p" isExecutable="true" atlas:personal="nachname" atlas:dataSubject="pnr">
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

// TestAnEngineWriteToAPersonalVariableIsRefused is the rule's other direction, and it
// corrects what this test used to assert.
//
// It used to accept an output mapping writing *into* a declared variable, on the reasoning
// that refusing it "would make the variable unusable rather than uncomputable, and no
// portal process could then hold a name at all". That reasoning was wrong on the facts.
// The engine holds no key and cannot encipher, so a value it computes into a declared
// variable is stored readable, in the log, permanently un-erasable — while the declaration
// says the opposite. And the variable is not unusable without it: a name arrives from a
// form, from a worker's result or from an operator, and each of those is an edge that
// seals. Those are exactly how names actually arrive.
//
// The writer refuses such a write at runtime too (engine/personal_test.go), for the paths
// no deploy can see. Refusing it here as well is not redundancy: it turns a parked incident
// somebody has to diagnose into a deploy that does not happen.
func TestAnEngineWriteToAPersonalVariableIsRefused(t *testing.T) {
	body := `<bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="w"/>
        <zeebe:ioMapping><zeebe:output source="= kuerzel" target="vorname"/></zeebe:ioMapping>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:sequenceFlow id="fa" sourceRef="s" targetRef="t"/>`
	_, err := Parse(1, 1, strings.NewReader(personalModel(`atlas:personal="vorname" atlas:dataSubject="pnr"`, body)))
	if err == nil {
		t.Fatal("an output mapping computing a value into a personal variable was accepted; it would be stored in the clear")
	}
	for _, want := range []string{"vorname", "output mapping", "holds no key"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// TestAPersonalVariableIsStillUsable is what keeps the rule from being wider than the
// mechanism needs. A declared variable is payload: it may arrive, be carried through the
// process and be handed to a worker. Nothing in the model has to mention it for that to
// work — which is why a process that declares one and computes on none deploys.
func TestAPersonalVariableIsStillUsable(t *testing.T) {
	body := `<bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="w"/>
        <zeebe:ioMapping><zeebe:output source="= kuerzel" target="kennung"/></zeebe:ioMapping>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:sequenceFlow id="fa" sourceRef="s" targetRef="t"/>`
	cp, err := Parse(1, 1, strings.NewReader(personalModel(`atlas:personal="vorname" atlas:dataSubject="pnr"`, body)))
	if err != nil {
		t.Fatalf("a process that merely carries a personal variable was refused: %v", err)
	}
	if !cp.IsPersonal("vorname") {
		t.Error("the declaration did not survive the build")
	}
}

// personalModel builds the smallest process that carries a declaration, with the given
// attributes on bpmn:process and an optional extra element body.
func personalModel(attrs, body string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true" ` + attrs + `>
    <bpmn:startEvent id="s"/>
    ` + body + `
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

// TestADeclarationWithoutADataSubjectIsRefused is the question ADR-0314 left implicit
// and the one that cannot be left implicit: the record says a declared variable "is
// enciphered under that data key" without saying how an instance knows whose key that
// is. Enciphering under the wrong subject makes an erasure either ineffective — the
// wrong key destroyed — or too broad, and both failures are silent.
//
// So the two declarations are refused apart. Personal data with no subject could never
// be erased, which is the entire purpose; a subject with nothing personal enciphers
// nothing and only looks like protection.
func TestADeclarationWithoutADataSubjectIsRefused(t *testing.T) {
	for _, tc := range []struct{ name, attrs, want string }{
		{"personal without a subject", `atlas:personal="vorname"`, "no atlas:dataSubject"},
		{"a subject with nothing personal", `atlas:dataSubject="pnr"`, "no variable is declared personal"},
		{"two subjects", `atlas:personal="vorname" atlas:dataSubject="pnr, andere"`, "more than one variable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(1, 1, strings.NewReader(personalModel(tc.attrs, "")))
			if err == nil {
				t.Fatalf("Parse accepted %s", tc.attrs)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not say %q", err, tc.want)
			}
		})
	}
}

// TestTheDataSubjectMayNotItselfBePersonal closes the circularity. The subject variable
// is what the enciphering edge looks the key up by; enciphered, there would be nothing
// left to look it up with, and the instance's values would be sealed under a key nobody
// could name. It stays in the clear because it is a reference, which is exactly what
// ADR-0314's primary defence keeps readable.
func TestTheDataSubjectMayNotItselfBePersonal(t *testing.T) {
	_, err := Parse(1, 1, strings.NewReader(personalModel(`atlas:personal="vorname,pnr" atlas:dataSubject="pnr"`, "")))
	if err == nil {
		t.Fatal("Parse accepted a data subject that is itself declared personal")
	}
	if !strings.Contains(err.Error(), "itself declared personal") {
		t.Errorf("error %q does not name the problem", err)
	}
}

// TestTheDataSubjectIsStillARoutableValue is the other half of that, and it is what
// keeps the rule usable: ADR-0314 sends routing onto references, so the one reference
// this mechanism introduces must be readable by expressions like any other variable.
// A rule that refused it would leave a process unable to branch on the very id it
// enciphers by.
func TestTheDataSubjectIsStillARoutableValue(t *testing.T) {
	body := `<bpmn:exclusiveGateway id="gw"/>
    <bpmn:endEvent id="e2"/>
    <bpmn:sequenceFlow id="fa" sourceRef="s" targetRef="gw"/>
    <bpmn:sequenceFlow id="fb" sourceRef="gw" targetRef="e2">
      <bpmn:conditionExpression>= starts with(pnr, "P-")</bpmn:conditionExpression>
    </bpmn:sequenceFlow>`
	cp, err := Parse(1, 1, strings.NewReader(personalModel(`atlas:personal="vorname" atlas:dataSubject="pnr"`, body)))
	if err != nil {
		t.Fatalf("a gateway reading the data subject's id was refused: %v", err)
	}
	if got := cp.DataSubjectVariable(); got != "pnr" {
		t.Errorf("DataSubjectVariable() = %q, want pnr", got)
	}
}

// TestPersonalAndSearchableAreMutuallyExclusive refuses a promise the mechanism cannot
// keep. The value index stores what the engine sees, and for a declared variable that
// is ciphertext under a random nonce: two writes of the same name produce different
// bytes, so an exact match can never match. Accepting the declaration would build an
// index that answers every query with nothing, which is the failure mode hardest to
// notice — a search that finds nothing looks like a search over data that is not there.
func TestPersonalAndSearchableAreMutuallyExclusive(t *testing.T) {
	_, err := Parse(1, 1, strings.NewReader(personalModel(`atlas:personal="vorname" atlas:searchable="vorname" atlas:dataSubject="pnr"`, "")))
	if err == nil {
		t.Fatal("Parse accepted a variable declared both personal and searchable")
	}
	for _, want := range []string{"vorname", "personal and searchable", "ciphertext"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	// The subject id, by contrast, is exactly what an operator needs to find a
	// subject's instances before erasing them — so declaring *it* searchable is not
	// only allowed, it is the intended combination.
	if _, err := Parse(1, 1, strings.NewReader(personalModel(`atlas:personal="vorname" atlas:searchable="pnr" atlas:dataSubject="pnr"`, ""))); err != nil {
		t.Errorf("a searchable data subject was refused, which is how a subject's instances are found: %v", err)
	}
}
