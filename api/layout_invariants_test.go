package api

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"testing"
)

// Layout quality, expressed as executable predicates over generated DI.
//
// The rest of the layout tests assert coordinates ("this label's right edge is at
// the event's center-x"). That pins the implementation, not the goal: when the
// generator improves, such a test fails even though the picture got better — and
// it stays silent about every model it doesn't name. The invariants here say what
// a *good* layout is instead — nothing overlaps, edges don't cut through boxes,
// the flow reads left to right — and are checked across a corpus of models. A
// change is then judged by whether the picture is sound, not by whether a pixel
// moved.
//
// Adding a model to layoutCorpus is the cheapest way to protect a shape class:
// every invariant applies to it automatically.

// layoutCase is one corpus entry: a name for failure messages and the semantic
// BPMN to lay out (deliberately DI-free, so generateDI has to synthesize).
type layoutCase struct {
	name string
	src  string
}

// layoutCorpus spans the structures the generator has to handle. Each entry is a
// shape class, not a specific past bug — a regression in any of them shows up as
// an invariant violation rather than as a coordinate mismatch.
var layoutCorpus = []layoutCase{
	{"linear", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="Start"/><serviceTask id="A"/><serviceTask id="B"/><endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="A"/>
    <sequenceFlow id="f2" sourceRef="A" targetRef="B"/>
    <sequenceFlow id="f3" sourceRef="B" targetRef="End"/>
  </process></definitions>`},

	// A rework loop: the reviewer sends the case back. The back edge must not drag
	// its target off to the right of the nodes that follow it.
	{"rework-loop", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="Start"/><serviceTask id="Review"/><exclusiveGateway id="Ok"/>
    <serviceTask id="Rework"/><endEvent id="Done"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Review"/>
    <sequenceFlow id="f2" sourceRef="Review" targetRef="Ok"/>
    <sequenceFlow id="f3" sourceRef="Ok" targetRef="Done"/>
    <sequenceFlow id="f4" sourceRef="Ok" targetRef="Rework"/>
    <sequenceFlow id="f5" sourceRef="Rework" targetRef="Review"/>
  </process></definitions>`},

	// A bypass: a short-circuit edge runs parallel to the real main sequence. The
	// long run is the happy path; the shortcut must not seize it.
	{"bypass", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="Start"/><exclusiveGateway id="Split"/>
    <serviceTask id="A"/><serviceTask id="B"/><serviceTask id="C"/><endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Split"/>
    <sequenceFlow id="f2" sourceRef="Split" targetRef="A"/>
    <sequenceFlow id="f3" sourceRef="A" targetRef="B"/>
    <sequenceFlow id="f4" sourceRef="B" targetRef="C"/>
    <sequenceFlow id="f5" sourceRef="C" targetRef="End"/>
    <sequenceFlow id="f6" sourceRef="Split" targetRef="End"/>
  </process></definitions>`},

	// The Invoke-DummyAction shape: a linear process with one boundary error handler.
	{"boundary-error", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="Start"/><serviceTask id="Init"/><serviceTask id="Execute"/>
    <serviceTask id="Log"/><endEvent id="EndSuccess"/>
    <boundaryEvent id="Err" name="Error" attachedToRef="Execute"/>
    <serviceTask id="Handle"/><endEvent id="EndError"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Init"/>
    <sequenceFlow id="f2" sourceRef="Init" targetRef="Execute"/>
    <sequenceFlow id="f3" sourceRef="Execute" targetRef="Log"/>
    <sequenceFlow id="f4" sourceRef="Log" targetRef="EndSuccess"/>
    <sequenceFlow id="e1" sourceRef="Err" targetRef="Handle"/>
    <sequenceFlow id="e2" sourceRef="Handle" targetRef="EndError"/>
  </process></definitions>`},

	// Two boundary events on one host, so their labels compete for the same corner.
	{"two-boundaries", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="Start"/><serviceTask id="Call"/><endEvent id="Done"/>
    <boundaryEvent id="Timeout" name="Timeout" attachedToRef="Call"/>
    <boundaryEvent id="Fault" name="Fault" attachedToRef="Call"/>
    <serviceTask id="Retry"/><serviceTask id="Abort"/>
    <endEvent id="E1"/><endEvent id="E2"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Call"/>
    <sequenceFlow id="f2" sourceRef="Call" targetRef="Done"/>
    <sequenceFlow id="t1" sourceRef="Timeout" targetRef="Retry"/>
    <sequenceFlow id="t2" sourceRef="Retry" targetRef="E1"/>
    <sequenceFlow id="u1" sourceRef="Fault" targetRef="Abort"/>
    <sequenceFlow id="u2" sourceRef="Abort" targetRef="E2"/>
  </process></definitions>`},

	// A parallel split/join: three branches share the columns between the gateways.
	{"gateway-fan", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="Start"/><parallelGateway id="Fork"/>
    <serviceTask id="A"/><serviceTask id="B"/><serviceTask id="C"/>
    <parallelGateway id="Join"/><endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Fork"/>
    <sequenceFlow id="a1" sourceRef="Fork" targetRef="A"/>
    <sequenceFlow id="b1" sourceRef="Fork" targetRef="B"/>
    <sequenceFlow id="c1" sourceRef="Fork" targetRef="C"/>
    <sequenceFlow id="a2" sourceRef="A" targetRef="Join"/>
    <sequenceFlow id="b2" sourceRef="B" targetRef="Join"/>
    <sequenceFlow id="c2" sourceRef="C" targetRef="Join"/>
    <sequenceFlow id="f2" sourceRef="Join" targetRef="End"/>
  </process></definitions>`},

	{"lanes", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <laneSet>
      <lane id="L1"><flowNodeRef>Start</flowNodeRef><flowNodeRef>Ask</flowNodeRef></lane>
      <lane id="L2"><flowNodeRef>Approve</flowNodeRef><flowNodeRef>Done</flowNodeRef></lane>
    </laneSet>
    <startEvent id="Start"/><userTask id="Ask"/><userTask id="Approve"/><endEvent id="Done"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Ask"/>
    <sequenceFlow id="f2" sourceRef="Ask" targetRef="Approve"/>
    <sequenceFlow id="f3" sourceRef="Approve" targetRef="Done"/>
  </process></definitions>`},

	{"nested-subprocess", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="Start"/>
    <subProcess id="Sub">
      <startEvent id="SubStart"/><serviceTask id="SubTask"/><endEvent id="SubEnd"/>
      <sequenceFlow id="s1" sourceRef="SubStart" targetRef="SubTask"/>
      <sequenceFlow id="s2" sourceRef="SubTask" targetRef="SubEnd"/>
    </subProcess>
    <endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Sub"/>
    <sequenceFlow id="f2" sourceRef="Sub" targetRef="End"/>
  </process></definitions>`},

	// Activity shapes beyond the plain task list: a call activity delegating to
	// another process, and the two message-carrying tasks. All are drawn as tasks.
	{"call-and-message-tasks", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="Start"/><callActivity id="Call"/><receiveTask id="Await"/>
    <sendTask id="Notify"/><endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Call"/>
    <sequenceFlow id="f2" sourceRef="Call" targetRef="Await"/>
    <sequenceFlow id="f3" sourceRef="Await" targetRef="Notify"/>
    <sequenceFlow id="f4" sourceRef="Notify" targetRef="End"/>
  </process></definitions>`},

	// An event-based gateway racing a message against a timer: the deferred choice
	// fans out like any other gateway.
	{"event-based-gateway", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="Start"/><eventBasedGateway id="Race"/>
    <intermediateCatchEvent id="Reply"/><intermediateCatchEvent id="Timeout"/>
    <endEvent id="EndReply"/><endEvent id="EndTimeout"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Race"/>
    <sequenceFlow id="f2" sourceRef="Race" targetRef="Reply"/>
    <sequenceFlow id="f3" sourceRef="Race" targetRef="Timeout"/>
    <sequenceFlow id="f4" sourceRef="Reply" targetRef="EndReply"/>
    <sequenceFlow id="f5" sourceRef="Timeout" targetRef="EndTimeout"/>
  </process></definitions>`},

	// A transaction: a container that holds its own flow, like a subprocess, and
	// carries a compensation handler off to the side.
	{"transaction", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="Start"/>
    <transaction id="Book">
      <startEvent id="TStart"/><serviceTask id="Reserve"/><endEvent id="TEnd"/>
      <sequenceFlow id="t1" sourceRef="TStart" targetRef="Reserve"/>
      <sequenceFlow id="t2" sourceRef="Reserve" targetRef="TEnd"/>
    </transaction>
    <boundaryEvent id="Cancelled" name="Cancelled" attachedToRef="Book"/>
    <serviceTask id="Undo"/><endEvent id="EndUndo"/><endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Book"/>
    <sequenceFlow id="f2" sourceRef="Book" targetRef="End"/>
    <sequenceFlow id="c1" sourceRef="Cancelled" targetRef="Undo"/>
    <sequenceFlow id="c2" sourceRef="Undo" targetRef="EndUndo"/>
  </process></definitions>`},
}

// TestLayoutInvariants runs every invariant over every corpus model. A failure
// names the model and the specific rule, so the offending picture is identifiable
// without reading coordinates.
func TestLayoutInvariants(t *testing.T) {
	for _, tc := range layoutCorpus {
		t.Run(tc.name, func(t *testing.T) {
			di, ok := generateDI([]byte(tc.src))
			if !ok {
				t.Fatalf("generateDI returned not-ok for corpus model %q", tc.name)
			}
			m := newLayoutModel(t, tc.src, di)
			m.checkCompleteness(t)
			m.checkNoShapeOverlap(t)
			m.checkEdgesOrthogonal(t)
			m.checkNoEdgeThroughShape(t)
			m.checkLabelsClear(t)
			m.checkForwardEdgesReadLeftToRight(t)
			m.checkTrunkCarriesLongestChain(t)
		})
	}
}

// layoutModel pairs the generated geometry with the semantic roles that decide
// which overlaps are legitimate: a pool, lane, or expanded subprocess is *meant*
// to contain other shapes, and a boundary event is *meant* to straddle its host's
// border. Roles come from the same parser the generator uses, so the checker and
// the generator can't disagree about what an element is.
type layoutModel struct {
	name   string
	di     string
	shapes map[string]shapeBox
	labels map[string]shapeBox
	edges  map[string][]point

	host     map[string]string // boundary event id -> the activity it rides
	flows    []layoutFlow      // every sequence flow, all nesting levels
	nodeIDs  []string          // every flow node that should have a shape
	backEdge map[string]bool   // flow ids that close a cycle
	hasLanes bool              // swimlanes: the flow is meant to step between bands
}

func newLayoutModel(t *testing.T, src, di string) *layoutModel {
	t.Helper()
	var defs layoutDefs
	if err := xml.Unmarshal([]byte(src), &defs); err != nil {
		t.Fatalf("corpus model does not parse: %v", err)
	}
	m := &layoutModel{
		di:     di,
		shapes: parseShapes(t, di),
		labels: parseLabels(t, di),
		edges:  parseEdges(t, di),
		host:   map[string]string{},
	}
	for _, p := range defs.Processes {
		m.collect(p)
	}
	m.backEdge = findBackEdges(m.nodeIDs, m.flows)
	return m
}

// collect walks a container and everything nested in it, recording the flow nodes
// that must be drawn, the boundary attachments, and the sequence flows.
func (m *layoutModel) collect(c layoutContainer) {
	for _, group := range [][]layoutElem{
		c.StartEvents, c.EndEvents, c.Tasks, c.ServiceTasks, c.ScriptTasks,
		c.UserTasks, c.ManualTasks, c.BizRuleTasks, c.ReceiveTasks, c.SendTasks,
		c.CallActivities, c.ExclusiveGws, c.ParallelGws, c.InclusiveGws,
		c.EventBasedGws, c.IntermediateCatchEvents, c.IntermediateThrowEvents,
	} {
		for _, e := range group {
			if e.Id != "" {
				m.nodeIDs = append(m.nodeIDs, e.Id)
			}
		}
	}
	for _, be := range c.BoundaryEvents {
		if be.Id != "" {
			m.nodeIDs = append(m.nodeIDs, be.Id)
			m.host[be.Id] = be.AttachedToRef
		}
	}
	m.flows = append(m.flows, c.Flows...)
	if len(c.LaneSets) > 0 {
		m.hasLanes = true
	}
	for _, sp := range c.subContainers() {
		if sp.Id != "" {
			m.nodeIDs = append(m.nodeIDs, sp.Id)
		}
		m.collect(sp)
	}
}

// isContainer reports whether a shape is one that legitimately holds others: a
// pool or lane band (isHorizontal) or an expanded subprocess (isExpanded).
func (m *layoutModel) isContainer(id string) bool {
	s := m.shapes[id]
	return s.expanded || s.horizontal
}

// --- invariants ---

// checkCompleteness: every flow node gets a shape and every sequence flow an edge.
// A layout that silently drops an element is worse than an ugly one — the picture
// no longer shows the model.
func (m *layoutModel) checkCompleteness(t *testing.T) {
	t.Helper()
	for _, id := range m.nodeIDs {
		if _, ok := m.shapes[id]; !ok {
			t.Errorf("invariant[completeness]: flow node %q has no shape", id)
		}
	}
	for _, f := range m.flows {
		if f.Id == "" {
			continue
		}
		if _, ok := m.edges[f.Id]; !ok {
			t.Errorf("invariant[completeness]: sequence flow %q has no edge", f.Id)
		}
	}
}

// checkNoShapeOverlap: no two shapes share interior area, except where the BPMN
// says they should — a container holding its children, or a boundary event
// straddling the border of the activity it is attached to.
func (m *layoutModel) checkNoShapeOverlap(t *testing.T) {
	t.Helper()
	ids := sortedKeys(m.shapes)
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			a, b := ids[i], ids[j]
			if m.isContainer(a) || m.isContainer(b) {
				continue // containers are meant to enclose other shapes
			}
			if m.host[a] == b || m.host[b] == a {
				continue // a boundary event rides its host's border by design
			}
			if overlapArea(m.shapes[a], m.shapes[b]) {
				t.Errorf("invariant[no-shape-overlap]: %q %v overlaps %q %v",
					a, box(m.shapes[a]), b, box(m.shapes[b]))
			}
		}
	}
}

// checkEdgesOrthogonal: every edge runs in axis-aligned segments. Diagonals read
// as sloppy and cut corners across whatever they pass.
func (m *layoutModel) checkEdgesOrthogonal(t *testing.T) {
	t.Helper()
	for _, id := range sortedKeys(m.edges) {
		pts := m.edges[id]
		if len(pts) < 2 {
			t.Errorf("invariant[orthogonal]: edge %q has %d waypoint(s)", id, len(pts))
			continue
		}
		if !isOrthogonal(pts) {
			t.Errorf("invariant[orthogonal]: edge %q has a diagonal segment: %v", id, pts)
		}
	}
}

// checkNoEdgeThroughShape: no edge crosses the interior of a shape that is not one
// of its own endpoints. This is the rule AGENTS.md states for hand-authored models
// ("never through the main-axis boxes") and the generator owes the same. Containers
// are exempt: an edge inside a subprocess or across a lane band legitimately runs
// within that box.
func (m *layoutModel) checkNoEdgeThroughShape(t *testing.T) {
	t.Helper()
	endpoints := map[string][2]string{}
	for _, f := range m.flows {
		endpoints[f.Id] = [2]string{f.SourceRef, f.TargetRef}
	}
	for _, fid := range sortedKeys(m.edges) {
		pts := m.edges[fid]
		ends := endpoints[fid]
		for _, sid := range sortedKeys(m.shapes) {
			if sid == ends[0] || sid == ends[1] || m.isContainer(sid) {
				continue
			}
			// A boundary event on this edge's source is part of the same attachment,
			// and the exception flow leaves right at it.
			if m.host[ends[0]] == sid || m.host[sid] == ends[0] {
				continue
			}
			for k := 1; k < len(pts); k++ {
				if segmentCutsBox(pts[k-1], pts[k], m.shapes[sid]) {
					t.Errorf("invariant[no-edge-through-shape]: edge %q segment %v->%v cuts through %q %v",
						fid, pts[k-1], pts[k], sid, box(m.shapes[sid]))
					break
				}
			}
		}
	}
}

// checkLabelsClear: an explicit label box overlaps no shape and no edge. This is
// the rule the boundary-event caption kept breaking — stated once, it holds for
// every label the generator ever emits, whichever corner it picks.
func (m *layoutModel) checkLabelsClear(t *testing.T) {
	t.Helper()
	for _, owner := range sortedKeys(m.labels) {
		lbl := m.labels[owner]
		for _, sid := range sortedKeys(m.shapes) {
			if sid == owner || m.isContainer(sid) {
				continue
			}
			if overlapArea(lbl, m.shapes[sid]) {
				t.Errorf("invariant[label-clear]: label of %q %v overlaps shape %q %v",
					owner, box(lbl), sid, box(m.shapes[sid]))
			}
		}
		for _, fid := range sortedKeys(m.edges) {
			pts := m.edges[fid]
			for k := 1; k < len(pts); k++ {
				if segmentCutsBox(pts[k-1], pts[k], lbl) {
					t.Errorf("invariant[label-clear]: label of %q %v is crossed by edge %q segment %v->%v",
						owner, box(lbl), fid, pts[k-1], pts[k])
					break
				}
			}
		}
	}
}

// checkForwardEdgesReadLeftToRight: every sequence flow that does not close a cycle
// advances to the right (or stays in the same column). A process reads left to
// right, so a forward step that moves backwards means the layering lost the plot —
// which is exactly what an unhandled loop does to longest-path layering. Back edges
// are exempt: a rework loop is *supposed* to return to an earlier column.
func (m *layoutModel) checkForwardEdgesReadLeftToRight(t *testing.T) {
	t.Helper()
	for _, f := range m.flows {
		if f.Id == "" || m.backEdge[f.Id] {
			continue
		}
		src, sok := m.shapes[f.SourceRef]
		tgt, tok := m.shapes[f.TargetRef]
		if !sok || !tok {
			continue // reported by checkCompleteness
		}
		if tgt.x < src.x {
			t.Errorf("invariant[left-to-right]: forward flow %q runs backwards: %q at x=%d -> %q at x=%d",
				f.Id, f.SourceRef, src.x, f.TargetRef, tgt.x)
		}
	}
}

// checkTrunkCarriesLongestChain: the main axis — the center line shared by the most
// nodes — must carry at least as many nodes as the longest chain of sequence flows.
// In other words the happy path is the *longest* run through the model and it is
// drawn straight. Without this, a short bypass edge can be mistaken for the main
// line and the real sequence gets exiled to a branch row: structurally sound, but
// the picture tells the wrong story. Lane models are exempt — there the flow is
// meant to step between bands.
func (m *layoutModel) checkTrunkCarriesLongestChain(t *testing.T) {
	t.Helper()
	if m.hasLanes {
		return
	}
	byAxis := map[int]int{}
	for _, id := range m.nodeIDs {
		if m.host[id] != "" {
			continue // boundary events ride their host, not the grid
		}
		s, ok := m.shapes[id]
		if !ok {
			continue
		}
		byAxis[s.y+s.h/2]++
	}
	onAxis := 0
	for _, n := range byAxis {
		if n > onAxis {
			onAxis = n
		}
	}
	if chain := m.longestChain(); onAxis < chain {
		t.Errorf("invariant[trunk-longest]: main axis carries %d node(s) but the longest chain is %d — "+
			"the happy path is not drawn straight (a shortcut may have taken the main line)", onAxis, chain)
	}
}

// longestChain returns the number of nodes on the longest path of forward sequence
// flows. Back edges are excluded so a loop doesn't make the chain unbounded.
func (m *layoutModel) longestChain() int {
	adj := map[string][]string{}
	for _, f := range m.flows {
		if f.Id == "" || m.backEdge[f.Id] || m.host[f.SourceRef] != "" {
			continue // skip loops and exception flows leaving a boundary event
		}
		adj[f.SourceRef] = append(adj[f.SourceRef], f.TargetRef)
	}
	memo := map[string]int{}
	var walk func(string) int
	walk = func(n string) int {
		if v, ok := memo[n]; ok {
			return v
		}
		memo[n] = 1 // guards against any residual cycle
		best := 1
		for _, t := range adj[n] {
			if v := 1 + walk(t); v > best {
				best = v
			}
		}
		memo[n] = best
		return best
	}
	best := 0
	for _, id := range m.nodeIDs {
		if v := walk(id); v > best {
			best = v
		}
	}
	return best
}

// --- geometry helpers ---

// overlapArea reports whether two boxes share interior area. Touching borders is
// fine — adjacent is not overlapping.
func overlapArea(a, b shapeBox) bool {
	return a.x < b.right() && b.x < a.right() && a.y < b.bottom() && b.y < a.bottom()
}

// segmentCutsBox reports whether the axis-aligned segment p->q passes through the
// interior of box. Running along a border, or merely touching it (as an edge does
// where it meets the node it enters), does not count.
func segmentCutsBox(p, q point, box shapeBox) bool {
	if p.y == q.y { // horizontal run: must be strictly inside the vertical span
		if p.y <= box.y || p.y >= box.bottom() {
			return false
		}
		return maxi(mini(p.x, q.x), box.x) < mini(maxi(p.x, q.x), box.right())
	}
	if p.x == q.x { // vertical run: must be strictly inside the horizontal span
		if p.x <= box.x || p.x >= box.right() {
			return false
		}
		return maxi(mini(p.y, q.y), box.y) < mini(maxi(p.y, q.y), box.bottom())
	}
	return false // diagonal: reported by the orthogonality invariant instead
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func mini(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// findBackEdges marks the sequence flows that close a cycle, via a DFS colouring:
// an edge to a node currently on the recursion stack is a back edge. Nodes are
// visited in declaration order so the result is deterministic.
func findBackEdges(nodeIDs []string, flows []layoutFlow) map[string]bool {
	out := map[string]bool{}
	adj := map[string][]layoutFlow{}
	for _, f := range flows {
		adj[f.SourceRef] = append(adj[f.SourceRef], f)
	}
	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := map[string]int{}
	var visit func(string)
	visit = func(n string) {
		color[n] = grey
		for _, f := range adj[n] {
			switch color[f.TargetRef] {
			case grey:
				out[f.Id] = true // target is on the stack: this edge closes a loop
			case white:
				visit(f.TargetRef)
			}
		}
		color[n] = black
	}
	for _, n := range nodeIDs {
		if color[n] == white {
			visit(n)
		}
	}
	return out
}

// parseLabels extracts the explicit BPMNLabel bounds per element, for the shapes
// that carry one. Shapes without a label are absent from the result.
func parseLabels(t *testing.T, di string) map[string]shapeBox {
	t.Helper()
	out := map[string]shapeBox{}
	for _, m := range labelShapeRe.FindAllStringSubmatch(di, -1) {
		atoi := func(s string) int {
			n, err := strconv.Atoi(s)
			if err != nil {
				t.Fatalf("bad number %q in DI: %v", s, err)
			}
			return n
		}
		out[m[1]] = shapeBox{x: atoi(m[2]), y: atoi(m[3]), w: atoi(m[4]), h: atoi(m[5])}
	}
	return out
}

var labelShapeRe = regexp.MustCompile(
	`(?s)<bpmndi:BPMNShape id="[^"]*" bpmnElement="([^"]*)"[^>]*>\s*` +
		`<omgdc:Bounds[^/]*/>\s*<bpmndi:BPMNLabel>\s*` +
		`<omgdc:Bounds x="(-?\d+)" y="(-?\d+)" width="(\d+)" height="(\d+)"/>`)

// sortedKeys returns a map's keys in a stable order, so failures are reported
// deterministically rather than in Go's randomized map order.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// box renders bounds compactly for failure messages.
func box(s shapeBox) string {
	return fmt.Sprintf("(%d,%d %dx%d)", s.x, s.y, s.w, s.h)
}
