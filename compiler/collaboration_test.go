package compiler

import (
	"strings"
	"testing"
)

// collabBPMN is a two-pool collaboration: a Buyer pool throws an "order" message
// and a Seller pool catches it, plus a black-box "External system" pool with no
// process (a message-flow counterpart left unmodeled).
const collabBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:collaboration id="Collab_1">
    <bpmn:participant id="P_buyer" name="Buyer" processRef="buyer"/>
    <bpmn:participant id="P_seller" name="Seller" processRef="seller"/>
    <bpmn:participant id="P_ext" name="External system"/>
  </bpmn:collaboration>
  <bpmn:message id="Msg_order" name="order">
    <bpmn:extensionElements><zeebe:subscription correlationKey="= orderId"/></bpmn:extensionElements>
  </bpmn:message>
  <bpmn:process id="buyer" name="Buyer process" isExecutable="true">
    <bpmn:startEvent id="b_start"/>
    <bpmn:intermediateThrowEvent id="b_throw">
      <bpmn:messageEventDefinition messageRef="Msg_order"/>
    </bpmn:intermediateThrowEvent>
    <bpmn:endEvent id="b_end"/>
    <bpmn:sequenceFlow id="bf1" sourceRef="b_start" targetRef="b_throw"/>
    <bpmn:sequenceFlow id="bf2" sourceRef="b_throw" targetRef="b_end"/>
  </bpmn:process>
  <bpmn:process id="seller" isExecutable="true">
    <bpmn:startEvent id="s_start"/>
    <bpmn:intermediateCatchEvent id="s_catch">
      <bpmn:messageEventDefinition messageRef="Msg_order"/>
    </bpmn:intermediateCatchEvent>
    <bpmn:endEvent id="s_end"/>
    <bpmn:sequenceFlow id="sf1" sourceRef="s_start" targetRef="s_catch"/>
    <bpmn:sequenceFlow id="sf2" sourceRef="s_catch" targetRef="s_end"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseAllCollaboration(t *testing.T) {
	deployables, err := ParseAll(100, 1, strings.NewReader(collabBPMN))
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	// The black-box pool is skipped; the two executable processes remain, keyed
	// baseKey and baseKey+1 in document order.
	if len(deployables) != 2 {
		t.Fatalf("deployables = %d, want 2 (black-box pool skipped)", len(deployables))
	}
	buyer, seller := deployables[0], deployables[1]
	if buyer.Process.Key != 100 || seller.Process.Key != 101 {
		t.Errorf("keys = %d,%d, want 100,101", buyer.Process.Key, seller.Process.Key)
	}
	if got := buyer.Process.Intern(buyer.Process.BpmnProcessId); got != "buyer" {
		t.Errorf("first process id = %q, want buyer", got)
	}
	if buyer.PoolName != "Buyer" || seller.PoolName != "Seller" {
		t.Errorf("pool names = %q,%q, want Buyer,Seller", buyer.PoolName, seller.PoolName)
	}
	if buyer.ProcessName != "Buyer process" {
		t.Errorf("buyer process name = %q, want \"Buyer process\"", buyer.ProcessName)
	}
	// The buyer's throw and seller's catch resolved the shared message.
	bThrow := buyer.Process.Flow(buyer.Process.Outgoing(buyer.Process.StartEvents()[0])[0]).Target
	if buyer.Process.Node(bThrow).Type != TypeMessageThrowEvent {
		t.Errorf("buyer node after start = %v, want MessageThrowEvent", buyer.Process.Node(bThrow).Type)
	}
}

func TestParseNamedPicksProcess(t *testing.T) {
	cp, err := ParseNamed(5, 2, strings.NewReader(collabBPMN), "seller")
	if err != nil {
		t.Fatalf("ParseNamed: %v", err)
	}
	if cp.Key != 5 || cp.Version != 2 {
		t.Errorf("key/version = %d/%d, want 5/2", cp.Key, cp.Version)
	}
	if got := cp.Intern(cp.BpmnProcessId); got != "seller" {
		t.Errorf("process id = %q, want seller", got)
	}
	if _, err := ParseNamed(5, 2, strings.NewReader(collabBPMN), "nope"); err == nil {
		t.Fatal("ParseNamed for a missing process id: want an error, got nil")
	}
}

func TestParseAllNoExecutableProcess(t *testing.T) {
	// A collaboration whose only pool references a start-event-less process.
	const noStart = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:collaboration id="C"><bpmn:participant id="p" name="Only" processRef="proc"/></bpmn:collaboration>
  <bpmn:process id="proc"><bpmn:endEvent id="e"/></bpmn:process>
</bpmn:definitions>`
	if _, err := ParseAll(1, 1, strings.NewReader(noStart)); err == nil {
		t.Fatal("ParseAll with no executable process: want an error, got nil")
	}
}

func TestParseAllPropagatesCompileError(t *testing.T) {
	// A pool whose process has a script task with no expression fails to compile;
	// ParseAll surfaces that error rather than silently dropping the pool.
	const bad = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                     xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <bpmn:collaboration id="C"><bpmn:participant id="p" name="P" processRef="proc"/></bpmn:collaboration>
  <bpmn:process id="proc">
    <bpmn:startEvent id="s"/>
    <bpmn:scriptTask id="t">
      <bpmn:extensionElements><zeebe:script expression="" resultVariable="x"/></bpmn:extensionElements>
    </bpmn:scriptTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	if _, err := ParseAll(1, 1, strings.NewReader(bad)); err == nil {
		t.Fatal("ParseAll with an invalid pool process: want a compile error, got nil")
	}
}
