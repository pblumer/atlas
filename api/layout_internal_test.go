package api

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// shapeBox is a shape's bounds, extracted from generated DI for geometric asserts.
type shapeBox struct {
	x, y, w, h int
	expanded   bool
}

func (s shapeBox) right() int  { return s.x + s.w }
func (s shapeBox) bottom() int { return s.y + s.h }

// contains reports whether inner sits fully within outer.
func (outer shapeBox) contains(inner shapeBox) bool {
	return inner.x >= outer.x && inner.y >= outer.y &&
		inner.right() <= outer.right() && inner.bottom() <= outer.bottom()
}

var shapeRe = regexp.MustCompile(
	`<bpmndi:BPMNShape id="[^"]*" bpmnElement="([^"]*)"( isExpanded="true")?>\s*` +
		`<omgdc:Bounds x="(-?\d+)" y="(-?\d+)" width="(\d+)" height="(\d+)"/>`)

var edgeRe = regexp.MustCompile(`(?s)<bpmndi:BPMNEdge id="[^"]*" bpmnElement="([^"]*)">(.*?)</bpmndi:BPMNEdge>`)
var waypointRe = regexp.MustCompile(`<omgdi:waypoint x="(-?\d+)" y="(-?\d+)"/>`)

// parseEdges extracts every edge's ordered waypoints from generated DI, keyed by
// the flow id, so tests can assert routing shape without pinning coordinates.
func parseEdges(t *testing.T, di string) map[string][]point {
	t.Helper()
	atoi := func(s string) int {
		n, err := strconv.Atoi(s)
		if err != nil {
			t.Fatalf("bad number %q in DI: %v", s, err)
		}
		return n
	}
	out := map[string][]point{}
	for _, m := range edgeRe.FindAllStringSubmatch(di, -1) {
		var pts []point
		for _, wp := range waypointRe.FindAllStringSubmatch(m[2], -1) {
			pts = append(pts, point{atoi(wp[1]), atoi(wp[2])})
		}
		out[m[1]] = pts
	}
	return out
}

// isOrthogonal reports whether every consecutive segment of pts is axis-aligned
// (horizontal or vertical) — i.e. no diagonal runs.
func isOrthogonal(pts []point) bool {
	for i := 1; i < len(pts); i++ {
		if pts[i-1].x != pts[i].x && pts[i-1].y != pts[i].y {
			return false
		}
	}
	return true
}

// parseShapes extracts every shape's bounds from generated DI, keyed by the
// element id, so tests can assert on geometry rather than exact coordinates.
func parseShapes(t *testing.T, di string) map[string]shapeBox {
	t.Helper()
	atoi := func(s string) int {
		n, err := strconv.Atoi(s)
		if err != nil {
			t.Fatalf("bad number %q in DI: %v", s, err)
		}
		return n
	}
	out := map[string]shapeBox{}
	for _, m := range shapeRe.FindAllStringSubmatch(di, -1) {
		out[m[1]] = shapeBox{x: atoi(m[3]), y: atoi(m[4]), w: atoi(m[5]), h: atoi(m[6]), expanded: m[2] != ""}
	}
	return out
}

// TestGenerateDIEdgeCases covers the non-happy exits of generateDI: unparseable
// XML, a process without an id, and a process with no layout-relevant nodes.
func TestGenerateDIEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"malformed xml", `<definitions><process`},
		{"no process", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"></definitions>`},
		{
			"process without id",
			`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process><startEvent id="s"/></process></definitions>`,
		},
		{
			"process with no nodes",
			`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p"><sequenceFlow id="f" sourceRef="a" targetRef="b"/></process></definitions>`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if di, ok := generateDI([]byte(tc.src)); ok || di != "" {
				t.Fatalf("generateDI = (%q, %v), want (\"\", false)", di, ok)
			}
		})
	}
}

// TestGenerateDISkipsDanglingFlows exercises emitEdges' skip branches: a flow to
// an unknown node, and a flow with no id, must both be dropped while the real edge
// is still rendered.
func TestGenerateDISkipsDanglingFlows(t *testing.T) {
	src := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p">
	    <startEvent id="s"/>
	    <endEvent id="e"/>
	    <sequenceFlow id="good" sourceRef="s" targetRef="e"/>
	    <sequenceFlow id="dangling" sourceRef="s" targetRef="ghost"/>
	    <sequenceFlow sourceRef="s" targetRef="e"/>
	  </process>
	</definitions>`
	di, ok := generateDI([]byte(src))
	if !ok {
		t.Fatal("generateDI returned ok=false for a valid process")
	}
	if !strings.Contains(di, `bpmnElement="good"`) {
		t.Fatalf("edge for real flow missing:\n%s", di)
	}
	if strings.Contains(di, `bpmnElement="dangling"`) {
		t.Fatalf("edge for dangling flow should have been skipped:\n%s", di)
	}
}

// TestGenerateDICyclicTerminates confirms the layering loop terminates (via its
// iteration cap) even when the sequence flows form a cycle.
func TestGenerateDICyclicTerminates(t *testing.T) {
	src := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p">
	    <task id="a"/>
	    <task id="b"/>
	    <sequenceFlow id="f1" sourceRef="a" targetRef="b"/>
	    <sequenceFlow id="f2" sourceRef="b" targetRef="a"/>
	  </process>
	</definitions>`
	di, ok := generateDI([]byte(src))
	if !ok || !strings.Contains(di, `bpmnElement="a"`) || !strings.Contains(di, `bpmnElement="b"`) {
		t.Fatalf("cyclic model should still lay out both nodes; ok=%v di=%s", ok, di)
	}
}

// TestInjectBeforeDefinitionsCloseNoTag returns src unchanged when there is no
// closing definitions tag to splice before.
func TestInjectBeforeDefinitionsCloseNoTag(t *testing.T) {
	src := []byte(`<something>no closing definitions here</something>`)
	if got := injectBeforeDefinitionsClose(src, "<di/>"); string(got) != string(src) {
		t.Fatalf("expected src returned unchanged, got %q", got)
	}
}

// TestStripDiagramLayout removes both the self-closing and the container forms of
// a BPMNDiagram (regardless of prefix) while leaving the semantic model and any
// inner self-closing shapes' *names* untouched.
func TestStripDiagramLayout(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"self-closing", `<definitions><process id="p"/><bpmndi:BPMNDiagram id="d"/></definitions>`},
		{"container", `<definitions><process id="p"/>` +
			`<bpmndi:BPMNDiagram id="d"><bpmndi:BPMNPlane><bpmndi:BPMNShape id="s_di"/></bpmndi:BPMNPlane></bpmndi:BPMNDiagram>` +
			`</definitions>`},
		{"no prefix", `<definitions><process id="p"/><BPMNDiagram><BPMNPlane/></BPMNDiagram></definitions>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := string(stripDiagramLayout([]byte(tc.src)))
			if strings.Contains(out, "BPMNDiagram") || strings.Contains(out, "BPMNPlane") || strings.Contains(out, "BPMNShape") {
				t.Fatalf("diagram interchange not fully stripped:\n%s", out)
			}
			if !strings.Contains(out, `<process id="p"/>`) {
				t.Fatalf("semantic model must survive stripping:\n%s", out)
			}
		})
	}
}

// TestStripDiagramLayoutTwoDiagrams confirms the non-greedy container match strips
// each of two adjacent diagrams independently rather than swallowing the gap
// between them.
func TestStripDiagramLayoutTwoDiagrams(t *testing.T) {
	src := `<definitions>` +
		`<bpmndi:BPMNDiagram id="a"><bpmndi:BPMNPlane/></bpmndi:BPMNDiagram>` +
		`<keep/>` +
		`<bpmndi:BPMNDiagram id="b"><bpmndi:BPMNPlane/></bpmndi:BPMNDiagram>` +
		`</definitions>`
	out := string(stripDiagramLayout([]byte(src)))
	if strings.Contains(out, "BPMNDiagram") {
		t.Fatalf("both diagrams should be stripped:\n%s", out)
	}
	if !strings.Contains(out, "<keep/>") {
		t.Fatalf("content between diagrams must be preserved:\n%s", out)
	}
}

// TestRelayoutDiagramRegenerates replaces an existing (minimal) layout with a
// freshly generated one: the old diagram is gone and a new one with shapes for
// every node is spliced in before </definitions>.
func TestRelayoutDiagramRegenerates(t *testing.T) {
	// A model whose stale layout places everything at the origin. Auto-layout must
	// discard it and lay the nodes out on the generated left-to-right grid.
	withStaleDI := semanticOnly[:len(semanticOnly)-len("</definitions>")] +
		`<bpmndi:BPMNDiagram id="stale"><bpmndi:BPMNPlane bpmnElement="order">` +
		`<bpmndi:BPMNShape id="Start_di" bpmnElement="Start">` +
		`<omgdc:Bounds x="0" y="0" width="36" height="36"/></bpmndi:BPMNShape>` +
		`</bpmndi:BPMNPlane></bpmndi:BPMNDiagram></definitions>`

	out := string(relayoutDiagram([]byte(withStaleDI)))
	if strings.Contains(out, `id="stale"`) {
		t.Fatalf("stale diagram should have been discarded:\n%s", out)
	}
	if n := strings.Count(out, "<bpmndi:BPMNDiagram"); n != 1 {
		t.Fatalf("expected exactly one regenerated diagram, got %d:\n%s", n, out)
	}
	for _, want := range []string{
		`bpmnElement="order"`,
		`<bpmndi:BPMNShape id="Start_di" bpmnElement="Start">`,
		`<bpmndi:BPMNShape id="Charge_di" bpmnElement="Charge">`,
		`<bpmndi:BPMNShape id="Done_di" bpmnElement="Done">`,
		`<bpmndi:BPMNEdge id="f1_di" bpmnElement="f1">`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("regenerated layout missing %q\n---\n%s", want, out)
		}
	}
	// The stale Start shape sat at the origin; the regenerated one must not.
	if strings.Contains(out, `<omgdc:Bounds x="0" y="0"`) {
		t.Fatalf("regenerated layout should not leave a node at the origin:\n%s", out)
	}
	if i, j := strings.Index(out, "<bpmndi:BPMNDiagram"), strings.LastIndex(out, "</definitions>"); i < 0 || j < 0 || i > j {
		t.Fatalf("regenerated diagram not placed before </definitions> (i=%d j=%d)", i, j)
	}
}

// TestRelayoutDiagramBestEffort leaves input untouched when no layout can be
// generated (non-BPMN input, or a model without layout-relevant nodes), so the
// button never strips a diagram without producing a replacement.
func TestRelayoutDiagramBestEffort(t *testing.T) {
	cases := []string{
		`<html><body>nope</body></html>`,
		`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p"><bpmndi:BPMNDiagram id="d"/></process></definitions>`,
	}
	for _, src := range cases {
		if got := string(relayoutDiagram([]byte(src))); got != src {
			t.Fatalf("expected unchanged input for %q, got %q", src, got)
		}
	}
}

// TestGenerateDISubProcessExpanded lays out an embedded subprocess: it must be an
// expanded box, and every child shape must fall inside that box.
func TestGenerateDISubProcessExpanded(t *testing.T) {
	src := []byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="P">
	    <startEvent id="Start"/>
	    <subProcess id="Sub">
	      <startEvent id="InnerStart"/>
	      <userTask id="InnerTask"/>
	      <endEvent id="InnerEnd"/>
	      <sequenceFlow id="i1" sourceRef="InnerStart" targetRef="InnerTask"/>
	      <sequenceFlow id="i2" sourceRef="InnerTask" targetRef="InnerEnd"/>
	    </subProcess>
	    <endEvent id="End"/>
	    <sequenceFlow id="s1" sourceRef="Start" targetRef="Sub"/>
	    <sequenceFlow id="s2" sourceRef="Sub" targetRef="End"/>
	  </process>
	</definitions>`)
	di, ok := generateDI(src)
	if !ok {
		t.Fatal("generateDI: want ok for a process with a subprocess")
	}
	shapes := parseShapes(t, di)

	sub, ok := shapes["Sub"]
	if !ok {
		t.Fatalf("subprocess shape missing:\n%s", di)
	}
	if !sub.expanded {
		t.Errorf("subprocess must be laid out as an expanded box (isExpanded):\n%s", di)
	}
	for _, child := range []string{"InnerStart", "InnerTask", "InnerEnd"} {
		c, ok := shapes[child]
		if !ok {
			t.Fatalf("subprocess child %q missing a shape:\n%s", child, di)
		}
		if !sub.contains(c) {
			t.Errorf("child %q (%+v) not inside subprocess box %+v", child, c, sub)
		}
	}
	// The internal flows are rendered too.
	for _, want := range []string{`bpmnElement="i1"`, `bpmnElement="i2"`} {
		if !strings.Contains(di, want) {
			t.Errorf("subprocess-internal flow missing %q:\n%s", want, di)
		}
	}
}

// TestGenerateDINestedSubProcess confirms two levels of nesting: the inner box
// sits inside the outer box, and the leaf task inside the inner box.
func TestGenerateDINestedSubProcess(t *testing.T) {
	src := []byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="P">
	    <startEvent id="Start"/>
	    <subProcess id="Outer">
	      <startEvent id="OuterStart"/>
	      <subProcess id="Inner">
	        <startEvent id="InnerStart"/>
	        <userTask id="Leaf"/>
	        <sequenceFlow id="l1" sourceRef="InnerStart" targetRef="Leaf"/>
	      </subProcess>
	      <sequenceFlow id="o1" sourceRef="OuterStart" targetRef="Inner"/>
	    </subProcess>
	    <sequenceFlow id="s1" sourceRef="Start" targetRef="Outer"/>
	  </process>
	</definitions>`)
	di, ok := generateDI(src)
	if !ok {
		t.Fatal("generateDI: want ok for nested subprocesses")
	}
	shapes := parseShapes(t, di)
	outer, ok1 := shapes["Outer"]
	inner, ok2 := shapes["Inner"]
	leaf, ok3 := shapes["Leaf"]
	if !ok1 || !ok2 || !ok3 {
		t.Fatalf("missing a nested shape (outer=%v inner=%v leaf=%v):\n%s", ok1, ok2, ok3, di)
	}
	if !outer.expanded || !inner.expanded {
		t.Errorf("both subprocess boxes must be expanded")
	}
	if !outer.contains(inner) {
		t.Errorf("inner box %+v not inside outer box %+v", inner, outer)
	}
	if !inner.contains(leaf) {
		t.Errorf("leaf task %+v not inside inner box %+v", leaf, inner)
	}
}

// TestGenerateDIBoundaryEvent places a boundary event on its host's border and
// routes its exception flow: the boundary shape straddles the host's bottom edge
// and its outgoing flow is rendered.
func TestGenerateDIBoundaryEvent(t *testing.T) {
	src := []byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="P">
	    <startEvent id="Start"/>
	    <userTask id="Review"/>
	    <boundaryEvent id="Timeout" attachedToRef="Review"/>
	    <endEvent id="Done"/>
	    <endEvent id="Escalated"/>
	    <sequenceFlow id="s1" sourceRef="Start" targetRef="Review"/>
	    <sequenceFlow id="s2" sourceRef="Review" targetRef="Done"/>
	    <sequenceFlow id="s3" sourceRef="Timeout" targetRef="Escalated"/>
	  </process>
	</definitions>`)
	di, ok := generateDI(src)
	if !ok {
		t.Fatal("generateDI: want ok for a boundary event")
	}
	shapes := parseShapes(t, di)
	host, ok1 := shapes["Review"]
	be, ok2 := shapes["Timeout"]
	if !ok1 || !ok2 {
		t.Fatalf("host or boundary shape missing (host=%v be=%v):\n%s", ok1, ok2, di)
	}
	// The boundary event's center must lie on the host's bottom border.
	if cy := be.y + be.h/2; cy != host.bottom() {
		t.Errorf("boundary center y=%d not on host bottom edge y=%d", cy, host.bottom())
	}
	if cx := be.x + be.w/2; cx < host.x || cx > host.right() {
		t.Errorf("boundary center x=%d not along host's bottom edge [%d,%d]", cx, host.x, host.right())
	}
	// The exception path is drawn.
	if !strings.Contains(di, `bpmnElement="s3"`) {
		t.Errorf("boundary event's outgoing flow missing:\n%s", di)
	}
}

// TestGenerateDIBoundaryDangling drops a boundary event whose attachedToRef points
// at nothing, rather than floating a stray shape at the origin.
func TestGenerateDIBoundaryDangling(t *testing.T) {
	src := []byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="P">
	    <startEvent id="Start"/>
	    <endEvent id="End"/>
	    <boundaryEvent id="Ghost" attachedToRef="nonexistent"/>
	    <sequenceFlow id="s1" sourceRef="Start" targetRef="End"/>
	  </process>
	</definitions>`)
	di, ok := generateDI(src)
	if !ok {
		t.Fatal("generateDI: want ok")
	}
	if strings.Contains(di, `bpmnElement="Ghost"`) {
		t.Errorf("a dangling boundary event should not get a shape:\n%s", di)
	}
}

// TestGenerateDIBoundaryOnSubProcess attaches a boundary event to a subprocess box
// (not a plain task): it must ride the box's bottom border.
func TestGenerateDIBoundaryOnSubProcess(t *testing.T) {
	src := []byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="P">
	    <startEvent id="Start"/>
	    <subProcess id="Sub">
	      <startEvent id="IS"/>
	      <userTask id="IT"/>
	      <sequenceFlow id="i1" sourceRef="IS" targetRef="IT"/>
	    </subProcess>
	    <boundaryEvent id="Cancel" attachedToRef="Sub"/>
	    <endEvent id="Aborted"/>
	    <sequenceFlow id="s1" sourceRef="Start" targetRef="Sub"/>
	    <sequenceFlow id="s2" sourceRef="Cancel" targetRef="Aborted"/>
	  </process>
	</definitions>`)
	di, ok := generateDI(src)
	if !ok {
		t.Fatal("generateDI: want ok for a boundary on a subprocess")
	}
	shapes := parseShapes(t, di)
	sub, ok1 := shapes["Sub"]
	be, ok2 := shapes["Cancel"]
	if !ok1 || !ok2 {
		t.Fatalf("subprocess or boundary shape missing:\n%s", di)
	}
	if cy := be.y + be.h/2; cy != sub.bottom() {
		t.Errorf("boundary center y=%d not on subprocess bottom edge y=%d", cy, sub.bottom())
	}
}

// TestGenerateDIOrthogonalRouting is the regression for diagonal branch edges: on a
// split/join, the main-axis flows run straight while the flows down to and up from
// the branch lane are right-angled elbows, never diagonals clipping the boxes.
func TestGenerateDIOrthogonalRouting(t *testing.T) {
	src := []byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="P">
	    <startEvent id="Start"/>
	    <exclusiveGateway id="Split"/>
	    <task id="Top"/>
	    <task id="Bottom"/>
	    <exclusiveGateway id="Join"/>
	    <endEvent id="End"/>
	    <sequenceFlow id="f1" sourceRef="Start" targetRef="Split"/>
	    <sequenceFlow id="fTop" sourceRef="Split" targetRef="Top"/>
	    <sequenceFlow id="fDown" sourceRef="Split" targetRef="Bottom"/>
	    <sequenceFlow id="fUp" sourceRef="Bottom" targetRef="Join"/>
	    <sequenceFlow id="fJoin" sourceRef="Top" targetRef="Join"/>
	    <sequenceFlow id="fEnd" sourceRef="Join" targetRef="End"/>
	  </process>
	</definitions>`)
	di, ok := generateDI(src)
	if !ok {
		t.Fatal("generateDI: want ok")
	}
	edges := parseEdges(t, di)

	// Every edge is orthogonal (no diagonal segment).
	for id, pts := range edges {
		if len(pts) < 2 {
			t.Errorf("edge %q has %d waypoints, want >= 2", id, len(pts))
		}
		if !isOrthogonal(pts) {
			t.Errorf("edge %q is not orthogonal: %v", id, pts)
		}
	}
	// The main-axis flow (Top sits on the spine with Split/Join) runs straight.
	if pts := edges["fTop"]; len(pts) != 2 || pts[0].y != pts[1].y {
		t.Errorf("main-axis flow fTop should be a straight horizontal run, got %v", pts)
	}
	// The branch flows change rows, so they must bend (more than two waypoints).
	for _, id := range []string{"fDown", "fUp"} {
		if pts := edges[id]; len(pts) < 3 {
			t.Errorf("branch flow %q should bend into its lane, got %v", id, pts)
		}
	}
}
