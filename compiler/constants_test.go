package compiler

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/expr"
)

// constantsModel wraps a process body in a definitions element that binds the atlas and
// zeebe prefixes, with the given constant declarations on the process. processAttrs and
// body let a test add a personal declaration or an element that writes a name.
func constantsModel(processAttrs, constants, body string) string {
	if body == "" {
		body = `<startEvent id="s"/><endEvent id="e"/><sequenceFlow id="f1" sourceRef="s" targetRef="e"/>`
	}
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
             xmlns:atlas="http://atlas/schema/1.0">
  <process id="p" isExecutable="true" ` + processAttrs + `>
    <extensionElements><atlas:constants>` + constants + `</atlas:constants></extensionElements>
    ` + body + `
  </process>
</definitions>`
}

// Each of the four types a constant takes is parsed into the canonical form any other
// variable is stored in, in declaration order, so the engine seeds it without parsing.
func TestConstantsAreParsedIntoTheirStoredForm(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(constantsModel("", `
      <atlas:constant name="DOMAIN_SUFFIX" type="string" value="contoso.ch"/>
      <atlas:constant name=" MAX_AGENTS " type="number" value="20.50"/>
      <atlas:constant name="DRY" type="boolean" value="true"/>
      <atlas:constant name="ROUTING" type="json" value="{&quot;mode&quot;:&quot;rr&quot;,&quot;n&quot;:[1,2]}"/>
      <atlas:constant name="UNTYPED" value="plain"/>`, "")))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []Constant{
		{Name: "DOMAIN_SUFFIX", Type: "string", Value: "contoso.ch", Kind: expr.KindString, Text: "contoso.ch"},
		{Name: "MAX_AGENTS", Type: "number", Value: "20.50", Kind: expr.KindNumber, Text: "20.5"},
		{Name: "DRY", Type: "boolean", Value: "true", Kind: expr.KindBool, Bool: true},
		{Name: "ROUTING", Type: "json", Value: `{"mode":"rr","n":[1,2]}`, Kind: expr.KindJSON, Text: `{"mode":"rr","n":[1,2]}`},
		{Name: "UNTYPED", Type: "string", Value: "plain", Kind: expr.KindString, Text: "plain"},
	}
	got := cp.Constants()
	if len(got) != len(want) {
		t.Fatalf("Constants = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Constants[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	if !cp.HasConstants() || !cp.IsConstant("MAX_AGENTS") || cp.IsConstant("maxAgents") {
		t.Errorf("HasConstants/IsConstant disagree with the declaration")
	}
}

func TestNoConstants(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(startFormBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cp.Constants() != nil || cp.HasConstants() || cp.IsConstant("anything") {
		t.Errorf("a process without constants reports some: %+v", cp.Constants())
	}
}

// A constant the engine could not seed as written fails the deploy, naming it. A
// constant is new, so no stored model can carry one that this would start refusing.
func TestMalformedConstantsAreRefused(t *testing.T) {
	cases := map[string]struct{ decl, want string }{
		"nameless":       {`<atlas:constant type="string" value="x"/>`, "has no name"},
		"declared twice": {`<atlas:constant name="A" value="1"/><atlas:constant name="A" value="2"/>`, `"A" is declared twice`},
		"unknown type":   {`<atlas:constant name="A" type="date" value="2026-01-01"/>`, `type "date"`},
		"not a number":   {`<atlas:constant name="A" type="number" value="twenty"/>`, "is not a number"},
		"two numbers":    {`<atlas:constant name="A" type="number" value="1 2"/>`, "is not a number"},
		"quoted number":  {`<atlas:constant name="A" type="number" value="&quot;1&quot;"/>`, "is not a number"},
		"not a boolean":  {`<atlas:constant name="A" type="boolean" value="yes"/>`, "is not a boolean"},
		"broken json":    {`<atlas:constant name="A" type="json" value="{&quot;a&quot;:"/>`, "not valid JSON"},
		"json and more":  {`<atlas:constant name="A" type="json" value="{} x"/>`, "not valid JSON"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(1, 1, strings.NewReader(constantsModel("", tc.decl, "")))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// A name cannot be fixed and also asked for, personal, or the data subject.
func TestConstantsThatContradictOtherDeclarationsAreRefused(t *testing.T) {
	cases := map[string]struct{ attrs, extra, want string }{
		"start variable": {"", `<atlas:startForm><atlas:startVariable name="TENANT_ID"/></atlas:startForm>`, "both as a start variable and as a constant"},
		"personal":       {`atlas:personal="TENANT_ID" atlas:dataSubject="pnr"`, "", "both personal and as a constant"},
		"data subject":   {`atlas:personal="vorname" atlas:dataSubject="TENANT_ID"`, "", "is the data subject and a constant"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			model := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:atlas="http://atlas/schema/1.0">
  <process id="p" isExecutable="true" ` + tc.attrs + `>
    <extensionElements>
      <atlas:constants><atlas:constant name="TENANT_ID" value="t-1"/></atlas:constants>` + tc.extra + `
    </extensionElements>
    <startEvent id="s"/><endEvent id="e"/><sequenceFlow id="f1" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`
			_, err := Parse(1, 1, strings.NewReader(model))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// The model may not write a constant: every writer the compiler can see is refused at
// deploy, where the mistake is cheapest to see.
func TestModelWritesToAConstantAreRefused(t *testing.T) {
	task := func(inner string) string {
		return `<startEvent id="s"/>` + inner + `<endEvent id="e"/>
      <sequenceFlow id="f1" sourceRef="s" targetRef="t"/><sequenceFlow id="f2" sourceRef="t" targetRef="e"/>`
	}
	cases := map[string]struct{ body, want string }{
		"feel script result": {task(`<scriptTask id="t"><extensionElements>
        <zeebe:script expression="=&quot;x&quot;" resultVariable="TENANT_ID"/></extensionElements></scriptTask>`),
			"a script task's result variable writes \"TENANT_ID\""},
		"job script result": {task(`<scriptTask id="t"><extensionElements>
        <atlas:jobScript language="powershell" resultVariable="TENANT_ID">Write-Output 1</atlas:jobScript></extensionElements></scriptTask>`),
			"a script task's result variable writes \"TENANT_ID\""},
		"output mapping": {task(`<scriptTask id="t"><extensionElements>
        <zeebe:script expression="=1" resultVariable="r"/>
        <zeebe:ioMapping><zeebe:output source="=r" target="TENANT_ID"/></zeebe:ioMapping></extensionElements></scriptTask>`),
			"an output mapping writes \"TENANT_ID\""},
		"input mapping": {task(`<scriptTask id="t"><extensionElements>
        <zeebe:script expression="=1" resultVariable="r"/>
        <zeebe:ioMapping><zeebe:input source="=&quot;other&quot;" target="TENANT_ID"/></zeebe:ioMapping></extensionElements></scriptTask>`),
			"an input mapping writes \"TENANT_ID\""},
		"multi-instance output collection": {task(`<scriptTask id="t">
        <extensionElements><zeebe:script expression="=1" resultVariable="r"/></extensionElements>
        <multiInstanceLoopCharacteristics><extensionElements>
          <zeebe:loopCharacteristics inputCollection="=[1,2]" outputCollection="TENANT_ID" outputElement="=r"/>
        </extensionElements></multiInstanceLoopCharacteristics></scriptTask>`),
			"a multi-instance output collection writes \"TENANT_ID\""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(1, 1, strings.NewReader(constantsModel("", `<atlas:constant name="TENANT_ID" value="t-1"/>`, tc.body)))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// A mapping that hands the constant on unchanged writes nothing the model computed — it
// is how a call activity passes a constant into its child — so it deploys.
func TestPassingAConstantThroughUnchangedDeploys(t *testing.T) {
	body := `<startEvent id="s"/>
      <scriptTask id="t"><extensionElements>
        <zeebe:script expression="=1" resultVariable="r"/>
        <zeebe:ioMapping>
          <zeebe:input source="=TENANT_ID" target="TENANT_ID"/>
          <zeebe:output source="=TENANT_ID" target="TENANT_ID"/>
        </zeebe:ioMapping></extensionElements></scriptTask>
      <endEvent id="e"/>
      <sequenceFlow id="f1" sourceRef="s" targetRef="t"/><sequenceFlow id="f2" sourceRef="t" targetRef="e"/>`
	if _, err := Parse(1, 1, strings.NewReader(constantsModel("", `<atlas:constant name="TENANT_ID" value="t-1"/>`, body))); err != nil {
		t.Fatalf("Parse: %v", err)
	}
}

// A constant bound to its application (ADR-0453)
// takes the application's value on the installation where an instance starts; its own
// value, when it declares one, is the fallback and is parsed like any constant's.
func TestApplicationBoundConstants(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(constantsModel("", `
      <atlas:constant name="TENANT_ID" type="string" source="application"/>
      <atlas:constant name="RETRIES" type="number" source="application" value="3"/>
      <atlas:constant name="MODE" source="model" value="rr"/>`, "")))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []Constant{
		{Name: "TENANT_ID", Type: "string", Namespace: ApplicationNamespace},
		{Name: "RETRIES", Type: "number", Value: "3", Kind: expr.KindNumber, Text: "3", Namespace: ApplicationNamespace, HasFallback: true},
		{Name: "MODE", Type: "string", Value: "rr", Kind: expr.KindString, Text: "rr"},
	}
	got := cp.Constants()
	if len(got) != len(want) {
		t.Fatalf("Constants = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Constants[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestMalformedApplicationBoundConstantsAreRefused(t *testing.T) {
	cases := map[string]struct{ decl, want string }{
		"unknown source": {`<atlas:constant name="A" source="vault"/>`, `source "vault" is not one of model, application, server`},
		"bad fallback":   {`<atlas:constant name="A" type="number" source="application" value="many"/>`, "is not a number"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(1, 1, strings.NewReader(constantsModel("", tc.decl, "")))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// ParseConstantValue is what the API parses an application's values with, so it must
// agree with the compiler on every type.
func TestParseConstantValue(t *testing.T) {
	for _, tc := range []struct {
		typ, raw string
		kind     expr.ValueKind
		b        bool
		text     string
	}{
		{"string", "contoso.ch", expr.KindString, false, "contoso.ch"},
		{"number", "7.50", expr.KindNumber, false, "7.5"},
		{"boolean", "false", expr.KindBool, false, ""},
		{"json", `{"b":1,"a":[true]}`, expr.KindJSON, false, `{"a":[true],"b":1}`},
	} {
		kind, b, text, err := ParseConstantValue(tc.typ, tc.raw)
		if err != nil || kind != tc.kind || b != tc.b || text != tc.text {
			t.Errorf("ParseConstantValue(%q, %q) = %v %v %q %v, want %v %v %q", tc.typ, tc.raw, kind, b, text, err, tc.kind, tc.b, tc.text)
		}
	}
	if _, _, _, err := ParseConstantValue("date", "2026-01-01"); err == nil {
		t.Error("ParseConstantValue accepted an unknown type")
	}
}

// A constant's name is written in capitals — letters A–Z, digits and underscores,
// starting with a letter (ADR-0454, amended) — and may not take the name of a namespace
// constants are read under, which PowerShell would not tell from the namespace itself.
func TestConstantNamesAreWrittenInCapitals(t *testing.T) {
	for _, name := range []string{"tenantId", "TenantId", "TENANT-ID", "1ST", "_X", "GRÖSSE", "TENANT ID"} {
		_, err := Parse(1, 1, strings.NewReader(constantsModel("", `<atlas:constant name="`+name+`" value="v"/>`, "")))
		if err == nil || !strings.Contains(err.Error(), "written in capitals") {
			t.Errorf("constant %q: Parse error = %v, want it refused for its spelling", name, err)
		}
	}
	for _, name := range []string{"APPLICATION", "SERVER"} {
		_, err := Parse(1, 1, strings.NewReader(constantsModel("", `<atlas:constant name="`+name+`" value="v"/>`, "")))
		if err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("constant %q: Parse error = %v, want it refused as reserved", name, err)
		}
	}
	for _, name := range []string{"A", "TENANT_ID", "X2", "MAX_AGENTS_2"} {
		if _, err := Parse(1, 1, strings.NewReader(constantsModel("", `<atlas:constant name="`+name+`" value="v"/>`, ""))); err != nil {
			t.Errorf("constant %q: Parse: %v", name, err)
		}
	}
}

// The constants bound to the application are the members of one root variable, the
// application namespace (ADR-0453, amended). That variable is the constant: the model may
// not write it or ask for it at the start, while a variable named like a member is the
// process's own. A process that binds nothing to its application leaves the name free.
func TestTheApplicationNamespaceIsTheConstant(t *testing.T) {
	bound := `<atlas:constant name="TENANT_ID" source="application"/>`
	cp, err := Parse(1, 1, strings.NewReader(constantsModel("", bound, "")))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !cp.IsConstant(ApplicationNamespace) || cp.IsConstant("TENANT_ID") || !cp.Binds(ApplicationNamespace) {
		t.Errorf("IsConstant(application)=%v IsConstant(TENANT_ID)=%v Binds=%v, want true false true",
			cp.IsConstant(ApplicationNamespace), cp.IsConstant("TENANT_ID"), cp.Binds(ApplicationNamespace))
	}

	writes := func(target string) string {
		return `<startEvent id="s"/><scriptTask id="t"><extensionElements>
        <zeebe:script expression="=&quot;x&quot;" resultVariable="` + target + `"/></extensionElements></scriptTask><endEvent id="e"/>
      <sequenceFlow id="f1" sourceRef="s" targetRef="t"/><sequenceFlow id="f2" sourceRef="t" targetRef="e"/>`
	}
	if _, err := Parse(1, 1, strings.NewReader(constantsModel("", bound, writes("application")))); err == nil ||
		!strings.Contains(err.Error(), "the namespace this process's application constants are read under") {
		t.Errorf("writing the namespace: Parse error = %v, want it refused", err)
	}
	if _, err := Parse(1, 1, strings.NewReader(constantsModel("", bound, writes("TENANT_ID")))); err != nil {
		t.Errorf("writing a variable named like a member: Parse: %v, want it to deploy", err)
	}
	asked := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0">
  <process id="p" isExecutable="true"><extensionElements>
    <atlas:constants>` + bound + `</atlas:constants>
    <atlas:startForm><atlas:startVariable name="application"/></atlas:startForm>
  </extensionElements><startEvent id="s"/><endEvent id="e"/><sequenceFlow id="f1" sourceRef="s" targetRef="e"/></process>
</definitions>`
	if _, err := Parse(1, 1, strings.NewReader(asked)); err == nil || !strings.Contains(err.Error(), "namespace") {
		t.Errorf("a start variable named application: Parse error = %v, want it refused", err)
	}

	free, err := Parse(1, 1, strings.NewReader(constantsModel("", `<atlas:constant name="MODE" value="rr"/>`, writes("application"))))
	if err != nil || free.IsConstant(ApplicationNamespace) || free.Binds(ApplicationNamespace) {
		t.Errorf("a process binding nothing: err=%v IsConstant(application)=%v, want the name free", err, free != nil && free.IsConstant(ApplicationNamespace))
	}
}

// The server namespace works like the application's (ADR-0456), and
// both carry facts Atlas fills itself in lower case — which no constant can be — declared
// like a constant so a namespace still holds only what its model reads.
func TestServerConstantsAndInfoFields(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(constantsModel("", `
      <atlas:constant name="TENANT_ID" source="server"/>
      <atlas:constant name="environment" source="server" value="DEV"/>
      <atlas:constant name="name" source="application"/>
      <atlas:constant name="TENANT_ID" source="application"/>`, "")))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []Constant{
		{Name: "TENANT_ID", Type: "string", Namespace: ServerNamespace},
		{Name: "environment", Type: "string", Value: "DEV", Kind: expr.KindString, Text: "DEV", Namespace: ServerNamespace, Info: true, HasFallback: true},
		{Name: "name", Type: "string", Namespace: ApplicationNamespace, Info: true},
		{Name: "TENANT_ID", Type: "string", Namespace: ApplicationNamespace},
	}
	got := cp.Constants()
	if len(got) != len(want) {
		t.Fatalf("Constants = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Constants[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	if !cp.IsConstant(ServerNamespace) || !cp.IsConstant(ApplicationNamespace) || !cp.Binds(ServerNamespace) {
		t.Errorf("both namespaces should be constants of this process")
	}

	for decl, wantErr := range map[string]string{
		`<atlas:constant name="environment" source="application"/>`:                            "written in capitals",
		`<atlas:constant name="name"/>`:                                                        "written in capitals",
		`<atlas:constant name="environment" source="server" type="number"/>`:                   "declare it with type string",
		`<atlas:constant name="X" source="server"/><atlas:constant name="X" source="server"/>`: "declared twice",
	} {
		if _, err := Parse(1, 1, strings.NewReader(constantsModel("", decl, ""))); err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Errorf("%s: Parse error = %v, want it to mention %q", decl, err, wantErr)
		}
	}
}

// A definition deployed before constant names were written in capitals keeps loading: the
// reload reports the name and brings the process back, where a deploy refuses it — a
// spelling must not keep a server from starting (ADR-0454, amended).
func TestALowerCaseConstantIsReportedOnReloadNotRefused(t *testing.T) {
	model := constantsModel("", `<atlas:constant name="TenantId" value="t-1"/>`, "")
	if _, err := ParseNamed(1, 1, strings.NewReader(model), "p"); err == nil || !strings.Contains(err.Error(), "written in capitals") {
		t.Fatalf("deploy: err = %v, want the name refused", err)
	}
	cp, problems, err := ReloadNamed(1, 1, strings.NewReader(model), "p")
	if err != nil || cp == nil {
		t.Fatalf("reload: %v, want the definition back", err)
	}
	if len(problems) != 1 || problems[0].Rule != RuleConstantName {
		t.Fatalf("reload problems = %+v, want one %s", problems, RuleConstantName)
	}
	if !cp.IsConstant("TenantId") {
		t.Errorf("the reloaded definition lost its constant")
	}
}
