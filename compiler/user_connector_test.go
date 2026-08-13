package compiler

import (
	"strings"
	"testing"
)

// A service task bearing an <atlas:userConnector> extension is a user-provisioning
// connector task (ADR-0120): it creates, sets the password of, or disables an
// Atlas login through the in-process user store via the job path. operation
// selects the action; the fields are model-authored literal-or-FEEL values, like
// the mail connector's message.
const userConnectorBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:userConnector operation="create" username="erika.muster"
                             email="erika@example.org" displayName="Erika Muster"
                             roles="user" password="initialpass1"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseUserConnectorTask(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(userConnectorBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	node := cp.Node(task)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	d := cp.ConnectorTask(node.Detail)
	if got := cp.Intern(d.JobType); got != UserConnectorJobType {
		t.Errorf("jobType = %q, want %q", got, UserConnectorJobType)
	}
	if d.JobType != UserConnectorJobTypeIndex {
		t.Errorf("jobType index = %d, want the reserved UserConnectorJobTypeIndex %d", d.JobType, UserConnectorJobTypeIndex)
	}
	if got := cp.Intern(d.UserOp); got != "create" {
		t.Errorf("operation = %q, want create", got)
	}
	if d.UserName.Expr != nil || d.UserName.Literal != "erika.muster" {
		t.Errorf("username = %+v, want the literal", d.UserName)
	}
	if d.UserEmail.Literal != "erika@example.org" || d.UserDisplayName.Literal != "Erika Muster" {
		t.Errorf("email/displayName = %+v / %+v", d.UserEmail, d.UserDisplayName)
	}
	if d.UserRoles.Literal != "user" || d.UserPassword.Literal != "initialpass1" {
		t.Errorf("roles/password = %+v / %+v", d.UserRoles, d.UserPassword)
	}
	// A user task leaves the mail/REST/clio-only fields unset.
	if d.To.Expr != nil || d.To.Literal != "" || cp.Intern(d.Method) != "" {
		t.Errorf("unrelated connector fields not empty for a user task")
	}
}

func TestParseUserConnectorFeelFields(t *testing.T) {
	const bpmn = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:userConnector operation="create" username="=benutzername"
                             email="=email" displayName="=vorname + &quot; &quot; + nachname"
                             roles="user" password="=initialpasswort"/>
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
	if d.UserName.Expr == nil || d.UserEmail.Expr == nil || d.UserDisplayName.Expr == nil || d.UserPassword.Expr == nil {
		t.Errorf("username/email/displayName/password should be FEEL expressions")
	}
	if d.UserRoles.Expr != nil || d.UserRoles.Literal != "user" {
		t.Errorf("roles should be a literal, got %+v", d.UserRoles)
	}
}

func TestParseUserConnectorErrors(t *testing.T) {
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
		"unknown operation":       `<atlas:userConnector operation="promote" username="u" password="p1"/>`,
		"missing operation":       `<atlas:userConnector username="u" password="p1"/>`,
		"missing username":        `<atlas:userConnector operation="create" password="p1"/>`,
		"create missing password": `<atlas:userConnector operation="create" username="u"/>`,
		"setpw missing password":  `<atlas:userConnector operation="set-password" username="u"/>`,
		"malformed FEEL username": `<atlas:userConnector operation="disable" username="=("/>`,
		"malformed FEEL password": `<atlas:userConnector operation="create" username="u" password="=("/>`,
		"malformed FEEL email":    `<atlas:userConnector operation="create" username="u" email="=(" password="p1"/>`,
	}
	for name, inner := range cases {
		if _, err := Parse(1, 1, strings.NewReader(wrap(inner))); err == nil {
			t.Errorf("%s: want a compile error, got nil", name)
		}
	}
}
