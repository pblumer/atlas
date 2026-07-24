package compiler

import (
	"strings"
	"testing"
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
