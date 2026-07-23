package compiler

import (
	"strings"
	"testing"
)

// A service task bearing an <atlas:restConnector> extension is an HTTP-REST
// connector task (ADR-0036): it delegates to a server-registered REST connector
// via the job path rather than to an external service-task worker.
const restConnectorBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:restConnector connector="crm" method="post" path="/customers"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseRestConnectorTask(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(restConnectorBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	node := cp.Node(task)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	d := cp.ConnectorTask(node.Detail)
	if got := cp.Intern(d.Connector); got != "crm" {
		t.Errorf("connector = %q, want crm", got)
	}
	if got := cp.Intern(d.Method); got != "POST" { // upper-cased at deploy time
		t.Errorf("method = %q, want POST", got)
	}
	if got := cp.Intern(d.Path); got != "/customers" {
		t.Errorf("path = %q, want /customers", got)
	}
	if got := cp.Intern(d.JobType); got != RestJobType {
		t.Errorf("jobType = %q, want %q", got, RestJobType)
	}
	// A REST task leaves the clio-only coordinates unset (-1 → "").
	if cp.Intern(d.Subject) != "" || cp.Intern(d.EventType) != "" {
		t.Errorf("subject/eventType = %q/%q, want empty for a REST task", cp.Intern(d.Subject), cp.Intern(d.EventType))
	}
}

// A REST connector task with no method defaults to GET.
func TestParseRestConnectorDefaultMethod(t *testing.T) {
	const noMethod = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:restConnector connector="crm" path="/customers/1"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := Parse(1, 1, strings.NewReader(noMethod))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	d := cp.ConnectorTask(cp.Node(task).Detail)
	if got := cp.Intern(d.Method); got != "GET" {
		t.Errorf("method = %q, want GET (the default)", got)
	}
}

func TestParseRestConnectorErrors(t *testing.T) {
	// A REST connector task missing its path fails to compile.
	const missingPath = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:restConnector connector="crm" method="POST"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	if _, err := Parse(1, 1, strings.NewReader(missingPath)); err == nil {
		t.Fatal("Parse: want an error for a rest connector task missing path, got nil")
	}

	// An unsupported HTTP method fails to compile.
	const badMethod = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:restConnector connector="crm" method="TRACE" path="/x"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	if _, err := Parse(1, 1, strings.NewReader(badMethod)); err == nil {
		t.Fatal("Parse: want an error for an unsupported HTTP method, got nil")
	}
}
