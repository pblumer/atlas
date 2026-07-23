package compiler

import (
	"strings"
	"testing"
)

// A service task bearing an <atlas:clioConnector> extension is a clio connector
// task (ADR-0026): it delegates to a server-registered connector via the job
// path rather than to an external service-task worker.
const clioConnectorBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:clioConnector connector="orders-clio" subject="orders/new" eventType="OrderPlaced"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseClioConnectorTask(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(clioConnectorBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	node := cp.Node(task)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	d := cp.ConnectorTask(node.Detail)
	if got := cp.Intern(d.Connector); got != "orders-clio" {
		t.Errorf("connector = %q, want orders-clio", got)
	}
	if got := cp.Intern(d.Subject); got != "orders/new" {
		t.Errorf("subject = %q, want orders/new", got)
	}
	if got := cp.Intern(d.EventType); got != "OrderPlaced" {
		t.Errorf("eventType = %q, want OrderPlaced", got)
	}
	if got := cp.Intern(d.JobType); got != ClioWriteJobType {
		t.Errorf("jobType = %q, want %q", got, ClioWriteJobType)
	}
}

func TestParseClioConnectorErrors(t *testing.T) {
	// A clio connector task missing a required attribute fails to compile.
	const missingSubject = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:clioConnector connector="c" eventType="E"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	if _, err := Parse(1, 1, strings.NewReader(missingSubject)); err == nil {
		t.Fatal("Parse: want an error for a clio connector task missing subject, got nil")
	}
}
