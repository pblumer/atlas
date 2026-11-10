package dmn

import (
	"context"
	"strings"
	"testing"
)

// unnamedRequirementsXML is a model as a hand or an agent writes it: three input
// data the decision requires through information requirements that carry no id.
// DMN allows that, but a diagram edge points at its requirement by id, so before the
// requirements were named the inputs were drawn with no arrow to the decision.
const unnamedRequirementsXML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="defs" name="rows" namespace="http://atlas/dmn">
  <inputData id="id_email" name="email"><variable name="email" typeRef="string"/></inputData>
  <inputData id="id_group" name="group"><variable name="group" typeRef="string"/></inputData>
  <inputData id="informationRequirement_RowValid_3" name="license"><variable name="license" typeRef="string"/></inputData>
  <decision id="RowValid" name="RowValid">
    <informationRequirement><requiredInput href="#id_email"/></informationRequirement>
    <informationRequirement><requiredInput href="#id_group"/></informationRequirement>
    <informationRequirement><requiredInput href="#informationRequirement_RowValid_3"/></informationRequirement>
    <decisionTable id="dt" hitPolicy="UNIQUE">
      <input id="in_e"><inputExpression typeRef="string"><text>email</text></inputExpression></input>
      <output id="o_v" name="valid" typeRef="boolean"/>
      <rule id="r1"><inputEntry><text>-</text></inputEntry><outputEntry><text>contains(email, "@")</text></outputEntry></rule>
    </decisionTable>
  </decision>
</definitions>`

// Requirements without an id are named when the diagram is drawn, so every input is
// connected to the decision that reads it — and the model still evaluates the same.
func TestUnnamedRequirementsAreDrawn(t *testing.T) {
	out, generated := EnsureDiagramReport([]byte(unnamedRequirementsXML))
	if !generated {
		t.Fatal("nothing generated for a model with no diagram")
	}
	got := string(out)
	if n := countElements(got, "DMNEdge"); n != 3 {
		t.Fatalf("edges = %d, want one per requirement\n%s", n, got)
	}
	for _, id := range []string{
		`id="informationRequirement_RowValid_1"`,
		`id="informationRequirement_RowValid_2"`,
		// The third name is taken by an input datum, so it gets a suffix.
		`id="informationRequirement_RowValid_3_2"`,
	} {
		if !strings.Contains(got, id) {
			t.Errorf("missing %s in\n%s", id, got)
		}
	}
	// Reading it again changes nothing: the names and the diagram are both there.
	if again, regenerated := EnsureDiagramReport(out); regenerated || string(again) != got {
		t.Error("a second read redrew the model")
	}
	reg := NewRegistry()
	if err := reg.Deploy(1, out); err != nil {
		t.Fatalf("the named model does not deploy: %v", err)
	}
	res, err := reg.Evaluate(context.Background(), 1, "RowValid", map[string]any{"email": "a@b.ch", "group": "x", "license": "PRO"})
	if err != nil || res["RowValid"] != true {
		t.Fatalf("evaluation = %v, %v", res, err)
	}
}

// Auto-layout names them too, a prefixed document keeps its prefix, and a model whose
// diagram already places every node is left exactly as it is.
func TestUnnamedRequirementsOnRegenerateAndPrefixes(t *testing.T) {
	prefixed := strings.NewReplacer(
		`<definitions xmlns=`, `<dmn:definitions xmlns:dmn=`, `</definitions>`, `</dmn:definitions>`,
		`<informationRequirement>`, `<dmn:informationRequirement>`, `</informationRequirement>`, `</dmn:informationRequirement>`,
		`<inputData `, `<dmn:inputData `, `</inputData>`, `</dmn:inputData>`,
		`<variable `, `<dmn:variable `, `<decision `, `<dmn:decision `, `</decision>`, `</dmn:decision>`,
		`<requiredInput `, `<dmn:requiredInput `, `<decisionTable `, `<dmn:decisionTable `, `</decisionTable>`, `</dmn:decisionTable>`,
		`<input `, `<dmn:input `, `</input>`, `</dmn:input>`, `<inputExpression `, `<dmn:inputExpression `, `</inputExpression>`, `</dmn:inputExpression>`,
		`<text>`, `<dmn:text>`, `</text>`, `</dmn:text>`, `<output `, `<dmn:output `, `<rule `, `<dmn:rule `, `</rule>`, `</dmn:rule>`,
		`<inputEntry>`, `<dmn:inputEntry>`, `</inputEntry>`, `</dmn:inputEntry>`, `<outputEntry>`, `<dmn:outputEntry>`, `</outputEntry>`, `</dmn:outputEntry>`,
	).Replace(unnamedRequirementsXML)
	got := string(RegenerateDiagram([]byte(prefixed)))
	if !strings.Contains(got, `<dmn:informationRequirement id="informationRequirement_RowValid_1">`) {
		t.Fatalf("prefixed requirement not named:\n%s", got)
	}
	if n := countElements(got, "DMNEdge"); n != 3 {
		t.Fatalf("edges = %d, want 3", n)
	}

	// A diagram that already draws every node is somebody's; reading leaves it alone,
	// requirement names and all.
	drawn := strings.Replace(unnamedRequirementsXML, `</definitions>`, `<dmndi:DMNDI xmlns:dmndi="https://www.omg.org/spec/DMN/20191111/DMNDI/" xmlns:dc="http://www.omg.org/spec/DMN/20180521/DC/">
    <dmndi:DMNDiagram>
      <dmndi:DMNShape dmnElementRef="id_email"><dc:Bounds x="0" y="200" width="125" height="45"/></dmndi:DMNShape>
      <dmndi:DMNShape dmnElementRef="id_group"><dc:Bounds x="150" y="200" width="125" height="45"/></dmndi:DMNShape>
      <dmndi:DMNShape dmnElementRef="informationRequirement_RowValid_3"><dc:Bounds x="300" y="200" width="125" height="45"/></dmndi:DMNShape>
      <dmndi:DMNShape dmnElementRef="RowValid"><dc:Bounds x="100" y="0" width="180" height="80"/></dmndi:DMNShape>
    </dmndi:DMNDiagram>
  </dmndi:DMNDI>
</definitions>`, 1)
	if out, generated := EnsureDiagramReport([]byte(drawn)); generated || string(out) != drawn {
		t.Error("a fully drawn model was changed on read")
	}

	// Something that is not XML comes back as it was.
	if out, changed := nameRequirements([]byte("<definitions><decision")); changed || string(out) != "<definitions><decision" {
		t.Error("broken XML was rewritten")
	}
}
