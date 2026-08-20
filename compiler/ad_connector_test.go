package compiler

import (
	"strings"
	"testing"
)

// A service task bearing an <atlas:adConnector> extension is an Active Directory
// connector task (ADR-0166): it performs an AD-specific provisioning operation against
// a model-authored server via the job path.
const adConnectorBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:adConnector url="ldaps://dc.example.com:636" bindDN="cn=svc,dc=example,dc=com"
                           bindSecret="AD_BIND" operation="set-password"
                           dn="cn=Arno,ou=users,dc=example,dc=com" newPassword="=neuesPasswort"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseAdConnectorTask(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(adConnectorBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	node := cp.Node(task)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	d := cp.ConnectorTask(node.Detail)
	if got := cp.Intern(d.JobType); got != AdJobType {
		t.Errorf("jobType = %q, want %q", got, AdJobType)
	}
	if d.JobType != AdJobTypeIndex {
		t.Errorf("jobType index = %d, want the reserved AdJobTypeIndex %d", d.JobType, AdJobTypeIndex)
	}
	if d.AdURL.Literal != "ldaps://dc.example.com:636" {
		t.Errorf("url = %+v", d.AdURL)
	}
	if got := cp.Intern(d.AdBindSecret); got != "AD_BIND" {
		t.Errorf("bindSecret = %q, want AD_BIND", got)
	}
	if got := cp.Intern(d.AdOp); got != "set-password" {
		t.Errorf("op = %q, want set-password", got)
	}
	if d.AdDN.Literal != "cn=Arno,ou=users,dc=example,dc=com" {
		t.Errorf("dn = %+v", d.AdDN)
	}
	if d.AdNewPassword.Expr == nil {
		t.Errorf("newPassword should be a compiled FEEL expression, got literal %q", d.AdNewPassword.Literal)
	}
	// An AD task authors no HTTP method and no auth (the bind password is a dedicated ref).
	if cp.Intern(d.Method) != "" || d.Auth != -1 {
		t.Errorf("method/auth = %q/%d, want empty/-1 for an AD task", cp.Intern(d.Method), d.Auth)
	}
}

// A group-membership operation authors a member DN (here a FEEL expression).
func TestParseAdConnectorGroupMember(t *testing.T) {
	const bpmn = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements>
      <atlas:adConnector url="ldaps://x" operation="add-group-member" dn="cn=Admins,dc=x" memberDN="=userDN"/>
    </bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	d := cp.ConnectorTask(cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target).Detail)
	if cp.Intern(d.AdOp) != "add-group-member" {
		t.Errorf("op = %q, want add-group-member", cp.Intern(d.AdOp))
	}
	if d.AdMemberDN.Expr == nil {
		t.Errorf("memberDN should be a compiled FEEL expression, got literal %q", d.AdMemberDN.Literal)
	}
}

func TestParseAdConnectorErrors(t *testing.T) {
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
		"missing url":                  `<atlas:adConnector operation="disable" dn="cn=x"/>`,
		"missing operation":            `<atlas:adConnector url="ldaps://x" dn="cn=x"/>`,
		"unknown operation":            `<atlas:adConnector url="ldaps://x" operation="rename" dn="cn=x"/>`,
		"missing dn":                   `<atlas:adConnector url="ldaps://x" operation="disable"/>`,
		"create without entryVar":      `<atlas:adConnector url="ldaps://x" operation="create-user" dn="cn=x"/>`,
		"password without newPass":     `<atlas:adConnector url="ldaps://x" operation="set-password" dn="cn=x"/>`,
		"add-member without member":    `<atlas:adConnector url="ldaps://x" operation="add-group-member" dn="cn=g"/>`,
		"remove-member without member": `<atlas:adConnector url="ldaps://x" operation="remove-group-member" dn="cn=g"/>`,
		"malformed feel url":           `<atlas:adConnector url="=(" operation="disable" dn="cn=x"/>`,
	}
	for name, inner := range cases {
		if _, err := Parse(1, 1, strings.NewReader(wrap(inner))); err == nil {
			t.Errorf("%s: want a compile error, got nil", name)
		}
	}
}
