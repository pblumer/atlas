package layout

import (
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The diagrams Atlas ships are held to the same invariants as the generator.
//
// The platform processes under api/systemprocesses and the examples the handbook
// and the Modeler's example gallery hand out (api/web/examples-catalog.json) carry
// hand-authored BPMN-DI, and nothing ever looked at it: they compile, they deploy,
// and the first person to see one is whoever opens it in the Modeler. That is how
// the three approval models shipped with their parallel gateway drawn on top of
// the approval task, both deadlines' captions printed over each other, and in one
// of them the deadlines riding a different task than the one they are attached
// to — and how an example shipped with a flow that ends in empty space a hand's
// width before the task it leads to. A model that renders like that is not done
// (AGENTS.md, "Authoring BPMN models").
//
// Hand-authored DI owes a few things the generator gets for free, so it is
// checked for them too: every edge has to touch the shapes it connects, a
// boundary event has to sit on its own host, and every named event and gateway
// needs an explicit caption position — without one bpmn-js parks the caption
// under the shape, where the label checks cannot see it.
//
// Two of the generator's invariants are replaced rather than applied. The trunk
// invariant states how the generator chooses a main axis, and a person drawing a
// model may centre a branch differently and still draw it legibly. And the
// generator's left-to-right rule compares the left edges of source and target,
// which a column of equal-width nodes satisfies but a hand-drawn drop from a
// gateway to the wider task beneath it does not, though nothing in it runs
// backwards; here the rule is read off the edge itself — a forward flow never
// steps left.
var shippedModelDirs = []string{"../systemprocesses", "../../examples"}

func TestShippedDiagramsAreReadable(t *testing.T) {
	for _, p := range shippedModels(t) {
		t.Run(strings.TrimLeft(filepath.ToSlash(p), "./"), func(t *testing.T) {
			src, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			h := newHandAuthoredModel(t, string(src))
			if len(h.shapes) == 0 {
				t.Fatal("the model carries no diagram, so whoever opens it sees whatever the generator " +
					"makes of it; a shipped model is laid out by hand (AGENTS.md)")
			}
			m := h.layoutModel
			m.checkCompleteness(t)
			m.checkNoShapeOverlap(t)
			m.checkEdgesOrthogonal(t)
			m.checkNoEdgeThroughShape(t)
			m.checkLabelsClear(t)
			m.checkNamedFlowsLabelled(t)
			m.checkGatewayBranchesLeaveSeparately(t)
			for _, v := range h.violations() {
				t.Error(v)
			}
			if cross, overlap := m.score(); cross > 0 || overlap > 0 {
				t.Errorf("score: %d edge crossing(s) and %d edge pair(s) drawn on top of one another; "+
					"a hand-drawn model has room for neither", cross, overlap)
			}
		})
	}
}

// shippedModels lists every .bpmn under shippedModelDirs, at any depth. A
// directory that yields none fails: a test that silently checks nothing because
// a tree moved is a test that passes for the wrong reason.
func shippedModels(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, dir := range shippedModelDirs {
		n := len(out)
		err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(p, ".bpmn") {
				out = append(out, p)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
		if len(out) == n {
			t.Fatalf("no models under %s — this test would pass on an empty set", dir)
		}
	}
	return out
}

// TestShippedDiagramChecksBite: each check added for hand-authored DI, run on the
// defect it exists for. A check that never fails passes for the wrong reason.
func TestShippedDiagramChecksBite(t *testing.T) {
	const src = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:dc="http://www.omg.org/spec/DD/20100524/DC"
  xmlns:di="http://www.omg.org/spec/DD/20100524/DI">
  <bpmn:process id="P">
    <bpmn:startEvent id="Start" name="Los"/>
    <bpmn:userTask id="A"/>
    <bpmn:userTask id="B"/>
    <bpmn:userTask id="C"/>
    <bpmn:boundaryEvent id="T" name="Frist" attachedToRef="A"/>
    <bpmn:sequenceFlow id="f1" sourceRef="Start" targetRef="A"/>
    <bpmn:sequenceFlow id="f2" sourceRef="A" targetRef="B"/>
    <bpmn:sequenceFlow id="f3" sourceRef="B" targetRef="C"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="D">
    <bpmndi:BPMNPlane id="Pl" bpmnElement="P">
      <bpmndi:BPMNShape id="S_Start" bpmnElement="Start"><dc:Bounds x="100" y="100" width="36" height="36" /></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="S_A" bpmnElement="A"><dc:Bounds x="200" y="78" width="100" height="80" /></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="S_B" bpmnElement="B"><dc:Bounds x="400" y="78" width="100" height="80" /></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="S_C" bpmnElement="C"><dc:Bounds x="380" y="250" width="100" height="80" /></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="S_T" bpmnElement="T"><dc:Bounds x="432" y="140" width="36" height="36" /></bpmndi:BPMNShape>
      <bpmndi:BPMNEdge id="E1" bpmnElement="f1"><di:waypoint x="136" y="118" /><di:waypoint x="250" y="118" /></bpmndi:BPMNEdge>
      <bpmndi:BPMNEdge id="E2" bpmnElement="f2"><di:waypoint x="300" y="118" /><di:waypoint x="400" y="118" /></bpmndi:BPMNEdge>
      <bpmndi:BPMNEdge id="E3" bpmnElement="f3"><di:waypoint x="450" y="158" /><di:waypoint x="450" y="200" /><di:waypoint x="430" y="200" /><di:waypoint x="430" y="250" /></bpmndi:BPMNEdge>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`
	h := newHandAuthoredModel(t, src)
	for _, tc := range []struct {
		name  string
		found []string
		want  string
	}{
		{"edge-attached", h.unattachedEdges(), `edge "f1" ends at (250,118)`},
		{"boundary-on-host", h.boundaryEventsOffHost(), `boundary event "T"`},
		{"node-label", h.unlabelledNamedNodes(), `"Start" ("Los")`},
		{"left-to-right", h.flowsSteppingLeft(), `flow "f3" steps left`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := strings.Join(tc.found, "\n"); !strings.Contains(got, tc.want) {
				t.Errorf("the check did not report the defect it exists for; want a finding containing %q, got:\n%s",
					tc.want, got)
			}
		})
	}
}

// --- hand-authored DI ---

// newHandAuthoredModel reads a model whose DI was written by a person or by
// bpmn-js rather than by generateDI. The regex parsers the generator's tests use
// match the generator's own spelling (omgdc:, no space before "/>"), which a
// hand-written file does not share, so the geometry is read with encoding/xml and
// matched on local names, whatever prefix the file binds.
func newHandAuthoredModel(t *testing.T, src string) *handModel {
	t.Helper()
	m := newLayoutModel(t, src, "")
	m.di = src
	var defs diDefinitions
	if err := xml.Unmarshal([]byte(src), &defs); err != nil {
		t.Fatalf("DI does not parse: %v", err)
	}
	for _, d := range defs.Diagrams {
		for _, pl := range d.Planes {
			for _, s := range pl.Shapes {
				m.shapes[s.Element] = shapeBox{
					x: round(s.Bounds.X), y: round(s.Bounds.Y), w: round(s.Bounds.W), h: round(s.Bounds.H),
					expanded:   s.Expanded == "true",
					horizontal: s.Horizontal == "true",
				}
				if s.Label != nil {
					m.labels[s.Element] = s.Label.Bounds.box()
				}
			}
			for _, e := range pl.Edges {
				pts := make([]point, 0, len(e.Waypoints))
				for _, wp := range e.Waypoints {
					pts = append(pts, point{round(wp.X), round(wp.Y)})
				}
				m.edges[e.Element] = pts
				if e.Label != nil {
					m.labels[e.Element] = e.Label.Bounds.box()
				}
			}
		}
	}
	kinds, names := nodeKindsAndNames(t, src)
	return &handModel{layoutModel: m, kind: kinds, names: names}
}

// handModel is a layoutModel plus what the hand-authored checks need on top: the
// outline each node is drawn with, and the names of the nodes whose caption sits
// outside the shape.
type handModel struct {
	*layoutModel
	kind  map[string]int    // flow node id -> outline class
	names map[string]string // event or gateway id -> its name, where it has one
}

// violations runs the hand-authored checks. They return their findings rather
// than report them, so TestShippedDiagramChecksBite can run each against the
// defect it exists for.
func (h *handModel) violations() []string {
	var out []string
	out = append(out, h.unattachedEdges()...)
	out = append(out, h.boundaryEventsOffHost()...)
	out = append(out, h.unlabelledNamedNodes()...)
	out = append(out, h.flowsSteppingLeft()...)
	return out
}

type diDefinitions struct {
	Diagrams []struct {
		Planes []struct {
			Shapes []struct {
				Element    string   `xml:"bpmnElement,attr"`
				Expanded   string   `xml:"isExpanded,attr"`
				Horizontal string   `xml:"isHorizontal,attr"`
				Bounds     diBounds `xml:"Bounds"`
				Label      *diLabel `xml:"BPMNLabel"`
			} `xml:"BPMNShape"`
			Edges []struct {
				Element   string `xml:"bpmnElement,attr"`
				Waypoints []struct {
					X float64 `xml:"x,attr"`
					Y float64 `xml:"y,attr"`
				} `xml:"waypoint"`
				Label *diLabel `xml:"BPMNLabel"`
			} `xml:"BPMNEdge"`
		} `xml:"BPMNPlane"`
	} `xml:"BPMNDiagram"`
}

type diLabel struct {
	Bounds diBounds `xml:"Bounds"`
}

type diBounds struct {
	X float64 `xml:"x,attr"`
	Y float64 `xml:"y,attr"`
	W float64 `xml:"width,attr"`
	H float64 `xml:"height,attr"`
}

func (b diBounds) box() shapeBox {
	return shapeBox{x: round(b.X), y: round(b.Y), w: round(b.W), h: round(b.H)}
}

func round(f float64) int { return int(math.Round(f)) }

// Outline classes: what an edge has to touch to look connected. A circle touches
// its bounding box only at the four side midpoints, and a diamond only at its
// vertices, so a point that is merely on the box border floats beside the shape.
const (
	outlineBox = iota
	outlineCircle
	outlineDiamond
)

// nodeKindsAndNames walks the semantic model, at any nesting depth, and records
// which outline each flow node is drawn with and the name of every event and
// gateway — the shapes whose caption bpmn-js draws outside the shape.
func nodeKindsAndNames(t *testing.T, src string) (map[string]int, map[string]string) {
	t.Helper()
	kinds, names := map[string]int{}, map[string]string{}
	dec := xml.NewDecoder(strings.NewReader(src))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("model does not parse: %v", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Space != bpmnModelNS {
			continue
		}
		var id, name string
		for _, a := range se.Attr {
			switch a.Name.Local {
			case "id":
				id = a.Value
			case "name":
				name = strings.TrimSpace(a.Value)
			}
		}
		if id == "" {
			continue
		}
		switch local := se.Name.Local; {
		case strings.HasSuffix(local, "Event"):
			kinds[id] = outlineCircle
		case strings.HasSuffix(local, "Gateway"):
			kinds[id] = outlineDiamond
		default:
			continue
		}
		if name != "" {
			names[id] = name
		}
	}
	return kinds, names
}

const bpmnModelNS = "http://www.omg.org/spec/BPMN/20100524/MODEL"

// onOutline reports whether p touches the outline of shape s drawn as kind, to
// within a pixel — the DI is integers here, but a file bpmn-js saved is not.
func onOutline(p point, s shapeBox, kind int) bool {
	const tol = 1.0
	px, py := float64(p.x), float64(p.y)
	cx, cy := float64(s.x)+float64(s.w)/2, float64(s.y)+float64(s.h)/2
	hw, hh := float64(s.w)/2, float64(s.h)/2
	switch kind {
	case outlineCircle:
		return math.Abs(math.Hypot(px-cx, py-cy)-hw) <= tol
	case outlineDiamond:
		// |dx|/hw + |dy|/hh = 1 on the diamond; scaled back to pixels.
		return math.Abs((math.Abs(px-cx)/hw+math.Abs(py-cy)/hh)-1)*math.Min(hw, hh) <= tol
	default:
		inX := px >= float64(s.x)-tol && px <= float64(s.right())+tol
		inY := py >= float64(s.y)-tol && py <= float64(s.bottom())+tol
		onV := math.Abs(px-float64(s.x)) <= tol || math.Abs(px-float64(s.right())) <= tol
		onH := math.Abs(py-float64(s.y)) <= tol || math.Abs(py-float64(s.bottom())) <= tol
		return (onV && inY) || (onH && inX)
	}
}

// unattachedEdges: an edge starts on its source's outline and ends on its
// target's. bpmn-js draws the waypoints it is given, so an edge that stops short
// of its target points into empty space, and one that ends inside a shape draws
// its arrowhead over the label — which is what the approval models did, with the
// start flow ending in the middle of the gateway it was meant to reach.
func (h *handModel) unattachedEdges() []string {
	var out []string
	for _, f := range h.flows {
		pts, ok := h.edges[f.Id]
		if f.Id == "" || !ok || len(pts) < 2 {
			continue // reported by checkCompleteness / checkEdgesOrthogonal
		}
		for _, end := range []struct {
			node, what string
			p          point
		}{{f.SourceRef, "starts", pts[0]}, {f.TargetRef, "ends", pts[len(pts)-1]}} {
			s, ok := h.shapes[end.node]
			if !ok {
				continue
			}
			if !onOutline(end.p, s, h.kind[end.node]) {
				out = append(out, fmt.Sprintf("invariant[edge-attached]: edge %q %s at (%d,%d), which is not on the outline of %q %v",
					f.Id, end.what, end.p.x, end.p.y, end.node, box(s)))
			}
		}
	}
	return out
}

// boundaryEventsOffHost: a boundary event is drawn centred on the border of
// the activity it is attached to. The semantics follow attachedToRef whatever the
// picture says, so a timer drawn on the neighbouring task tells the reader the
// wrong task is on a deadline — and the Modeler, which moves a boundary event with
// its host, moves it with the wrong one.
func (h *handModel) boundaryEventsOffHost() []string {
	var out []string
	for _, be := range sortedKeys(h.host) {
		s, sok := h.shapes[be]
		host, hok := h.shapes[h.host[be]]
		if !sok || !hok {
			continue // reported by checkCompleteness
		}
		c := point{s.x + s.w/2, s.y + s.h/2}
		if !onOutline(c, host, outlineBox) {
			out = append(out, fmt.Sprintf("invariant[boundary-on-host]: boundary event %q is centred at (%d,%d), "+
				"not on the border of its host %q %v", be, c.x, c.y, h.host[be], box(host)))
		}
	}
	return out
}

// unlabelledNamedNodes: a named event or gateway states where its caption
// goes. Without a position bpmn-js puts it under the shape at a fixed width — two
// boundary events side by side then print their captions over each other — and
// checkLabelsClear, which can only judge a box it knows, never sees it.
func (h *handModel) unlabelledNamedNodes() []string {
	var out []string
	for _, id := range sortedKeys(h.names) {
		if _, ok := h.labels[id]; !ok {
			out = append(out, fmt.Sprintf("invariant[node-label]: %q (%q) is named but has no label position", id, h.names[id]))
		}
	}
	return out
}

// flowsSteppingLeft: a forward sequence flow never moves left. A process reads
// left to right, and an edge that doubles back reads as a loop that is not there.
// Read off the waypoints rather than off the shapes: a drop from a gateway
// straight down into a wider task is not a step back, though the task's left edge
// lies left of the gateway's. Loops are exempt — returning is what they are for.
func (h *handModel) flowsSteppingLeft() []string {
	var out []string
	for _, f := range h.flows {
		if f.Id == "" || h.backEdge[f.Id] {
			continue
		}
		pts := h.edges[f.Id]
		for k := 1; k < len(pts); k++ {
			if pts[k].x < pts[k-1].x {
				out = append(out, fmt.Sprintf("invariant[left-to-right]: forward flow %q steps left from (%d,%d) to (%d,%d)",
					f.Id, pts[k-1].x, pts[k-1].y, pts[k].x, pts[k].y))
				break
			}
		}
	}
	return out
}
