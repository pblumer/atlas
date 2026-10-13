package compiler

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/expr"
)

const startFormBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:process id="onboard" isExecutable="true">
    <bpmn:startEvent id="start" name="Start">
      <bpmn:extensionElements>
        <zeebe:formDefinition formId="onboarding-form"/>
      </bpmn:extensionElements>
    </bpmn:startEvent>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`

func TestStartEventForm(t *testing.T) {
	cp, err := Parse(10, 1, strings.NewReader(startFormBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := cp.StartFormId(); got != "onboarding-form" {
		t.Errorf("StartFormId = %q, want \"onboarding-form\"", got)
	}
}

func TestStartEventNoForm(t *testing.T) {
	const noForm = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="start"/>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := Parse(10, 1, strings.NewReader(noForm))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := cp.StartFormId(); got != "" {
		t.Errorf("StartFormId = %q, want \"\" (no start form)", got)
	}
}

// A process that declares start variables *and* links a start form keeps both: the
// form's id and the declaration, in the order authored. The start surfaces need the
// declaration beside the form, because the form does not ask for every declared value.
func TestStartVariablesBesideStartForm(t *testing.T) {
	const withVars = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <bpmn:process id="ucc" isExecutable="true">
    <bpmn:extensionElements>
      <atlas:startForm>
        <atlas:startVariable name="TenantId" type="string" required="true"/>
        <atlas:startVariable name=" Retries " type="number" default="3"/>
        <atlas:startVariable name="Options" type="json" default="{&quot;a&quot;:1}"/>
        <atlas:startVariable name="Dry" type="boolean" default="false" required="false"/>
      </atlas:startForm>
    </bpmn:extensionElements>
    <bpmn:startEvent id="start">
      <bpmn:extensionElements><zeebe:formDefinition formId="collective-line"/></bpmn:extensionElements>
    </bpmn:startEvent>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := Parse(10, 1, strings.NewReader(withVars))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := cp.StartFormId(); got != "collective-line" {
		t.Errorf("StartFormId = %q, want \"collective-line\"", got)
	}
	// Each default is also read into the form the engine seeds it in
	// (ADR-draft-start-variable-defaults).
	want := []StartVariable{
		{Name: "TenantId", Type: "string", Required: true},
		{Name: "Retries", Type: "number", Default: "3", HasDefault: true, Kind: expr.KindNumber, Text: "3"},
		{Name: "Options", Type: "json", Default: `{"a":1}`, HasDefault: true, Kind: expr.KindJSON, Text: `{"a":1}`},
		{Name: "Dry", Type: "boolean", Default: "false", HasDefault: true, Kind: expr.KindBool},
	}
	got := cp.StartVariables()
	if len(got) != len(want) {
		t.Fatalf("StartVariables = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("StartVariables[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// The declaration is read the way the Modeler reads it and never refuses a deploy: a
// stored definition compiles again on every restart, so an accepted model must keep
// loading. A nameless row is dropped, an unknown type reads as string, and a repeated
// name keeps its first row.
func TestStartVariablesLenient(t *testing.T) {
	const odd = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:extensionElements>
      <atlas:startForm>
        <atlas:startVariable name="" type="number"/>
        <atlas:startVariable name="when" type="date" default="2026-01-01"/>
        <atlas:startVariable name="when" type="number" default="7"/>
      </atlas:startForm>
    </bpmn:extensionElements>
    <bpmn:startEvent id="start"/>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := Parse(10, 1, strings.NewReader(odd))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := cp.StartVariables()
	want := StartVariable{Name: "when", Type: "string", Default: "2026-01-01",
		HasDefault: true, Kind: expr.KindString, Text: "2026-01-01"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("StartVariables = %+v, want [%+v]", got, want)
	}
}

func TestNoStartVariables(t *testing.T) {
	cp, err := Parse(10, 1, strings.NewReader(startFormBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := cp.StartVariables(); got != nil {
		t.Errorf("StartVariables = %+v, want nil (none declared)", got)
	}
}
