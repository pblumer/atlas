package api

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"regexp"
	"sort"
	"strings"
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
	ExclusiveGws []layoutElem `xml:"exclusiveGateway"`
	ParallelGws  []layoutElem `xml:"parallelGateway"`
	InclusiveGws []layoutElem `xml:"inclusiveGateway"`

	IntermediateCatchEvents []layoutElem `xml:"intermediateCatchEvent"`
	IntermediateThrowEvents []layoutElem `xml:"intermediateThrowEvent"`

	// Expanded subprocesses laid out inline: each is sized to its own contents and
	// its children rendered in the same plane, offset into the box.
	SubProcesses []layoutContainer `xml:"subProcess"`

	// Boundary events attach to an activity in this container via attachedToRef and
	// ride its border rather than a grid cell.
	BoundaryEvents []layoutBoundary `xml:"boundaryEvent"`

	// Lane sets partition the container into horizontal swimlanes; each lane claims
	// a set of flow nodes by id and becomes a band the nodes are laid out within.
	LaneSets []layoutLaneSet `xml:"laneSet"`

	Flows []layoutFlow `xml:"sequenceFlow"`
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
}

type layoutFlow struct {
	Id        string `xml:"id,attr"`
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

// Layout spacing. gapX/gapY separate columns and rows; the sub* paddings size an
// expanded subprocess around its laid-out children — a header strip on top for the
// title, symmetric side and bottom padding — and the sub minimums keep an empty
// subprocess readable as a container.
const (
	layoutMarginX  = 150
	layoutMarginY  = 90
	layoutGapX     = 50
	layoutGapY     = 40
	subPadX        = 30
	subHeaderTop   = 40
	subPadBottom   = 30
	subMinW        = 200
	subMinH        = 100
	laneLabelStrip = 30 // lane title lane on the left
	lanePadY       = 20 // vertical breathing room above/below a lane's content
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
	host  string   // boundary: the activity id it is attached to
}

// placedShape and placedEdge are the emitted diagram interchange for one container
// (and everything nested inside it), in a single coordinate space.
type placedShape struct {
	id         string
	x, y, w, h int
	expanded   bool // an expanded subprocess box
	horizontal bool // a swimlane band (isHorizontal)
}

type point struct{ x, y int }

type placedEdge struct {
	id  string
	pts []point
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
	assignLayers(nodes, idx, c.Flows)
	trunk := markTrunk(nodes, idx, c.Flows)

	var laneShapes []placedShape
	if lanes, laneOf := collectLanes(c, nodes); len(lanes) > 0 {
		laneShapes = placeLaned(nodes, lanes, laneOf, trunk)
	} else {
		placeNodes(nodes, trunk)
	}
	placeBoundaries(nodes, idx, c.Flows)

	var lo laidOut
	lo.shapes = append(lo.shapes, laneShapes...) // lane bands before the nodes they hold
	emitShapes(&lo, nodes, inner)
	emitEdges(&lo, nodes, idx, c.Flows)
	normalize(&lo)
	return lo
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
	add(c.ExclusiveGws, kindGateway)
	add(c.ParallelGws, kindGateway)
	add(c.InclusiveGws, kindGateway)

	for i := range c.SubProcesses {
		sp := c.SubProcesses[i]
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
		nodes = append(nodes, lnode{id: be.Id, w: kindEvent.w, h: kindEvent.h, bound: true, host: be.AttachedToRef})
	}
	return nodes, inner
}

// assignLayers computes each node's column via longest-path layering over the
// sequence flows, plus an attachment rule that pins a boundary event to its host's
// layer so the exception flow leaving it lands one column past the host. Iteration
// is capped at the node count so a cyclic model still terminates.
func assignLayers(nodes []lnode, idx map[string]int, flows []layoutFlow) {
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
		for _, f := range flows {
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
func markTrunk(nodes []lnode, idx map[string]int, flows []layoutFlow) []bool {
	trunk := make([]bool, len(nodes))
	succ := make([][]int, len(nodes))
	indeg := make([]int, len(nodes))
	for _, f := range flows {
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
	// Walk forward, always continuing to the successor that reaches the furthest
	// layer (ties keep flow-declaration order), until the path ends or loops back.
	for cur := start; cur != -1 && !trunk[cur]; {
		trunk[cur] = true
		next := -1
		for _, t := range succ[cur] {
			if nodes[t].bound {
				continue
			}
			if next == -1 || nodes[t].layer > nodes[next].layer {
				next = t
			}
		}
		cur = next
	}
	return trunk
}

// orderLayer returns the indices of one layer's non-boundary nodes with the trunk
// node first, so it takes the main axis; the rest keep their existing relative
// order below it.
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
func placeNodes(nodes []lnode, trunk []bool) {
	laneW := map[int]int{}
	maxLayer := 0
	byLayer := map[int][]int{}
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
		byLayer[l] = append(byLayer[l], i)
	}
	colX := make([]int, maxLayer+1)
	for l := 1; l <= maxLayer; l++ {
		colX[l] = colX[l-1] + laneW[l-1] + layoutGapX
	}
	for l := 0; l <= maxLayer; l++ {
		var top int // running top edge; branches stack upward above the trunk
		for row, i := range orderLayer(byLayer[l], trunk) {
			if row == 0 {
				nodes[i].y = -nodes[i].h / 2 // trunk row centered on the main axis
			} else {
				nodes[i].y = top - layoutGapY - nodes[i].h
			}
			nodes[i].x = colX[l] + (laneW[l]-nodes[i].w)/2
			top = nodes[i].y
		}
	}
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
		cx := host.x + host.w*(k+1)/(count[nodes[i].host]+1)
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
	}
}

// emitShapes writes a shape for every placed node. A subprocess box is marked
// expanded and its inner layout translated into the box (past the header strip)
// and appended, so its children carry absolute coordinates in the same plane.
func emitShapes(lo *laidOut, nodes []lnode, inner map[string]*laidOut) {
	for i := range nodes {
		n := nodes[i]
		if n.sub != nil {
			lo.shapes = append(lo.shapes, placedShape{id: n.id, x: n.x, y: n.y, w: n.w, h: n.h, expanded: true})
			child := inner[n.id]
			lo.shapes = append(lo.shapes, shiftShapes(child.shapes, n.x+subPadX, n.y+subHeaderTop)...)
			lo.edges = append(lo.edges, shiftEdges(child.edges, n.x+subPadX, n.y+subHeaderTop)...)
			continue
		}
		lo.shapes = append(lo.shapes, placedShape{id: n.id, x: n.x, y: n.y, w: n.w, h: n.h})
	}
}

// emitEdges writes an edge for each sequence flow whose endpoints are both placed,
// routed orthogonally (AGENTS.md: "clean orthogonal waypoints … never through the
// main-axis boxes"). A flow on the main axis (source and target on the same row) is
// a single straight segment; a branch to another row is an elbow — out, across,
// then in — so the alternate path reads as its own lane instead of a diagonal that
// clips the boxes it passes.
func emitEdges(lo *laidOut, nodes []lnode, idx map[string]int, flows []layoutFlow) {
	for _, f := range flows {
		s, sok := idx[f.SourceRef]
		t, tok := idx[f.TargetRef]
		if !sok || !tok || f.Id == "" {
			continue
		}
		lo.edges = append(lo.edges, placedEdge{id: f.Id, pts: routeFlow(nodes[s], nodes[t])})
	}
}

// routeFlow builds the orthogonal waypoints from src to tgt. A normal source leaves
// on its right edge, a boundary event drops out of the vertical edge facing its
// target (top when the handler sits above, else bottom); every target is entered on
// its left. When the two ends already share a row the run is straight; otherwise a
// vertical riser at the mid-x turns the diagonal into a right-angled out-across-in
// path.
func routeFlow(src, tgt lnode) []point {
	tx, ty := tgt.x, tgt.y+tgt.h/2 // target entry: left-center
	if src.bound {
		// Exception path: out of the edge facing the handler, then across into it.
		sx, sy := src.x+src.w/2, src.y+src.h // default: leave from the bottom
		if ty < src.y+src.h/2 {
			sy = src.y // handler above: leave from the top
		}
		if sx == tx {
			return []point{{sx, sy}, {tx, ty}}
		}
		return []point{{sx, sy}, {sx, ty}, {tx, ty}}
	}
	sx, sy := src.x+src.w, src.y+src.h/2 // source exit: right-center
	if sy == ty {
		return []point{{sx, sy}, {tx, ty}}
	}
	midX := (sx + tx) / 2
	return []point{{sx, sy}, {midX, sy}, {midX, ty}, {tx, ty}}
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
}

// shiftShapes / shiftEdges return copies translated by (dx,dy). They never mutate
// their input, so a recursively laid-out subprocess can be offset into its box
// without disturbing the cached inner layout.
func shiftShapes(ss []placedShape, dx, dy int) []placedShape {
	out := make([]placedShape, len(ss))
	for i, s := range ss {
		s.x += dx
		s.y += dy
		out[i] = s
	}
	return out
}

func shiftEdges(es []placedEdge, dx, dy int) []placedEdge {
	out := make([]placedEdge, len(es))
	for i, e := range es {
		pts := make([]point, len(e.pts))
		for j, p := range e.pts {
			pts[j] = point{p.x + dx, p.y + dy}
		}
		out[i] = placedEdge{id: e.id, pts: pts}
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
		writeEdge(&b, mf.Id, []point{
			{src.x + src.w/2, src.y + src.h},
			{tgt.x + tgt.w/2, tgt.y},
		})
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
		writeEdge(b, e.id, e.pts)
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
	b.WriteString("      </bpmndi:BPMNShape>\n")
}

// writeEdge renders one BPMNEdge for a flow (or message flow) from its ordered
// waypoints — two for a straight run, more for an orthogonal elbow.
func writeEdge(b *strings.Builder, id string, pts []point) {
	fmt.Fprintf(b, "      <bpmndi:BPMNEdge id=\"%s\" bpmnElement=\"%s\">\n", attr(id+"_di"), attr(id))
	for _, p := range pts {
		fmt.Fprintf(b, "        <omgdi:waypoint x=\"%d\" y=\"%d\"/>\n", p.x, p.y)
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
