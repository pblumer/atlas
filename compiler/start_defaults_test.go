package compiler

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/expr"
)

// startVarsModel is a process declaring the given <atlas:startVariable/> rows, with
// processAttrs on the <process> element.
func startVarsModel(processAttrs, rows string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:atlas="http://atlas/schema/1.0">
  <process id="p" isExecutable="true" ` + processAttrs + `>
    <extensionElements><atlas:startForm>` + rows + `</atlas:startForm></extensionElements>
    <startEvent id="s"/><endEvent id="e"/><sequenceFlow id="f1" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`
}

func startVarsOf(t *testing.T, model string) map[string]StartVariable {
	t.Helper()
	cp, err := Parse(10, 1, strings.NewReader(model))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out := map[string]StartVariable{}
	for _, sv := range cp.StartVariables() {
		out[sv.Name] = sv
	}
	return out
}

// A default is read once, at deploy, into the form a variable is stored in
// (ADR-draft-start-variable-defaults), so the engine seeds it without parsing. The
// authored text stays beside it for the start forms to prefill.
func TestStartVariableDefaultsAreParsedAtDeploy(t *testing.T) {
	got := startVarsOf(t, startVarsModel("", `
      <atlas:startVariable name="Region" type="string" default="CH"/>
      <atlas:startVariable name="MaxAgents" type="number" default="7.50"/>
      <atlas:startVariable name="DryRun" type="boolean" default="false"/>
      <atlas:startVariable name="Limits" type="json" default='{"a":1}'/>
      <atlas:startVariable name="Note" type="string"/>`))

	cases := []struct {
		name string
		kind expr.ValueKind
		text string
	}{
		{"Region", expr.KindString, "CH"},
		{"MaxAgents", expr.KindNumber, "7.5"},
		{"DryRun", expr.KindBool, ""},
		{"Limits", expr.KindJSON, `{"a":1}`},
	}
	for _, tc := range cases {
		sv := got[tc.name]
		if !sv.HasDefault || sv.Kind != tc.kind || sv.Text != tc.text || sv.DefaultProblem != "" {
			t.Errorf("%s = %+v, want a default of kind %v and text %q", tc.name, sv, tc.kind, tc.text)
		}
	}
	if got["MaxAgents"].Default != "7.50" {
		t.Errorf("MaxAgents.Default = %q, want the authored 7.50 kept for the start forms", got["MaxAgents"].Default)
	}
	if got["DryRun"].Bool {
		t.Errorf("DryRun default = true, want false")
	}
	if sv := got["Note"]; sv.HasDefault || sv.DefaultProblem != "" {
		t.Errorf("Note = %+v, want no default and nothing to warn about", sv)
	}
}

// A default the engine cannot seed is withheld, with the reason — and the model still
// deploys: the declaration has always been accepted as written, and a stored model is
// compiled again on every restart.
func TestAMalformedStartVariableDefaultIsWithheldNotRefused(t *testing.T) {
	got := startVarsOf(t, startVarsModel("", `
      <atlas:startVariable name="MaxAgents" type="number" default="many"/>
      <atlas:startVariable name="DryRun" type="boolean" default="yes"/>
      <atlas:startVariable name="Limits" type="json" default="{broken"/>`))
	for name, want := range map[string]string{
		"MaxAgents": "not a number",
		"DryRun":    "not a boolean",
		"Limits":    "not valid JSON",
	} {
		sv := got[name]
		if sv.HasDefault || !strings.Contains(sv.DefaultProblem, want) {
			t.Errorf("%s = %+v, want no default and a problem containing %q", name, sv, want)
		}
	}
}

// A default on personal data, or on the data subject, is withheld too: the engine would
// refuse a personal value in the clear and park the instance it was starting, and a
// default subject would make every instance started without one the same person's.
func TestPersonalStartVariableDefaultsAreWithheld(t *testing.T) {
	got := startVarsOf(t, startVarsModel(`atlas:personal="vorname" atlas:dataSubject="pnr"`, `
      <atlas:startVariable name="vorname" type="string" default="Anna"/>
      <atlas:startVariable name="pnr" type="string" default="P-1"/>
      <atlas:startVariable name="Region" type="string" default="CH"/>`))
	for name, want := range map[string]string{"vorname": "personal data", "pnr": "data subject"} {
		sv := got[name]
		if sv.HasDefault || !strings.Contains(sv.DefaultProblem, want) {
			t.Errorf("%s = %+v, want the default withheld because it is %s", name, sv, want)
		}
		if sv.Default == "" {
			t.Errorf("%s lost its authored default; the start form still prefills it", name)
		}
	}
	if !got["Region"].HasDefault {
		t.Errorf("Region = %+v, want its default: it is not personal", got["Region"])
	}
}
