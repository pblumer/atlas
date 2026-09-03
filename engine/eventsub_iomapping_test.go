package engine_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// espMappingXML gives an event subprocess a zeebe:ioMapping that reads a process
// variable, adds to it, and writes it back — the read-modify-write a handler uses to
// keep a register across triggers.
//
// The main path parks on a message that is never published, so the instance (and the
// armed trigger) stay alive while the handler's trigger fires.
const espMappingXML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <message id="Msg_go" name="go">
    <extensionElements><zeebe:subscription correlationKey="= &quot;K&quot;"/></extensionElements>
  </message>
  <message id="Msg_never" name="never">
    <extensionElements><zeebe:subscription correlationKey="= &quot;K&quot;"/></extensionElements>
  </message>
  <process id="esp-mapping" isExecutable="true">
    <startEvent id="start"/>
    <scriptTask id="seed">
      <extensionElements><zeebe:script expression="= [1]" resultVariable="reg"/></extensionElements>
    </scriptTask>
    <intermediateCatchEvent id="park">
      <messageEventDefinition messageRef="Msg_never"/>
    </intermediateCatchEvent>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="seed"/>
    <sequenceFlow id="f2" sourceRef="seed" targetRef="park"/>
    <sequenceFlow id="f3" sourceRef="park" targetRef="end"/>
    <subProcess id="esp" triggeredByEvent="true">
      <extensionElements>
        <zeebe:ioMapping>
          <zeebe:input source="= reg" target="cur"/>
          <zeebe:output source="= append(cur, 2)" target="reg"/>
        </zeebe:ioMapping>
      </extensionElements>
      <startEvent id="esp_start" isInterrupting="false">
        <messageEventDefinition messageRef="Msg_go"/>
      </startEvent>
      <endEvent id="esp_end"/>
      <sequenceFlow id="ef1" sourceRef="esp_start" targetRef="esp_end"/>
    </subProcess>
  </process>
</definitions>`

// TestEventSubProcessMappingsRunOnTheHandlerNotTheArmedTrigger pins down a defect
// found while modelling an identity register: an event subprocess with an
// ioMapping destroyed the variable it was supposed to maintain.
//
// The cause is that an armed trigger is an element instance of its own — a
// TypeEventSubProcessStart whose ElementId points at the *handler* node, so it can
// read the trigger detail. The generic mapping code looks mappings up by element id
// and so found the handler's, and applied them to the guard: the input evaluated at
// instance creation, when the variable did not exist yet, and the output wrote that
// null back over the real value the moment the trigger fired — before the handler
// ever ran.
//
// Mappings belong to the handler's execution. After one trigger the register must
// read [1,2]: seeded by the main path, extended once by the handler.
func TestEventSubProcessMappingsRunOnTheHandlerNotTheArmedTrigger(t *testing.T) {
	dir := t.TempDir()
	cp, err := compiler.Parse(1, 1, strings.NewReader(espMappingXML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	h := openHarness(t, dir)
	defer h.close(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	piKey := model.NewKey(1, 1)
	if v := readVar(t, h.store, piKey, "reg"); v == nil || v.Text != "[1]" {
		t.Fatalf("reg after seeding = %v, want [1]", v)
	}

	p.PublishMessage("go", "K")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after trigger: %v", err)
	}
	v := readVar(t, h.store, piKey, "reg")
	if v == nil || v.Text != "[1,2]" {
		t.Errorf("reg after one trigger = %v, want [1,2] — the armed trigger applied the handler's mappings", v)
	}
}
