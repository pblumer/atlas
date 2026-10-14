package compiler

import (
	"fmt"
	"strings"
	"testing"
)

// A personal value may leave a subprocess under its own name (ADR-0314, amended): an
// output mapping that only names the variable it targets is a copy of the sealed record,
// not an expression the engine evaluates. Everything else that touches a declared
// variable in an output mapping stays refused.

// passThroughBPMN is a subprocess with one screen, declaring iban and konto personal, and
// the given output mappings on the subprocess.
func passThroughBPMN(outputs string) string {
	return fmt.Sprintf(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:atlas="http://atlas/schema/1.0"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="bogen" isExecutable="true" atlas:personal="iban,konto" atlas:dataSubject="pnr">
    <startEvent id="s"/>
    <subProcess id="sitting">
      <extensionElements><zeebe:ioMapping>%s</zeebe:ioMapping></extensionElements>
      <startEvent id="ws"/>
      <userTask id="eins"/>
      <endEvent id="we"/>
      <sequenceFlow id="w1" sourceRef="ws" targetRef="eins"/>
      <sequenceFlow id="w2" sourceRef="eins" targetRef="we"/>
    </subProcess>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="sitting"/>
    <sequenceFlow id="f2" sourceRef="sitting" targetRef="e"/>
  </process>
</definitions>`, outputs)
}

func TestAPersonalValueMayLeaveASubprocessUnderItsOwnName(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(passThroughBPMN(
		`<zeebe:output source="=iban" target="iban"/><zeebe:output source="= iban " target="iban"/><zeebe:output source="=pnr" target="pnr"/>`)))
	if err != nil {
		t.Fatalf("a personal value handed out under its own name was refused: %v", err)
	}
	sitting, ok := cp.ElementIndexOf("sitting")
	if !ok {
		t.Fatal("no subprocess")
	}
	outs := cp.IOOutputs(sitting)
	if len(outs) != 3 {
		t.Fatalf("outputs = %d, want 3", len(outs))
	}
	if !outs[0].Verbatim || !outs[1].Verbatim {
		t.Error("the personal pass-through is not marked to be copied verbatim")
	}
	// A variable nobody declared personal keeps its evaluated mapping: nothing about it
	// changes.
	if outs[2].Verbatim {
		t.Error("a mapping of a variable that is not personal was marked verbatim")
	}
}

func TestEverythingElseAnOutputMappingDoesWithAPersonalValueIsRefused(t *testing.T) {
	cases := []struct {
		name, outputs, want string
	}{
		// The sealed value is bound to its name: under another one it would not open.
		{"renamed into a variable that is not personal", `<zeebe:output source="=iban" target="ziel"/>`, "map it to its own name unchanged"},
		{"renamed into another personal variable", `<zeebe:output source="=iban" target="konto"/>`, "map it to its own name unchanged"},
		{"computed from it", `<zeebe:output source="=upper case(iban)" target="iban"/>`, "writes personal variable"},
		{"computed into it from something else", `<zeebe:output source="=pnr" target="iban"/>`, "writes personal variable"},
		{"combined with another", `<zeebe:output source="=iban + konto" target="iban"/>`, "writes personal variable"},
		{"computed from it into a variable that is not personal", `<zeebe:output source="=upper case(iban)" target="ziel"/>`, "reads personal variable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(1, 1, strings.NewReader(passThroughBPMN(c.outputs)))
			if err == nil {
				t.Fatalf("%s deployed", c.outputs)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %v, want it to say %q", err, c.want)
			}
		})
	}
}

// TestAnInputMappingStillMayNotCopyAPersonalValue: the pass-through is for handing a value
// out, which is what a wizard needs; an input mapping into a personal variable stays
// refused, so a scope cannot be seeded with a copy it then shadows the original with.
func TestAnInputMappingStillMayNotCopyAPersonalValue(t *testing.T) {
	src := strings.Replace(passThroughBPMN(""), `<userTask id="eins"/>`,
		`<userTask id="eins"><extensionElements><zeebe:ioMapping><zeebe:input source="=iban" target="iban"/></zeebe:ioMapping></extensionElements></userTask>`, 1)
	if _, err := Parse(1, 1, strings.NewReader(src)); err == nil || !strings.Contains(err.Error(), "input mapping writes personal variable") {
		t.Errorf("an input mapping copying a personal value: %v, want refused", err)
	}
}
