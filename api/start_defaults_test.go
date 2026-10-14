package api_test

import (
	"strings"
	"testing"
)

// defaultsBPMN declares three start variables: Region with a default, MaxAgents with one
// that is not a number, and Note with none. A user task keeps the instance alive so its
// variables can be read.
const defaultsBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:atlas="http://atlas/schema/1.0">
  <process id="defaults" isExecutable="true">
    <extensionElements>
      <atlas:startForm>
        <atlas:startVariable name="Region" type="string" default="CH"/>
        <atlas:startVariable name="MaxAgents" type="number" default="many"/>
        <atlas:startVariable name="Note" type="string"/>
      </atlas:startForm>
    </extensionElements>
    <startEvent id="s"/>
    <userTask id="u" name="Prüfen"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="u"/>
    <sequenceFlow id="f2" sourceRef="u" targetRef="e"/>
  </process>
</definitions>`

// An instance started over the API without a declared start variable has its default
// (ADR-0455) — the start that used to leave it out, because only
// the start forms ever read the declaration. A default that is not a literal of its type
// is not seeded, and the deploy says so instead of refusing the model.
func TestAnAPIStartTakesTheDeclaredDefaults(t *testing.T) {
	ts := newTestServer(t)
	dep := deployInto(t, ts, "", defaultsBPMN)
	if !strings.Contains(strings.Join(dep.Warnings, "\n"),
		`defaults: the default of start variable "MaxAgents" is not seeded into an instance started without it: value "many" is not a number`) {
		t.Errorf("deploy warnings = %q, want one naming the malformed MaxAgents default", dep.Warnings)
	}

	code, vars, msg := startVars(t, ts, dep.Key)
	if code != 200 {
		t.Fatalf("start: status=%d body=%s", code, msg)
	}
	if vars["Region"] != "CH" {
		t.Errorf("Region = %v, want the default CH", vars["Region"])
	}
	for _, name := range []string{"MaxAgents", "Note"} {
		if v, ok := vars[name]; ok {
			t.Errorf("%s = %v, want no variable: it has no default the engine can seed", name, v)
		}
	}
}
