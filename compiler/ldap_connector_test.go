package compiler

import (
	"strings"
	"testing"
)

// A service task bearing an <atlas:ldapConnector> extension is a generic LDAP
// task (ADR-0154): it performs a directory operation against a
// model-authored server via the job path rather than an external service-task worker.
const ldapConnectorBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:ldapConnector url="ldaps://dir.example.com:636" bindDN="cn=admin,dc=example,dc=com"
                             bindSecret="LDAP_PW" operation="search"
                             baseDN="ou=people,dc=example,dc=com" filter="(uid=ada)" scope="sub"
                             resultVariable="hits"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseLdapConnectorTask(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(ldapConnectorBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	node := cp.Node(task)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	d := cp.ConnectorTask(node.Detail)
	if got := cp.Intern(d.JobType); got != LdapJobType {
		t.Errorf("jobType = %q, want %q", got, LdapJobType)
	}
	if d.JobType != LdapJobTypeIndex {
		t.Errorf("jobType index = %d, want the reserved LdapJobTypeIndex %d", d.JobType, LdapJobTypeIndex)
	}
	if d.LdapURL.Literal != "ldaps://dir.example.com:636" {
		t.Errorf("url = %+v, want the literal server URL", d.LdapURL)
	}
	if d.LdapBindDN.Literal != "cn=admin,dc=example,dc=com" {
		t.Errorf("bindDN = %+v", d.LdapBindDN)
	}
	if got := cp.Intern(d.LdapBindSecret); got != "LDAP_PW" {
		t.Errorf("bindSecret = %q, want LDAP_PW", got)
	}
	if got := cp.Intern(d.LdapOp); got != "search" {
		t.Errorf("op = %q, want search", got)
	}
	if got := cp.Intern(d.LdapScope); got != "sub" {
		t.Errorf("scope = %q, want sub", got)
	}
	if d.LdapBaseDN.Literal != "ou=people,dc=example,dc=com" || d.LdapFilter.Literal != "(uid=ada)" {
		t.Errorf("base/filter = %+v/%+v", d.LdapBaseDN, d.LdapFilter)
	}
	if got := cp.Intern(d.ResultVar); got != "hits" {
		t.Errorf("resultVar = %q, want hits", got)
	}
	// A search authors no HTTP method and no auth (the bind password is a dedicated ref).
	if cp.Intern(d.Method) != "" || d.Auth != -1 {
		t.Errorf("method/auth = %q/%d, want empty/-1 for an LDAP task", cp.Intern(d.Method), d.Auth)
	}
}

// A search with no scope defaults to the whole subtree; an add authors a DN + entry
// variable and a leading '=' compiles the DN to a FEEL expression.
func TestParseLdapConnectorDefaultsAndFeel(t *testing.T) {
	const noScope = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements>
      <atlas:ldapConnector url="ldap://x" operation="search" baseDN="dc=x"/>
    </bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := Parse(1, 1, strings.NewReader(noScope))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	d := cp.ConnectorTask(cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target).Detail)
	if got := cp.Intern(d.LdapScope); got != "sub" {
		t.Errorf("default scope = %q, want sub", got)
	}

	const feelDN = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements>
      <atlas:ldapConnector url="ldap://x" operation="add" dn="=&quot;uid=&quot; + uid + &quot;,ou=people&quot;" entryVariable="entry"/>
    </bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	cp2, err := Parse(1, 1, strings.NewReader(feelDN))
	if err != nil {
		t.Fatalf("Parse feel: %v", err)
	}
	d2 := cp2.ConnectorTask(cp2.Node(cp2.Flow(cp2.Outgoing(cp2.StartEvents()[0])[0]).Target).Detail)
	if d2.LdapDN.Expr == nil {
		t.Errorf("dn should be a compiled FEEL expression, got literal %q", d2.LdapDN.Literal)
	}
	if got := cp2.Intern(d2.LdapEntryVar); got != "entry" {
		t.Errorf("entryVar = %q, want entry", got)
	}
}

func TestParseLdapConnectorErrors(t *testing.T) {
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
		"missing url":              `<atlas:ldapConnector operation="search" baseDN="dc=x"/>`,
		"missing operation":        `<atlas:ldapConnector url="ldap://x" baseDN="dc=x"/>`,
		"unknown operation":        `<atlas:ldapConnector url="ldap://x" operation="rename" dn="uid=x"/>`,
		"search without baseDN":    `<atlas:ldapConnector url="ldap://x" operation="search"/>`,
		"unknown scope":            `<atlas:ldapConnector url="ldap://x" operation="search" baseDN="dc=x" scope="deep"/>`,
		"add without entryVar":     `<atlas:ldapConnector url="ldap://x" operation="add" dn="uid=x"/>`,
		"modify without entryVar":  `<atlas:ldapConnector url="ldap://x" operation="modify" dn="uid=x"/>`,
		"delete without dn":        `<atlas:ldapConnector url="ldap://x" operation="delete"/>`,
		"password without newPass": `<atlas:ldapConnector url="ldap://x" operation="modify-password" dn="uid=x"/>`,
		"malformed feel url":       `<atlas:ldapConnector url="=(" operation="delete" dn="uid=x"/>`,
	}
	for name, inner := range cases {
		if _, err := Parse(1, 1, strings.NewReader(wrap(inner))); err == nil {
			t.Errorf("%s: want a compile error, got nil", name)
		}
	}
}

// ldapTaskBPMN builds a one-task model from raw <atlas:ldapConnector> attributes.
func ldapTaskBPMN(attrs string) string {
	return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:ldapConnector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

func ldapDetailOf(t *testing.T, attrs string) (*CompiledProcess, *ConnectorTaskDetail) {
	t.Helper()
	cp, err := Parse(1, 1, strings.NewReader(ldapTaskBPMN(attrs)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target)
	return cp, cp.ConnectorTask(node.Detail)
}

// A search carries its bounds compiled in, so the runtime interprets nothing (I5):
// the defaults when the model authors none, and the authored numbers otherwise.
func TestLdapSearchBoundDefaults(t *testing.T) {
	_, d := ldapDetailOf(t, `url="ldap://dc" operation="search" baseDN="dc=x" resultVariable="r"`)
	if d.LdapPageSize != defaultLdapPageSize || d.LdapMaxEntries != defaultLdapMaxEntries {
		t.Errorf("bounds = %d / %d, want the defaults %d / %d",
			d.LdapPageSize, d.LdapMaxEntries, defaultLdapPageSize, defaultLdapMaxEntries)
	}

	_, d2 := ldapDetailOf(t, `url="ldap://dc" operation="search" baseDN="dc=x" resultVariable="r" pageSize="100" maxEntries="50"`)
	if d2.LdapPageSize != 100 || d2.LdapMaxEntries != 50 {
		t.Errorf("bounds = %d / %d, want 100 / 50", d2.LdapPageSize, d2.LdapMaxEntries)
	}

	// Zero is how a model asks for unbounded, and it must survive as zero rather than
	// being mistaken for "unset" and replaced by the default.
	_, d3 := ldapDetailOf(t, `url="ldap://dc" operation="search" baseDN="dc=x" resultVariable="r" pageSize="0" maxEntries="0"`)
	if d3.LdapPageSize != 0 || d3.LdapMaxEntries != 0 {
		t.Errorf("bounds = %d / %d, want 0 / 0 (explicitly unbounded)", d3.LdapPageSize, d3.LdapMaxEntries)
	}

	// A non-search operation carries no bounds at all.
	_, d4 := ldapDetailOf(t, `url="ldap://dc" operation="delete" dn="uid=a"`)
	if d4.LdapPageSize != 0 || d4.LdapMaxEntries != 0 {
		t.Errorf("bounds = %d / %d, want 0 / 0 for a non-search", d4.LdapPageSize, d4.LdapMaxEntries)
	}
}

// The per-value operations compile and carry the attribute object, so a multi-valued
// attribute can be changed without restating everyone else's values.
func TestLdapPerValueOperations(t *testing.T) {
	for _, op := range []string{"add-values", "delete-values"} {
		cp, d := ldapDetailOf(t, `url="ldap://dc" operation="`+op+`" dn="cn=team,dc=x" entryVariable="mitglieder"`)
		if got := cp.Intern(d.LdapOp); got != op {
			t.Errorf("operation = %q, want %q", got, op)
		}
		if got := cp.Intern(d.LdapEntryVar); got != "mitglieder" {
			t.Errorf("entryVariable = %q", got)
		}
	}
}

// A client certificate is a reference, never a value — the same rule the bind
// password follows (ADR-0041).
func TestLdapClientCertSecret(t *testing.T) {
	cp, d := ldapDetailOf(t, `url="ldaps://dc" operation="delete" dn="uid=a" clientCertSecret="LDAP_CLIENT_CERT"`)
	if got := cp.Intern(d.LdapClientCertSecret); got != "LDAP_CLIENT_CERT" {
		t.Errorf("clientCertSecret = %q, want the reference", got)
	}
}

func TestLdapHardeningValidation(t *testing.T) {
	for _, tc := range []struct{ name, attrs, want string }{
		{"pageSize on a non-search", `url="ldap://dc" operation="delete" dn="uid=a" pageSize="10"`, "pageSize"},
		{"maxEntries on a non-search", `url="ldap://dc" operation="delete" dn="uid=a" maxEntries="10"`, "maxEntries"},
		{"pageSize not a number", `url="ldap://dc" operation="search" baseDN="dc=x" resultVariable="r" pageSize="viele"`, "pageSize"},
		{"maxEntries negative", `url="ldap://dc" operation="search" baseDN="dc=x" resultVariable="r" maxEntries="-1"`, "maxEntries"},
		{"add-values without entry", `url="ldap://dc" operation="add-values" dn="cn=t"`, "entryVariable"},
		{"delete-values without entry", `url="ldap://dc" operation="delete-values" dn="cn=t"`, "entryVariable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(1, 1, strings.NewReader(ldapTaskBPMN(tc.attrs)))
			if err == nil {
				t.Fatalf("want an error mentioning %q, got none", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}
