package dmn

import (
	"encoding/xml"
	"strings"
	"testing"
)

// What a DMN model's diagram is for, and what happens when it is missing or half
// there (ADR-0325). Every test here drives the
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
type shapeBounds struct{ x, y, w, h float64 }

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
						W float64 `xml:"width,attr"`
						H float64 `xml:"height,attr"`
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
			at[sh.Ref] = shapeBounds{sh.Bounds.X, sh.Bounds.Y, sh.Bounds.W, sh.Bounds.H}
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
		nsDMNDI13, nsDC, nsDI,
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

// A DMN file carries two independent namespaces — MODEL for the logic and DMNDI for
// the picture — and dmn-js binds them together: opened as DMN 1.3 it reads DMNDI
// from .../20191111/DMNDI/, opened as 1.5 from .../20230324/DMNDI/. So a diagram
// generated in the 1.3 namespace and attached to a 1.5 model is a diagram the
// editor will not see: the model opens and its layout is silently gone.
//
// Measured on the tree that found this: the whole table of DMN models on one
// installation was 1.3, and the single 1.5 model was the one an author could not
// open (#994).
func TestTheGeneratedDiagramUsesTheModelsOwnDMNDINamespace(t *testing.T) {
	const ns15 = "https://www.omg.org/spec/DMN/20230324/DMNDI/"
	for _, tc := range []struct {
		name  string
		model string // the MODEL namespace the source declares
		want  string // the DMNDI namespace the generated diagram must use
	}{
		{"DMN 1.3", "https://www.omg.org/spec/DMN/20191111/MODEL/", nsDMNDI13},
		{"DMN 1.5", "https://www.omg.org/spec/DMN/20230324/MODEL/", ns15},
		// Anything else keeps the behaviour every stored model was written under.
		{"an unknown namespace falls back", "http://example.invalid/dmn", nsDMNDI13},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := strings.Replace(drgXML, "https://www.omg.org/spec/DMN/20191111/MODEL/", tc.model, 1)
			got := string(EnsureDiagram([]byte(src)))
			if !strings.Contains(got, `xmlns:dmndi="`+tc.want+`"`) {
				t.Errorf("generated diagram does not declare dmndi=%q:\n%s", tc.want, got)
			}
			// The two DMNDI namespaces must not both appear: one of them would be the
			// one dmn-js ignores.
			other := nsDMNDI13
			if tc.want == nsDMNDI13 {
				other = ns15
			}
			if strings.Contains(got, other) {
				t.Errorf("generated diagram also declares the wrong dmndi namespace %q:\n%s", other, got)
			}
			// DC and DI are version-independent — the vendored dmn-js binds exactly one
			// URI for each — so they must not move with the model's version.
			for _, want := range []string{nsDC, nsDI} {
				if !strings.Contains(got, want) {
					t.Errorf("generated diagram does not declare %q:\n%s", want, got)
				}
			}
		})
	}
}

// RegenerateDiagram is the author-triggered Auto-layout, and it writes the same
// block Ensure does — so it has to make the same namespace choice, or using
// Auto-layout on a 1.5 model would be the way to lose its diagram.
func TestAutoLayoutMakesTheSameNamespaceChoice(t *testing.T) {
	src := strings.Replace(drgXML,
		"https://www.omg.org/spec/DMN/20191111/MODEL/",
		"https://www.omg.org/spec/DMN/20230324/MODEL/", 1)
	got := string(RegenerateDiagram([]byte(src)))
	if !strings.Contains(got, `xmlns:dmndi="https://www.omg.org/spec/DMN/20230324/DMNDI/"`) {
		t.Errorf("Auto-layout on a DMN 1.5 model did not use the 1.5 DMNDI namespace:\n%s", got)
	}
}

// --- decision services (ADR-0398) ---

// serviceLayoutXML is the shape of a published decision service: one output decision
// above the divider, one encapsulated decision below it, and an input decision
// that DMN draws OUTSIDE the box because the caller supplies it. No diagram.
const serviceLayoutXML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="defs_svc" name="Approval" namespace="http://atlas/dmn/svc">
  <inputData id="id_amount" name="amount"/>
  <decision id="boundary" name="Boundary">
    <informationRequirement id="ir_boundary"><requiredInput href="#id_amount"/></informationRequirement>
  </decision>
  <decision id="inner" name="Inner">
    <informationRequirement id="ir_inner"><requiredInput href="#id_amount"/></informationRequirement>
  </decision>
  <decision id="outer" name="Outer">
    <informationRequirement id="ir_outer_1"><requiredDecision href="#inner"/></informationRequirement>
    <informationRequirement id="ir_outer_2"><requiredDecision href="#boundary"/></informationRequirement>
  </decision>
  <decisionService id="svc" name="Approval Service">
    <outputDecision href="#outer"/>
    <encapsulatedDecision href="#inner"/>
    <inputDecision href="#boundary"/>
  </decisionService>
</definitions>`

// dividerY reads back the single divider line of the generated service box.
func dividerY(t *testing.T, out string) float64 {
	t.Helper()
	var doc struct {
		DI struct {
			Diagrams []struct {
				Shapes []struct {
					Ref     string `xml:"dmnElementRef,attr"`
					Divider struct {
						Waypoints []struct {
							X float64 `xml:"x,attr"`
							Y float64 `xml:"y,attr"`
						} `xml:"waypoint"`
					} `xml:"DMNDecisionServiceDividerLine"`
				} `xml:"DMNShape"`
			} `xml:"DMNDiagram"`
		} `xml:"DMNDI"`
	}
	if err := xml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("read back the generated diagram: %v\n%s", err, out)
	}
	for _, d := range doc.DI.Diagrams {
		for _, sh := range d.Shapes {
			if sh.Ref != "svc" {
				continue
			}
			if len(sh.Divider.Waypoints) != 2 {
				t.Fatalf("divider has %d waypoints, want 2\n%s", len(sh.Divider.Waypoints), out)
			}
			return sh.Divider.Waypoints[0].Y
		}
	}
	t.Fatalf("no shape for the decision service\n%s", out)
	return 0
}

// A decision service is drawn, as a box with a divider line. Without a shape of its
// own dmn-js invents an empty one, and because it rereads membership from geometry,
// the next save writes that emptiness into the document.
func TestADecisionServiceIsDrawnWithItsDivider(t *testing.T) {
	out := string(EnsureDiagram([]byte(serviceLayoutXML)))

	at := boundsOf(t, out)
	box, ok := at["svc"]
	if !ok {
		t.Fatalf("the decision service got no shape\n%s", out)
	}
	if box.w <= 0 || box.h <= 0 {
		t.Fatalf("service box has no size: %+v", box)
	}
	if n := countElements(out, "DMNDecisionServiceDividerLine"); n != 1 {
		t.Fatalf("got %d divider lines, want 1\n%s", n, out)
	}
}

// The box holds what the service declares and nothing else: its output and
// encapsulated decisions are inside it, the input decision it names as its boundary
// is not.
func TestTheServiceBoxHoldsItsMembersAndNotTheBoundary(t *testing.T) {
	out := string(EnsureDiagram([]byte(serviceLayoutXML)))
	at := boundsOf(t, out)
	box := at["svc"]

	inside := func(b shapeBounds) bool {
		return b.x >= box.x && b.y >= box.y &&
			b.x+b.w <= box.x+box.w && b.y+b.h <= box.y+box.h
	}
	for _, id := range []string{"outer", "inner"} {
		if !inside(at[id]) {
			t.Errorf("%s at %+v is not inside the service box %+v", id, at[id], box)
		}
	}
	if inside(at["boundary"]) {
		t.Errorf("the input decision at %+v was drawn inside the service box %+v",
			at["boundary"], box)
	}
}

// The divider separates the compartments the document declares: the output decision
// above it, the encapsulated one below. dmn-js classifies a member by which side of
// this line it is on, so a picture that got this backwards would rewrite the model.
func TestTheDividerSeparatesOutputFromEncapsulated(t *testing.T) {
	out := string(EnsureDiagram([]byte(serviceLayoutXML)))
	at := boundsOf(t, out)
	y := dividerY(t, out)

	if mid := at["outer"].y + at["outer"].h/2; mid >= y {
		t.Errorf("the output decision's centre %g is not above the divider %g", mid, y)
	}
	if mid := at["inner"].y + at["inner"].h/2; mid <= y {
		t.Errorf("the encapsulated decision's centre %g is not below the divider %g", mid, y)
	}
}

// The declared membership wins over the requirements graph. Here the DRG alone
// would stack them the other way round: the encapsulated decision requires the
// output one, so layering by depth would put it higher.
func TestTheDividerFollowsTheDocumentNotTheGraph(t *testing.T) {
	const invertedXML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="defs_inv" name="Inverted" namespace="http://atlas/dmn/inv">
  <inputData id="id_amount" name="amount"/>
  <decision id="outer" name="Outer">
    <informationRequirement id="ir_outer"><requiredInput href="#id_amount"/></informationRequirement>
  </decision>
  <decision id="inner" name="Inner">
    <informationRequirement id="ir_inner"><requiredDecision href="#outer"/></informationRequirement>
  </decision>
  <decisionService id="svc" name="Inverted Service">
    <outputDecision href="#outer"/>
    <encapsulatedDecision href="#inner"/>
  </decisionService>
</definitions>`

	out := string(EnsureDiagram([]byte(invertedXML)))
	at := boundsOf(t, out)
	y := dividerY(t, out)

	if mid := at["outer"].y + at["outer"].h/2; mid >= y {
		t.Errorf("the output decision's centre %g is not above the divider %g\n%s", mid, y, out)
	}
	if mid := at["inner"].y + at["inner"].h/2; mid <= y {
		t.Errorf("the encapsulated decision's centre %g is not below the divider %g\n%s", mid, y, out)
	}
}

// The service is written before the decisions it holds. A viewer that does not
// treat the box as a container paints in document order, so a service written last
// covers its own contents.
func TestTheServiceIsDrawnBeforeWhatItHolds(t *testing.T) {
	out := string(EnsureDiagram([]byte(serviceLayoutXML)))

	svc := strings.Index(out, `dmnElementRef="svc"`)
	outer := strings.Index(out, `dmnElementRef="outer"`)
	inner := strings.Index(out, `dmnElementRef="inner"`)
	if svc < 0 || outer < 0 || inner < 0 {
		t.Fatalf("not every element was drawn\n%s", out)
	}
	if svc > outer || svc > inner {
		t.Errorf("the service box is written after what it holds (svc %d, outer %d, inner %d)",
			svc, outer, inner)
	}
}

// A diagram that draws every decision but not the service is not a human
// arrangement to preserve — it is what a tool that predates DMN 1.5 leaves behind.
// It is laid out afresh rather than handed to dmn-js to complete.
func TestADiagramMissingOnlyTheServiceIsLaidOutAfresh(t *testing.T) {
	const partialXML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" xmlns:dmndi="https://www.omg.org/spec/DMN/20230324/DMNDI/" xmlns:dc="http://www.omg.org/spec/DMN/20180521/DC/" id="defs_part" name="Partial" namespace="http://atlas/dmn/part">
  <inputData id="id_amount" name="amount"/>
  <decision id="outer" name="Outer">
    <informationRequirement id="ir_outer"><requiredInput href="#id_amount"/></informationRequirement>
  </decision>
  <decisionService id="svc" name="Partial Service">
    <outputDecision href="#outer"/>
  </decisionService>
  <dmndi:DMNDI>
    <dmndi:DMNDiagram id="D">
      <dmndi:DMNShape id="S1" dmnElementRef="id_amount"><dc:Bounds x="10" y="200" width="125" height="45"/></dmndi:DMNShape>
      <dmndi:DMNShape id="S2" dmnElementRef="outer"><dc:Bounds x="10" y="60" width="180" height="80"/></dmndi:DMNShape>
    </dmndi:DMNDiagram>
  </dmndi:DMNDI>
</definitions>`

	out, generated := EnsureDiagramReport([]byte(partialXML))
	if !generated {
		t.Fatal("a diagram that does not draw the decision service was left as it was")
	}
	if _, ok := boundsOf(t, string(out))["svc"]; !ok {
		t.Errorf("the redrawn diagram still has no shape for the service\n%s", out)
	}
}

// A diagram that draws the service too is somebody's arrangement, and is left
// alone. This is the guard on the rule above: completing a diagram must not become
// an excuse to move one.
func TestADiagramThatDrawsTheServiceIsLeftAlone(t *testing.T) {
	const completeXML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" xmlns:dmndi="https://www.omg.org/spec/DMN/20230324/DMNDI/" xmlns:dc="http://www.omg.org/spec/DMN/20180521/DC/" xmlns:di="http://www.omg.org/spec/DMN/20180521/DI/" id="defs_full" name="Complete" namespace="http://atlas/dmn/full">
  <inputData id="id_amount" name="amount"/>
  <decision id="outer" name="Outer">
    <informationRequirement id="ir_outer"><requiredInput href="#id_amount"/></informationRequirement>
  </decision>
  <decisionService id="svc" name="Complete Service">
    <outputDecision href="#outer"/>
  </decisionService>
  <dmndi:DMNDI>
    <dmndi:DMNDiagram id="D">
      <dmndi:DMNShape id="S0" dmnElementRef="svc">
        <dc:Bounds x="500" y="500" width="300" height="200"/>
        <dmndi:DMNDecisionServiceDividerLine><di:waypoint x="500" y="600"/><di:waypoint x="800" y="600"/></dmndi:DMNDecisionServiceDividerLine>
      </dmndi:DMNShape>
      <dmndi:DMNShape id="S1" dmnElementRef="id_amount"><dc:Bounds x="10" y="200" width="125" height="45"/></dmndi:DMNShape>
      <dmndi:DMNShape id="S2" dmnElementRef="outer"><dc:Bounds x="520" y="520" width="180" height="80"/></dmndi:DMNShape>
    </dmndi:DMNDiagram>
  </dmndi:DMNDI>
</definitions>`

	out, generated := EnsureDiagramReport([]byte(completeXML))
	if generated {
		t.Error("a complete diagram was redrawn")
	}
	if string(out) != completeXML {
		t.Error("a complete diagram came back changed")
	}
}

// A service naming nothing this model declares still gets a box. Without one
// EnsureDiagram would find the diagram incomplete on every read and redraw the
// model each time it is opened.
func TestAServiceWithNothingToHoldStillGetsABox(t *testing.T) {
	const emptyXML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="defs_empty" name="Empty" namespace="http://atlas/dmn/empty">
  <inputData id="id_amount" name="amount"/>
  <decision id="outer" name="Outer">
    <informationRequirement id="ir_outer"><requiredInput href="#id_amount"/></informationRequirement>
  </decision>
  <decisionService id="svc" name="Empty Service"/>
</definitions>`

	once, generated := EnsureDiagramReport([]byte(emptyXML))
	if !generated {
		t.Fatal("a model with no diagram was not drawn")
	}
	if _, ok := boundsOf(t, string(once))["svc"]; !ok {
		t.Fatalf("the empty service got no shape\n%s", once)
	}
	if _, again := EnsureDiagramReport(once); again {
		t.Error("reading the drawn model back redrew it again")
	}
}

// A decision named in both compartments has no side of the line to be on. The box
// is still drawn and the divider still lands inside it, because a service whose
// shape is missing or malformed is what sends dmn-js back to inventing one.
func TestAContradictoryServiceStillGetsAUsableBox(t *testing.T) {
	const bothXML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="defs_both" name="Both" namespace="http://atlas/dmn/both">
  <inputData id="id_amount" name="amount"/>
  <decision id="only" name="Only">
    <informationRequirement id="ir_only"><requiredInput href="#id_amount"/></informationRequirement>
  </decision>
  <decisionService id="svc" name="Contradictory Service">
    <outputDecision href="#only"/>
    <encapsulatedDecision href="#only"/>
  </decisionService>
</definitions>`

	out := string(EnsureDiagram([]byte(bothXML)))
	box, ok := boundsOf(t, out)["svc"]
	if !ok {
		t.Fatalf("the service got no shape\n%s", out)
	}
	y := dividerY(t, out)
	if y <= box.y || y >= box.y+box.h {
		t.Errorf("divider at %g is outside the box %+v", y, box)
	}
}
