package compiler

import (
	"strings"
	"testing"
)

const boundaryTimerBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="publish" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="tweet">
      <extensionElements><zeebe:taskDefinition type="post"/></extensionElements>
    </serviceTask>
    <boundaryEvent id="timeout" attachedToRef="tweet">
      <timerEventDefinition><timeDuration>PT30M</timeDuration></timerEventDefinition>
    </boundaryEvent>
    <endEvent id="done"/>
    <endEvent id="escalated"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="tweet"/>
    <sequenceFlow id="f2" sourceRef="tweet" targetRef="done"/>
    <sequenceFlow id="f3" sourceRef="timeout" targetRef="escalated"/>
  </process>
</definitions>`

func TestParseBoundaryTimerInterrupting(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(boundaryTimerBPMN))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	host := nodeByBpmnId(t, cp, "tweet")
	bevs := cp.BoundaryEvents(host.ElementId)
	if len(bevs) != 1 {
		t.Fatalf("host boundary events = %d, want 1", len(bevs))
	}
	be := cp.Node(bevs[0])
	if be.Type != TypeBoundaryEvent {
		t.Fatalf("boundary node type = %s, want BoundaryEvent", be.Type)
	}
	d := cp.BoundaryEvent(be.Detail)
	if d.HostNode != host.ElementId {
		t.Errorf("HostNode = %d, want %d", d.HostNode, host.ElementId)
	}
	if !d.Interrupting {
		t.Error("expected interrupting by default (no cancelActivity)")
	}
	if d.Kind != BoundaryTimer {
		t.Errorf("Kind = %d, want BoundaryTimer", d.Kind)
	}
	if d.DurationNanos != 30*60*1e9 {
		t.Errorf("DurationNanos = %d, want %d", d.DurationNanos, int64(30*60*1e9))
	}
	// The boundary event carries an outgoing flow like any node.
	if out := cp.Outgoing(be.ElementId); len(out) != 1 {
		t.Errorf("boundary outgoing flows = %d, want 1", len(out))
	}
}

func TestParseBoundaryNonInterrupting(t *testing.T) {
	const bpmn = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <serviceTask id="t"><extensionElements><zeebe:taskDefinition type="x"/></extensionElements></serviceTask>
    <boundaryEvent id="b" attachedToRef="t" cancelActivity="false">
      <timerEventDefinition><timeDuration>PT5S</timeDuration></timerEventDefinition>
    </boundaryEvent>
    <endEvent id="e"/>
    <endEvent id="e2"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
    <sequenceFlow id="f3" sourceRef="b" targetRef="e2"/>
  </process>
</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	be := cp.Node(cp.BoundaryEvents(nodeByBpmnId(t, cp, "t").ElementId)[0])
	if d := cp.BoundaryEvent(be.Detail); d.Interrupting {
		t.Error("cancelActivity=false must be non-interrupting")
	}
}

func TestParseBoundaryMessage(t *testing.T) {
	const bpmn = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <message id="Msg" name="cancel">
    <extensionElements><zeebe:subscription correlationKey="=orderId"/></extensionElements>
  </message>
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <userTask id="t"/>
    <boundaryEvent id="b" attachedToRef="t">
      <messageEventDefinition messageRef="Msg"/>
    </boundaryEvent>
    <endEvent id="e"/>
    <endEvent id="e2"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
    <sequenceFlow id="f3" sourceRef="b" targetRef="e2"/>
  </process>
</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	be := cp.Node(cp.BoundaryEvents(nodeByBpmnId(t, cp, "t").ElementId)[0])
	d := cp.BoundaryEvent(be.Detail)
	if d.Kind != BoundaryMessage {
		t.Fatalf("Kind = %d, want BoundaryMessage", d.Kind)
	}
	if d.MessageName != "cancel" {
		t.Errorf("MessageName = %q, want \"cancel\"", d.MessageName)
	}
	if d.CorrelationKey == nil {
		t.Error("expected a compiled correlation key")
	}
}

func TestParseBoundaryErrors(t *testing.T) {
	cases := []struct {
		name, bpmn, want string
	}{
		{
			name: "unknown attachedToRef",
			bpmn: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <startEvent id="s"/><endEvent id="e"/>
    <boundaryEvent id="b" attachedToRef="ghost">
      <timerEventDefinition><timeDuration>PT1S</timeDuration></timerEventDefinition>
    </boundaryEvent>
    <sequenceFlow id="f1" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`,
			want: "unknown activity",
		},
		{
			name: "unsupported definition",
			bpmn: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <serviceTask id="t"><extensionElements><zeebe:taskDefinition type="x"/></extensionElements></serviceTask>
    <boundaryEvent id="b" attachedToRef="t"><errorEventDefinition/></boundaryEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </process>
</definitions>`,
			want: "only timer and message",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(1, 1, strings.NewReader(tc.bpmn))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

// nodeByBpmnId finds a compiled node by its source BPMN id. Every node in a
// parsed process carries a recorded id, and ElementBpmnId returns "" only past
// the last node, so the scan terminates at the first empty id.
func nodeByBpmnId(t *testing.T, cp *CompiledProcess, id string) *CompiledNode {
	t.Helper()
	for i := int32(0); cp.ElementBpmnId(i) != ""; i++ {
		if cp.ElementBpmnId(i) == id {
			return cp.Node(i)
		}
	}
	t.Fatalf("no node with bpmn id %q", id)
	return nil
}
