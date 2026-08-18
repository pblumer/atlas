package compiler

import (
	"strings"
	"testing"
)

// A service task bearing an <atlas:sharepointConnector> extension is a SharePoint
// connector task (ADR-0140): it creates a list item in a model-authored site/list
// through a server-registered SharePoint provider (Microsoft Graph) via the job
// path, mirroring the mail connector (the Graph base and OAuth credential live
// server-side, never in the model) while the target (site, list, item fields) is
// model-authored like REST's endpoint.
const sharePointConnectorBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:sharepointConnector connector="contoso" site="contoso.sharepoint.com,/sites/ops"
                                   list="Incidents" resultVariable="created">
          <atlas:itemField name="Title" value="Order shipped"/>
          <atlas:itemField name="Customer" value="=customer.name"/>
        </atlas:sharepointConnector>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseSharePointConnectorTask(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(sharePointConnectorBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	node := cp.Node(task)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	d := cp.ConnectorTask(node.Detail)
	if got := cp.Intern(d.JobType); got != SharePointJobType {
		t.Errorf("jobType = %q, want %q", got, SharePointJobType)
	}
	if d.JobType != SharePointJobTypeIndex {
		t.Errorf("jobType index = %d, want the reserved SharePointJobTypeIndex %d", d.JobType, SharePointJobTypeIndex)
	}
	if got := cp.Intern(d.Connector); got != "contoso" {
		t.Errorf("connector = %q, want contoso", got)
	}
	if d.Site.Expr != nil || d.Site.Literal != "contoso.sharepoint.com,/sites/ops" {
		t.Errorf("site = %+v, want the literal site", d.Site)
	}
	if d.List.Expr != nil || d.List.Literal != "Incidents" {
		t.Errorf("list = %+v, want the literal list", d.List)
	}
	if got := cp.Intern(d.ResultVar); got != "created" {
		t.Errorf("resultVariable = %q, want created", got)
	}
	if len(d.Fields) != 2 {
		t.Fatalf("fields = %+v, want two item fields", d.Fields)
	}
	if d.Fields[0].Name != "Title" || d.Fields[0].Val.Literal != "Order shipped" {
		t.Errorf("field[0] = %+v, want literal Title", d.Fields[0])
	}
	if d.Fields[1].Name != "Customer" || d.Fields[1].Val.Expr == nil {
		t.Errorf("field[1] = %+v, want a FEEL Customer field", d.Fields[1])
	}
	// A SharePoint task leaves the REST-only URL and the clio-only coordinates unset.
	if d.Url.Expr != nil || d.Url.Literal != "" {
		t.Errorf("url = %+v, want empty for a SharePoint task", d.Url)
	}
	if cp.Intern(d.EventType) != "" || cp.Intern(d.Method) != "" {
		t.Errorf("clio/REST-only fields not empty for a SharePoint task: eventType=%q method=%q",
			cp.Intern(d.EventType), cp.Intern(d.Method))
	}
}

// The site and list fields may be FEEL expressions (a leading '='), evaluated over
// the instance's variables at call time; a literal stays literal.
func TestParseSharePointConnectorFeelFields(t *testing.T) {
	const bpmn = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:sharepointConnector connector="contoso" site="=siteRef" list="=listName">
          <atlas:itemField name="Title" value="=subject"/>
        </atlas:sharepointConnector>
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
	if d.Site.Expr == nil {
		t.Errorf("site should be a FEEL expression, got literal %q", d.Site.Literal)
	}
	if d.List.Expr == nil {
		t.Errorf("list should be a FEEL expression, got literal %q", d.List.Literal)
	}
	if len(d.Fields) != 1 || d.Fields[0].Val.Expr == nil {
		t.Errorf("item field should be a FEEL expression, got %+v", d.Fields)
	}
}

func TestParseSharePointConnectorErrors(t *testing.T) {
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
	// Each required-field and malformed-FEEL case exercises one validation branch.
	cases := map[string]string{
		"missing connector":   `<atlas:sharepointConnector site="s" list="l"/>`,
		"missing site":        `<atlas:sharepointConnector connector="c" list="l"/>`,
		"missing list":        `<atlas:sharepointConnector connector="c" site="s"/>`,
		"malformed FEEL site": `<atlas:sharepointConnector connector="c" site="=(" list="l"/>`,
		"malformed FEEL list": `<atlas:sharepointConnector connector="c" site="s" list="=("/>`,
		"malformed FEEL field": `<atlas:sharepointConnector connector="c" site="s" list="l">
			<atlas:itemField name="Title" value="=("/></atlas:sharepointConnector>`,
		"duplicate field": `<atlas:sharepointConnector connector="c" site="s" list="l">
			<atlas:itemField name="Title" value="a"/><atlas:itemField name="Title" value="b"/></atlas:sharepointConnector>`,
	}
	for name, inner := range cases {
		if _, err := Parse(1, 1, strings.NewReader(wrap(inner))); err == nil {
			t.Errorf("%s: want a compile error, got nil", name)
		}
	}
}
