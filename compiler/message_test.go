package compiler

import (
	"strings"
	"testing"
)

// A collaboration-shaped model reduced to what the compiler executes: a message
// intermediate catch event and a message intermediate throw event, both
// referencing a top-level <message> whose Zeebe subscription carries the FEEL
// correlation-key expression.
const messageBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:message id="Msg_order" name="order-received">
    <bpmn:extensionElements>
      <zeebe:subscription correlationKey="= orderId"/>
    </bpmn:extensionElements>
  </bpmn:message>
  <bpmn:process id="fulfil" isExecutable="true">
    <bpmn:startEvent id="start"/>
    <bpmn:intermediateThrowEvent id="throw">
      <bpmn:messageEventDefinition id="ted" messageRef="Msg_order"/>
    </bpmn:intermediateThrowEvent>
    <bpmn:intermediateCatchEvent id="catch">
      <bpmn:messageEventDefinition id="ced" messageRef="Msg_order"/>
    </bpmn:intermediateCatchEvent>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="throw"/>
    <bpmn:sequenceFlow id="f2" sourceRef="throw" targetRef="catch"/>
    <bpmn:sequenceFlow id="f3" sourceRef="catch" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseMessageEvents(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(messageBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	start := cp.StartEvents()[0]
	throw := cp.Flow(cp.Outgoing(start)[0]).Target
	if cp.Node(throw).Type != TypeMessageThrowEvent {
		t.Fatalf("node after start = %v, want MessageThrowEvent", cp.Node(throw).Type)
	}
	td := cp.MessageThrow(cp.Node(throw).Detail)
	if td.MessageName != "order-received" {
		t.Errorf("throw message name = %q, want order-received", td.MessageName)
	}
	if td.CorrelationKey == nil {
		t.Fatal("throw correlation key expr is nil, want compiled")
	}
	if got := td.CorrelationKey.Inputs(); len(got) != 1 || got[0] != "orderId" {
		t.Errorf("throw key inputs = %v, want [orderId]", got)
	}

	catch := cp.Flow(cp.Outgoing(throw)[0]).Target
	if cp.Node(catch).Type != TypeMessageCatchEvent {
		t.Fatalf("node after throw = %v, want MessageCatchEvent", cp.Node(catch).Type)
	}
	cd := cp.MessageCatch(cp.Node(catch).Detail)
	if cd.MessageName != "order-received" {
		t.Errorf("catch message name = %q, want order-received", cd.MessageName)
	}
	if cd.CorrelationKey == nil {
		t.Fatal("catch correlation key expr is nil, want compiled")
	}
}

func TestParseMessageErrors(t *testing.T) {
	cases := []struct {
		name string
		xml  string
	}{
		{
			name: "unknown message ref",
			xml: `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:intermediateCatchEvent id="c">
      <bpmn:messageEventDefinition messageRef="Missing"/>
    </bpmn:intermediateCatchEvent>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="c"/>
    <bpmn:sequenceFlow id="f2" sourceRef="c" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`,
		},
		{
			name: "message without a name",
			xml: `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:message id="Msg"/>
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:intermediateCatchEvent id="c">
      <bpmn:messageEventDefinition messageRef="Msg"/>
    </bpmn:intermediateCatchEvent>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="c"/>
    <bpmn:sequenceFlow id="f2" sourceRef="c" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`,
		},
		{
			name: "throw event that is not a message",
			xml: `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:intermediateThrowEvent id="t"/>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(1, 1, strings.NewReader(tc.xml)); err == nil {
				t.Fatal("Parse: want an error, got nil")
			}
		})
	}
}
