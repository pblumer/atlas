package mimimport

import (
	"fmt"
	"strings"
)

// box is a node's rectangle on the diagram plane, in BPMN DI coordinates.
type box struct{ x, y, w, h int }

func (b box) cy() int { return b.y + b.h/2 }

// nodeSize returns the conventional BPMN shape size for a node kind.
func nodeSize(kind string) (int, int) {
	switch kind {
	case "startEvent", "endEvent":
		return 36, 36
	case "exclusiveGateway", "parallelGateway":
		return 50, 50
	default: // userTask, serviceTask, task
		return 100, 80
	}
}

// layout places every node on a simple left-to-right layered grid: the column
// (x) is the node's longest-path distance from the start event, and rows (y)
// stack nodes that share a column. It is deliberately simple — not crossing-
// minimised — but it renders immediately and can be re-flowed in the Modeler.
// Back edges (a while loop's return) do not count towards a column but are drawn
// like any other edge.
func (b *builder) layout() map[string]box {
	const (
		colGap  = 200
		rowGap  = 160
		originX = 160
		originY = 120
	)
	start := ""
	for _, n := range b.nodes {
		if n.kind == "startEvent" {
			start = n.id
			break
		}
	}
	layer := b.layers(start)

	// Assign each node a row within its column, in declaration order for stability.
	rows := map[int]int{}
	boxes := make(map[string]box, len(b.nodes))
	for _, n := range b.nodes {
		l := layer[n.id] // unreached nodes default to column 0
		row := rows[l]
		rows[l]++
		w, h := nodeSize(n.kind)
		cx := originX + l*colGap
		cy := originY + row*rowGap
		boxes[n.id] = box{x: cx, y: cy - h/2, w: w, h: h}
	}
	return boxes
}

// emitDI writes the BPMNDiagram/BPMNPlane with a shape per node and a two-point
// edge per sequence flow, wired centre-right of the source to centre-left of the
// target.
func (b *builder) emitDI(s *strings.Builder, boxes map[string]box) {
	fmt.Fprintf(s, "  <bpmndi:BPMNDiagram id=\"BPMNDiagram_1\">\n")
	fmt.Fprintf(s, "    <bpmndi:BPMNPlane id=\"BPMNPlane_1\" bpmnElement=%q>\n", b.report.ProcessID)
	for _, n := range b.nodes {
		bx := boxes[n.id]
		fmt.Fprintf(s, "      <bpmndi:BPMNShape id=%q bpmnElement=%q>\n", "di_"+n.id, n.id)
		fmt.Fprintf(s, "        <dc:Bounds x=\"%d\" y=\"%d\" width=\"%d\" height=\"%d\"/>\n", bx.x, bx.y, bx.w, bx.h)
		s.WriteString("      </bpmndi:BPMNShape>\n")
	}
	for _, f := range b.flows {
		sb, tb := boxes[f.from], boxes[f.to]
		fmt.Fprintf(s, "      <bpmndi:BPMNEdge id=%q bpmnElement=%q>\n", "di_"+f.id, f.id)
		fmt.Fprintf(s, "        <di:waypoint x=\"%d\" y=\"%d\"/>\n", sb.x+sb.w, sb.cy())
		fmt.Fprintf(s, "        <di:waypoint x=\"%d\" y=\"%d\"/>\n", tb.x, tb.cy())
		s.WriteString("      </bpmndi:BPMNEdge>\n")
	}
	s.WriteString("    </bpmndi:BPMNPlane>\n")
	s.WriteString("  </bpmndi:BPMNDiagram>\n")
}

// layers assigns each node its column: the longest path from the start event
// over the graph with back edges removed.
//
// Longest, not shortest: a split that both enters an activity and bypasses it —
// a guarded MIMWAL activity, an empty if/else branch — reaches the merge in one
// hop and through the activity in two. Shortest-path layering would then put the
// merge in the same column as the activity and draw the edge between them
// pointing backwards. Nodes the start event cannot reach keep column 0, as
// before.
func (b *builder) layers(start string) map[string]int {
	layer := map[string]int{}
	if start == "" {
		return layer
	}
	out := map[string][]int{} // node id → indices of its outgoing flows
	for i, f := range b.flows {
		out[f.from] = append(out[f.from], i)
	}

	// A depth-first walk marks the flows that close a cycle: an edge into a node
	// still on the walk's own stack cannot be a forward edge.
	const (
		onStack = 1
		done    = 2
	)
	state := map[string]int{}
	back := map[int]bool{}
	var walk func(string)
	walk = func(u string) {
		state[u] = onStack
		for _, i := range out[u] {
			switch v := b.flows[i].to; state[v] {
			case onStack:
				back[i] = true
			case done:
			default:
				walk(v)
			}
		}
		state[u] = done
	}
	walk(start)

	// Longest path over what is left, in topological order.
	indeg := map[string]int{}
	for i, f := range b.flows {
		if back[i] || state[f.from] != done {
			continue // a back edge, or an edge out of a node the start cannot reach
		}
		indeg[f.to]++
	}
	for q := []string{start}; len(q) > 0; {
		u := q[0]
		q = q[1:]
		for _, i := range out[u] {
			if back[i] {
				continue
			}
			v := b.flows[i].to
			if l := layer[u] + 1; l > layer[v] {
				layer[v] = l
			}
			if indeg[v]--; indeg[v] == 0 {
				q = append(q, v)
			}
		}
	}
	return layer
}
