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

// A process whose sole entry point is a message start event: a correlating
// message instantiates it (ADR-0035). It references a top-level <message> the
// same way catch/throw events do.
const messageStartBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:message id="Msg_req" name="request">
    <bpmn:extensionElements>
      <zeebe:subscription correlationKey="= orderId"/>
    </bpmn:extensionElements>
  </bpmn:message>
  <bpmn:process id="responder" isExecutable="true">
    <bpmn:startEvent id="start">
      <bpmn:messageEventDefinition id="sed" messageRef="Msg_req"/>
    </bpmn:startEvent>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseMessageStartEvent(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(messageStartBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// It is a process entry point, so the engine can activate it once instantiated.
	if len(cp.StartEvents()) != 1 {
		t.Fatalf("start events = %d, want 1", len(cp.StartEvents()))
	}
	start := cp.StartEvents()[0]
	if cp.Node(start).Type != TypeMessageStartEvent {
		t.Fatalf("start node type = %v, want MessageStartEvent", cp.Node(start).Type)
	}

	// It is indexed as a message start so deploy can register the subscription.
	starts := cp.MessageStarts()
	if len(starts) != 1 {
		t.Fatalf("message starts = %d, want 1", len(starts))
	}
	if starts[0].MessageName != "request" {
		t.Errorf("message-start name = %q, want request", starts[0].MessageName)
	}
	if md := cp.MessageStart(cp.Node(start).Detail); md.MessageName != "request" {
		t.Errorf("MessageStart(detail).name = %q, want request", md.MessageName)
	}
	if starts[0].CorrelationKey == nil {
		t.Error("message-start correlation key expr is nil, want compiled")
	}
}

// TestParseAllMessageStartPoolExecutable checks that a collaboration pool whose
// only start event is a message start event is compiled (not skipped as a
// black box), so a message can instantiate it.
func TestParseAllMessageStartPoolExecutable(t *testing.T) {
	const collab = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:collaboration id="C">
    <bpmn:participant id="P_buyer" name="Buyer" processRef="buyer"/>
    <bpmn:participant id="P_seller" name="Seller" processRef="seller"/>
  </bpmn:collaboration>
  <bpmn:message id="Msg_req" name="request">
    <bpmn:extensionElements><zeebe:subscription correlationKey="= orderId"/></bpmn:extensionElements>
  </bpmn:message>
  <bpmn:process id="buyer" isExecutable="true">
    <bpmn:startEvent id="b_start"/>
    <bpmn:intermediateThrowEvent id="b_throw"><bpmn:messageEventDefinition messageRef="Msg_req"/></bpmn:intermediateThrowEvent>
    <bpmn:endEvent id="b_end"/>
    <bpmn:sequenceFlow id="bf1" sourceRef="b_start" targetRef="b_throw"/>
    <bpmn:sequenceFlow id="bf2" sourceRef="b_throw" targetRef="b_end"/>
  </bpmn:process>
  <bpmn:process id="seller" isExecutable="true">
    <bpmn:startEvent id="s_start"><bpmn:messageEventDefinition messageRef="Msg_req"/></bpmn:startEvent>
    <bpmn:endEvent id="s_end"/>
    <bpmn:sequenceFlow id="sf1" sourceRef="s_start" targetRef="s_end"/>
  </bpmn:process>
</bpmn:definitions>`
	deployables, err := ParseAll(100, 1, strings.NewReader(collab))
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	if len(deployables) != 2 {
		t.Fatalf("deployables = %d, want 2 (message-start pool must not be skipped)", len(deployables))
	}
	seller := deployables[1].Process
	if len(seller.MessageStarts()) != 1 || seller.MessageStarts()[0].MessageName != "request" {
		t.Errorf("seller message starts = %v, want one named request", seller.MessageStarts())
	}
}

func TestParseMessageStartUnknownRef(t *testing.T) {
	const bad = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"><bpmn:messageEventDefinition messageRef="Missing"/></bpmn:startEvent>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	if _, err := Parse(1, 1, strings.NewReader(bad)); err == nil {
		t.Fatal("Parse: want an error for a message start with an unknown messageRef, got nil")
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
