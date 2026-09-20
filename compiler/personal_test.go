package compiler

import (
	"strings"
	"testing"
)

// W5 of ADR-0314, first step: the declaration itself.
//
// This is the data the rule needs, and deliberately not yet the rule. A declaration with
// no deploy-time refusal behind it is precisely what ADR-0314 says is not enough — its
// own diagnosis of why the ISDS recommendation R-06 is still amber is *"a rule that
// relies on a modeller remembering"* — so nothing here should be read as closing that
// risk. The refusal is the next step on this branch, and it stopped short of this commit
// on a boundary question the record does not settle: whether a worker expression reading
// a personal variable is a violation or exactly the place the plaintext legitimately
// exists, since ADR-0314 permits deciphering in the post-commit phase where the job
// payload is assembled.
//
// What the declaration does buy, on its own and immediately: the engine can ask one
// question per variable write about whether a value is personal, in the same shape and
// the same place as ADR-0244's searchable variables — which is what the record means by
// "this adds a list, not a mechanism".

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
