package dmn

import (
	"encoding/xml"
	"strings"
	"testing"
)

// What a DMN model's diagram is for, and what happens when it is missing or half
// there (ADR-draft-dmn-diagram-is-completed-on-read). Every test here drives the
// exported entry points over real DMN XML — the bytes a model actually arrives as.

// drgXML is one input datum feeding one decision, with no diagram at all: the
// shape of every model an agent, temis or a hand uploads.
const drgXML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="defs" name="Eligibility" namespace="http://atlas/dmn">
  <inputData id="id_amount" name="amount"/>
  <decision id="eligibility" name="eligibility">
    <informationRequirement id="ir1"><requiredInput href="#id_amount"/></informationRequirement>
    <decisionTable id="dt" hitPolicy="UNIQUE">
      <input id="in1"><inputExpression id="ie1" typeRef="number"><text>amount</text></inputExpression></input>
      <output id="out1" name="eligibility" typeRef="string"/>
      <rule id="r1"><inputEntry id="e1"><text>&gt;= 100</text></inputEntry><outputEntry id="o1"><text>"approve"</text></outputEntry></rule>
    </decisionTable>
  </decision>
</definitions>`

// shapeBounds is one drawn element's position, read back out of the generated
// diagram so a test asserts the picture rather than the generator's internals.
type shapeBounds struct{ x, y float64 }

// boundsOf maps each drawn element id to where the generated diagram put it.
func boundsOf(t *testing.T, out string) map[string]shapeBounds {
	t.Helper()
	var doc struct {
		DI struct {
			Diagrams []struct {
				Shapes []struct {
					Ref    string `xml:"dmnElementRef,attr"`
					Bounds struct {
						X float64 `xml:"x,attr"`
						Y float64 `xml:"y,attr"`
					} `xml:"Bounds"`
				} `xml:"DMNShape"`
			} `xml:"DMNDiagram"`
		} `xml:"DMNDI"`
	}
	if err := xml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("read back the generated diagram: %v\n%s", err, out)
	}
	at := map[string]shapeBounds{}
	for _, d := range doc.DI.Diagrams {
		for _, sh := range d.Shapes {
			at[sh.Ref] = shapeBounds{sh.Bounds.X, sh.Bounds.Y}
		}
	}
	return at
}

// countElements is a crude but honest check on generated DI: how many times a tag
// name occurs. The generator is the only thing writing these, so counting them
// answers "did every element get drawn".
func countElements(xml, tag string) int {
	return strings.Count(xml, "<dmndi:"+tag)
}

// A model with no diagram gets a whole one: a shape for every node, an edge for
// every requirement, and the DMN namespaces bound so dmn-js can resolve them.
func TestADiagramlessModelIsDrawn(t *testing.T) {
	out, generated := EnsureDiagramReport([]byte(drgXML))
	if !generated {
		t.Fatal("EnsureDiagramReport reported nothing generated for a model with no DMNDI")
	}
	got := string(out)
	if countElements(got, "DMNShape") != 2 {
		t.Errorf("shapes = %d, want one for the input data and one for the decision:\n%s", countElements(got, "DMNShape"), got)
	}
	if countElements(got, "DMNEdge") != 1 {
		t.Errorf("edges = %d, want one for the information requirement", countElements(got, "DMNEdge"))
	}
	for _, want := range []string{
		`dmnElementRef="id_amount"`, `dmnElementRef="eligibility"`, `dmnElementRef="ir1"`,
		nsDMNDI, nsDC, nsDI,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated diagram does not contain %q:\n%s", want, got)
		}
	}
	// The semantic model is untouched: only the diagram is added.
	if !strings.Contains(got, `<rule id="r1">`) || !strings.Contains(got, `hitPolicy="UNIQUE"`) {
		t.Errorf("the semantic model was altered:\n%s", got)
	}
}

// Information flows upward in a DRG: what a decision requires sits below it. The
// picture has to say so, or the arrows read backwards.
func TestWhatADecisionRequiresIsPlacedBelowIt(t *testing.T) {
	out := EnsureDiagram([]byte(drgXML))
	at := boundsOf(t, string(out))
	if at["eligibility"].y >= at["id_amount"].y {
		t.Fatalf("decision at y=%v, input data at y=%v — what is required must sit below what requires it",
			at["eligibility"].y, at["id_amount"].y)
	}
}

// A diagram somebody placed is never moved. That is the whole difference between
// Ensure and the Auto-layout button.
func TestAnAuthoredDiagramIsLeftAlone(t *testing.T) {
	authored := strings.Replace(drgXML, "</definitions>", `  <dmndi:DMNDI xmlns:dmndi="https://www.omg.org/spec/DMN/20191111/DMNDI/" xmlns:dc="http://www.omg.org/spec/DMN/20180521/DC/">
    <dmndi:DMNDiagram id="D1">
      <dmndi:DMNShape id="s1" dmnElementRef="id_amount"><dc:Bounds x="900" y="900" width="125" height="45"/></dmndi:DMNShape>
      <dmndi:DMNShape id="s2" dmnElementRef="eligibility"><dc:Bounds x="700" y="700" width="180" height="80"/></dmndi:DMNShape>
    </dmndi:DMNDiagram>
  </dmndi:DMNDI>
</definitions>`, 1)

	out, generated := EnsureDiagramReport([]byte(authored))
	if generated {
		t.Error("a complete diagram was regenerated; an author's arrangement must survive a read")
	}
	if string(out) != authored {
		t.Error("a complete diagram came back changed")
	}
}

// The defect this record exists for: dmn-js draws what it can and saves back a
// diagram covering only that, so the model comes out of one visit worse than it
// went in. A partial diagram is therefore laid out afresh rather than completed
// around coordinates no person chose.
func TestAHalfDrawnDiagramIsLaidOutAfresh(t *testing.T) {
	partial := strings.Replace(drgXML, "</definitions>", `  <dmndi:DMNDI xmlns:dmndi="https://www.omg.org/spec/DMN/20191111/DMNDI/" xmlns:dc="http://www.omg.org/spec/DMN/20180521/DC/">
    <dmndi:DMNDiagram id="D1">
      <dmndi:DMNShape id="s2" dmnElementRef="eligibility"><dc:Bounds x="150" y="150" width="180" height="80"/></dmndi:DMNShape>
    </dmndi:DMNDiagram>
  </dmndi:DMNDI>
</definitions>`, 1)

	out, generated := EnsureDiagramReport([]byte(partial))
	if !generated {
		t.Fatal("a diagram covering one of two nodes was accepted as complete")
	}
	got := string(out)
	if countElements(got, "DMNShape") != 2 || countElements(got, "DMNEdge") != 1 {
		t.Fatalf("shapes=%d edges=%d, want the whole graph drawn:\n%s",
			countElements(got, "DMNShape"), countElements(got, "DMNEdge"), got)
	}
	if strings.Count(got, `dmnElementRef="eligibility"`) != 1 {
		t.Errorf("the old shape was left behind beside the new one:\n%s", got)
	}
}

// Auto-layout is the author asking for the whole graph to be re-flowed, so it
// discards what is there — including a complete diagram Ensure would have kept.
func TestRegenerateReplacesEvenACompleteDiagram(t *testing.T) {
	authored := strings.Replace(drgXML, "</definitions>", `  <dmndi:DMNDI xmlns:dmndi="https://www.omg.org/spec/DMN/20191111/DMNDI/" xmlns:dc="http://www.omg.org/spec/DMN/20180521/DC/">
    <dmndi:DMNDiagram id="D1">
      <dmndi:DMNShape id="s1" dmnElementRef="id_amount"><dc:Bounds x="900" y="900" width="125" height="45"/></dmndi:DMNShape>
      <dmndi:DMNShape id="s2" dmnElementRef="eligibility"><dc:Bounds x="700" y="700" width="180" height="80"/></dmndi:DMNShape>
    </dmndi:DMNDiagram>
  </dmndi:DMNDI>
</definitions>`, 1)

	got := string(RegenerateDiagram([]byte(authored)))
	if strings.Contains(got, `x="900"`) || strings.Contains(got, `x="700"`) {
		t.Errorf("Regenerate kept the old coordinates:\n%s", got)
	}
	if countElements(got, "DMNShape") != 2 || countElements(got, "DMNEdge") != 1 {
		t.Errorf("shapes=%d edges=%d, want the whole graph re-drawn", countElements(got, "DMNShape"), countElements(got, "DMNEdge"))
	}
}

// A decision that requires another decision is one layer higher again, so a chain
// reads bottom-to-top rather than piling into one row.
func TestAChainOfDecisionsIsLayered(t *testing.T) {
	chain := `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="defs" name="Chain" namespace="http://atlas/dmn">
  <inputData id="amount" name="amount"/>
  <decision id="risk" name="risk">
    <informationRequirement id="r1"><requiredInput href="#amount"/></informationRequirement>
    <literalExpression id="le1"><text>1</text></literalExpression>
  </decision>
  <decision id="verdict" name="verdict">
    <informationRequirement id="r2"><requiredDecision href="#risk"/></informationRequirement>
    <literalExpression id="le2"><text>2</text></literalExpression>
  </decision>
</definitions>`
	at := boundsOf(t, string(EnsureDiagram([]byte(chain))))
	if !(at["verdict"].y < at["risk"].y && at["risk"].y < at["amount"].y) {
		t.Fatalf("layers = verdict %v, risk %v, amount %v — want each requirement below what needs it",
			at["verdict"].y, at["risk"].y, at["amount"].y)
	}
}

// Nodes sharing a layer are spread sideways rather than stacked on one another,
// or the picture is a single box with the rest hidden behind it.
func TestNodesInOneLayerDoNotOverlap(t *testing.T) {
	wide := `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="defs" name="Wide" namespace="http://atlas/dmn">
  <inputData id="a" name="a"/>
  <inputData id="b" name="b"/>
  <inputData id="c" name="c"/>
  <decision id="d" name="d">
    <informationRequirement id="r1"><requiredInput href="#a"/></informationRequirement>
    <informationRequirement id="r2"><requiredInput href="#b"/></informationRequirement>
    <informationRequirement id="r3"><requiredInput href="#c"/></informationRequirement>
    <literalExpression id="le"><text>1</text></literalExpression>
  </decision>
</definitions>`
	at := boundsOf(t, string(EnsureDiagram([]byte(wide))))
	seen := map[float64]string{}
	for _, id := range []string{"a", "b", "c"} {
		if other, ok := seen[at[id].x]; ok {
			t.Fatalf("%q and %q are both at x=%v", id, other, at[id].x)
		}
		seen[at[id].x] = id
	}
	if at["a"].y != at["b"].y || at["b"].y != at["c"].y {
		t.Errorf("three unrequiring inputs are not on one row: %v %v %v", at["a"].y, at["b"].y, at["c"].y)
	}
}

// A requirement cycle is not a legal DRG, but a file on disk can hold one. The
// layouter must terminate and still draw every node rather than hang or panic.
func TestACycleStillTerminates(t *testing.T) {
	cyclic := `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="defs" name="Cycle" namespace="http://atlas/dmn">
  <decision id="a" name="a"><informationRequirement id="r1"><requiredDecision href="#b"/></informationRequirement></decision>
  <decision id="b" name="b"><informationRequirement id="r2"><requiredDecision href="#a"/></informationRequirement></decision>
</definitions>`
	got := string(EnsureDiagram([]byte(cyclic)))
	if countElements(got, "DMNShape") != 2 {
		t.Fatalf("shapes = %d, want both decisions drawn even though they require each other", countElements(got, "DMNShape"))
	}
}

// Best-effort, like the BPMN generator: what cannot be laid out comes back
// unchanged rather than mangled or refused.
func TestWhatCannotBeLaidOutComesBackUnchanged(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"not xml at all", "<definitions"},
		{"no nodes to draw", `<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="d"></definitions>`},
		{"no closing tag to inject before", `<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/"><decision id="a"/>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(EnsureDiagram([]byte(tc.src))); got != tc.src {
				t.Fatalf("EnsureDiagram = %q, want it unchanged", got)
			}
			if got := string(RegenerateDiagram([]byte(tc.src))); got != tc.src {
				t.Fatalf("RegenerateDiagram = %q, want it unchanged", got)
			}
		})
	}
}

// The same bytes in must give the same bytes out: Ensure runs on every read, so a
// wobbling layout would be a diff on every fetch and a flaky test everywhere.
func TestTheLayoutIsDeterministic(t *testing.T) {
	first := string(EnsureDiagram([]byte(drgXML)))
	for i := 0; i < 5; i++ {
		if got := string(EnsureDiagram([]byte(drgXML))); got != first {
			t.Fatalf("run %d differs from the first", i+1)
		}
	}
}
