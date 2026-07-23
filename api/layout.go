package api

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"regexp"
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

// --- parsing (independent of the compiler's own XML structs) ---

type layoutDefs struct {
	Processes     []layoutProcess `xml:"process"`
	Collaboration *layoutCollab   `xml:"collaboration"`
}

type layoutCollab struct {
	Id           string              `xml:"id,attr"`
	Participants []layoutParticipant `xml:"participant"`
}

type layoutParticipant struct {
	Id         string `xml:"id,attr"`
	ProcessRef string `xml:"processRef,attr"`
}

type layoutProcess struct {
	Id           string       `xml:"id,attr"`
	StartEvents  []layoutElem `xml:"startEvent"`
	EndEvents    []layoutElem `xml:"endEvent"`
	Tasks        []layoutElem `xml:"task"`
	ServiceTasks []layoutElem `xml:"serviceTask"`
	UserTasks    []layoutElem `xml:"userTask"`
	ExclusiveGws []layoutElem `xml:"exclusiveGateway"`
	ParallelGws  []layoutElem `xml:"parallelGateway"`
	InclusiveGws []layoutElem `xml:"inclusiveGateway"`
	Flows        []layoutFlow `xml:"sequenceFlow"`
}

type layoutElem struct {
	Id string `xml:"id,attr"`
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

type layoutNode struct {
	id   string
	kind nodeKind
	// filled during layout
	x, y int
}

// generateDI parses the model and returns a BPMNDiagram fragment, reporting
// whether it produced anything usable. A collaboration is laid out as stacked
// pools (one per participant); a plain model as a single left-to-right process.
func generateDI(src []byte) (string, bool) {
	var defs layoutDefs
	if err := xml.Unmarshal(src, &defs); err != nil {
		return "", false
	}
	if defs.Collaboration != nil && defs.Collaboration.Id != "" && len(defs.Collaboration.Participants) > 0 {
		return generateCollaborationDI(defs)
	}
	if len(defs.Processes) == 0 {
		return "", false
	}
	p := defs.Processes[0]
	if p.Id == "" {
		return "", false
	}
	nodes := collectNodes(p)
	if len(nodes) == 0 {
		return "", false
	}
	idx := nodeIndex(nodes)
	positionNodes(nodes, idx, p.Flows)

	var b strings.Builder
	openPlane(&b, p.Id)
	renderShapesAndEdges(&b, nodes, idx, p.Flows)
	closePlane(&b)
	return b.String(), true
}

// collectNodes gathers a process's layout nodes (events, tasks, gateways).
func collectNodes(p layoutProcess) []layoutNode {
	var nodes []layoutNode
	add := func(elems []layoutElem, k nodeKind) {
		for _, e := range elems {
			if e.Id != "" {
				nodes = append(nodes, layoutNode{id: e.Id, kind: k})
			}
		}
	}
	add(p.StartEvents, kindEvent)
	add(p.EndEvents, kindEvent)
	add(p.Tasks, kindTask)
	add(p.ServiceTasks, kindTask)
	add(p.UserTasks, kindTask)
	add(p.ExclusiveGws, kindGateway)
	add(p.ParallelGws, kindGateway)
	add(p.InclusiveGws, kindGateway)
	return nodes
}

func nodeIndex(nodes []layoutNode) map[string]int {
	idx := make(map[string]int, len(nodes))
	for i, n := range nodes {
		idx[n.id] = i
	}
	return idx
}

// generateCollaborationDI lays out a collaboration as horizontally stacked pools.
// Each participant's process is laid out left-to-right inside its own band; a
// participant with no resolvable/eventful process still gets an (empty) pool so
// the collaboration structure is visible. The plane binds to the collaboration.
func generateCollaborationDI(defs layoutDefs) (string, bool) {
	byID := make(map[string]layoutProcess, len(defs.Processes))
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

	poolTop := poolTop0
	any := false
	for _, part := range defs.Collaboration.Participants {
		if part.Id == "" {
			continue
		}
		proc, ok := byID[part.ProcessRef]
		nodes := []layoutNode(nil)
		if ok {
			nodes = collectNodes(proc)
		}
		if len(nodes) == 0 {
			// Black-box pool: an empty band, still part of the picture.
			poolShape(&b, part.Id, poolLeft, poolTop, emptyPoolW, emptyPoolH)
			poolTop += emptyPoolH + poolGap
			any = true
			continue
		}
		idx := nodeIndex(nodes)
		positionNodes(nodes, idx, proc.Flows)

		// Shift the process's nodes into this pool's band (past the label strip and
		// below the band top), and measure the band.
		minY, maxYBot, maxXRight := nodeExtents(nodes)
		yShift := poolTop + innerPadY - minY
		for i := range nodes {
			nodes[i].x += labelStrip
			nodes[i].y += yShift
		}
		poolH := (maxYBot - minY) + 2*innerPadY
		poolW := maxXRight + labelStrip + innerPadY

		poolShape(&b, part.Id, poolLeft, poolTop, poolW, poolH)
		renderShapesAndEdges(&b, nodes, idx, proc.Flows)
		poolTop += poolH + poolGap
		any = true
	}
	closePlane(&b)
	if !any {
		return "", false
	}
	return b.String(), true
}

// nodeExtents returns the min top, max bottom, and max right edge of nodes.
func nodeExtents(nodes []layoutNode) (minY, maxYBot, maxXRight int) {
	minY = nodes[0].y
	for _, n := range nodes {
		if n.y < minY {
			minY = n.y
		}
		if bot := n.y + n.kind.h; bot > maxYBot {
			maxYBot = bot
		}
		if right := n.x + n.kind.w; right > maxXRight {
			maxXRight = right
		}
	}
	return minY, maxYBot, maxXRight
}

// positionNodes assigns each node a coordinate using longest-path layering: a
// node's column is the longest chain of sequence flows reaching it from a source,
// and nodes sharing a column are stacked vertically. Iteration is capped at the
// node count so a cyclic model still terminates.
func positionNodes(nodes []layoutNode, idx map[string]int, flows []layoutFlow) {
	const (
		marginX = 150
		marginY = 90
		colW    = 150
		rowH    = 110
	)

	layer := make([]int, len(nodes))
	for iter := 0; iter < len(nodes); iter++ {
		changed := false
		for _, f := range flows {
			s, sok := idx[f.SourceRef]
			t, tok := idx[f.TargetRef]
			if !sok || !tok {
				continue
			}
			if layer[t] < layer[s]+1 {
				layer[t] = layer[s] + 1
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	rowInLayer := map[int]int{}
	for i := range nodes {
		l := layer[i]
		row := rowInLayer[l]
		rowInLayer[l] = row + 1
		cx := marginX + l*colW
		cy := marginY + row*rowH
		nodes[i].x = cx - nodes[i].kind.w/2
		nodes[i].y = cy - nodes[i].kind.h/2
	}
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

// renderShapesAndEdges writes the BPMNShape for each node and the BPMNEdge for
// each sequence flow, using the nodes' assigned coordinates.
func renderShapesAndEdges(b *strings.Builder, nodes []layoutNode, idx map[string]int, flows []layoutFlow) {
	for _, n := range nodes {
		fmt.Fprintf(b, "      <bpmndi:BPMNShape id=\"%s\" bpmnElement=\"%s\">\n", attr(n.id+"_di"), attr(n.id))
		fmt.Fprintf(b, "        <omgdc:Bounds x=\"%d\" y=\"%d\" width=\"%d\" height=\"%d\"/>\n",
			n.x, n.y, n.kind.w, n.kind.h)
		b.WriteString("      </bpmndi:BPMNShape>\n")
	}

	for _, f := range flows {
		s, sok := idx[f.SourceRef]
		t, tok := idx[f.TargetRef]
		if !sok || !tok || f.Id == "" {
			continue
		}
		x1 := nodes[s].x + nodes[s].kind.w
		y1 := nodes[s].y + nodes[s].kind.h/2
		x2 := nodes[t].x
		y2 := nodes[t].y + nodes[t].kind.h/2
		fmt.Fprintf(b, "      <bpmndi:BPMNEdge id=\"%s\" bpmnElement=\"%s\">\n", attr(f.Id+"_di"), attr(f.Id))
		fmt.Fprintf(b, "        <omgdi:waypoint x=\"%d\" y=\"%d\"/>\n", x1, y1)
		fmt.Fprintf(b, "        <omgdi:waypoint x=\"%d\" y=\"%d\"/>\n", x2, y2)
		b.WriteString("      </bpmndi:BPMNEdge>\n")
	}
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
