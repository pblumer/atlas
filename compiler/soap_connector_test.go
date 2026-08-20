package compiler

import (
	"strings"
	"testing"
)

// A service task bearing an <atlas:soapConnector> extension is a SOAP / Web Services
// connector task (ADR-0165): it invokes a SOAP operation against the model-authored
// web-service endpoint via the job path rather than delegating to an external
// service-task worker.
const soapConnectorBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:soapConnector endpoint="https://ws.example.com/UserService"
                             operation="GetUser" soapAction="urn:GetUser"
                             body="&lt;tns:GetUser xmlns:tns=&quot;urn:x&quot;&gt;&lt;id&gt;1&lt;/id&gt;&lt;/tns:GetUser&gt;"
                             resultVariable="user"
                             authType="basic" authUsername="svc" authSecret="WS_PASSWORD"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseSoapConnectorTask(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(soapConnectorBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	node := cp.Node(task)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	d := cp.ConnectorTask(node.Detail)
	if got := cp.Intern(d.JobType); got != SoapJobType {
		t.Errorf("jobType = %q, want %q", got, SoapJobType)
	}
	if d.JobType != SoapJobTypeIndex {
		t.Errorf("jobType index = %d, want the reserved SoapJobTypeIndex %d", d.JobType, SoapJobTypeIndex)
	}
	if d.SoapEndpoint.Expr != nil || d.SoapEndpoint.Literal != "https://ws.example.com/UserService" {
		t.Errorf("endpoint = %+v, want the literal endpoint", d.SoapEndpoint)
	}
	if got := cp.Intern(d.SoapOp); got != "GetUser" {
		t.Errorf("op = %q, want GetUser", got)
	}
	if d.SoapAction.Literal != "urn:GetUser" {
		t.Errorf("action = %+v, want urn:GetUser", d.SoapAction)
	}
	if !strings.Contains(d.SoapBody.Literal, "<tns:GetUser") {
		t.Errorf("body = %q, want the request payload XML", d.SoapBody.Literal)
	}
	// A blank soapVersion defaults to 1.1 at compile time.
	if got := cp.Intern(d.SoapVersion); got != "1.1" {
		t.Errorf("version = %q, want the default 1.1", got)
	}
	if got := cp.Intern(d.ResultVar); got != "user" {
		t.Errorf("resultVar = %q, want user", got)
	}
	if got := cp.Intern(d.Auth); got != `{"type":"basic","username":"svc","secretRef":"WS_PASSWORD"}` {
		t.Errorf("auth = %q, want a basic auth referencing the secret", got)
	}
	// A SOAP task carries no HTTP method (the operation, not a method, is authored)
	// and leaves the clio-only coordinates unset (-1 → "").
	if cp.Intern(d.Method) != "" {
		t.Errorf("method = %q, want empty for a SOAP task", cp.Intern(d.Method))
	}
	if cp.Intern(d.Subject) != "" || cp.Intern(d.Connector) != "" {
		t.Errorf("clio fields not empty for a SOAP task: connector=%q subject=%q",
			cp.Intern(d.Connector), cp.Intern(d.Subject))
	}
}

// A FEEL endpoint/action/body compiles to an expression evaluated at call time, and an
// explicit soapVersion="1.2" is preserved.
func TestParseSoapConnectorFeelAndVersion(t *testing.T) {
	const bpmn = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:soapConnector endpoint="=serviceUrl" operation="Provision" soapVersion="1.2"
                             body="=requestXml" resultVariable="resp"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	d := cp.ConnectorTask(cp.Node(task).Detail)
	if d.SoapEndpoint.Expr == nil {
		t.Errorf("endpoint should be a compiled FEEL expression, got literal %q", d.SoapEndpoint.Literal)
	}
	if d.SoapBody.Expr == nil {
		t.Errorf("body should be a compiled FEEL expression, got literal %q", d.SoapBody.Literal)
	}
	if got := cp.Intern(d.SoapVersion); got != "1.2" {
		t.Errorf("version = %q, want 1.2", got)
	}
}

func TestParseSoapConnectorErrors(t *testing.T) {
	wrap := func(inner string) string {
		return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements>` + inner + `</bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	}
	cases := map[string]string{
		"missing endpoint":      `<atlas:soapConnector operation="Op" body="&lt;x/&gt;"/>`,
		"missing operation":     `<atlas:soapConnector endpoint="https://x" body="&lt;x/&gt;"/>`,
		"missing body":          `<atlas:soapConnector endpoint="https://x" operation="Op"/>`,
		"unknown version":       `<atlas:soapConnector endpoint="https://x" operation="Op" body="&lt;x/&gt;" soapVersion="3.0"/>`,
		"bearer without secret": `<atlas:soapConnector endpoint="https://x" operation="Op" body="&lt;x/&gt;" authType="bearer"/>`,
		"unknown auth scheme":   `<atlas:soapConnector endpoint="https://x" operation="Op" body="&lt;x/&gt;" authType="kerberos" authSecret="K"/>`,
		"malformed feel body":   `<atlas:soapConnector endpoint="https://x" operation="Op" body="=("/>`,
	}
	for name, inner := range cases {
		if _, err := Parse(1, 1, strings.NewReader(wrap(inner))); err == nil {
			t.Errorf("%s: want a compile error, got nil", name)
		}
	}
}
