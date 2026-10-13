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
      <atlas:constant name="DomainSuffix" type="string" value="contoso.ch"/>
      <atlas:constant name=" MaxAgents " type="number" value="20.50"/>
      <atlas:constant name="Dry" type="boolean" value="true"/>
      <atlas:constant name="Routing" type="json" value="{&quot;mode&quot;:&quot;rr&quot;,&quot;n&quot;:[1,2]}"/>
      <atlas:constant name="Untyped" value="plain"/>`, "")))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []Constant{
		{Name: "DomainSuffix", Type: "string", Value: "contoso.ch", Kind: expr.KindString, Text: "contoso.ch"},
		{Name: "MaxAgents", Type: "number", Value: "20.50", Kind: expr.KindNumber, Text: "20.5"},
		{Name: "Dry", Type: "boolean", Value: "true", Kind: expr.KindBool, Bool: true},
		{Name: "Routing", Type: "json", Value: `{"mode":"rr","n":[1,2]}`, Kind: expr.KindJSON, Text: `{"mode":"rr","n":[1,2]}`},
		{Name: "Untyped", Type: "string", Value: "plain", Kind: expr.KindString, Text: "plain"},
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
	if !cp.HasConstants() || !cp.IsConstant("MaxAgents") || cp.IsConstant("maxAgents") {
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
		"declared twice": {`<atlas:constant name="a" value="1"/><atlas:constant name="a" value="2"/>`, `"a" is declared twice`},
		"unknown type":   {`<atlas:constant name="a" type="date" value="2026-01-01"/>`, `type "date"`},
		"not a number":   {`<atlas:constant name="a" type="number" value="twenty"/>`, "is not a number"},
		"two numbers":    {`<atlas:constant name="a" type="number" value="1 2"/>`, "is not a number"},
		"quoted number":  {`<atlas:constant name="a" type="number" value="&quot;1&quot;"/>`, "is not a number"},
		"not a boolean":  {`<atlas:constant name="a" type="boolean" value="yes"/>`, "is not a boolean"},
		"broken json":    {`<atlas:constant name="a" type="json" value="{&quot;a&quot;:"/>`, "not valid JSON"},
		"json and more":  {`<atlas:constant name="a" type="json" value="{} x"/>`, "not valid JSON"},
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
		"start variable": {"", `<atlas:startForm><atlas:startVariable name="TenantId"/></atlas:startForm>`, "both as a start variable and as a constant"},
		"personal":       {`atlas:personal="TenantId" atlas:dataSubject="pnr"`, "", "both personal and as a constant"},
		"data subject":   {`atlas:personal="vorname" atlas:dataSubject="TenantId"`, "", "is the data subject and a constant"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			model := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:atlas="http://atlas/schema/1.0">
  <process id="p" isExecutable="true" ` + tc.attrs + `>
    <extensionElements>
      <atlas:constants><atlas:constant name="TenantId" value="t-1"/></atlas:constants>` + tc.extra + `
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
        <zeebe:script expression="=&quot;x&quot;" resultVariable="TenantId"/></extensionElements></scriptTask>`),
			"a script task's result variable writes \"TenantId\""},
		"job script result": {task(`<scriptTask id="t"><extensionElements>
        <atlas:jobScript language="powershell" resultVariable="TenantId">Write-Output 1</atlas:jobScript></extensionElements></scriptTask>`),
			"a script task's result variable writes \"TenantId\""},
		"output mapping": {task(`<scriptTask id="t"><extensionElements>
        <zeebe:script expression="=1" resultVariable="r"/>
        <zeebe:ioMapping><zeebe:output source="=r" target="TenantId"/></zeebe:ioMapping></extensionElements></scriptTask>`),
			"an output mapping writes \"TenantId\""},
		"input mapping": {task(`<scriptTask id="t"><extensionElements>
        <zeebe:script expression="=1" resultVariable="r"/>
        <zeebe:ioMapping><zeebe:input source="=&quot;other&quot;" target="TenantId"/></zeebe:ioMapping></extensionElements></scriptTask>`),
			"an input mapping writes \"TenantId\""},
		"multi-instance output collection": {task(`<scriptTask id="t">
        <extensionElements><zeebe:script expression="=1" resultVariable="r"/></extensionElements>
        <multiInstanceLoopCharacteristics><extensionElements>
          <zeebe:loopCharacteristics inputCollection="=[1,2]" outputCollection="TenantId" outputElement="=r"/>
        </extensionElements></multiInstanceLoopCharacteristics></scriptTask>`),
			"a multi-instance output collection writes \"TenantId\""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(1, 1, strings.NewReader(constantsModel("", `<atlas:constant name="TenantId" value="t-1"/>`, tc.body)))
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
          <zeebe:input source="=TenantId" target="TenantId"/>
          <zeebe:output source="=TenantId" target="TenantId"/>
        </zeebe:ioMapping></extensionElements></scriptTask>
      <endEvent id="e"/>
      <sequenceFlow id="f1" sourceRef="s" targetRef="t"/><sequenceFlow id="f2" sourceRef="t" targetRef="e"/>`
	if _, err := Parse(1, 1, strings.NewReader(constantsModel("", `<atlas:constant name="TenantId" value="t-1"/>`, body))); err != nil {
		t.Fatalf("Parse: %v", err)
	}
}

// A constant bound to its application (ADR-draft-application-values-per-installation)
// takes the application's value on the installation where an instance starts; its own
// value, when it declares one, is the fallback and is parsed like any constant's.
func TestApplicationBoundConstants(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(constantsModel("", `
      <atlas:constant name="TenantId" type="string" source="application"/>
      <atlas:constant name="Retries" type="number" source="application" value="3"/>
      <atlas:constant name="Mode" source="model" value="rr"/>`, "")))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []Constant{
		{Name: "TenantId", Type: "string", FromApplication: true},
		{Name: "Retries", Type: "number", Value: "3", Kind: expr.KindNumber, Text: "3", FromApplication: true, HasFallback: true},
		{Name: "Mode", Type: "string", Value: "rr", Kind: expr.KindString, Text: "rr"},
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
		"unknown source": {`<atlas:constant name="a" source="vault"/>`, `source "vault" is not one of model, application`},
		"bad fallback":   {`<atlas:constant name="a" type="number" source="application" value="many"/>`, "is not a number"},
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
