package compiler

import (
	"strings"
	"testing"
)

// A service task bearing an <atlas:scimConnector> extension is a SCIM 2.0 connector
// task (ADR-0153): it performs a resource operation against the model-authored SCIM
// service provider via the job path rather than delegating to an external
// service-task worker.
const scimConnectorBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:scimConnector baseUrl="https://idp.example.com/scim/v2" resource="Users"
                             operation="create" bodyVariable="resource" resultVariable="created"
                             authType="bearer" authSecret="SCIM_TOKEN"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseScimConnectorTask(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(scimConnectorBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	node := cp.Node(task)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	d := cp.ConnectorTask(node.Detail)
	if got := cp.Intern(d.JobType); got != ScimJobType {
		t.Errorf("jobType = %q, want %q", got, ScimJobType)
	}
	if d.JobType != ScimJobTypeIndex {
		t.Errorf("jobType index = %d, want the reserved ScimJobTypeIndex %d", d.JobType, ScimJobTypeIndex)
	}
	if d.ScimBaseURL.Expr != nil || d.ScimBaseURL.Literal != "https://idp.example.com/scim/v2" {
		t.Errorf("baseUrl = %+v, want the literal base URL", d.ScimBaseURL)
	}
	if d.ScimResource.Literal != "Users" {
		t.Errorf("resource = %+v, want Users", d.ScimResource)
	}
	if got := cp.Intern(d.ScimOp); got != "create" {
		t.Errorf("op = %q, want create", got)
	}
	if got := cp.Intern(d.ScimBody); got != "resource" {
		t.Errorf("bodyVar = %q, want resource", got)
	}
	if got := cp.Intern(d.ResultVar); got != "created" {
		t.Errorf("resultVar = %q, want created", got)
	}
	if got := cp.Intern(d.Auth); got != `{"type":"bearer","secretRef":"SCIM_TOKEN"}` {
		t.Errorf("auth = %q, want a bearer auth referencing the secret", got)
	}
	// A SCIM task carries no HTTP method (the operation, not a method, is authored)
	// and leaves the clio-only coordinates unset (-1 → "").
	if cp.Intern(d.Method) != "" {
		t.Errorf("method = %q, want empty for a SCIM task", cp.Intern(d.Method))
	}
	if cp.Intern(d.Subject) != "" || cp.Intern(d.Connector) != "" {
		t.Errorf("clio fields not empty for a SCIM task: connector=%q subject=%q",
			cp.Intern(d.Connector), cp.Intern(d.Subject))
	}
}

// A single-resource operation (get/replace/patch/delete) authors its resource id and
// needs no body variable; a leading '=' compiles the id to a FEEL expression.
func TestParseScimConnectorFeelResourceId(t *testing.T) {
	const bpmn = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:scimConnector baseUrl="https://x/scim/v2" resource="Users" operation="get"
                             resourceId="=userId" resultVariable="user"/>
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
	if d.ScimResourceID.Expr == nil {
		t.Errorf("resourceId should be a compiled FEEL expression, got literal %q", d.ScimResourceID.Literal)
	}
	if cp.Intern(d.ScimOp) != "get" {
		t.Errorf("op = %q, want get", cp.Intern(d.ScimOp))
	}
}

// A search authors a FEEL filter and no id.
func TestParseScimConnectorSearchFilter(t *testing.T) {
	const bpmn = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:scimConnector baseUrl="https://x/scim/v2" resource="Groups" operation="search"
                             filter="=&quot;displayName eq &quot; + team" resultVariable="hits"/>
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
	if d.ScimFilter.Expr == nil {
		t.Errorf("filter should be a compiled FEEL expression, got literal %q", d.ScimFilter.Literal)
	}
	if cp.Intern(d.ScimOp) != "search" {
		t.Errorf("op = %q, want search", cp.Intern(d.ScimOp))
	}
}

func TestParseScimConnectorErrors(t *testing.T) {
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
		"missing baseUrl":       `<atlas:scimConnector resource="Users" operation="create"/>`,
		"missing resource":      `<atlas:scimConnector baseUrl="https://x" operation="create"/>`,
		"missing operation":     `<atlas:scimConnector baseUrl="https://x" resource="Users"/>`,
		"unknown operation":     `<atlas:scimConnector baseUrl="https://x" resource="Users" operation="upsert"/>`,
		"get without id":        `<atlas:scimConnector baseUrl="https://x" resource="Users" operation="get"/>`,
		"delete without id":     `<atlas:scimConnector baseUrl="https://x" resource="Users" operation="delete"/>`,
		"bearer without secret": `<atlas:scimConnector baseUrl="https://x" resource="Users" operation="get" resourceId="1" authType="bearer"/>`,
		"unknown auth scheme":   `<atlas:scimConnector baseUrl="https://x" resource="Users" operation="get" resourceId="1" authType="oauth2" authSecret="K"/>`,
		"malformed feel id":     `<atlas:scimConnector baseUrl="https://x" resource="Users" operation="get" resourceId="=("/>`,
	}
	for name, inner := range cases {
		if _, err := Parse(1, 1, strings.NewReader(wrap(inner))); err == nil {
			t.Errorf("%s: want a compile error, got nil", name)
		}
	}
}
