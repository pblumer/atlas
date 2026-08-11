package compiler

import (
	"strings"
	"testing"
)

// TestParseSendTask checks that a send task with a zeebe:taskDefinition compiles to a
// TypeSendTask carrying the job type and retries, and that it is an activity — a boundary
// event may attach to it (ADR-0112). A send task is a service task under a different label:
// it creates a job and waits, so it reuses ServiceTaskDetail and the activity machinery.
func TestParseSendTask(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <sendTask id="notify">
	      <extensionElements><zeebe:taskDefinition type="send-email" retries="5"/></extensionElements>
	    </sendTask>
	    <boundaryEvent id="timeout" attachedToRef="notify" cancelActivity="true">
	      <timerEventDefinition><timeDuration>PT1H</timeDuration></timerEventDefinition>
	    </boundaryEvent>
	    <endEvent id="e"/>
	    <endEvent id="late"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="notify"/>
	    <sequenceFlow id="f2" sourceRef="notify" targetRef="e"/>
	    <sequenceFlow id="f3" sourceRef="timeout" targetRef="late"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	st := nodeByBpmnId(t, cp, "notify")
	if st.Type != TypeSendTask || st.Type.String() != "SendTask" {
		t.Fatalf("send task type = %v (%q), want SendTask", st.Type, st.Type.String())
	}
	d := cp.SendTask(st.Detail)
	if got := cp.Intern(d.JobType); got != "send-email" {
		t.Errorf("send task job type = %q, want send-email", got)
	}
	if d.Retries != 5 {
		t.Errorf("send task retries = %d, want 5", d.Retries)
	}
	// The boundary attached to the send task — proof it is treated as an activity.
	if b := nodeByBpmnId(t, cp, "timeout"); b.Type != TypeBoundaryEvent {
		t.Fatalf("boundary type = %v, want BoundaryEvent (a boundary must attach to the send task)", b.Type)
	}
	if problems := Validate(cp); HasErrors(problems) {
		t.Errorf("Validate found errors on a send task with a boundary: %+v", problems)
	}
}

// TestParseSendTaskDefaultRetries checks that a send task with no explicit retries gets the
// default, exactly as a service task does — the shared registration path applies.
func TestParseSendTaskDefaultRetries(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <sendTask id="notify">
	      <extensionElements><zeebe:taskDefinition type="send-email"/></extensionElements>
	    </sendTask>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="notify"/>
	    <sequenceFlow id="f2" sourceRef="notify" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	d := cp.SendTask(nodeByBpmnId(t, cp, "notify").Detail)
	if d.Retries != defaultRetries {
		t.Errorf("send task retries = %d, want the default %d", d.Retries, defaultRetries)
	}
}

// TestParseSendTaskConnector checks that a connector extension on a send task takes the
// connector path exactly as on a service task — an <atlas:mailConnector> send task compiles
// to a TypeConnectorTask, so outbound-send connectors author on a send task unchanged
// (ADR-0112).
func TestParseSendTaskConnector(t *testing.T) {
	const xml = `<?xml version="1.0" encoding="UTF-8"?>
	<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
	                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
	  <bpmn:process id="p" isExecutable="true">
	    <bpmn:startEvent id="s"/>
	    <bpmn:sendTask id="notify">
	      <bpmn:extensionElements>
	        <atlas:mailConnector connector="office365" to="ops@example.com"
	                             subject="Order shipped" body="Your order is on its way."/>
	      </bpmn:extensionElements>
	    </bpmn:sendTask>
	    <bpmn:endEvent id="e"/>
	    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="notify"/>
	    <bpmn:sequenceFlow id="f2" sourceRef="notify" targetRef="e"/>
	  </bpmn:process>
	</bpmn:definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := nodeByBpmnId(t, cp, "notify")
	if node.Type != TypeConnectorTask {
		t.Fatalf("connector send task type = %v, want ConnectorTask", node.Type)
	}
	if got := cp.Intern(cp.ConnectorTask(node.Detail).JobType); got != MailJobType {
		t.Errorf("jobType = %q, want %q", got, MailJobType)
	}
}

// TestParseSendTaskMessageKind checks that a <sendTask messageRef> — the message kind — compiles
// to a TypeMessageThrowEvent carrying the referenced message's name, reusing the intermediate
// message throw's path (ADR-0112). It is a correlating throw in task form: no new runtime.
func TestParseSendTaskMessageKind(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <message id="Msg_notify" name="notify">
	    <extensionElements><zeebe:subscription correlationKey="=orderId"/></extensionElements>
	  </message>
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <sendTask id="send" messageRef="Msg_notify"/>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="send"/>
	    <sequenceFlow id="f2" sourceRef="send" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	st := nodeByBpmnId(t, cp, "send")
	if st.Type != TypeMessageThrowEvent {
		t.Fatalf("message-kind send task type = %v, want MessageThrowEvent (a correlating throw)", st.Type)
	}
	if d := cp.MessageThrow(st.Detail); d.MessageName != "notify" {
		t.Errorf("thrown message name = %q, want notify", d.MessageName)
	}
}

// TestParseSendTaskUnknownMessage rejects a message-kind send task whose messageRef names no
// declared message, mirroring the message throw's check (ADR-0112/0020).
func TestParseSendTaskUnknownMessage(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <sendTask id="send" messageRef="Nope"/>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="send"/>
	    <sequenceFlow id="f2" sourceRef="send" targetRef="e"/>
	  </process>
	</definitions>`
	if _, err := Parse(1, 1, strings.NewReader(xml)); err == nil {
		t.Fatal("Parse: want an error for a send task referencing an unknown message, got nil")
	}
}

// TestParseSendTaskNoTaskDefinition rejects a bare send task (no task definition, no
// connector): like a service task with no taskDefinition type, it cannot execute, so it is a
// deploy error naming the send task (ADR-0112).
func TestParseSendTaskNoTaskDefinition(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <sendTask id="notify"/>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="notify"/>
	    <sequenceFlow id="f2" sourceRef="notify" targetRef="e"/>
	  </process>
	</definitions>`
	_, err := Parse(1, 1, strings.NewReader(xml))
	if err == nil {
		t.Fatal("Parse: want an error for a send task with no task definition, got nil")
	}
	for _, want := range []string{"notify", "send task", "task definition"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err.Error(), want)
		}
	}
}
