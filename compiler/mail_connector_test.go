package compiler

import (
	"strings"
	"testing"
)

// A service task bearing an <atlas:mailConnector> extension is an outbound mail
// connector task (ADR-0079): it sends an e-mail through a server-registered mail
// provider via the job path, mirroring clio's registry-managed endpoint (the
// provider host and credentials live server-side, never in the model) while the
// message fields (recipients, subject, body) are model-authored like REST's.
const mailConnectorBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:mailConnector connector="office365" to="ops@example.com"
                             subject="Order shipped" body="Your order is on its way."/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseMailConnectorTask(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(mailConnectorBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	node := cp.Node(task)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	d := cp.ConnectorTask(node.Detail)
	if got := cp.Intern(d.JobType); got != MailJobType {
		t.Errorf("jobType = %q, want %q", got, MailJobType)
	}
	if d.JobType != MailJobTypeIndex {
		t.Errorf("jobType index = %d, want the reserved MailJobTypeIndex %d", d.JobType, MailJobTypeIndex)
	}
	if got := cp.Intern(d.Connector); got != "office365" {
		t.Errorf("connector = %q, want office365", got)
	}
	if d.To.Expr != nil || d.To.Literal != "ops@example.com" {
		t.Errorf("to = %+v, want the literal recipient", d.To)
	}
	if d.MailSubject.Literal != "Order shipped" {
		t.Errorf("subject = %+v, want the literal subject", d.MailSubject)
	}
	if d.Body.Literal != "Your order is on its way." {
		t.Errorf("body = %+v, want the literal body", d.Body)
	}
	// A mail task leaves the REST-only URL and the clio-only coordinates unset.
	if d.Url.Expr != nil || d.Url.Literal != "" {
		t.Errorf("url = %+v, want empty for a mail task", d.Url)
	}
	if cp.Intern(d.EventType) != "" || cp.Intern(d.Method) != "" {
		t.Errorf("clio/REST-only fields not empty for a mail task: eventType=%q method=%q",
			cp.Intern(d.EventType), cp.Intern(d.Method))
	}
}

// Mail recipient and content fields may be FEEL expressions (a leading '='),
// evaluated over the instance's variables at send time; a literal stays literal.
func TestParseMailConnectorFeelFields(t *testing.T) {
	const bpmn = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:mailConnector connector="gmail" to="=customer.email" cc="team@example.com"
                             from="=sender" subject="=&quot;Hi &quot; + customer.name" body="=greeting"/>
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
	if d.To.Expr == nil {
		t.Errorf("to should be a FEEL expression, got literal %q", d.To.Literal)
	}
	if d.Cc.Expr != nil || d.Cc.Literal != "team@example.com" {
		t.Errorf("cc should be a literal, got %+v", d.Cc)
	}
	if d.From.Expr == nil {
		t.Errorf("from should be a FEEL expression, got literal %q", d.From.Literal)
	}
	if d.MailSubject.Expr == nil || d.Body.Expr == nil {
		t.Errorf("subject/body should be FEEL expressions, got %+v / %+v", d.MailSubject, d.Body)
	}
}

func TestParseMailConnectorErrors(t *testing.T) {
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
	// A malformed FEEL expression in any message field fails the compile — each field
	// is validated on its own, so a case per field exercises every validation branch.
	cases := map[string]string{
		"missing connector":      `<atlas:mailConnector to="a@b.c" subject="hi" body="x"/>`,
		"missing recipient":      `<atlas:mailConnector connector="m" subject="hi" body="x"/>`,
		"malformed FEEL to":      `<atlas:mailConnector connector="m" to="=(" subject="hi" body="x"/>`,
		"malformed FEEL cc":      `<atlas:mailConnector connector="m" to="a@b.c" cc="=(" body="x"/>`,
		"malformed FEEL bcc":     `<atlas:mailConnector connector="m" to="a@b.c" bcc="=(" body="x"/>`,
		"malformed FEEL from":    `<atlas:mailConnector connector="m" to="a@b.c" from="=(" body="x"/>`,
		"malformed FEEL subject": `<atlas:mailConnector connector="m" to="a@b.c" subject="=(" body="x"/>`,
		"malformed FEEL body":    `<atlas:mailConnector connector="m" to="a@b.c" subject="hi" body="=("/>`,
	}
	for name, inner := range cases {
		if _, err := Parse(1, 1, strings.NewReader(wrap(inner))); err == nil {
			t.Errorf("%s: want a compile error, got nil", name)
		}
	}
}
