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
		"malformed feel bindDN":        `<atlas:adConnector url="ldaps://x" bindDN="=(" operation="disable" dn="cn=x"/>`,
		"malformed feel dn":            `<atlas:adConnector url="ldaps://x" operation="disable" dn="=("/>`,
		"malformed feel memberDN":      `<atlas:adConnector url="ldaps://x" operation="add-group-member" dn="cn=g" memberDN="=("/>`,
		"malformed feel newPassword":   `<atlas:adConnector url="ldaps://x" operation="set-password" dn="cn=x" newPassword="=("/>`,
	}
	for name, inner := range cases {
		if _, err := Parse(1, 1, strings.NewReader(wrap(inner))); err == nil {
			t.Errorf("%s: want a compile error, got nil", name)
		}
	}
}

// adTaskBPMN builds a one-task model from raw <atlas:adConnector> attributes.
func adTaskBPMN(attrs string) string {
	return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:adConnector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

func adDetail(t *testing.T, attrs string) (*CompiledProcess, *ConnectorTaskDetail) {
	t.Helper()
	cp, err := Parse(1, 1, strings.NewReader(adTaskBPMN(attrs)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target)
	return cp, cp.ConnectorTask(node.Detail)
}

// The operations that complete the lifecycle: an entry can now be changed, moved,
// deleted, and a group created — not only created, password-set and enabled.
func TestParseAdLifecycleOperations(t *testing.T) {
	const server = `url="ldaps://dc.example.com:636" bindDN="cn=svc,dc=example,dc=com" bindSecret="AD_BIND"`

	cp, d := adDetail(t, server+` operation="update-attributes" dn="cn=Arno,ou=users,dc=example,dc=com" entryVariable="aenderungen"`)
	if got := cp.Intern(d.AdOp); got != "update-attributes" {
		t.Errorf("operation = %q", got)
	}
	if got := cp.Intern(d.AdEntryVar); got != "aenderungen" {
		t.Errorf("entryVariable = %q", got)
	}

	cp2, d2 := adDetail(t, server+` operation="move" dn="cn=Arno,ou=users,dc=example,dc=com" newDN="=zielDN"`)
	if got := cp2.Intern(d2.AdOp); got != "move" {
		t.Errorf("operation = %q", got)
	}
	if d2.AdNewDN.Expr == nil {
		t.Errorf("newDN should be a compiled FEEL expression, got literal %q", d2.AdNewDN.Literal)
	}

	cp3, d3 := adDetail(t, server+` operation="delete" dn="cn=Alt,ou=users,dc=example,dc=com"`)
	if got := cp3.Intern(d3.AdOp); got != "delete" {
		t.Errorf("operation = %q", got)
	}

	cp4, d4 := adDetail(t, server+` operation="create-group" dn="cn=Team,ou=groups,dc=example,dc=com" entryVariable="gruppe"`)
	if got := cp4.Intern(d4.AdOp); got != "create-group" {
		t.Errorf("operation = %q", got)
	}
	if got := cp4.Intern(d4.AdEntryVar); got != "gruppe" {
		t.Errorf("entryVariable = %q", got)
	}
}

func TestAdLifecycleValidation(t *testing.T) {
	const server = `url="ldaps://dc" bindSecret="R"`
	for _, tc := range []struct{ name, attrs, want string }{
		{"update without entry", server + ` operation="update-attributes" dn="cn=a"`, "entryVariable"},
		{"create-group without entry", server + ` operation="create-group" dn="cn=a"`, "entryVariable"},
		{"move without newDN", server + ` operation="move" dn="cn=a"`, "newDN"},
		{"move without dn", server + ` operation="move" newDN="cn=b"`, "dn"},
		{"delete without dn", server + ` operation="delete"`, "dn"},
		{"bad FEEL newDN", server + ` operation="move" dn="cn=a" newDN="="`, "newDN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(1, 1, strings.NewReader(adTaskBPMN(tc.attrs)))
			if err == nil {
				t.Fatalf("want an error mentioning %q, got none", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// The sync operation reads a subtree delta rather than acting on one entry, so it is
// the one AD operation that takes a base rather than a dn (ADR-0166, amended).
func TestParseAdSync(t *testing.T) {
	cp, d := adDetail(t, `url="ldaps://dc" bindSecret="AD_BIND" operation="sync"
	    baseDN="dc=example,dc=com" filter="(objectClass=user)" cookieVariable="dirsyncCookie"
	    resultVariable="aenderungen" maxEntries="250" objectSecurity="true"`)
	if got := cp.Intern(d.AdOp); got != "sync" {
		t.Errorf("operation = %q, want sync", got)
	}
	if d.AdBaseDN.Literal != "dc=example,dc=com" || d.AdFilter.Literal != "(objectClass=user)" {
		t.Errorf("base/filter = %+v / %+v", d.AdBaseDN, d.AdFilter)
	}
	if got := cp.Intern(d.AdCookieVar); got != "dirsyncCookie" {
		t.Errorf("cookieVariable = %q", got)
	}
	if got := cp.Intern(d.ResultVar); got != "aenderungen" {
		t.Errorf("resultVariable = %q", got)
	}
	if d.AdMaxEntries != 250 || !d.AdObjectSecurity {
		t.Errorf("maxEntries/objectSecurity = %d / %v", d.AdMaxEntries, d.AdObjectSecurity)
	}
	// A sync addresses a naming context, not an entry.
	if d.AdDN.Literal != "" {
		t.Errorf("dn = %q, want none for a sync", d.AdDN.Literal)
	}
}

// The cap defaults on, and costs nothing: a pass is resumable by construction, so a
// bound only means a second pass rather than a lost answer.
func TestAdSyncMaxEntriesDefault(t *testing.T) {
	_, d := adDetail(t, `url="ldaps://dc" operation="sync" baseDN="dc=x" cookieVariable="c" resultVariable="r"`)
	if d.AdMaxEntries != defaultAdSyncMaxEntries {
		t.Errorf("maxEntries = %d, want the default %d", d.AdMaxEntries, defaultAdSyncMaxEntries)
	}
	// Every other operation carries no cap at all.
	_, d2 := adDetail(t, `url="ldaps://dc" operation="disable" dn="cn=a"`)
	if d2.AdMaxEntries != 0 {
		t.Errorf("maxEntries = %d, want 0 for a non-sync", d2.AdMaxEntries)
	}
}

func TestAdSyncValidation(t *testing.T) {
	for _, tc := range []struct{ name, attrs, want string }{
		{"no baseDN", `url="ldaps://dc" operation="sync" cookieVariable="c" resultVariable="r"`, "baseDN"},
		{"no cookie variable", `url="ldaps://dc" operation="sync" baseDN="dc=x" resultVariable="r"`, "cookieVariable"},
		{"no result variable", `url="ldaps://dc" operation="sync" baseDN="dc=x" cookieVariable="c"`, "resultVariable"},
		{"maxEntries on a write", `url="ldaps://dc" operation="disable" dn="cn=a" maxEntries="5"`, "maxEntries"},
		{"maxEntries not a number", `url="ldaps://dc" operation="sync" baseDN="dc=x" cookieVariable="c" resultVariable="r" maxEntries="viele"`, "maxEntries"},
		{"maxEntries negative", `url="ldaps://dc" operation="sync" baseDN="dc=x" cookieVariable="c" resultVariable="r" maxEntries="-1"`, "maxEntries"},
		{"bad FEEL baseDN", `url="ldaps://dc" operation="sync" baseDN="=" cookieVariable="c" resultVariable="r"`, "baseDN"},
		{"bad FEEL filter", `url="ldaps://dc" operation="sync" baseDN="dc=x" filter="=" cookieVariable="c" resultVariable="r"`, "filter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(1, 1, strings.NewReader(adTaskBPMN(tc.attrs)))
			if err == nil {
				t.Fatalf("want an error mentioning %q, got none", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// A task addresses its directory by naming a connector an operator configured, the way
// every other credential-bearing kind is addressed
// (ADR-0206). The model then carries the operation and its
// DNs, and nothing about where the directory is or who binds to it.
func TestAdConnectorTaskNamesAConfiguredDirectory(t *testing.T) {
	cp, d := adDetail(t, `connector="prod-forest" operation="disable" dn="cn=Arno,dc=example,dc=com"`)
	if got := cp.Intern(d.Connector); got != "prod-forest" {
		t.Errorf("connector = %q, want prod-forest", got)
	}
	// And nothing about the directory itself: those slots stay empty, so nothing
	// downstream can quietly prefer a stale url the model happens to still carry.
	if d.AdURL.Literal != "" || d.AdBindDN.Literal != "" || d.AdBindSecret != -1 {
		t.Errorf("url/bindDN/bindSecret = %q / %q / %d, want none of them authored",
			d.AdURL.Literal, d.AdBindDN.Literal, d.AdBindSecret)
	}
}

// The older shape still compiles, and carries no connector reference — which is what
// keeps the deploy-time "a connector of kind ad is required here" check off models
// written before records existed.
func TestAdConnectorTaskStillCompilesWithItsOwnURL(t *testing.T) {
	cp, d := adDetail(t, `url="ldaps://dc" bindDN="cn=svc,dc=x" bindSecret="AD_BIND" operation="disable" dn="cn=a"`)
	if d.Connector != -1 {
		t.Errorf("connector = %d, want none for a task carrying its own url", d.Connector)
	}
	if d.AdURL.Literal != "ldaps://dc" || cp.Intern(d.AdBindSecret) != "AD_BIND" {
		t.Errorf("url/bindSecret = %q / %q", d.AdURL.Literal, cp.Intern(d.AdBindSecret))
	}
	// The reference view agrees: no connector to check against the store.
	if _, ok := cp.NodeConnectorRef(0); ok {
		if ref, _ := cp.NodeConnectorRef(0); ref.Connector != "" {
			t.Errorf("NodeConnectorRef = %+v, want no connector reference", ref)
		}
	}
}

// Naming a connector *and* carrying a url is refused rather than resolved by
// precedence. Whichever rule we picked, half the readers of the model would assume the
// other — and the two point at different directories, so a silent winner writes to the
// wrong forest.
func TestAdConnectorTaskRefusesBothShapesAtOnce(t *testing.T) {
	for _, attrs := range []string{
		`connector="prod" url="ldaps://dc" operation="disable" dn="cn=a"`,
		`connector="prod" bindDN="cn=svc,dc=x" operation="disable" dn="cn=a"`,
		`connector="prod" bindSecret="AD_BIND" operation="disable" dn="cn=a"`,
	} {
		_, err := Parse(1, 1, strings.NewReader(adTaskBPMN(attrs)))
		if err == nil {
			t.Fatalf("a task carrying both shapes compiled: %s", attrs)
		}
		if !strings.Contains(err.Error(), "keep one") {
			t.Errorf("error = %v, want it to say which to keep", err)
		}
	}
}

// A task carrying neither is refused, naming both ways out — the message is the only
// place a modeler learns that a Console connector is now an option.
func TestAdConnectorTaskNeedsADirectoryOneWayOrTheOther(t *testing.T) {
	_, err := Parse(1, 1, strings.NewReader(adTaskBPMN(`operation="disable" dn="cn=a"`)))
	if err == nil {
		t.Fatal("a task naming no directory at all compiled")
	}
	for _, want := range []string{"connector", "url"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", err, want)
		}
	}
}
