package api

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// BPMN DI namespace URIs. bpmn-js resolves diagram interchange by namespace URI,
// so the prefixes we pick are arbitrary as long as they are declared.
const (
	nsBpmnDI = "http://www.omg.org/spec/BPMN/20100524/DI"
	nsOmgDC  = "http://www.omg.org/spec/DD/20100524/DC"
	nsOmgDI  = "http://www.omg.org/spec/DD/20100524/DI"
)

// ensureDiagramLayout returns src unchanged if it already carries BPMN diagram
// interchange (a <BPMNDiagram>); otherwise it generates a simple left-to-right
// layered layout and injects one so bpmn-js can render the model. It is
// best-effort: on any parse or structural problem it returns src unchanged.
//
// This runs when the UI fetches a model's XML — a rendering concern, never the
// engine hot path — so models deployed as pure semantic XML (no layout) still
// show up in the editor and the live overlay.
func ensureDiagramLayout(src []byte) []byte {
	if bytes.Contains(src, []byte("BPMNDiagram")) {
		return src // already laid out
	}
	di, ok := generateDI(src)
	if !ok {
		return src
	}
	return injectBeforeDefinitionsClose(src, di)
}

// relayoutDiagram discards whatever diagram interchange the model already carries
// and generates a fresh left-to-right layout in its place. It backs the Modeler's
// "Auto-layout" button: a diagram a user has tangled by hand is re-flowed by the
// same generator that lays out a layout-less deployed model. Only shape and edge
// coordinates change — the semantic model (processes, flows, ids) is untouched.
//
// Best-effort, like ensureDiagramLayout: if a new layout can't be generated (the
// XML won't parse, or the model has no layout-relevant nodes) src is returned
// unchanged rather than stripped of the layout it had.
func relayoutDiagram(src []byte) []byte {
	// generateDI reads only the semantic elements, so it ignores any existing DI;
	// generate first and only strip the old layout once we have a replacement.
	di, ok := generateDI(src)
	if !ok {
		return src
	}
	return injectBeforeDefinitionsClose(stripDiagramLayout(src), di)
}

// BPMN diagram-interchange blocks to strip before regenerating layout. Two shapes
// occur: a self-closing <BPMNDiagram .../> and a full <BPMNDiagram>…</BPMNDiagram>
// container. The namespace prefix is arbitrary, so both patterns allow any. The
// self-closing form is removed first: its [^>]* stops at the first '>' and so can
// never swallow a container's contents, while the container's non-greedy body then
// matches each remaining block up to its own closing tag.
var (
	bpmnDiagramSelfClose = regexp.MustCompile(`(?is)<\s*([a-z0-9_.]+:)?BPMNDiagram\b[^>]*/\s*>`)
	bpmnDiagramBlock     = regexp.MustCompile(`(?is)<\s*([a-z0-9_.]+:)?BPMNDiagram\b.*?</\s*([a-z0-9_.]+:)?BPMNDiagram\s*>`)
)

// stripDiagramLayout removes every BPMN diagram-interchange block from src, leaving
// the semantic model behind. Whitespace around a removed block is left as-is; the
// regenerated layout is injected separately, before </definitions>.
func stripDiagramLayout(src []byte) []byte {
	src = bpmnDiagramSelfClose.ReplaceAll(src, nil)
	src = bpmnDiagramBlock.ReplaceAll(src, nil)
	return src
}

// --- parsing (independent of the compiler's own XML structs) ---

type layoutDefs struct {
	Processes     []layoutContainer `xml:"process"`
	Collaboration *layoutCollab     `xml:"collaboration"`
}

type layoutCollab struct {
	Id           string              `xml:"id,attr"`
	Participants []layoutParticipant `xml:"participant"`
	MessageFlows []layoutMessageFlow `xml:"messageFlow"`
}

type layoutMessageFlow struct {
	Id        string `xml:"id,attr"`
	SourceRef string `xml:"sourceRef,attr"`
	TargetRef string `xml:"targetRef,attr"`
}

type layoutParticipant struct {
	Id         string `xml:"id,attr"`
	ProcessRef string `xml:"processRef,attr"`
}

// layoutContainer is a process or an (expanded) subprocess: the flow nodes it
// holds, the subprocesses nested inside it, the boundary events attached to its
// activities, and the sequence flows among them. A process and a subProcess share
// this shape, so SubProcesses recurses to arbitrary depth.
type layoutContainer struct {
	Id           string       `xml:"id,attr"`
	StartEvents  []layoutElem `xml:"startEvent"`
	EndEvents    []layoutElem `xml:"endEvent"`
	Tasks        []layoutElem `xml:"task"`
	ServiceTasks []layoutElem `xml:"serviceTask"`
	ScriptTasks  []layoutElem `xml:"scriptTask"`
	UserTasks    []layoutElem `xml:"userTask"`
	ManualTasks  []layoutElem `xml:"manualTask"`
	BizRuleTasks []layoutElem `xml:"businessRuleTask"`
	ReceiveTasks []layoutElem `xml:"receiveTask"`
	SendTasks    []layoutElem `xml:"sendTask"`

	// A call activity delegates to another process. It is drawn as an activity in
	// this plane — the called process has its own diagram.
	CallActivities []layoutElem `xml:"callActivity"`

	ExclusiveGws  []layoutElem `xml:"exclusiveGateway"`
	ParallelGws   []layoutElem `xml:"parallelGateway"`
	InclusiveGws  []layoutElem `xml:"inclusiveGateway"`
	EventBasedGws []layoutElem `xml:"eventBasedGateway"`

	IntermediateCatchEvents []layoutElem `xml:"intermediateCatchEvent"`
	IntermediateThrowEvents []layoutElem `xml:"intermediateThrowEvent"`

	// Expanded subprocesses laid out inline: each is sized to its own contents and
	// its children rendered in the same plane, offset into the box.
	SubProcesses []layoutContainer `xml:"subProcess"`

	// A transaction and an ad-hoc subprocess are subprocesses with extra execution
	// semantics; for layout they are the same shape class — an expanded container
	// whose children are laid out inside it.
	Transactions      []layoutContainer `xml:"transaction"`
	AdHocSubProcesses []layoutContainer `xml:"adHocSubProcess"`

	// Boundary events attach to an activity in this container via attachedToRef and
	// ride its border rather than a grid cell.
	BoundaryEvents []layoutBoundary `xml:"boundaryEvent"`

	// Lane sets partition the container into horizontal swimlanes; each lane claims
	// a set of flow nodes by id and becomes a band the nodes are laid out within.
	LaneSets []layoutLaneSet `xml:"laneSet"`

	Flows []layoutFlow `xml:"sequenceFlow"`
}

// subContainers is every child that is laid out as an expanded container: plain
// subprocesses plus the variants that only differ in execution semantics. Both
// the generator and the layout invariants walk this, so neither can go blind to a
// container class the other knows about.
func (c layoutContainer) subContainers() []layoutContainer {
	if len(c.Transactions) == 0 && len(c.AdHocSubProcesses) == 0 {
		return c.SubProcesses
	}
	subs := make([]layoutContainer, 0, len(c.SubProcesses)+len(c.Transactions)+len(c.AdHocSubProcesses))
	subs = append(subs, c.SubProcesses...)
	subs = append(subs, c.Transactions...)
	subs = append(subs, c.AdHocSubProcesses...)
	return subs
}

type layoutLaneSet struct {
	Lanes []layoutLane `xml:"lane"`
}

type layoutLane struct {
	Id           string   `xml:"id,attr"`
	FlowNodeRefs []string `xml:"flowNodeRef"`
}

type layoutElem struct {
	Id string `xml:"id,attr"`
}

type layoutBoundary struct {
	Id            string `xml:"id,attr"`
	AttachedToRef string `xml:"attachedToRef,attr"`
	Name          string `xml:"name,attr"`
}

type layoutFlow struct {
	Id        string `xml:"id,attr"`
	Name      string `xml:"name,attr"`
	SourceRef string `xml:"sourceRef,attr"`
	TargetRef string `xml:"targetRef,attr"`
}

// nodeKind fixes a shape's footprint.
type nodeKind struct{ w, h int }

var (
	kindEvent   = nodeKind{36, 36}
	kindTask    = nodeKind{100, 80}
	kindGateway = nodeKind{50, 50}
)

// Layout spacing. gapX/gapY separate columns and rows. gapX is 50, which with the
// 100px task width puts columns on the 150px pitch AGENTS.md prescribes for
// hand-authored models — the spacing a reader is used to, and what the diagrams in
// this repository already use. gapY stays roomier: rows carry the horizontal
// corridors that exception flows run along, and an event's caption sits below its
// 36px circle. The sub* paddings size an expanded subprocess around its laid-out
// children — a header strip on top for the title, symmetric side and bottom padding
// — and the sub minimums keep an empty subprocess readable as a container.
const (
	layoutMarginX  = 150
	layoutMarginY  = 90
	layoutGapX     = 50
	layoutGapY     = 60
	subPadX        = 30
	subHeaderTop   = 40
	subPadBottom   = 30
	subMinW        = 200
	subMinH        = 100
	laneLabelStrip = 30 // lane title lane on the left
	lanePadY       = 25 // vertical breathing room above/below a lane's content
	laneMinH       = 80 // a lane reads as a band even when nearly empty
)

// lnode is one node while a single container is being laid out: a flow node
// (event/task/gateway), an expanded subprocess box, or a boundary event. A
// boundary event takes part in layering — so the exception flow leaving it is
// pushed one column past its host — but is positioned on the host's border rather
// than on the grid.
type lnode struct {
	id    string
	w, h  int
	layer int
	x, y  int      // local position, filled during placement
	sub   *laidOut // non-nil for an expanded subprocess: its inner layout
	bound bool     // a boundary event
	gwy   bool     // drawn as a diamond, so it has vertices to leave from
	dummy bool     // a virtual node: reserves space in a layer, emits no shape
	host  string   // boundary: the activity id it is attached to
	label string   // boundary: its name, emitted as an explicit label placed clear of the host
	atTop bool     // boundary: riding the host's top edge (label goes above, else below)

}

// placedShape and placedEdge are the emitted diagram interchange for one container
// (and everything nested inside it), in a single coordinate space.
type placedShape struct {
	id         string
	x, y, w, h int
	expanded   bool // an expanded subprocess box
	horizontal bool // a swimlane band (isHorizontal)

	// The caption this shape needs placed off itself (currently only boundary events:
	// left to bpmn-js, theirs lands on the host's title). placeShapeLabels turns it
	// into a box; labelUp asks for the side the event faces, since it rides one of
	// its host's horizontal borders and the free space is on that side.
	label   string
	labelUp bool

	// The placed label box. Zero when hasLabel is false: bpmn-js then auto-places.
	hasLabel       bool
	labelX, labelY int
	labelW, labelH int
}

type point struct{ x, y int }

type placedEdge struct {
	id  string
	pts []point

	// The flow's caption and where it sits. Zero when hasLabel is false: bpmn-js
	// then falls back to the polyline's midpoint, which for a branch stepping into
	// another row lands on the riser between two arrows.
	label          string
	hasLabel       bool
	labelX, labelY int
	labelW, labelH int
}

// laidOut is a fully positioned container: its shapes and edges in one coordinate
// space rooted at (0,0), and the extent they occupy.
type laidOut struct {
	shapes []placedShape
	edges  []placedEdge
	w, h   int
}

// generateDI parses the model and returns a BPMNDiagram fragment, reporting
// whether it produced anything usable. A collaboration is laid out as stacked
// pools (one per participant); a plain model as a single left-to-right process,
// with nested subprocesses expanded inline and boundary events on their hosts.
func generateDI(src []byte) (string, bool) {
	var defs layoutDefs
	if err := xml.Unmarshal(src, &defs); err != nil {
		return "", false
	}
	if defs.Collaboration != nil && defs.Collaboration.Id != "" && len(defs.Collaboration.Participants) > 0 {
		return generateCollaborationDI(defs)
	}
	if len(defs.Processes) == 0 || defs.Processes[0].Id == "" {
		return "", false
	}
	p := defs.Processes[0]
	lo := layoutOf(p)
	if len(lo.shapes) == 0 {
		return "", false
	}
	lo.shapes = shiftShapes(lo.shapes, layoutMarginX, layoutMarginY)
	lo.edges = shiftEdges(lo.edges, layoutMarginX, layoutMarginY)

	var b strings.Builder
	openPlane(&b, p.Id)
	writeLaidOut(&b, lo)
	closePlane(&b)
	return b.String(), true
}

// layoutOf lays out one container (a process or a subprocess) into a single
// coordinate space rooted at (0,0): a left-to-right layered grid for its flow
// nodes, an expanded box per nested subprocess with its children offset inside,
// and boundary events sitting on their hosts' bottom borders. When the container
// declares swimlanes, its nodes are partitioned into horizontal lane bands (the
// columns stay shared, so the happy path still runs straight across). It returns
// the shapes, edges, and extent occupied. An empty container yields an empty
// laidOut (no shapes), which callers treat as "nothing to draw".
func layoutOf(c layoutContainer) laidOut {
	nodes, inner := containerNodes(c)
	if len(nodes) == 0 {
		return laidOut{}
	}
	idx := make(map[string]int, len(nodes))
	for i, n := range nodes {
		idx[n.id] = i
	}
	back := backEdgeIDs(nodes, idx, c.Flows)
	assignLayers(nodes, idx, c.Flows, back)
	trunk := markTrunk(nodes, idx, c.Flows, back)

	var laneShapes []placedShape
	chains := map[int][]int{}
	if lanes, laneOf := collectLanes(c, nodes); len(lanes) > 0 {
		// Swimlanes own the vertical axis: a node's row is its lane, so there is no
		// free row for a corridor to claim and the lane bands keep edges apart.
		laneShapes = placeLaned(nodes, lanes, laneOf, trunk)
	} else {
		chains = insertDummies(&nodes, idx, c.Flows, back)
		for len(trunk) < len(nodes) {
			trunk = append(trunk, false) // a corridor never holds the main axis
		}
		placeNodes(nodes, trunk, c.Flows, idx, chains)
	}
	placeBoundaries(nodes, idx, c.Flows)

	var lo laidOut
	lo.shapes = append(lo.shapes, laneShapes...) // lane bands before the nodes they hold
	emitShapes(&lo, nodes, inner)
	colRight, colLeft := columnEdges(nodes)
	emitEdges(&lo, nodes, idx, c.Flows, back, colRight, colLeft, loopChannelY(nodes), chains)
	// Captions last, once every shape and edge is placed: both placers fit a box
	// against the finished picture, shapes first because they are more constrained.
	placeShapeLabels(&lo)
	placeFlowLabels(&lo)
	normalize(&lo)
	return lo
}

// loopChannelY picks the y of the horizontal lane a back edge runs along: below
// every node in the container, with a full row gap of clearance. Side branches
// stack upward (see placeNodes), so the space beneath the trunk is free and a loop
// drawn there crosses nothing.
func loopChannelY(nodes []lnode) int {
	bottom := 0
	for i := range nodes {
		if b := nodes[i].y + nodes[i].h; b > bottom {
			bottom = b
		}
	}
	return bottom + layoutGapY
}

// containerNodes gathers the layout nodes of one container: its simple flow nodes
// (events/tasks/gateways), an expanded box per nested subprocess (laid out
// recursively so the box is sized to its contents), and a boundary event per
// attachment whose host is present. The recursively laid-out inner containers are
// returned keyed by subprocess id so emitShapes can offset their children into the
// box.
func containerNodes(c layoutContainer) (nodes []lnode, inner map[string]*laidOut) {
	inner = map[string]*laidOut{}
	add := func(elems []layoutElem, k nodeKind) {
		for _, e := range elems {
			if e.Id != "" {
				nodes = append(nodes, lnode{id: e.Id, w: k.w, h: k.h})
			}
		}
	}
	add(c.StartEvents, kindEvent)
	add(c.EndEvents, kindEvent)
	add(c.IntermediateCatchEvents, kindEvent)
	add(c.IntermediateThrowEvents, kindEvent)
	add(c.Tasks, kindTask)
	add(c.ServiceTasks, kindTask)
	add(c.ScriptTasks, kindTask)
	add(c.UserTasks, kindTask)
	add(c.ManualTasks, kindTask)
	add(c.BizRuleTasks, kindTask)
	add(c.ReceiveTasks, kindTask)
	add(c.SendTasks, kindTask)
	add(c.CallActivities, kindTask)
	addGateway := func(elems []layoutElem) {
		for _, e := range elems {
			if e.Id != "" {
				nodes = append(nodes, lnode{id: e.Id, w: kindGateway.w, h: kindGateway.h, gwy: true})
			}
		}
	}
	addGateway(c.ExclusiveGws)
	addGateway(c.ParallelGws)
	addGateway(c.InclusiveGws)
	addGateway(c.EventBasedGws)

	subs := c.subContainers()
	for i := range subs {
		sp := subs[i]
		if sp.Id == "" {
			continue
		}
		lo := layoutOf(sp)
		w := lo.w + 2*subPadX
		if w < subMinW {
			w = subMinW
		}
		h := lo.h + subHeaderTop + subPadBottom
		if h < subMinH {
			h = subMinH
		}
		inner[sp.Id] = &lo
		nodes = append(nodes, lnode{id: sp.Id, w: w, h: h, sub: &lo})
	}

	// A boundary event only lays out if it attaches to an activity in this
	// container; a dangling attachedToRef is dropped rather than floated at the
	// origin.
	hosts := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		hosts[n.id] = true
	}
	for _, be := range c.BoundaryEvents {
		if be.Id == "" || !hosts[be.AttachedToRef] {
			continue
		}
		nodes = append(nodes, lnode{id: be.Id, w: kindEvent.w, h: kindEvent.h, bound: true, host: be.AttachedToRef, label: strings.TrimSpace(be.Name)})
	}
	return nodes, inner
}

// backEdgeIDs reports which sequence flows close a cycle, keyed by their index in
// flows. A BPMN model is routinely cyclic — a rework loop sends a case back to an
// earlier task — but layering is only meaningful on a DAG: fed a cycle, the
// longest-path relaxation below keeps pushing the loop's target one column further
// on every pass, so a reviewed-and-returned task ends up drawn to the right of the
// nodes that follow it. Classifying the returning flow as a back edge takes it out
// of the forward pass; it is drawn afterwards as an explicit loop.
//
// Detection is a depth-first colouring: an edge into a node still on the recursion
// stack (grey) closes a loop. Roots are visited first and nodes in slice order, so
// which edge of a cycle is called the back edge is deterministic.
func backEdgeIDs(nodes []lnode, idx map[string]int, flows []layoutFlow) map[int]bool {
	back := map[int]bool{}
	adj := make([][]int, len(nodes)) // node index -> outgoing flow indices
	indeg := make([]int, len(nodes))
	for fi, f := range flows {
		s, sok := idx[f.SourceRef]
		t, tok := idx[f.TargetRef]
		if !sok || !tok {
			continue
		}
		adj[s] = append(adj[s], fi)
		indeg[t]++
	}
	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := make([]int, len(nodes))
	var visit func(int)
	visit = func(n int) {
		color[n] = grey
		for _, fi := range adj[n] {
			t := idx[flows[fi].TargetRef]
			switch color[t] {
			case grey:
				back[fi] = true // target is still open: this flow returns into it
			case white:
				visit(t)
			}
		}
		color[n] = black
	}
	for i := range nodes {
		if indeg[i] == 0 && color[i] == white {
			visit(i) // entry points first, so loops are cut at the returning edge
		}
	}
	for i := range nodes {
		if color[i] == white {
			visit(i) // a component reachable only from within a cycle
		}
	}
	return back
}

// assignLayers computes each node's column via longest-path layering over the
// forward sequence flows (back edges are excluded — see backEdgeIDs — so a loop
// cannot inflate its target's column), plus an attachment rule that pins a boundary
// event to its host's layer so the exception flow leaving it lands one column past
// the host. Iteration is capped at the node count as a belt-and-braces bound.
func assignLayers(nodes []lnode, idx map[string]int, flows []layoutFlow, back map[int]bool) {
	for iter := 0; iter < len(nodes); iter++ {
		changed := false
		for i := range nodes {
			if !nodes[i].bound {
				continue
			}
			if h, ok := idx[nodes[i].host]; ok && nodes[i].layer < nodes[h].layer {
				nodes[i].layer = nodes[h].layer
				changed = true
			}
		}
		for fi, f := range flows {
			if back[fi] {
				continue
			}
			s, sok := idx[f.SourceRef]
			t, tok := idx[f.TargetRef]
			if !sok || !tok {
				continue
			}
			if nodes[t].layer < nodes[s].layer+1 {
				nodes[t].layer = nodes[s].layer + 1
				changed = true
			}
		}
		if !changed {
			break
		}
	}
}

// markTrunk traces the primary ("happy") path through the container and reports
// which node indices lie on it. Starting from the entry node (an event with no
// incoming flow, else the lowest-layer node), it walks forward always taking the
// successor that continues furthest — so the trunk is the longest straight run —
// and placeNodes/placeLaned then keep that node on the main axis. Without this,
// which node holds the centre of a shared column is decided by node-slice order
// (grouped by element type), so a side branch's end event can displace the
// happy-path task and bend the main line into a staircase. Boundary events never
// join the trunk; they ride their host. Iteration is capped by the node count so a
// cyclic model still terminates.
func markTrunk(nodes []lnode, idx map[string]int, flows []layoutFlow, back map[int]bool) []bool {
	trunk := make([]bool, len(nodes))
	succ := make([][]int, len(nodes))
	indeg := make([]int, len(nodes))
	for fi, f := range flows {
		if back[fi] {
			continue // a loop's returning edge is not a way onward through the model
		}
		s, sok := idx[f.SourceRef]
		t, tok := idx[f.TargetRef]
		if !sok || !tok {
			continue
		}
		succ[s] = append(succ[s], t)
		indeg[t]++
	}
	start := -1
	pick := func(i int) {
		if start == -1 || nodes[i].layer < nodes[start].layer {
			start = i
		}
	}
	for i := range nodes {
		if !nodes[i].bound && indeg[i] == 0 {
			pick(i)
		}
	}
	if start == -1 { // every node has an incoming flow (a cycle); seed at the shallowest.
		for i := range nodes {
			if !nodes[i].bound {
				pick(i)
			}
		}
	}
	// Walk forward, always continuing to the successor with the longest run still
	// ahead of it (ties keep flow-declaration order), until the path ends or loops
	// back. Boundary events are never sequence-flow targets, so a successor is always
	// a normal flow node.
	//
	// The choice is by remaining path length, not by the successor's own column: a
	// bypass edge jumps straight to a node deep in the model, so "furthest column"
	// picks the shortcut and makes the one-hop detour the happy path, exiling the
	// real sequence to a branch row. Longest-run-ahead follows the main line.
	depth := depthFrom(succ)
	for cur := start; cur != -1 && !trunk[cur]; {
		trunk[cur] = true
		next := -1
		for _, t := range succ[cur] {
			if next == -1 || depth[t] > depth[next] {
				next = t
			}
		}
		cur = next
	}
	return trunk
}

// depthFrom returns, for each node, how many nodes lie on the longest path
// starting there. succ carries only forward flows (back edges are already
// excluded), so this is a DAG walk; the memo doubles as a guard should a cycle
// survive, keeping the recursion finite.
func depthFrom(succ [][]int) []int {
	depth := make([]int, len(succ))
	var walk func(int) int
	walk = func(n int) int {
		if depth[n] != 0 {
			return depth[n]
		}
		depth[n] = 1
		best := 1
		for _, t := range succ[n] {
			if v := 1 + walk(t); v > best {
				best = v
			}
		}
		depth[n] = best
		return best
	}
	for n := range succ {
		walk(n)
	}
	return depth
}

// orderLayer returns the indices of one layer's non-boundary nodes with the trunk
// node first, so it takes the main axis; the rest keep their existing relative
// order below it.
// insertDummies gives every forward flow that skips a layer a virtual node in each
// layer it passes over (ADR-0127, phase 2). The dummies emit no shape; they exist
// to claim a row across those columns, so the edge has a reserved corridor to run
// along instead of being detoured beneath the whole diagram. The returned map is
// flow index -> its dummy node indices, in layer order.
//
// Flows touching a boundary event are left out: one leaves its host's border rather
// than a grid cell, and its handler sits in the next column anyway.
func insertDummies(nodes *[]lnode, idx map[string]int, flows []layoutFlow, back map[int]bool) map[int][]int {
	chains := map[int][]int{}
	for fi, f := range flows {
		if back[fi] || f.Id == "" {
			continue
		}
		s, sok := idx[f.SourceRef]
		t, tok := idx[f.TargetRef]
		if !sok || !tok || (*nodes)[s].bound || (*nodes)[t].bound {
			continue
		}
		from, to := (*nodes)[s].layer+1, (*nodes)[t].layer-1
		if to < from {
			continue // adjacent columns: a short elbow, no corridor needed
		}
		var chain []int
		for l := from; l <= to; l++ {
			*nodes = append(*nodes, lnode{id: fmt.Sprintf("\x00dummy:%d:%d", fi, l), layer: l, dummy: true})
			chain = append(chain, len(*nodes)-1)
		}
		chains[fi] = chain
	}
	return chains
}

// layoutUnit is one thing that claims a row: a single node, or a dummy chain that
// has to keep the same row across every column it spans so its corridor runs
// straight.
type layoutUnit struct {
	members []int // node indices
	lo, hi  int   // column span
}

func orderLayer(idxs []int, trunk []bool) []int {
	sort.SliceStable(idxs, func(a, b int) bool { return trunk[idxs[a]] && !trunk[idxs[b]] })
	return idxs
}

// placeNodes assigns each non-boundary node a local position: columns are spaced by
// the widest node in each preceding column, and nodes sharing a column stack
// upward from the trunk (happy-path) node, which is centered on the main axis
// (y=0). A linear happy path thus renders as one straight line even when node
// heights differ, and side branches — error handlers and the like — rise above it
// rather than dropping below. Boundary events are skipped here and positioned by
// placeBoundaries.
func placeNodes(nodes []lnode, trunk []bool, flows []layoutFlow, idx map[string]int, chains map[int][]int) {
	laneW := map[int]int{}
	maxLayer := 0
	for i := range nodes {
		if nodes[i].bound {
			continue
		}
		l := nodes[i].layer
		if nodes[i].w > laneW[l] {
			laneW[l] = nodes[i].w
		}
		if l > maxLayer {
			maxLayer = l
		}
	}
	colX := make([]int, maxLayer+1)
	for l := 1; l <= maxLayer; l++ {
		colX[l] = colX[l-1] + laneW[l-1] + layoutGapX
	}

	row := assignRows(nodes, trunk, flows, idx, chains)
	maxRow := 0
	rowH := map[int]int{}
	for i := range nodes {
		if nodes[i].bound {
			continue
		}
		if nodes[i].h > rowH[row[i]] {
			rowH[row[i]] = nodes[i].h
		}
		if row[i] > maxRow {
			maxRow = row[i]
		}
	}
	// Row 0 (the trunk) straddles the main axis; higher rows stack above it, each as
	// tall as its tallest node so a branch clears the happy path.
	rowTop := make([]int, maxRow+1)
	rowTop[0] = -rowH[0] / 2
	for r := 1; r <= maxRow; r++ {
		rowTop[r] = rowTop[r-1] - layoutGapY - rowH[r]
	}
	for i := range nodes {
		if nodes[i].bound {
			continue
		}
		l, r := nodes[i].layer, row[i]
		nodes[i].x = colX[l] + (laneW[l]-nodes[i].w)/2
		nodes[i].y = rowTop[r] + (rowH[r]-nodes[i].h)/2 // centered in its row band
	}
}

// assignRows places the trunk on row 0 and every other node on the lowest row above
// it that is free across the node's whole footprint. A branch therefore keeps one
// row across the columns it spans — so it runs straight instead of sagging toward
// the happy path column by column — while two branches that never share a column
// pack onto the same row. Boundary events get no row; placeBoundaries handles them.
//
// A node's footprint is wider than the column it stands in: an edge arriving from
// several columns back travels horizontally at *this* node's row, so those columns
// have to stay clear too. Reserving only the node's own column is what let an
// unrelated branch settle into the corridor of a shared error handler — the
// handler that eight exception flows fan into — leaving the flows nowhere to run.
//
// Footprints are claimed widest first, so the long corridor takes the row nearest
// the trunk and short branches stack above it, rather than a one-column branch
// taking the low row and forcing the corridor to detour around it.
func assignRows(nodes []lnode, trunk []bool, flows []layoutFlow, idx map[string]int, chains map[int][]int) []int {
	row := make([]int, len(nodes))
	used := map[int]map[int]bool{} // column -> rows already taken
	take := func(col, r int) {
		if used[col] == nil {
			used[col] = map[int]bool{}
		}
		used[col][r] = true
	}
	for i := range nodes {
		if !nodes[i].bound && trunk[i] {
			take(nodes[i].layer, 0)
		}
	}

	// Rows are claimed by units, not by single nodes: a dummy chain has to keep one
	// row across every column it spans, or its corridor would zig-zag.
	lo, hi := footprints(nodes, flows, idx)
	units := make([]layoutUnit, 0, len(nodes))
	inChain := make([]bool, len(nodes))
	// Sorted, never map order: the row a corridor ends up on must not depend on Go's
	// map iteration (ADR-0127 requires the generator stay deterministic).
	chainFlows := make([]int, 0, len(chains))
	for fi := range chains {
		chainFlows = append(chainFlows, fi)
	}
	sort.Ints(chainFlows)
	for _, fi := range chainFlows {
		chain := chains[fi]
		if len(chain) == 0 {
			continue
		}
		units = append(units, layoutUnit{members: chain, lo: nodes[chain[0]].layer, hi: nodes[chain[len(chain)-1]].layer})
		for _, n := range chain {
			inChain[n] = true
		}
	}
	for i := range nodes {
		if nodes[i].bound || trunk[i] || inChain[i] {
			continue
		}
		units = append(units, layoutUnit{members: []int{i}, lo: lo[i], hi: hi[i]})
	}

	// Widest footprint first, so a long corridor claims the row nearest the trunk.
	// Among equal footprints, left to right: a unit's row is chosen relative to its
	// predecessors, so those want to be settled first.
	order := make([]int, len(units))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		x, y := units[order[a]], units[order[b]]
		if wx, wy := x.hi-x.lo, y.hi-y.lo; wx != wy {
			return wx > wy
		}
		return x.lo < y.lo
	})

	pred := predecessors(nodes, flows, idx)
	assigned := make([]bool, len(nodes))
	for i := range nodes {
		if !nodes[i].bound && trunk[i] {
			assigned[i] = true // the trunk is already on row 0
		}
	}
	for _, ui := range order {
		u := units[ui]
		// A corridor carries no node of its own, so nothing flows into it to line up
		// with: it takes the row nearest the trunk. A real node lines up with what
		// flows into it.
		want := 1
		if len(u.members) == 1 {
			want = preferredRow(u.members[0], pred, row, assigned, nodes, idx)
		}
		r := nearestFreeRow(used, u.lo, u.hi, want)
		for _, n := range u.members {
			row[n] = r
			assigned[n] = true
		}
		for c := u.lo; c <= u.hi; c++ {
			take(c, r)
		}
	}
	return row
}

// preferredRow is the row a node would sit on to line up with what flows into it:
// the median row of its already-placed predecessors. Lining a node up with its
// predecessor turns the connecting edge into a straight run instead of a bend, and
// keeps two branches from swapping rows midway and drawing their risers on top of
// each other in the same column gap. With nothing placed to guide it, the node
// falls back to the row closest to the trunk.
func preferredRow(i int, pred [][]int, row []int, assigned []bool, nodes []lnode, idx map[string]int) int {
	var rows []int
	for _, p := range pred[i] {
		// A boundary event rides its host, so it predicts its host's row.
		if nodes[p].bound {
			if h, ok := idx[nodes[p].host]; ok {
				p = h
			}
		}
		if assigned[p] {
			rows = append(rows, row[p])
		}
	}
	if len(rows) == 0 {
		return 1
	}
	sort.Ints(rows)
	if m := rows[len(rows)/2]; m > 1 {
		return m
	}
	return 1 // row 0 is the trunk's; the nearest a branch can get is 1
}

// nearestFreeRow returns the row nearest want (never below 1) that is free across
// [lo,hi], preferring the lower row when two are equally near.
func nearestFreeRow(used map[int]map[int]bool, lo, hi, want int) int {
	if want < 1 {
		want = 1
	}
	for d := 0; ; d++ {
		if r := want - d; r >= 1 && freeAcross(used, lo, hi, r) {
			return r
		}
		if r := want + d; d > 0 && freeAcross(used, lo, hi, r) {
			return r
		}
	}
}

// predecessors lists, per node, the nodes that flow into it, in declaration order.
func predecessors(nodes []lnode, flows []layoutFlow, idx map[string]int) [][]int {
	pred := make([][]int, len(nodes))
	for _, f := range flows {
		s, sok := idx[f.SourceRef]
		t, tok := idx[f.TargetRef]
		if !sok || !tok {
			continue
		}
		pred[t] = append(pred[t], s)
	}
	return pred
}

// footprints returns each node's column span: the column it stands in, widened to
// take in every column an incoming multi-column edge crosses on its way here. Such
// an edge runs horizontally at this node's row, so it needs the same row kept clear
// the whole way. An edge from the adjacent column is left out — it is a short elbow
// in the column gap, not a corridor, and counting it would break a two-node branch
// (handler → end event) apart onto separate rows.
func footprints(nodes []lnode, flows []layoutFlow, idx map[string]int) (lo, hi []int) {
	lo = make([]int, len(nodes))
	hi = make([]int, len(nodes))
	for i := range nodes {
		lo[i], hi[i] = nodes[i].layer, nodes[i].layer
	}
	for _, f := range flows {
		s, sok := idx[f.SourceRef]
		t, tok := idx[f.TargetRef]
		if !sok || !tok {
			continue
		}
		if nodes[s].layer+1 < nodes[t].layer && nodes[s].layer < lo[t] {
			lo[t] = nodes[s].layer
		}
	}
	return lo, hi
}

// freeAcross reports whether row r is untaken in every column of [lo,hi].
func freeAcross(used map[int]map[int]bool, lo, hi, r int) bool {
	for c := lo; c <= hi; c++ {
		if used[c][r] {
			return false
		}
	}
	return true
}

// collectLanes flattens the container's lane sets into an ordered lane list and a
// per-node lane assignment (node index -> lane slot, -1 for none). Reporting no
// lanes when none are declared lets the caller fall back to the plain grid. Any
// non-boundary node no lane claims is folded into the first lane so it still lands
// in a band rather than floating outside every swimlane.
func collectLanes(c layoutContainer, nodes []lnode) (lanes []layoutLane, laneOf []int) {
	for _, ls := range c.LaneSets {
		lanes = append(lanes, ls.Lanes...)
	}
	if len(lanes) == 0 {
		return nil, nil
	}
	idx := make(map[string]int, len(nodes))
	for i, n := range nodes {
		idx[n.id] = i
	}
	laneOf = make([]int, len(nodes))
	for i := range laneOf {
		laneOf[i] = -1
	}
	for li, ln := range lanes {
		for _, ref := range ln.FlowNodeRefs {
			if j, ok := idx[strings.TrimSpace(ref)]; ok && laneOf[j] == -1 {
				laneOf[j] = li
			}
		}
	}
	for i := range nodes {
		if !nodes[i].bound && laneOf[i] == -1 {
			laneOf[i] = 0
		}
	}
	return lanes, laneOf
}

// placeLaned positions non-boundary nodes inside horizontal swimlane bands and
// returns the lane band shapes. Columns are shared across lanes (so a flow that
// stays on one row runs straight even as it crosses lanes); within a lane each
// column's stack is centered in the band, and the band is tall enough for its
// busiest column. A boundary event is left to placeBoundaries, riding its host and
// so inheriting the host's lane.
func placeLaned(nodes []lnode, lanes []layoutLane, laneOf []int, trunk []bool) []placedShape {
	laneW := map[int]int{}
	maxLayer := 0
	for i := range nodes {
		if nodes[i].bound {
			continue
		}
		l := nodes[i].layer
		if nodes[i].w > laneW[l] {
			laneW[l] = nodes[i].w
		}
		if l > maxLayer {
			maxLayer = l
		}
	}
	colX := make([]int, maxLayer+1)
	for l := 1; l <= maxLayer; l++ {
		colX[l] = colX[l-1] + laneW[l-1] + layoutGapX
	}
	contentW := colX[maxLayer] + laneW[maxLayer]
	laneWidth := laneLabelStrip + contentW + layoutGapX

	// Node indices grouped by (lane, column).
	type cell struct{ lane, layer int }
	byCell := map[cell][]int{}
	for i := range nodes {
		if nodes[i].bound {
			continue
		}
		c := cell{laneOf[i], nodes[i].layer}
		byCell[c] = append(byCell[c], i)
	}
	// stackHeight is the total height of a column's nodes within a lane.
	stackHeight := func(idxs []int) int {
		h := 0
		for _, ni := range idxs {
			h += nodes[ni].h
		}
		if n := len(idxs); n > 1 {
			h += layoutGapY * (n - 1)
		}
		return h
	}

	var shapes []placedShape
	bandTop := 0
	for li := range lanes {
		laneContentH := 0
		for l := 0; l <= maxLayer; l++ {
			if h := stackHeight(byCell[cell{li, l}]); h > laneContentH {
				laneContentH = h
			}
		}
		bandH := laneContentH + 2*lanePadY
		if bandH < laneMinH {
			bandH = laneMinH
		}
		for l := 0; l <= maxLayer; l++ {
			idxs := orderLayer(byCell[cell{li, l}], trunk)
			y := bandTop + (bandH-stackHeight(idxs))/2
			for _, ni := range idxs {
				nodes[ni].x = laneLabelStrip + colX[l] + (laneW[l]-nodes[ni].w)/2
				nodes[ni].y = y
				y += nodes[ni].h + layoutGapY
			}
		}
		if lanes[li].Id != "" {
			shapes = append(shapes, placedShape{id: lanes[li].Id, x: 0, y: bandTop, w: laneWidth, h: bandH, horizontal: true})
		}
		bandTop += bandH
	}
	return shapes
}

// placeBoundaries sits each boundary event on its host's border — centered when it
// is the only one, spread evenly along the edge when several share a host, and
// straddling the border so bpmn-js snaps it on. The event rides whichever of the
// top/bottom edges faces its exception handler (side branches rise above the trunk,
// so a handler is usually above), so the exception flow leaves toward the handler
// without doubling back across the host. With no resolvable handler it defaults to
// the top edge, matching the upward branch convention.
func placeBoundaries(nodes []lnode, idx map[string]int, flows []layoutFlow) {
	// Each boundary's exception-flow target, so it can ride the facing edge.
	target := map[string]int{}
	for _, f := range flows {
		s, sok := idx[f.SourceRef]
		t, tok := idx[f.TargetRef]
		if sok && tok && nodes[s].bound {
			target[nodes[s].id] = t
		}
	}
	count := map[string]int{}
	for i := range nodes {
		if nodes[i].bound {
			count[nodes[i].host]++
		}
	}
	seen := map[string]int{}
	for i := range nodes {
		if !nodes[i].bound {
			continue
		}
		h, ok := idx[nodes[i].host]
		if !ok {
			continue
		}
		host := nodes[h]
		k := seen[nodes[i].host]
		seen[nodes[i].host]++
		cx := boundaryCenterX(host, k, count[nodes[i].host], nodes[i].w)
		nodes[i].x = cx - nodes[i].w/2
		onBottom := false // default to the top edge (branches rise above the trunk)
		if t, ok := target[nodes[i].id]; ok && nodes[t].y+nodes[t].h/2 > host.y+host.h/2 {
			onBottom = true // handler sits below: ride the bottom edge instead
		}
		if onBottom {
			nodes[i].y = host.y + host.h - nodes[i].h/2
		} else {
			nodes[i].y = host.y - nodes[i].h/2
		}
		nodes[i].atTop = !onBottom
	}
}

// boundaryCenterX places the k-th of n boundary events along its host's top or
// bottom edge. Events are spread evenly across the border, but never closer than
// one event width plus a hair — the even spread alone puts two 36px events on a
// 100px task only 33px apart, so they overlap. When the minimum pitch no longer
// fits inside the border the group is centered on the host and allowed to reach
// slightly past its corners: an event a little off the edge reads far better than
// two drawn on top of each other.
func boundaryCenterX(host lnode, k, n, w int) int {
	pitch := host.w / (n + 1)
	if min := w + 8; pitch < min {
		pitch = min // too tight to draw: fall back to the minimum, overflowing if need be
	}
	span := pitch * (n - 1) // center-to-center width of the whole run
	return host.x + host.w/2 - span/2 + k*pitch
}

// emitShapes writes a shape for every placed node. A subprocess box is marked
// expanded and its inner layout translated into the box (past the header strip)
// and appended, so its children carry absolute coordinates in the same plane.
func emitShapes(lo *laidOut, nodes []lnode, inner map[string]*laidOut) {
	for i := range nodes {
		n := nodes[i]
		if n.dummy {
			continue // a virtual node holds a corridor open; it draws nothing
		}
		if n.sub != nil {
			lo.shapes = append(lo.shapes, placedShape{id: n.id, x: n.x, y: n.y, w: n.w, h: n.h, expanded: true})
			child := inner[n.id]
			lo.shapes = append(lo.shapes, shiftShapes(child.shapes, n.x+subPadX, n.y+subHeaderTop)...)
			lo.edges = append(lo.edges, shiftEdges(child.edges, n.x+subPadX, n.y+subHeaderTop)...)
			continue
		}
		ps := placedShape{id: n.id, x: n.x, y: n.y, w: n.w, h: n.h}
		if n.bound && n.label != "" {
			// Only record what the caption is and which way the event faces; where it
			// goes is placeShapeLabels' job, once every shape and edge exists to be
			// avoided. Computing it here — before the exception flows are routed — is
			// what put a caption under a neighbour's riser.
			ps.label, ps.labelUp = n.label, n.atTop
		}
		lo.shapes = append(lo.shapes, ps)
	}
}

// boundaryLabelWidth estimates the pixel width of a boundary event's caption so its
// label box roughly fits the text (bpmn-js centers the caption on the box). A rough
// per-rune estimate is enough — the box only needs to position the label, and the
// renderer reflows the actual text.
func boundaryLabelWidth(s string) int {
	// 5.5px per rune. bpmn-js writes measured label bounds back into the DI, and in
	// hand-drawn models those come out near 5.2px per rune for the default external-
	// label font ("Session Error" → 67px). Guessing high is not the safe direction:
	// the renderer centers the caption in whatever box it is given, so an oversized
	// box pushes the visible text off the anchor rather than reserving slack.
	w := utf8.RuneCountInString(s) * 11 / 2
	if w < 20 {
		w = 20
	}
	return w
}

// emitEdges writes an edge for each sequence flow whose endpoints are both placed,
// routed orthogonally (AGENTS.md: "clean orthogonal waypoints … never through the
// main-axis boxes"). A flow on the main axis (source and target on the same row) is
// a single straight segment; a branch to another row is an elbow — out, across,
// then in — so the alternate path reads as its own lane instead of a diagonal that
// clips the boxes it passes.
func emitEdges(lo *laidOut, nodes []lnode, idx map[string]int, flows []layoutFlow, back map[int]bool, colRight, colLeft map[int]int, channelY int, chains map[int][]int) {
	lane := 0 // each channel-routed edge gets its own horizontal lane below the nodes
	for fi, f := range flows {
		s, sok := idx[f.SourceRef]
		t, tok := idx[f.TargetRef]
		if !sok || !tok || f.Id == "" {
			continue
		}
		pts := routeFlow(nodes[s], nodes[t])
		// A forward edge that skips columns runs along the corridor its dummy chain
		// reserved (ADR-0127, phase 2), so it stays among the nodes instead of being
		// sent under the diagram.
		if chain, ok := chains[fi]; ok && len(chain) > 0 && !back[fi] {
			through := routeCorridor(nodes[s], nodes[t], nodes[chain[0]].y,
				colRight[nodes[s].layer]+layoutGapX/2, colLeft[nodes[t].layer]-layoutGapX/2)
			if !crossesForeignNode(through, nodes, s, t) {
				pts = through
			}
		}
		// What is left for the channel: a loop's returning edge, and the rare forward
		// route that still cuts a node it does not connect.
		if back[fi] || crossesForeignNode(pts, nodes, s, t) {
			y := channelY + lane*layoutGapY/2
			lane++
			pts = routeChannel(nodes[s], nodes[t],
				colRight[nodes[s].layer]+layoutGapX/2, colLeft[nodes[t].layer]-layoutGapX/2, y)
		}
		lo.edges = append(lo.edges, placedEdge{id: f.Id, pts: pts, label: strings.TrimSpace(f.Name)})
	}
}

// crossesForeignNode reports whether any segment of pts passes through the interior
// of a node other than the edge's own endpoints. Boundary events are skipped: one
// rides its host's border, so an edge leaving the host necessarily brushes it.
func crossesForeignNode(pts []point, nodes []lnode, src, tgt int) bool {
	for i := range nodes {
		if i == src || i == tgt || nodes[i].bound {
			continue
		}
		for k := 1; k < len(pts); k++ {
			if segmentCutsNode(pts[k-1], pts[k], nodes[i]) {
				return true
			}
		}
	}
	return false
}

// segmentCutsNode reports whether the axis-aligned segment p->q passes through n's
// interior. Touching or running along a border does not count — that is how an edge
// meets the node it enters.
func segmentCutsNode(p, q point, n lnode) bool {
	left, right, top, bottom := n.x, n.x+n.w, n.y, n.y+n.h
	if p.y == q.y {
		if p.y <= top || p.y >= bottom {
			return false
		}
		return maxInt(minInt(p.x, q.x), left) < minInt(maxInt(p.x, q.x), right)
	}
	if p.x == q.x {
		if p.x <= left || p.x >= right {
			return false
		}
		return maxInt(minInt(p.y, q.y), top) < minInt(maxInt(p.y, q.y), bottom)
	}
	return false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// routeChannel draws an edge as a U beneath the diagram: out of the source's right
// edge into the gutter past its column, down to a clear channel below every node,
// along it, then up into the target's bottom. It serves the two cases a direct
// route cannot: a loop's returning edge (whose target sits to the left, so a
// forward route would run back across the nodes it loops over) and a bypass that
// skips columns (whose straight line would cross everything in between).
//
// Both vertical legs run in gutters — the gaps after the source's column and before
// the target's — rather than in the columns themselves. A column may hold a node at
// any row, so a leg drawn inside one cuts whatever shares it: descending at the
// source's own center-x cuts the trunk node beneath a branch, and climbing at the
// target's center-x cuts the trunk node beneath a handler that sits above it. A
// gutter is column-free by construction, so neither leg can cross anything.
func routeChannel(src, tgt lnode, gutterOut, gutterIn, channelY int) []point {
	sy := src.y + src.h/2
	ty := tgt.y + tgt.h/2
	return []point{
		{src.x + src.w, sy},
		{gutterOut, sy},
		{gutterOut, channelY},
		{gutterIn, channelY},
		{gutterIn, ty},
		{tgt.x, ty},
	}
}

// routeCorridor draws a forward edge along the row its dummy chain reserved: out of
// the source's right edge, up (or down) to the corridor in the gap after the
// source's column, across every column the chain spans, then down into the target's
// left edge from the gap before it. Both risers stand in a column gap, which holds
// no node by construction, and the horizontal run is on the reserved row — so the
// whole path crosses nothing.
//
// The risers come from the column edges (gutterOut/gutterIn), the same ones
// routeChannel uses, not from the endpoints' own x. An endpoint is only the extreme
// node of its column by coincidence: on labelled-detours the target end event starts
// 32px right of the task sharing its column, so a riser measured from the event
// would have stood inside that task and the route would have been rejected for
// cutting it.
func routeCorridor(src, tgt lnode, corridorY, gutterOut, gutterIn int) []point {
	sx, sy := src.x+src.w, src.y+src.h/2
	tx, ty := tgt.x, tgt.y+tgt.h/2
	riserX, dropX := gutterOut, gutterIn
	if dropX <= riserX {
		return routeFlow(src, tgt)
	}
	if sy == corridorY && corridorY == ty {
		return []point{{sx, sy}, {tx, ty}}
	}
	// A gateway rises out of the vertex facing the corridor rather than out of its
	// side, the same rule routeFlow applies to a branch that steps off the row. That
	// keeps the riser inside the gateway's own column instead of standing in the gap
	// after it — and the gap after a gateway is exactly where its other branches'
	// captions have to fit. On labelled-detours the gap is 50px and the caption of
	// the branch alongside needs 36 of them, so the two cannot share it.
	if src.gwy {
		vx, vy := src.x+src.w/2, src.y
		if corridorY > sy {
			vy = src.y + src.h
		}
		return []point{
			{vx, vy},
			{vx, corridorY},
			{dropX, corridorY},
			{dropX, ty},
			{tx, ty},
		}
	}
	return []point{
		{sx, sy},
		{riserX, sy},
		{riserX, corridorY},
		{dropX, corridorY},
		{dropX, ty},
		{tx, ty},
	}
}

// columnEdges returns each layer's right-most and left-most node edge, so a
// channel-routed edge can run its vertical legs through the gaps beside a column
// instead of through the column itself.
func columnEdges(nodes []lnode) (right, left map[int]int) {
	right, left = map[int]int{}, map[int]int{}
	for i := range nodes {
		if nodes[i].bound {
			continue
		}
		l := nodes[i].layer
		if r, seen := right[l]; !seen || nodes[i].x+nodes[i].w > r {
			right[l] = nodes[i].x + nodes[i].w
		}
		if x, seen := left[l]; !seen || nodes[i].x < x {
			left[l] = nodes[i].x
		}
	}
	return right, left
}

// routeFlow builds the orthogonal waypoints from src to tgt. A normal source leaves
// on its right edge; a boundary event, and a gateway branch that steps off the row,
// drop out of the vertical edge facing the target instead. Every target is entered
// on its left. When the two ends already share a row the run is straight; otherwise a
// vertical riser at the mid-x turns the diagonal into a right-angled out-across-in
// path.
func routeFlow(src, tgt lnode) []point {
	tx, ty := tgt.x, tgt.y+tgt.h/2 // target entry: left-center
	if src.bound {
		return vertexRoute(src, tx, ty) // exception path: out of the edge facing the handler
	}
	sx, sy := src.x+src.w, src.y+src.h/2 // source exit: right-center
	if sy == ty {
		return []point{{sx, sy}, {tx, ty}}
	}
	// A diamond has four vertices and BPMN uses them: the branch that carries on
	// leaves the side, the one that steps to another row leaves the top or the
	// bottom. Sharing the side exit draws both along one line for the width of the
	// gap, so the fork reads as a single edge that splits somewhere out in the open.
	if src.gwy {
		return vertexRoute(src, tx, ty)
	}
	midX := (sx + tx) / 2
	return []point{{sx, sy}, {midX, sy}, {midX, ty}, {tx, ty}}
}

// vertexRoute leaves src by the vertical edge facing (tx,ty) — the top when the
// target sits higher, else the bottom — and elbows across into it. A boundary event
// and a forking gateway route the same way: both hang off the middle of a shape
// whose side exit belongs to another edge.
func vertexRoute(src lnode, tx, ty int) []point {
	vx, vy := src.x+src.w/2, src.y+src.h // default: leave from the bottom
	if ty < src.y+src.h/2 {
		vy = src.y // target above: leave from the top
	}
	if vx == tx {
		return []point{{vx, vy}, {tx, ty}}
	}
	return []point{{vx, vy}, {vx, ty}, {tx, ty}}
}

// normalize shifts a laid-out container so its top-left sits at (0,0) and records
// the extent it occupies. bpmn-js tolerates negative coordinates, but a (0,0)
// origin keeps nested translation and pool sizing simple.
func normalize(lo *laidOut) {
	if len(lo.shapes) == 0 {
		return
	}
	minX, minY := lo.shapes[0].x, lo.shapes[0].y
	for _, s := range lo.shapes {
		if s.x < minX {
			minX = s.x
		}
		if s.y < minY {
			minY = s.y
		}
	}
	// Edges are measured too: a loop's channel runs below every node, so a container
	// sized to its shapes alone would let the returning edge spill out of the box.
	for _, e := range lo.edges {
		for _, p := range e.pts {
			if p.x < minX {
				minX = p.x
			}
			if p.y < minY {
				minY = p.y
			}
		}
	}
	lo.shapes = shiftShapes(lo.shapes, -minX, -minY)
	lo.edges = shiftEdges(lo.edges, -minX, -minY)
	for _, s := range lo.shapes {
		if r := s.x + s.w; r > lo.w {
			lo.w = r
		}
		if b := s.y + s.h; b > lo.h {
			lo.h = b
		}
	}
	for _, e := range lo.edges {
		for _, p := range e.pts {
			if p.x > lo.w {
				lo.w = p.x
			}
			if p.y > lo.h {
				lo.h = p.y
			}
		}
	}
}

// shiftShapes / shiftEdges return copies translated by (dx,dy). They never mutate
// their input, so a recursively laid-out subprocess can be offset into its box
// without disturbing the cached inner layout.
func shiftShapes(ss []placedShape, dx, dy int) []placedShape {
	out := make([]placedShape, len(ss))
	for i, s := range ss {
		s.x += dx
		s.y += dy
		if s.hasLabel {
			s.labelX += dx
			s.labelY += dy
		}
		out[i] = s
	}
	return out
}

// flowLabelH is a caption's line height; its width comes from the same rune
// estimate boundary-event labels use.
const flowLabelH = 14

// labelWraps is how many lines a caption may be reflowed onto, in the order tried.
// Beyond three the box is taller than the line it labels and stops reading as a
// caption, so the search gives up rather than going narrower still.
var labelWraps = []int{1, 2, 3}

// wrapWidth is the box width that reflows a caption of full width onto n lines.
// It is an estimate over an estimate — the renderer breaks at word boundaries, not
// at an exact fraction — so it floors at a width that still fits a short word
// rather than promising a column no text could occupy.
func wrapWidth(full, lines int) int {
	if lines <= 1 {
		return full
	}
	w := (full + lines - 1) / lines
	if w < 36 {
		w = 36
	}
	if w > full {
		w = full
	}
	return w
}

// flowLabelGap is how far a caption sits off its own line — close enough to read as
// belonging to it, far enough not to be struck by it.
const flowLabelGap = 4

// placeFlowLabels gives every named flow a caption box. A label with no position is
// left to the renderer, which puts it at the polyline's midpoint: for a branch that
// steps into another row that is the middle of the riser, stranded between two
// arrows and readable as belonging to either. Candidates run from the source end
// outwards — a gateway's branches are read at the fork — and the first one that
// clears every shape and every other edge wins.
// placeShapeLabels positions the captions shapes carry — today a boundary event's
// name, which bpmn-js would otherwise drop onto the host's title. It runs after
// shapes and edges are placed, so a caption is fitted against the finished picture
// rather than against a guess: the pocket beside an event may be taken by a
// neighbour's exception riser, by another caption, or by the host itself, and only
// the complete layout knows which.
//
// Shape captions are placed before flow captions because they are the more
// constrained of the two — a boundary caption has to stay next to its event, while
// a flow caption may slide anywhere along its line.
func placeShapeLabels(lo *laidOut) {
	for i := range lo.shapes {
		s := &lo.shapes[i]
		if s.label == "" {
			continue
		}
		box := lnode{x: s.x, y: s.y, w: s.w, h: s.h}
		if x, y, w, h, ok := fitLabel(shapeLabelCandidates, box, s.labelUp, s.label, lo); ok {
			s.hasLabel, s.labelX, s.labelY, s.labelW, s.labelH = true, x, y, w, h
			continue
		}
		// Nothing clear anywhere: sit beside the event on the side it faces. An
		// imperfect caption beats leaving it to land on the host's title.
		w, h := boundaryLabelWidth(s.label), flowLabelH
		s.hasLabel, s.labelW, s.labelH = true, w, h
		s.labelX = s.x + s.w + flowLabelGap
		if s.labelUp {
			s.labelY = s.y - h - flowLabelGap
		} else {
			s.labelY = s.y + s.h + flowLabelGap
		}
	}
}

// shapeLabelCandidates rings a shape with caption positions, best first: the side
// the shape faces before the other, and beside it before above or below — a
// boundary caption reads as belonging to its event when it sits next to it.
func shapeLabelCandidates(box lnode, up bool, w, h int) []point {
	near, far := box.y-h-flowLabelGap, box.y+box.h+flowLabelGap // above, below
	if !up {
		near, far = far, near
	}
	right, left := box.x+box.w+flowLabelGap, box.x-w-flowLabelGap
	midY := box.y + box.h/2 - h/2
	return []point{
		{right, near}, {left, near}, // beside, on the facing side
		{right, midY}, {left, midY}, // level with the event
		{box.x + box.w/2 - w/2, near}, // centred on the facing side
		{right, far}, {left, far},     // the far side, last
	}
}

// fitLabel finds a caption box: for each wrap shape in turn, the first candidate
// position that clears every shape, every edge, and every caption already placed.
// Reports whether anything fitted at all.
//
// Trying the wrap shapes outermost means a caption stays on one line wherever one
// line fits, and only reflows where the picture is tight — the renderer wraps text
// into whatever box it is given, so the width is the placer's to spend.
func fitLabel(cands func(lnode, bool, int, int) []point, box lnode, up bool, text string, lo *laidOut) (x, y, w, h int, ok bool) {
	full := boundaryLabelWidth(text)
	for _, lines := range labelWraps {
		w, h := wrapWidth(full, lines), flowLabelH*lines
		for _, c := range cands(box, up, w, h) {
			if labelBoxClear(c, w, h, lo) {
				return c.x, c.y, w, h, true
			}
		}
	}
	return 0, 0, 0, 0, false
}

func placeFlowLabels(lo *laidOut) {
	for i := range lo.edges {
		e := &lo.edges[i]
		if e.label == "" || len(e.pts) < 2 {
			continue
		}
		full := boundaryLabelWidth(e.label)
		// A caption's width is a choice, not a constraint: bpmn-js reflows the text
		// into whatever box it is given, so a caption too wide for the gap beside its
		// line can be asked to wrap and try again. Widen the search over shapes of the
		// same caption — one line, then two, then three — before settling.
		for _, lines := range labelWraps {
			w, h := wrapWidth(full, lines), flowLabelH*lines
			for _, c := range flowLabelCandidates(e.pts, w, h) {
				if labelBoxClear(c, w, h, lo) {
					e.hasLabel, e.labelX, e.labelY, e.labelW, e.labelH = true, c.x, c.y, w, h
					break
				}
			}
			if e.hasLabel {
				break
			}
		}
		// Nothing fits at any shape: take the first candidate anyway. A caption that
		// sits by its own line beats one the renderer drops on the riser.
		if !e.hasLabel {
			if cands := flowLabelCandidates(e.pts, full, flowLabelH); len(cands) > 0 {
				e.hasLabel, e.labelX, e.labelY, e.labelW, e.labelH = true, cands[0].x, cands[0].y, full, flowLabelH
			}
		}
	}
}

// flowLabelCandidates lists caption positions along a polyline, best first: beside
// each segment from the source end, the far side of the line before the near one.
func flowLabelCandidates(pts []point, w, h int) []point {
	var out []point
	for k := 1; k < len(pts); k++ {
		p, q := pts[k-1], pts[k]
		switch {
		case p.y == q.y && p.x != q.x: // horizontal: above, then below
			above, below := p.y-flowLabelGap-h, p.y+flowLabelGap
			for _, x := range alongSegment(p.x, q.x, w) {
				out = append(out, point{x, above}, point{x, below})
			}
		case p.x == q.x && p.y != q.y: // vertical: right, then left
			right, left := p.x+flowLabelGap, p.x-flowLabelGap-w
			for _, y := range alongSegment(p.y, q.y, h) {
				out = append(out, point{right, y}, point{left, y})
			}
		}
	}
	return out
}

// alongSegment lists where a caption of extent size may start along a segment
// running from a to b: centred first, then slid to either end. A caption pinned to
// the midpoint has nowhere to go when something crosses the line exactly there —
// another branch's riser through the gap beside a gateway does precisely that — and
// reflowing it narrower does not help, because a narrower box centred on the same
// point still straddles the crossing.
func alongSegment(a, b, size int) []int {
	lo, hi := minInt(a, b), maxInt(a, b)
	mid := (lo+hi)/2 - size/2
	if hi-lo <= size {
		return []int{mid} // no room to slide
	}
	return []int{mid, hi - size, lo}
}

// labelBoxClear reports whether a caption box at p avoids every shape, is struck by
// no edge, and does not land on a caption already placed. Containers are skipped —
// a label inside an expanded subprocess or a lane band is where it belongs —
// matching the layout invariants.
//
// Checking captions against each other is what lets the two placers share this: a
// boundary caption and a flow caption competing for the same gap would otherwise
// each be "clear" of everything the other could see.
func labelBoxClear(p point, w, h int, lo *laidOut) bool {
	box := lnode{x: p.x, y: p.y, w: w, h: h}
	overlaps := func(x, y, bw, bh int) bool {
		return p.x < x+bw && x < p.x+w && p.y < y+bh && y < p.y+h
	}
	for _, s := range lo.shapes {
		if !s.expanded && !s.horizontal && overlaps(s.x, s.y, s.w, s.h) {
			return false
		}
		if s.hasLabel && overlaps(s.labelX, s.labelY, s.labelW, s.labelH) {
			return false
		}
	}
	for _, e := range lo.edges {
		if e.hasLabel && overlaps(e.labelX, e.labelY, e.labelW, e.labelH) {
			return false
		}
		for k := 1; k < len(e.pts); k++ {
			if segmentCutsNode(e.pts[k-1], e.pts[k], box) {
				return false
			}
		}
	}
	return true
}

func shiftEdges(es []placedEdge, dx, dy int) []placedEdge {
	out := make([]placedEdge, len(es))
	for i, e := range es {
		pts := make([]point, len(e.pts))
		for j, p := range e.pts {
			pts[j] = point{p.x + dx, p.y + dy}
		}
		out[i] = e
		out[i].pts = pts
		if e.hasLabel {
			out[i].labelX, out[i].labelY = e.labelX+dx, e.labelY+dy
		}
	}
	return out
}

// generateCollaborationDI lays out a collaboration as horizontally stacked pools.
// Each participant's process is laid out left-to-right inside its own band; a
// participant with no resolvable/eventful process still gets an (empty) pool so
// the collaboration structure is visible. The plane binds to the collaboration.
func generateCollaborationDI(defs layoutDefs) (string, bool) {
	byID := make(map[string]layoutContainer, len(defs.Processes))
	for _, p := range defs.Processes {
		byID[p.Id] = p
	}

	const (
		poolLeft   = 30
		poolTop0   = 40
		poolGap    = 40
		labelStrip = 30 // pool header lane on the left
		innerPadY  = 30
		emptyPoolH = 120
		emptyPoolW = 600
	)

	var b strings.Builder
	openPlane(&b, defs.Collaboration.Id)

	// Absolute bounds of every emitted shape, keyed by element id, for cross-pool
	// message flow edges (nested nodes included).
	placed := map[string]placedShape{}

	poolTop := poolTop0
	any := false
	for _, part := range defs.Collaboration.Participants {
		if part.Id == "" {
			continue
		}
		proc, ok := byID[part.ProcessRef]
		var lo laidOut
		if ok {
			lo = layoutOf(proc)
		}
		if len(lo.shapes) == 0 {
			// Black-box pool: an empty band, still part of the picture.
			poolShape(&b, part.Id, poolLeft, poolTop, emptyPoolW, emptyPoolH)
			poolTop += emptyPoolH + poolGap
			any = true
			continue
		}
		// Shift the process's nodes into this pool's band, past the label strip and
		// below the band top, and measure the band.
		dx := poolLeft + labelStrip
		dy := poolTop + innerPadY
		lo.shapes = shiftShapes(lo.shapes, dx, dy)
		lo.edges = shiftEdges(lo.edges, dx, dy)
		poolH := lo.h + 2*innerPadY
		poolW := lo.w + labelStrip + innerPadY

		poolShape(&b, part.Id, poolLeft, poolTop, poolW, poolH)
		writeLaidOut(&b, lo)
		for _, s := range lo.shapes {
			placed[s.id] = s
		}
		poolTop += poolH + poolGap
		any = true
	}

	// Message flow edges connect elements across pools: down out of the source's
	// bottom, up into the target's top (pools are stacked vertically).
	for _, mf := range defs.Collaboration.MessageFlows {
		src, sok := placed[mf.SourceRef]
		tgt, tok := placed[mf.TargetRef]
		if !sok || !tok || mf.Id == "" {
			continue
		}
		writeEdge(&b, placedEdge{id: mf.Id, pts: []point{
			{src.x + src.w/2, src.y + src.h},
			{tgt.x + tgt.w/2, tgt.y},
		}})
	}

	closePlane(&b)
	if !any {
		return "", false
	}
	return b.String(), true
}

// openPlane writes the BPMNDiagram + BPMNPlane opening, bound to planeElement (a
// process id for a plain model, the collaboration id for a collaboration).
func openPlane(b *strings.Builder, planeElement string) {
	fmt.Fprintf(b, "\n  <bpmndi:BPMNDiagram xmlns:bpmndi=%q xmlns:omgdc=%q xmlns:omgdi=%q id=\"BPMNDiagram_atlas\">\n",
		nsBpmnDI, nsOmgDC, nsOmgDI)
	fmt.Fprintf(b, "    <bpmndi:BPMNPlane id=\"BPMNPlane_atlas\" bpmnElement=\"%s\">\n", attr(planeElement))
}

func closePlane(b *strings.Builder) {
	b.WriteString("    </bpmndi:BPMNPlane>\n  </bpmndi:BPMNDiagram>\n")
}

// poolShape writes a participant (pool) shape. isHorizontal marks it a lane band
// so bpmn-js renders the pool with its label strip on the left.
func poolShape(b *strings.Builder, id string, x, y, w, h int) {
	fmt.Fprintf(b, "      <bpmndi:BPMNShape id=\"%s\" bpmnElement=\"%s\" isHorizontal=\"true\">\n", attr(id+"_di"), attr(id))
	fmt.Fprintf(b, "        <omgdc:Bounds x=\"%d\" y=\"%d\" width=\"%d\" height=\"%d\"/>\n", x, y, w, h)
	b.WriteString("      </bpmndi:BPMNShape>\n")
}

// writeLaidOut renders a positioned container's shapes then its edges.
func writeLaidOut(b *strings.Builder, lo laidOut) {
	for _, s := range lo.shapes {
		writeShape(b, s)
	}
	for _, e := range lo.edges {
		writeEdge(b, e)
	}
}

// writeShape renders one BPMNShape. An expanded subprocess carries isExpanded so
// bpmn-js draws it as an open container with its children in-plane; a swimlane band
// carries isHorizontal so it renders as a lane.
func writeShape(b *strings.Builder, s placedShape) {
	attrs := ""
	if s.expanded {
		attrs = ` isExpanded="true"`
	}
	if s.horizontal {
		attrs += ` isHorizontal="true"`
	}
	fmt.Fprintf(b, "      <bpmndi:BPMNShape id=\"%s\" bpmnElement=\"%s\"%s>\n", attr(s.id+"_di"), attr(s.id), attrs)
	fmt.Fprintf(b, "        <omgdc:Bounds x=\"%d\" y=\"%d\" width=\"%d\" height=\"%d\"/>\n", s.x, s.y, s.w, s.h)
	if s.hasLabel {
		b.WriteString("        <bpmndi:BPMNLabel>\n")
		fmt.Fprintf(b, "          <omgdc:Bounds x=\"%d\" y=\"%d\" width=\"%d\" height=\"%d\"/>\n", s.labelX, s.labelY, s.labelW, s.labelH)
		b.WriteString("        </bpmndi:BPMNLabel>\n")
	}
	b.WriteString("      </bpmndi:BPMNShape>\n")
}

// writeEdge renders one BPMNEdge for a flow (or message flow) from its ordered
// waypoints — two for a straight run, more for an orthogonal elbow.
func writeEdge(b *strings.Builder, e placedEdge) {
	fmt.Fprintf(b, "      <bpmndi:BPMNEdge id=\"%s\" bpmnElement=\"%s\">\n", attr(e.id+"_di"), attr(e.id))
	for _, p := range e.pts {
		fmt.Fprintf(b, "        <omgdi:waypoint x=\"%d\" y=\"%d\"/>\n", p.x, p.y)
	}
	if e.hasLabel {
		b.WriteString("        <bpmndi:BPMNLabel>\n")
		fmt.Fprintf(b, "          <omgdc:Bounds x=\"%d\" y=\"%d\" width=\"%d\" height=\"%d\"/>\n", e.labelX, e.labelY, e.labelW, e.labelH)
		b.WriteString("        </bpmndi:BPMNLabel>\n")
	}
	b.WriteString("      </bpmndi:BPMNEdge>\n")
}

var definitionsClose = regexp.MustCompile(`(?is)</\s*([a-z0-9_.]+:)?definitions\s*>`)

// injectBeforeDefinitionsClose splices di in just before the closing
// </definitions> tag. If no such tag is found it returns src unchanged.
func injectBeforeDefinitionsClose(src []byte, di string) []byte {
	loc := definitionsClose.FindIndex(src)
	if loc == nil {
		return src
	}
	out := make([]byte, 0, len(src)+len(di))
	out = append(out, src[:loc[0]]...)
	out = append(out, di...)
	out = append(out, src[loc[0]:]...)
	return out
}

// attr escapes a string for use as an XML attribute value's contents.
func attr(s string) string {
	r := strings.NewReplacer(`&`, "&amp;", `<`, "&lt;", `>`, "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
