package compiler

import (
	"strings"
	"testing"
)

// The AD worker's inline attributes, select and foundVariable (ADR-0166, amended
// 2026-11-03): the three attributes that let a process write and find a person's data in
// a directory while declaring it personal (ADR-0314).

// An entry authored inline compiles to one FEEL context, the way Entra's attributes do,
// and leaves the entry variable unset.
func TestAdInlineAttributesCompile(t *testing.T) {
	cp, d := adDetail(t, `connector="ad" operation="create-user" dn="=dn"
	    attributes="{&quot;sAMAccountName&quot;: &quot;=konto&quot;, &quot;givenName&quot;: &quot;=vorname&quot;, &quot;objectClass&quot;: [&quot;top&quot;, &quot;person&quot;, &quot;organizationalPerson&quot;, &quot;user&quot;], &quot;userAccountControl&quot;: 514, &quot;description&quot;: null}"`)
	if d.AdAttributes.Expr == nil {
		t.Fatal("inline attributes did not compile to an expression")
	}
	if d.AdEntryVar != -1 {
		t.Errorf("entryVariable = %q, want none when the entry is inline", cp.Intern(d.AdEntryVar))
	}
	inputs := strings.Join(d.AdAttributes.Expr.Inputs(), ",")
	for _, want := range []string{"konto", "vorname"} {
		if !strings.Contains(inputs, want) {
			t.Errorf("the attributes expression reads %q, want it to read %q", inputs, want)
		}
	}
}

// Every entry operation takes either form; the variable form still compiles as before.
func TestAdEntryOperationsTakeEitherForm(t *testing.T) {
	for _, op := range []string{"create-user", "create-group", "create-contact", "update-attributes"} {
		t.Run(op, func(t *testing.T) {
			_, inline := adDetail(t, `connector="ad" operation="`+op+`" dn="cn=a,dc=x" attributes="{&quot;description&quot;: &quot;x&quot;}"`)
			if inline.AdAttributes.Expr == nil {
				t.Error("inline attributes not compiled")
			}
			_, byVar := adDetail(t, `connector="ad" operation="`+op+`" dn="cn=a,dc=x" entryVariable="eintrag"`)
			if byVar.AdAttributes.Expr != nil || byVar.AdEntryVar == -1 {
				t.Error("the entry variable form no longer compiles to an entry variable")
			}
		})
	}
}

func TestAdEntryValidation(t *testing.T) {
	for _, tc := range []struct{ name, attrs, want string }{
		{"both forms", `connector="ad" operation="create-user" dn="cn=a" entryVariable="e" attributes="{&quot;cn&quot;: &quot;a&quot;}"`, "both"},
		{"neither form", `connector="ad" operation="create-user" dn="cn=a"`, "entryVariable"},
		{"neither form names attributes too", `connector="ad" operation="update-attributes" dn="cn=a"`, "inline attributes"},
		{"attributes on a non-entry operation", `connector="ad" operation="disable" dn="cn=a" attributes="{&quot;cn&quot;: &quot;a&quot;}"`, "writes no entry"},
		{"not JSON", `connector="ad" operation="create-user" dn="cn=a" attributes="{cn: a}"`, "invalid attributes JSON"},
		{"not an object", `connector="ad" operation="create-user" dn="cn=a" attributes="[&quot;a&quot;]"`, "JSON object"},
		{"nested object", `connector="ad" operation="create-user" dn="cn=a" attributes="{&quot;manager&quot;: {&quot;dn&quot;: &quot;cn=b&quot;}}"`, "is an object"},
		{"nested list", `connector="ad" operation="create-user" dn="cn=a" attributes="{&quot;otherMailbox&quot;: [[&quot;a&quot;]]}"`, "lists a"},
		{"empty name", `connector="ad" operation="create-user" dn="cn=a" attributes="{&quot;&quot;: &quot;a&quot;}"`, "empty name"},
		{"bad FEEL", `connector="ad" operation="create-user" dn="cn=a" attributes="{&quot;cn&quot;: &quot;=(&quot;}"`, "did not compile"},
		{"empty FEEL", `connector="ad" operation="create-user" dn="cn=a" attributes="{&quot;cn&quot;: &quot;=&quot;}"`, "empty =expression"},
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

// select is normalised to the names as written, comma-joined, and foundVariable is
// interned beside the result variable.
func TestAdSearchSelectAndFoundVariable(t *testing.T) {
	cp, d := adDetail(t, `connector="ad" operation="search" baseDN="dc=x" filter="(cn=a)"
	    select=" cn, mail ,userAccountControl;binary , 1.2.840.113556.1.4.656" resultVariable="treffer" foundVariable="gefunden"`)
	if got := cp.Intern(d.AdSelect); got != "cn,mail,userAccountControl;binary,1.2.840.113556.1.4.656" {
		t.Errorf("select = %q", got)
	}
	if got := cp.Intern(d.AdFoundVar); got != "gefunden" {
		t.Errorf("foundVariable = %q, want gefunden", got)
	}
	if got := cp.Intern(d.ResultVar); got != "treffer" {
		t.Errorf("resultVariable = %q, want treffer", got)
	}
	// The two selectors RFC 4511 defines pass too.
	cp, d = adDetail(t, `connector="ad" operation="search" baseDN="dc=x" select="*,+" resultVariable="r"`)
	if got := cp.Intern(d.AdSelect); got != "*,+" {
		t.Errorf("select = %q, want the two selectors kept", got)
	}
	// Neither authored: nothing interned, and today's behaviour.
	_, d = adDetail(t, `connector="ad" operation="search" baseDN="dc=x" resultVariable="r"`)
	if d.AdSelect != -1 || d.AdFoundVar != -1 {
		t.Errorf("select / foundVariable = %d / %d, want neither", d.AdSelect, d.AdFoundVar)
	}
}

// A foundVariable on its own is a whole search: "is this name taken?" needs only the
// boolean, and writing the entries too would put a person's data where nothing reads it.
func TestAdSearchWithOnlyAFoundVariable(t *testing.T) {
	cp, d := adDetail(t, `connector="ad" operation="search" baseDN="dc=x" filter="(sAMAccountName=a)" foundVariable="vergeben"`)
	if d.ResultVar != -1 {
		t.Errorf("resultVariable = %q, want none", cp.Intern(d.ResultVar))
	}
	if got := cp.Intern(d.AdFoundVar); got != "vergeben" {
		t.Errorf("foundVariable = %q", got)
	}
}

func TestAdSelectAndFoundVariableValidation(t *testing.T) {
	for _, tc := range []struct{ name, attrs, want string }{
		{"neither result nor found", `connector="ad" operation="search" baseDN="dc=x"`, "foundVariable"},
		{"select on a write", `connector="ad" operation="disable" dn="cn=a" select="cn"`, "search only"},
		{"foundVariable on a write", `connector="ad" operation="disable" dn="cn=a" foundVariable="f"`, "search only"},
		{"select with a space in a name", `connector="ad" operation="search" baseDN="dc=x" resultVariable="r" select="given name"`, "not an LDAP attribute name"},
		{"select with a leading digit", `connector="ad" operation="search" baseDN="dc=x" resultVariable="r" select="1mail"`, "not an LDAP attribute name"},
		{"select with a filter character", `connector="ad" operation="search" baseDN="dc=x" resultVariable="r" select="cn,(mail)"`, "not an LDAP attribute name"},
		{"select with ranged retrieval", `connector="ad" operation="search" baseDN="dc=x" resultVariable="r" select="member;range=0-*"`, "not an LDAP attribute name"},
		{"select with an empty name", `connector="ad" operation="search" baseDN="dc=x" resultVariable="r" select="cn,,mail"`, "empty attribute name"},
		{"select naming one twice", `connector="ad" operation="search" baseDN="dc=x" resultVariable="r" select="mail,Mail"`, "twice"},
		{"foundVariable not a name", `connector="ad" operation="search" baseDN="dc=x" resultVariable="r" foundVariable="r.found"`, "not a variable name"},
		{"foundVariable with a space", `connector="ad" operation="search" baseDN="dc=x" resultVariable="r" foundVariable="ist gefunden"`, "not a variable name"},
		{"foundVariable starting with a digit", `connector="ad" operation="search" baseDN="dc=x" resultVariable="r" foundVariable="1f"`, "not a variable name"},
		{"foundVariable equals resultVariable", `connector="ad" operation="search" baseDN="dc=x" resultVariable="r" foundVariable="r"`, "same variable"},
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

// The point of the inline attributes: a variable the process declares personal may be
// read there, because the worker evaluates the template at handoff — the edge ADR-0314
// permits plaintext at — while the very same read in a script task, which the engine
// evaluates over ciphertext, is refused at deploy. A search may write its result into a
// personal variable, and a gateway still branches on the separate foundVariable.
func TestAdInlineAttributesMayReadAPersonalVariable(t *testing.T) {
	const adTask = `<bpmn:serviceTask id="such"><bpmn:extensionElements>
      <atlas:adConnector connector="ad" operation="search" baseDN="ou=users,dc=x"
          filter="=&quot;(sAMAccountName=&quot; + konto + &quot;)&quot;" select="cn,mail"
          resultVariable="treffer" foundVariable="gefunden"/>
    </bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:exclusiveGateway id="gw" default="fneu"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements>
      <atlas:adConnector connector="ad" operation="create-user"
          dn="=&quot;CN=&quot; + vorname + &quot; &quot; + nachname + &quot;,OU=users,DC=x&quot;"
          attributes="{&quot;sAMAccountName&quot;: &quot;=konto&quot;, &quot;givenName&quot;: &quot;=vorname&quot;, &quot;sn&quot;: &quot;=nachname&quot;, &quot;displayName&quot;: &quot;=vorname + \&quot; \&quot; + nachname&quot;}"/>
    </bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e1"/>
    <bpmn:endEvent id="e2"/>
    <bpmn:sequenceFlow id="g1" sourceRef="s" targetRef="such"/>
    <bpmn:sequenceFlow id="g2" sourceRef="such" targetRef="gw"/>
    <bpmn:sequenceFlow id="fneu" sourceRef="gw" targetRef="t"/>
    <bpmn:sequenceFlow id="fda" sourceRef="gw" targetRef="e2"><bpmn:conditionExpression>=gefunden</bpmn:conditionExpression></bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="g3" sourceRef="t" targetRef="e1"/>`
	const decl = `atlas:personal="vorname,nachname,konto,treffer" atlas:dataSubject="recipient"`
	if _, err := Parse(1, 1, strings.NewReader(personalModel(decl, adTask))); err != nil {
		t.Fatalf("an AD task reading personal variables in its attributes, dn and filter was refused; the worker evaluates them, where ADR-0314 permits plaintext: %v", err)
	}

	const script = `<bpmn:scriptTask id="t">
      <bpmn:extensionElements><zeebe:script expression="=vorname + &quot; &quot; + nachname" resultVariable="anzeige"/></bpmn:extensionElements>
    </bpmn:scriptTask>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="g1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="g2" sourceRef="t" targetRef="e1"/>`
	_, err := Parse(1, 1, strings.NewReader(personalModel(decl, script)))
	if err == nil {
		t.Fatal("a script task reading a personal variable was accepted; the engine would evaluate it over ciphertext")
	}
	if !strings.Contains(err.Error(), "vorname") || !strings.Contains(err.Error(), "personal") {
		t.Errorf("error = %v, want it to name the variable and say why", err)
	}

	// And the gateway may not read the sealed result itself — which is what
	// foundVariable is for.
	gw := strings.Replace(adTask, "<bpmn:conditionExpression>=gefunden<", "<bpmn:conditionExpression>=treffer.found<", 1)
	if _, err := Parse(1, 1, strings.NewReader(personalModel(decl, gw))); err == nil {
		t.Fatal("a gateway reading the personal search result was accepted")
	} else if !strings.Contains(err.Error(), "treffer") || !strings.Contains(err.Error(), "personal") {
		t.Errorf("error = %v, want it refused for reading the personal result", err)
	}
}
