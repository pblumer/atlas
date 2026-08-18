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
	horizontal bool
}

func (s shapeBox) right() int  { return s.x + s.w }
func (s shapeBox) bottom() int { return s.y + s.h }

// contains reports whether inner sits fully within outer.
func (outer shapeBox) contains(inner shapeBox) bool {
	return inner.x >= outer.x && inner.y >= outer.y &&
		inner.right() <= outer.right() && inner.bottom() <= outer.bottom()
}

var shapeRe = regexp.MustCompile(
	`<bpmndi:BPMNShape id="[^"]*" bpmnElement="([^"]*)"([^>]*)>\s*` +
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
		out[m[1]] = shapeBox{
			x: atoi(m[3]), y: atoi(m[4]), w: atoi(m[5]), h: atoi(m[6]),
			expanded:   strings.Contains(m[2], "isExpanded"),
			horizontal: strings.Contains(m[2], "isHorizontal"),
		}
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
	// The handler (Escalated) is a side branch, so it sits above the trunk and the
	// boundary event rides the host's top border, facing it.
	if cy := be.y + be.h/2; cy != host.y {
		t.Errorf("boundary center y=%d not on host top edge y=%d", cy, host.y)
	}
	if cx := be.x + be.w/2; cx < host.x || cx > host.right() {
		t.Errorf("boundary center x=%d not along host's top edge [%d,%d]", cx, host.x, host.right())
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
// (not a plain task): it must ride the box's border facing its handler (here the
// top, as the handler is a side branch above the trunk).
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
	if cy := be.y + be.h/2; cy != sub.y {
		t.Errorf("boundary center y=%d not on subprocess top edge y=%d", cy, sub.y)
	}
}

// TestGenerateDIBoundaryHandlerBelowRidesBottom covers the direction-aware boundary
// placement: side branches normally rise above the trunk, so a boundary event rides
// its host's top edge — but when its handler ends up below the host (here in a lower
// swimlane), the event rides the bottom edge instead and its exception flow leaves
// downward, never doubling back across the host.
func TestGenerateDIBoundaryHandlerBelowRidesBottom(t *testing.T) {
	src := []byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="P">
	    <laneSet>
	      <lane id="Top"><flowNodeRef>Start</flowNodeRef><flowNodeRef>Host</flowNodeRef></lane>
	      <lane id="Bot"><flowNodeRef>Handler</flowNodeRef><flowNodeRef>Aborted</flowNodeRef></lane>
	    </laneSet>
	    <startEvent id="Start"/>
	    <task id="Host"/>
	    <boundaryEvent id="Guard" name="X" attachedToRef="Host"/>
	    <task id="Handler"/>
	    <endEvent id="Aborted"/>
	    <sequenceFlow id="s1" sourceRef="Start" targetRef="Host"/>
	    <sequenceFlow id="s2" sourceRef="Guard" targetRef="Handler"/>
	    <sequenceFlow id="s3" sourceRef="Handler" targetRef="Aborted"/>
	  </process>
	</definitions>`)
	di, ok := generateDI(src)
	if !ok {
		t.Fatal("generateDI: want ok")
	}
	shapes := parseShapes(t, di)
	host, ok1 := shapes["Host"]
	be, ok2 := shapes["Guard"]
	handler, ok3 := shapes["Handler"]
	if !ok1 || !ok2 || !ok3 {
		t.Fatalf("host, boundary, or handler shape missing:\n%s", di)
	}
	if handler.y+handler.h/2 <= host.y+host.h/2 {
		t.Fatalf("test setup: handler should sit below the host, got handler cy=%d host cy=%d",
			handler.y+handler.h/2, host.y+host.h/2)
	}
	// Its label rides below the host (the event is on the bottom edge), clear of the
	// task box rather than over its title.
	if lbl := boundaryLabelBox(t, di, "Guard"); lbl.y < host.bottom() {
		t.Errorf("bottom-edge boundary label (y=%d) not clear below host bottom y=%d", lbl.y, host.bottom())
	}
	// The handler is below, so the boundary event rides the host's bottom edge.
	if cy := be.y + be.h/2; cy != host.bottom() {
		t.Errorf("boundary center y=%d not on host bottom edge y=%d", cy, host.bottom())
	}
	// The exception flow leaves from the boundary event's bottom, heading down.
	if pts := parseEdges(t, di)["s2"]; len(pts) < 2 || pts[0].y < be.y+be.h/2 {
		t.Errorf("exception flow s2 should leave downward from the boundary bottom, got %v", pts)
	}
}

// TestGenerateDIHappyPathStaysStraight is the regression for auto-layout bending a
// linear main flow into a staircase. When side branches (here boundary-event error
// handlers ending in their own end events) share the happy path's columns, every
// node on the main sequence must keep a single shared center line — the branch's
// end events must not seize the main axis and shove the happy-path tasks off it.
func TestGenerateDIHappyPathStaysStraight(t *testing.T) {
	src := []byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="P">
	    <startEvent id="Start"/>
	    <serviceTask id="Init"/>
	    <serviceTask id="Establish"/>
	    <serviceTask id="Discover"/>
	    <serviceTask id="Execute"/>
	    <serviceTask id="Log"/>
	    <serviceTask id="Cleanup"/>
	    <endEvent id="EndSuccess"/>
	    <serviceTask id="HandleSessionError"/>
	    <serviceTask id="HandleError"/>
	    <endEvent id="EndError1"/>
	    <endEvent id="EndError2"/>
	    <boundaryEvent id="SessionError" attachedToRef="Establish"/>
	    <boundaryEvent id="Error" attachedToRef="Execute"/>
	    <sequenceFlow id="f1" sourceRef="Start" targetRef="Init"/>
	    <sequenceFlow id="f2" sourceRef="Init" targetRef="Establish"/>
	    <sequenceFlow id="f3" sourceRef="Establish" targetRef="Discover"/>
	    <sequenceFlow id="f4" sourceRef="Discover" targetRef="Execute"/>
	    <sequenceFlow id="f5" sourceRef="Execute" targetRef="Log"/>
	    <sequenceFlow id="f6" sourceRef="Log" targetRef="Cleanup"/>
	    <sequenceFlow id="f7" sourceRef="Cleanup" targetRef="EndSuccess"/>
	    <sequenceFlow id="e1" sourceRef="SessionError" targetRef="HandleSessionError"/>
	    <sequenceFlow id="e2" sourceRef="HandleSessionError" targetRef="EndError1"/>
	    <sequenceFlow id="e3" sourceRef="Error" targetRef="HandleError"/>
	    <sequenceFlow id="e4" sourceRef="HandleError" targetRef="EndError2"/>
	  </process>
	</definitions>`)
	di, ok := generateDI(src)
	if !ok {
		t.Fatal("generateDI: want ok")
	}
	shapes := parseShapes(t, di)
	happyPath := []string{"Start", "Init", "Establish", "Discover", "Execute", "Log", "Cleanup", "EndSuccess"}
	want := shapes[happyPath[0]]
	wantCY := want.y + want.h/2
	for _, id := range happyPath {
		s, ok := shapes[id]
		if !ok {
			t.Fatalf("happy-path shape %q missing:\n%s", id, di)
		}
		if cy := s.y + s.h/2; cy != wantCY {
			t.Errorf("happy-path node %q center y=%d, want %d (main flow must stay one straight line)", id, cy, wantCY)
		}
	}
	// The side branches rise above the main axis, never displacing it or dropping
	// below it (smaller y is higher on the canvas).
	for _, id := range []string{"EndError1", "EndError2", "HandleSessionError", "HandleError"} {
		s, ok := shapes[id]
		if !ok {
			t.Fatalf("branch shape %q missing:\n%s", id, di)
		}
		if cy := s.y + s.h/2; cy >= wantCY {
			t.Errorf("branch node %q y-center %d is not above the main axis %d", id, cy, wantCY)
		}
	}
	// A branch runs straight along one row: each handler and the end event it flows
	// into share a center line, so the connecting edge is a single horizontal run
	// rather than sagging toward the happy-path column below.
	for _, b := range []struct{ handler, end, flow string }{
		{"HandleSessionError", "EndError1", "e2"},
		{"HandleError", "EndError2", "e4"},
	} {
		h, end := shapes[b.handler], shapes[b.end]
		if hcy, ecy := h.y+h.h/2, end.y+end.h/2; hcy != ecy {
			t.Errorf("branch %q not on one row: %s cy=%d, %s cy=%d", b.flow, b.handler, hcy, b.end, ecy)
		}
		if pts := parseEdges(t, di)[b.flow]; len(pts) != 2 || pts[0].y != pts[1].y {
			t.Errorf("branch flow %q should be one straight horizontal run, got %v", b.flow, pts)
		}
	}
}

// boundaryLabelBox extracts the explicit BPMNLabel bounds emitted inside one
// boundary event's shape, so a test can assert where its caption sits.
func boundaryLabelBox(t *testing.T, di, id string) shapeBox {
	t.Helper()
	re := regexp.MustCompile(`(?s)<bpmndi:BPMNShape id="` + id +
		`_di".*?<bpmndi:BPMNLabel>\s*<omgdc:Bounds x="(-?\d+)" y="(-?\d+)" width="(\d+)" height="(\d+)"/>`)
	m := re.FindStringSubmatch(di)
	if m == nil {
		t.Fatalf("no explicit label for boundary %q in DI:\n%s", id, di)
	}
	atoi := func(s string) int {
		n, err := strconv.Atoi(s)
		if err != nil {
			t.Fatalf("bad number %q: %v", s, err)
		}
		return n
	}
	return shapeBox{x: atoi(m[1]), y: atoi(m[2]), w: atoi(m[3]), h: atoi(m[4])}
}

// TestGenerateDIBoundaryLabelClearsHost is the regression for a boundary event's
// caption landing on top of its host's title (or tucked behind the exception riser).
// The generator emits an explicit label box for a named boundary event, placed off
// the host — above it when it rides the top edge — with the box's left edge just past
// the event's right side, so the caption sits in the open pocket beside the event
// instead of over the host or behind the upward riser (which leaves from the center).
func TestGenerateDIBoundaryLabelClearsHost(t *testing.T) {
	src := []byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="P">
	    <startEvent id="Start"/>
	    <serviceTask id="Task"/>
	    <boundaryEvent id="B" name="Timeout" attachedToRef="Task"/>
	    <serviceTask id="Handler"/>
	    <endEvent id="Done"/>
	    <endEvent id="Aborted"/>
	    <sequenceFlow id="s1" sourceRef="Start" targetRef="Task"/>
	    <sequenceFlow id="s2" sourceRef="Task" targetRef="Done"/>
	    <sequenceFlow id="s3" sourceRef="B" targetRef="Handler"/>
	    <sequenceFlow id="s4" sourceRef="Handler" targetRef="Aborted"/>
	  </process>
	</definitions>`)
	di, ok := generateDI(src)
	if !ok {
		t.Fatal("generateDI: want ok")
	}
	host := parseShapes(t, di)["Task"]
	be := parseShapes(t, di)["B"]
	lbl := boundaryLabelBox(t, di, "B")
	// The handler is above, so the boundary rides the top edge and its label sits in
	// the gap above the host, never over the task box.
	if lbl.bottom() > host.y {
		t.Errorf("boundary label (y %d..%d) not clear above host top y=%d", lbl.y, lbl.bottom(), host.y)
	}
	// The label sits to the right of the event circle, so the upward exception riser
	// (which leaves from the event's center) runs clear of the caption instead of
	// through it.
	if lbl.x < be.right() {
		t.Errorf("label left edge %d not clear of the event's right side %d (caption crowds the riser)", lbl.x, be.right())
	}
	// Only the boundary event carries an explicit label; other events auto-place.
	if n := strings.Count(di, "<bpmndi:BPMNLabel>"); n != 1 {
		t.Errorf("want exactly one explicit label (the boundary), got %d", n)
	}
	// A boundary event with no name gets no explicit label box.
	di2, _ := generateDI([]byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="P">
	    <startEvent id="Start"/><serviceTask id="Task"/><endEvent id="Done"/><endEvent id="Ab"/>
	    <boundaryEvent id="B" attachedToRef="Task"/>
	    <serviceTask id="H"/>
	    <sequenceFlow id="s1" sourceRef="Start" targetRef="Task"/>
	    <sequenceFlow id="s2" sourceRef="Task" targetRef="Done"/>
	    <sequenceFlow id="s3" sourceRef="B" targetRef="H"/>
	    <sequenceFlow id="s4" sourceRef="H" targetRef="Ab"/>
	  </process>
	</definitions>`))
	if strings.Contains(di2, "<bpmndi:BPMNLabel>") {
		t.Errorf("an unnamed boundary event should not get an explicit label:\n%s", di2)
	}
}

// TestGenerateDIBranchesStackOnDistinctRows covers two side branches that share a
// column: they cannot occupy the same row, so they stack onto distinct rows above
// the trunk rather than overlapping.
func TestGenerateDIBranchesStackOnDistinctRows(t *testing.T) {
	src := []byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="P">
	    <startEvent id="S"/>
	    <task id="M1"/>
	    <task id="M2"/>
	    <endEvent id="E"/>
	    <task id="A"/>
	    <task id="B"/>
	    <sequenceFlow id="m1" sourceRef="S" targetRef="M1"/>
	    <sequenceFlow id="m2" sourceRef="M1" targetRef="M2"/>
	    <sequenceFlow id="m3" sourceRef="M2" targetRef="E"/>
	    <sequenceFlow id="a" sourceRef="S" targetRef="A"/>
	    <sequenceFlow id="b" sourceRef="S" targetRef="B"/>
	  </process>
	</definitions>`)
	di, ok := generateDI(src)
	if !ok {
		t.Fatal("generateDI: want ok")
	}
	shapes := parseShapes(t, di)
	m1, a, b := shapes["M1"], shapes["A"], shapes["B"]
	trunkCY := m1.y + m1.h/2
	acy, bcy := a.y+a.h/2, b.y+b.h/2
	if acy == bcy {
		t.Errorf("A and B share a column and must not share a row, both cy=%d", acy)
	}
	if acy >= trunkCY || bcy >= trunkCY {
		t.Errorf("branch rows must sit above the trunk (cy=%d); got A=%d B=%d", trunkCY, acy, bcy)
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

// TestGenerateDILanesBanded lays out a swimlane process: each lane is a horizontal
// band, the bands stack without overlapping, every node sits inside its lane's
// band, and the columns stay shared so the flow still advances left-to-right across
// lanes.
func TestGenerateDILanesBanded(t *testing.T) {
	src := []byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="P">
	    <laneSet id="ls">
	      <lane id="Top" name="Top"><flowNodeRef>Start</flowNodeRef><flowNodeRef>End</flowNodeRef></lane>
	      <lane id="Bottom" name="Bottom"><flowNodeRef>Work</flowNodeRef></lane>
	    </laneSet>
	    <startEvent id="Start"/>
	    <userTask id="Work"/>
	    <endEvent id="End"/>
	    <sequenceFlow id="f1" sourceRef="Start" targetRef="Work"/>
	    <sequenceFlow id="f2" sourceRef="Work" targetRef="End"/>
	  </process>
	</definitions>`)
	di, ok := generateDI(src)
	if !ok {
		t.Fatal("generateDI: want ok for a laned process")
	}
	shapes := parseShapes(t, di)

	top, ok1 := shapes["Top"]
	bottom, ok2 := shapes["Bottom"]
	if !ok1 || !ok2 {
		t.Fatalf("lane band shapes missing (top=%v bottom=%v):\n%s", ok1, ok2, di)
	}
	if !top.horizontal || !bottom.horizontal {
		t.Errorf("lane bands must carry isHorizontal")
	}
	// Bands stack, same width, no vertical overlap.
	if top.w != bottom.w {
		t.Errorf("lane bands should share a width, got %d vs %d", top.w, bottom.w)
	}
	if top.bottom() > bottom.y {
		t.Errorf("lane bands overlap: top ends at %d, bottom starts at %d", top.bottom(), bottom.y)
	}
	// Each node is inside its lane's band.
	for _, n := range []string{"Start", "End"} {
		if !top.contains(shapes[n]) {
			t.Errorf("node %q (%+v) not inside its lane band %+v", n, shapes[n], top)
		}
	}
	if !bottom.contains(shapes["Work"]) {
		t.Errorf("node Work (%+v) not inside its lane band %+v", shapes["Work"], bottom)
	}
	// Columns are shared across lanes: Work (lane 2, column 2) sits to the right of
	// Start and to the left of End even though they are in the other lane.
	if !(shapes["Start"].x < shapes["Work"].x && shapes["Work"].x < shapes["End"].x) {
		t.Errorf("columns not shared across lanes: Start.x=%d Work.x=%d End.x=%d",
			shapes["Start"].x, shapes["Work"].x, shapes["End"].x)
	}
}

// TestGenerateDILanesUnassignedNodeInFirstLane confirms a node no lane claims still
// lands inside the first lane's band rather than floating outside every lane.
func TestGenerateDILanesUnassignedNodeInFirstLane(t *testing.T) {
	src := []byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="P">
	    <laneSet id="ls">
	      <lane id="First" name="First"><flowNodeRef>Start</flowNodeRef></lane>
	      <lane id="Second" name="Second"><flowNodeRef>B</flowNodeRef></lane>
	    </laneSet>
	    <startEvent id="Start"/>
	    <userTask id="A"/>
	    <userTask id="B"/>
	    <sequenceFlow id="f1" sourceRef="Start" targetRef="A"/>
	    <sequenceFlow id="f2" sourceRef="A" targetRef="B"/>
	  </process>
	</definitions>`)
	di, ok := generateDI(src)
	if !ok {
		t.Fatal("generateDI: want ok")
	}
	shapes := parseShapes(t, di)
	if !shapes["First"].contains(shapes["A"]) {
		t.Errorf("unassigned node A (%+v) should fall into the first lane %+v", shapes["A"], shapes["First"])
	}
}

// TestGenerateDILanesInsidePool nests lanes within a collaboration pool: the pool
// band contains both lane bands, which in turn contain the nodes.
func TestGenerateDILanesInsidePool(t *testing.T) {
	src := []byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <collaboration id="C">
	    <participant id="Pool" name="Org" processRef="P"/>
	  </collaboration>
	  <process id="P">
	    <laneSet id="ls">
	      <lane id="L1" name="Sales"><flowNodeRef>Start</flowNodeRef></lane>
	      <lane id="L2" name="Ops"><flowNodeRef>Do</flowNodeRef></lane>
	    </laneSet>
	    <startEvent id="Start"/>
	    <userTask id="Do"/>
	    <sequenceFlow id="f1" sourceRef="Start" targetRef="Do"/>
	  </process>
	</definitions>`)
	di, ok := generateDI(src)
	if !ok {
		t.Fatal("generateDI: want ok for a laned pool")
	}
	shapes := parseShapes(t, di)
	pool, ok0 := shapes["Pool"]
	l1, ok1 := shapes["L1"]
	l2, ok2 := shapes["L2"]
	if !ok0 || !ok1 || !ok2 {
		t.Fatalf("missing pool or lane shapes:\n%s", di)
	}
	if !pool.horizontal || !l1.horizontal || !l2.horizontal {
		t.Errorf("pool and lanes must all be horizontal bands")
	}
	if !pool.contains(l1) || !pool.contains(l2) {
		t.Errorf("lane bands must sit inside the pool (pool=%+v l1=%+v l2=%+v)", pool, l1, l2)
	}
	if !l1.contains(shapes["Start"]) {
		t.Errorf("Start (%+v) not inside lane L1 %+v", shapes["Start"], l1)
	}
	if !l2.contains(shapes["Do"]) {
		t.Errorf("Do (%+v) not inside lane L2 %+v", shapes["Do"], l2)
	}
}

// TestGenerateDILaneNoIdOmitsShape leaves out a band shape for a lane with no id
// (bpmn-js would choke on a shape whose bpmnElement points nowhere) while still
// placing that lane's nodes.
func TestGenerateDILaneNoIdOmitsShape(t *testing.T) {
	src := []byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="P">
	    <laneSet id="ls">
	      <lane name="anon"><flowNodeRef>Start</flowNodeRef></lane>
	    </laneSet>
	    <startEvent id="Start"/>
	    <endEvent id="End"/>
	    <sequenceFlow id="f1" sourceRef="Start" targetRef="End"/>
	  </process>
	</definitions>`)
	di, ok := generateDI(src)
	if !ok {
		t.Fatal("generateDI: want ok")
	}
	// The node is still drawn even though its lane has no id and thus no band shape.
	if _, drawn := parseShapes(t, di)["Start"]; !drawn {
		t.Errorf("Start should still be laid out:\n%s", di)
	}
}

// TestVertexRouteLeavesByFacingEdge pins the shared contract of the two sources that
// hang off the middle of a shape rather than its side: a boundary event and a gateway
// branch that steps off its row. Both have to leave by the horizontal edge the target
// faces, so the stub points at where the edge is going instead of doubling back.
func TestVertexRouteLeavesByFacingEdge(t *testing.T) {
	src := lnode{x: 100, y: 100, w: 50, h: 50} // center (125,125)
	tests := []struct {
		name   string
		tx, ty int
		want   []point
	}{
		{"target above leaves by the top", 300, 40, []point{{125, 100}, {125, 40}, {300, 40}}},
		{"target below leaves by the bottom", 300, 210, []point{{125, 150}, {125, 210}, {300, 210}}},
		{"target dead above needs no elbow", 125, 40, []point{{125, 100}, {125, 40}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := vertexRoute(src, tc.tx, tc.ty)
			if len(got) != len(tc.want) {
				t.Fatalf("vertexRoute = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("vertexRoute = %v, want %v", got, tc.want)
				}
			}
			if !isOrthogonal(got) {
				t.Errorf("vertexRoute = %v, want an orthogonal path", got)
			}
		})
	}
}

// TestGenerateDISubProcessMinimumSize covers the floor an expanded subprocess is
// drawn at. Its frame is normally its content plus padding, but a subprocess
// holding one small element — or nothing at all, which a model being built up in
// the Modeler goes through — would otherwise come out as a box too small to read
// its own title, let alone drop an element into.
func TestGenerateDISubProcessMinimumSize(t *testing.T) {
	src := []byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="P">
	    <startEvent id="S"/>
	    <subProcess id="Sub" name="Handle it">
	      <startEvent id="Inner"/>
	    </subProcess>
	    <endEvent id="E"/>
	    <sequenceFlow id="f1" sourceRef="S" targetRef="Sub"/>
	    <sequenceFlow id="f2" sourceRef="Sub" targetRef="E"/>
	  </process>
	</definitions>`)
	di, ok := generateDI(src)
	if !ok {
		t.Fatal("generateDI: want ok")
	}
	sub := parseShapes(t, di)["Sub"]
	if sub.w < subMinW || sub.h < subMinH {
		t.Errorf("subprocess frame = %dx%d, want at least %dx%d", sub.w, sub.h, subMinW, subMinH)
	}
	if !sub.expanded {
		t.Error("subprocess with a child should be drawn expanded")
	}
	if inner, ok := parseShapes(t, di)["Inner"]; !ok || !sub.contains(inner) {
		t.Errorf("inner element %+v should sit inside the frame %+v", inner, sub)
	}
}

// TestGenerateDISubProcessWithoutIdSkipped covers a subprocess carrying no id.
// Nothing can reference it, so no shape can be emitted for it — the rest of the
// model still has to lay out rather than the whole diagram failing.
func TestGenerateDISubProcessWithoutIdSkipped(t *testing.T) {
	src := []byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="P">
	    <startEvent id="S"/>
	    <subProcess name="nameless"><task id="Buried"/></subProcess>
	    <endEvent id="E"/>
	    <sequenceFlow id="f1" sourceRef="S" targetRef="E"/>
	  </process>
	</definitions>`)
	di, ok := generateDI(src)
	if !ok {
		t.Fatal("generateDI: want ok — the id-less subprocess must not sink the diagram")
	}
	shapes := parseShapes(t, di)
	for _, want := range []string{"S", "E"} {
		if _, drawn := shapes[want]; !drawn {
			t.Errorf("%s should still be laid out:\n%s", want, di)
		}
	}
	if _, drawn := shapes["Buried"]; drawn {
		t.Error("an element inside an unreferenceable subprocess must not be placed on the root plane")
	}
}

// TestGenerateDIPureCycleLaysOut covers a model where every node has an incoming
// flow, so there is no entry point to start the main axis from — a closed loop,
// which is malformed BPMN but arrives from hand-edited XML. The layouter has to
// seed the trunk anyway and terminate rather than spin looking for a start.
func TestGenerateDIPureCycleLaysOut(t *testing.T) {
	src := []byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="P">
	    <task id="A"/><task id="B"/><task id="C"/>
	    <sequenceFlow id="f1" sourceRef="A" targetRef="B"/>
	    <sequenceFlow id="f2" sourceRef="B" targetRef="C"/>
	    <sequenceFlow id="f3" sourceRef="C" targetRef="A"/>
	  </process>
	</definitions>`)
	di, ok := generateDI(src)
	if !ok {
		t.Fatal("generateDI: want ok for a closed loop")
	}
	shapes := parseShapes(t, di)
	if len(shapes) != 3 {
		t.Fatalf("shapes = %d, want all 3 nodes placed:\n%s", len(shapes), di)
	}
	for id, s := range shapes {
		if s.w <= 0 || s.h <= 0 {
			t.Errorf("%s has empty bounds %+v", id, s)
		}
	}
	for id, pts := range parseEdges(t, di) {
		if !isOrthogonal(pts) {
			t.Errorf("edge %s is not orthogonal: %v", id, pts)
		}
	}
}

// TestSegmentCutsNode pins the predicate crossesForeignNode is built on. The
// distinction that matters is the one in its doc comment: an edge that runs along
// a node's border, or stops on it, is meeting that node — only passing through the
// interior counts as cutting it.
func TestSegmentCutsNode(t *testing.T) {
	n := lnode{x: 100, y: 100, w: 100, h: 80} // x 100..200, y 100..180
	tests := []struct {
		name string
		p, q point
		want bool
	}{
		{"horizontal through the middle", point{50, 140}, point{250, 140}, true},
		{"vertical through the middle", point{150, 50}, point{150, 250}, true},
		{"horizontal along the top border", point{50, 100}, point{250, 100}, false},
		{"horizontal along the bottom border", point{50, 180}, point{250, 180}, false},
		{"vertical along the left border", point{100, 50}, point{100, 250}, false},
		{"vertical along the right border", point{200, 50}, point{200, 250}, false},
		{"horizontal clear above", point{50, 40}, point{250, 40}, false},
		{"horizontal stopping short", point{20, 140}, point{100, 140}, false},
		{"vertical stopping short", point{150, 20}, point{150, 100}, false},
		{"diagonal is not an orthogonal segment", point{50, 50}, point{250, 250}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := segmentCutsNode(tc.p, tc.q, n); got != tc.want {
				t.Errorf("segmentCutsNode(%v, %v) = %v, want %v", tc.p, tc.q, got, tc.want)
			}
		})
	}
}

// TestWrapWidth covers the label-column estimate at both ends. It divides the
// available width by the line count, then refuses to promise a column narrower
// than a short word — and that floor must never widen the result past the space
// actually available.
func TestWrapWidth(t *testing.T) {
	tests := []struct {
		name              string
		full, lines, want int
	}{
		{"single line takes the full width", 120, 1, 120},
		{"zero lines is treated as one", 120, 0, 120},
		{"two lines split the width", 120, 2, 60},
		{"rounds up so the last line fits", 200, 3, 67},
		{"floors at a short word's width", 100, 8, 36},
		{"the floor never exceeds the width available", 30, 4, 30},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := wrapWidth(tc.full, tc.lines); got != tc.want {
				t.Errorf("wrapWidth(%d, %d) = %d, want %d", tc.full, tc.lines, got, tc.want)
			}
		})
	}
}

// TestRouteCorridorDegenerateCases covers the two shapes routeCorridor guards
// against, both of which the corpus models never produce: columns so close that no
// gap is left to rise in, and a corridor that already sits on both endpoints' line.
func TestRouteCorridorDegenerateCases(t *testing.T) {
	src := lnode{x: 0, y: 0, w: 100, h: 80}

	// Target immediately after the source: the two gutters land on top of each
	// other, so there is no room for a riser and a drop and the corridor route gives
	// way to the ordinary elbow.
	near := lnode{x: 110, y: 200, w: 100, h: 80}
	got := routeCorridor(src, near, 40, 105, 105)
	want := routeFlow(src, near)
	if len(got) != len(want) {
		t.Fatalf("cramped columns: got %v, want the plain route %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("cramped columns: got %v, want the plain route %v", got, want)
		}
	}

	// Source, corridor and target on one line: a straight run, no risers.
	level := lnode{x: 400, y: 0, w: 100, h: 80}
	if got := routeCorridor(src, level, 40, 125, 375); len(got) != 2 {
		t.Errorf("level run: got %v, want two waypoints", got)
	}
}
