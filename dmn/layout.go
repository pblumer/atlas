package dmn

import (
	"encoding/xml"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// DMN diagram interchange, generated (ADR-0325).
//
// A DMN model has a semantic half — decisions, input data, requirements, decision
// tables — and a diagram half, the <dmndi:DMNDI> block saying where each element is
// drawn. dmn-js needs the second to render the first, and most models that reach
// Atlas carry only the first: an agent writing a decision table over MCP writes
// logic, not a picture, and so does temis, and so does a hand.
//
// This is the DMN counterpart of api/layout for BPMN, and the same decision
// (ADR-0124): generate the diagram in Go, on the server, with one generator behind
// two entry points — EnsureDiagram when a model is read, RegenerateDiagram when an
// author presses Auto-layout. It is deliberately free of any engine dependency: it
// reads DMN XML with its own minimal structs and writes DMN-DI back, nothing more,
// and it runs only when a human opens or re-arranges a model.
//
// A decision requirements graph is not a process. There is no happy path to keep
// straight and no port model to get right, so the layered placement *is* the
// layout: what a decision requires sits below it, and what shares a layer is spread
// sideways.

// The diagram-interchange namespaces. dmn-js resolves diagram interchange by
// namespace URI, so the prefixes are arbitrary as long as they are declared — which
// they are, on the injected block itself, so the model's own root element is never
// rewritten.
//
// DMNDI is versioned and DC/DI are not. dmn-js binds the two DMNDI URIs to the DMN
// version the document is opened as, and exactly one DC and one DI URI for both. A
// diagram written in the wrong DMNDI namespace therefore does not fail loudly: the
// model opens and its layout is simply not there, which is how a 1.5 model came to
// be uneditable while a read-only view of it rendered (#994).
const (
	nsDMNDI13 = "https://www.omg.org/spec/DMN/20191111/DMNDI/"
	nsDMNDI15 = "https://www.omg.org/spec/DMN/20230324/DMNDI/"
	nsDC      = "http://www.omg.org/spec/DMN/20180521/DC/"
	nsDI      = "http://www.omg.org/spec/DMN/20180521/DI/"

	// The MODEL namespace that picks nsDMNDI15. Every other value — 1.3, an older
	// draft, a typo — keeps 1.3, which is what every already-stored model was
	// written under and what the editor's own seed emits.
	nsModel15 = "https://www.omg.org/spec/DMN/20230324/MODEL/"
)

// dmndiFor answers which DMNDI namespace belongs with a model's own MODEL
// namespace. It is the whole of the version decision, stated once, so the read path
// and the Auto-layout path cannot come to disagree about it
// (ADR-0379).
func dmndiFor(modelNS string) string {
	if modelNS == nsModel15 {
		return nsDMNDI15
	}
	return nsDMNDI13
}

// The drawn size of each kind of node, and the space between them. The sizes are
// dmn-js's own defaults, so a generated diagram opens looking like one drawn in the
// editor rather than one that has to be tidied first.
const (
	decisionW = 180.0
	decisionH = 80.0
	inputW    = 125.0
	inputH    = 45.0
	gapX      = 50.0
	gapY      = 80.0
	padding   = 60.0
)

// EnsureDiagram completes a DMN model's diagram so dmn-js can draw it, and returns
// the model unchanged when it already has one. Best-effort: anything it cannot
// parse or lay out comes back untouched rather than mangled.
func EnsureDiagram(src []byte) []byte {
	out, _ := EnsureDiagramReport(src)
	return out
}

// EnsureDiagramReport is EnsureDiagram, additionally reporting whether it drew
// anything — which a caller storing the result wants to know.
//
// Three cases, and the third is the one this exists for:
//
//   - the diagram covers every node → unchanged. Somebody placed these.
//   - there is no diagram → one is generated.
//   - the diagram covers *some* nodes → the whole graph is laid out afresh.
//
// A partial diagram is not a half-finished human arrangement; no DMN tool emits
// one on purpose. It is the residue of a tool that drew what it could and saved
// back only that, so the coordinates in it were invented by the thing that could
// not draw the rest. Completing around them would mean placing the missing nodes in
// whatever space was left rather than laying out the graph the model describes.
func EnsureDiagramReport(src []byte) ([]byte, bool) {
	g, ok := parseDRG(src)
	if !ok {
		return src, false
	}
	if g.fullyDrawn() {
		return src, false
	}
	di, ok := generateDMNDI(g)
	if !ok {
		return src, false
	}
	return injectBeforeDefinitionsClose(stripDMNDI(src), di), true
}

// RegenerateDiagram discards whatever diagram the model carries and lays the whole
// graph out afresh. It backs the editor's Auto-layout action: the author asking for
// it, which is the one thing that moves a diagram somebody placed.
func RegenerateDiagram(src []byte) []byte {
	g, ok := parseDRG(src)
	if !ok {
		return src
	}
	di, ok := generateDMNDI(g)
	if !ok {
		return src
	}
	return injectBeforeDefinitionsClose(stripDMNDI(src), di)
}

// --- parsing (its own structs, independent of temis) ---

// drgNode is one element of the requirements graph, with the ids it requires. kind
// decides how large it is drawn; requires decides how high.
type drgNode struct {
	id       string
	isInput  bool
	requires []string
}

// drgEdge is one requirement, drawn from the required element to the one that
// requires it — the direction a DMN arrow points.
type drgEdge struct {
	id     string
	source string
	target string
}

// drgService is one decision service: the box to draw, and the decisions it draws
// around, split by the compartment each belongs in. `inputDecision` is not here
// because DMN draws it OUTSIDE the box — it names the boundary the caller supplies
// (services.go).
type drgService struct {
	id           string
	outputs      []string
	encapsulated []string
}

// drg is a model's requirements graph plus which of its nodes the model's own
// diagram already draws.
type drg struct {
	nodes []drgNode
	edges []drgEdge
	// services are the decision services drawn around parts of that graph. They are
	// not nodes: they take no layer and require nothing, they are boxes placed
	// around what they hold once everything else has a position.
	services []drgService
	drawn    map[string]bool
	// modelNS is the document's own MODEL namespace, kept because the diagram to be
	// written has to match the DMN version the model is in — see dmndiFor.
	modelNS string
}

// fullyDrawn reports whether the model's diagram already covers every node and
// every decision service. A graph with no nodes counts as drawn: there is nothing
// to draw.
//
// A service counts. A model whose decisions are all placed but whose service has no
// shape is the residue of a tool that does not know DMN 1.5, and leaving it alone
// is not neutral: dmn-js then invents an empty default box, and because it rereads
// membership from geometry, the next save writes that emptiness back into the
// document — a service can lose its output decision and start returning nothing.
func (g drg) fullyDrawn() bool {
	for _, n := range g.nodes {
		if !g.drawn[n.id] {
			return false
		}
	}
	for _, s := range g.services {
		if !g.drawn[s.id] {
			return false
		}
	}
	return true
}

type xmlDefs struct {
	// XMLName records the root element as the parser resolved it, so modelNS below
	// is the namespace the document actually declares rather than one guessed from
	// the bytes.
	XMLName   xml.Name
	InputData []xmlElem     `xml:"inputData"`
	Decisions []xmlDecision `xml:"decision"`
	BKMs      []xmlElem     `xml:"businessKnowledgeModel"`
	Services  []xmlService  `xml:"decisionService"`
	DI        []xmlDiagrams `xml:"DMNDI"`
}

type xmlElem struct {
	ID string `xml:"id,attr"`
}

type xmlDecision struct {
	ID                      string           `xml:"id,attr"`
	InformationRequirements []xmlRequirement `xml:"informationRequirement"`
	KnowledgeRequirements   []xmlRequirement `xml:"knowledgeRequirement"`
}

// xmlRequirement is one requirement edge. DMN spells the far end three ways
// depending on what is required; exactly one of them is present.
type xmlRequirement struct {
	ID                string  `xml:"id,attr"`
	RequiredInput     *xmlRef `xml:"requiredInput"`
	RequiredDecision  *xmlRef `xml:"requiredDecision"`
	RequiredKnowledge *xmlRef `xml:"requiredKnowledge"`
}

type xmlRef struct {
	Href string `xml:"href,attr"`
}

type xmlDiagrams struct {
	Diagrams []xmlDiagram `xml:"DMNDiagram"`
}

type xmlDiagram struct {
	Shapes []xmlShape `xml:"DMNShape"`
}

type xmlShape struct {
	Ref string `xml:"dmnElementRef,attr"`
}

// parseDRG reads the requirements graph and the set of elements the model's own
// diagram draws. It reports false when there is nothing to lay out — the XML does
// not parse, or the model declares no drawable node.
func parseDRG(src []byte) (drg, bool) {
	var defs xmlDefs
	if err := xml.Unmarshal(src, &defs); err != nil {
		return drg{}, false
	}
	g := drg{drawn: map[string]bool{}, modelNS: defs.XMLName.Space}
	for _, in := range defs.InputData {
		if in.ID != "" {
			g.nodes = append(g.nodes, drgNode{id: in.ID, isInput: true})
		}
	}
	for _, bkm := range defs.BKMs {
		if bkm.ID != "" {
			g.nodes = append(g.nodes, drgNode{id: bkm.ID})
		}
	}
	for _, d := range defs.Decisions {
		if d.ID == "" {
			continue
		}
		n := drgNode{id: d.ID}
		reqs := append(append([]xmlRequirement{}, d.InformationRequirements...), d.KnowledgeRequirements...)
		for _, req := range reqs {
			from := localRef(req.RequiredInput, req.RequiredDecision, req.RequiredKnowledge)
			if from == "" {
				continue // an external href points at another model; there is nothing here to draw
			}
			n.requires = append(n.requires, from)
			g.edges = append(g.edges, drgEdge{id: req.ID, source: from, target: d.ID})
		}
		g.nodes = append(g.nodes, n)
	}
	for _, svc := range defs.Services {
		if svc.ID == "" {
			continue
		}
		g.services = append(g.services, drgService{
			id:           svc.ID,
			outputs:      localRefs(svc.Outputs),
			encapsulated: localRefs(svc.Encapsulated),
		})
	}
	if len(g.nodes) == 0 {
		return drg{}, false
	}
	for _, block := range defs.DI {
		for _, d := range block.Diagrams {
			for _, sh := range d.Shapes {
				if sh.Ref != "" {
					g.drawn[sh.Ref] = true
				}
			}
		}
	}
	return g, true
}

// localRef returns the id the first present reference points at, for a reference
// inside this model ("#id"). An href naming another document resolves to nothing
// here, and is reported as empty so the caller can skip it.
func localRef(refs ...*xmlRef) string {
	for _, r := range refs {
		if r == nil || r.Href == "" {
			continue
		}
		if strings.HasPrefix(r.Href, "#") {
			return strings.TrimPrefix(r.Href, "#")
		}
		return ""
	}
	return ""
}

// localRefs is localRef over a list, keeping only the references that name
// something in this model.
func localRefs(refs []xmlRef) []string {
	var out []string
	for i := range refs {
		if id := localRef(&refs[i]); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// --- placement ---

// placed is one node with the box it was given.
type placed struct {
	node          drgNode
	x, y, w, h    float64
	layer, column int
}

// layout assigns every node a layer — zero for a node that requires nothing, one
// more than the deepest thing it requires otherwise — and then a box.
//
// Layer zero is drawn at the bottom and the deepest layer at the top, so a DRG
// reads the way DMN draws one: information flows upward, and what a decision
// requires sits under it.
func layout(g drg) []placed {
	known := map[string]bool{}
	for _, n := range g.nodes {
		known[n.id] = true
	}
	requires := map[string][]string{}
	for _, n := range g.nodes {
		for _, r := range n.requires {
			if known[r] {
				requires[n.id] = append(requires[n.id], r)
			}
		}
	}
	// A decision service draws its output decisions above the divider and its
	// encapsulated ones below, so the layers have to come out that way round. The
	// DRG usually says so already — an output decision is what the encapsulated ones
	// feed — but nothing in DMN requires it, and a layout that contradicts the
	// declared membership is worse than no layout at all: dmn-js rereads membership
	// from the geometry, so a picture that put an encapsulated decision on top would
	// rewrite the document into saying so. These constraints only deepen a layer;
	// they are never drawn as edges.
	for _, svc := range g.services {
		for _, out := range svc.outputs {
			if !known[out] {
				continue
			}
			for _, enc := range svc.encapsulated {
				if known[enc] && enc != out {
					requires[out] = append(requires[out], enc)
				}
			}
		}
	}
	// depth, memoised, with the walk's own path carried along: a requirement cycle
	// is not a legal DRG but it can be in a file, and a layouter that recursed into
	// one would never return.
	depth := map[string]int{}
	var depthOf func(id string, path map[string]bool) int
	depthOf = func(id string, path map[string]bool) int {
		if d, ok := depth[id]; ok {
			return d
		}
		if path[id] {
			return 0 // a cycle: stop here rather than follow it round again
		}
		path[id] = true
		d := 0
		for _, r := range requires[id] {
			if rd := depthOf(r, path) + 1; rd > d {
				d = rd
			}
		}
		delete(path, id)
		depth[id] = d
		return d
	}
	maxDepth := 0
	for _, n := range g.nodes {
		if d := depthOf(n.id, map[string]bool{}); d > maxDepth {
			maxDepth = d
		}
	}

	// Nodes keep the model's own order within a layer, so the same model always
	// produces the same picture — except that a service's members are pulled
	// together first, so the box drawn around them later is a rectangle holding its
	// own members rather than a band across whatever else shares the row.
	group := serviceGroups(g)
	byLayer := map[int][]drgNode{}
	for _, n := range g.nodes {
		byLayer[depth[n.id]] = append(byLayer[depth[n.id]], n)
	}
	for l := range byLayer {
		row := byLayer[l]
		sort.SliceStable(row, func(i, j int) bool { return group[row[i].id] < group[row[j].id] })
	}
	layers := make([]int, 0, len(byLayer))
	for l := range byLayer {
		layers = append(layers, l)
	}
	sort.Ints(layers)

	// Each layer is as tall as its tallest node, and rows are stacked from the
	// deepest layer down so the y of a layer does not depend on what is above it.
	rowTop := map[int]float64{}
	y := padding
	for i := maxDepth; i >= 0; i-- {
		rowTop[i] = y
		h := inputH
		for _, n := range byLayer[i] {
			if !n.isInput {
				h = decisionH
				break
			}
		}
		y += h + gapY
	}

	out := make([]placed, 0, len(g.nodes))
	for _, l := range layers {
		x := padding
		for col, n := range byLayer[l] {
			w, h := decisionW, decisionH
			if n.isInput {
				w, h = inputW, inputH
			}
			out = append(out, placed{node: n, x: x, y: rowTop[l], w: w, h: h, layer: l, column: col})
			x += w + gapX
		}
	}
	return out
}

// serviceMargin is the space between a decision service's box and the members it
// is drawn around.
const serviceMargin = 30.0

// The box a decision service gets when none of its members could be placed — an
// empty service, or one naming only decisions this model does not declare. It is
// still drawn, because a service with no shape is exactly the state that makes
// dmn-js invent one (fullyDrawn).
const (
	emptyServiceW = 300.0
	emptyServiceH = 200.0
)

// serviceGroups ranks each node by the decision service that draws it, so a row
// can put one service's members side by side. A node no service draws sorts last,
// and a node two services both name belongs, for drawing, to the first that claims
// it — the picture can only nest it once.
func serviceGroups(g drg) map[string]int {
	group := make(map[string]int, len(g.nodes))
	// Everything starts unclaimed, which sorts behind every service: a decision no
	// service draws — a service's own inputDecision among them — has to end up
	// beside the box, not inside it.
	for _, n := range g.nodes {
		group[n.id] = len(g.services)
	}
	for i, svc := range g.services {
		for _, id := range append(append([]string{}, svc.outputs...), svc.encapsulated...) {
			if claimed, ok := group[id]; ok && claimed == len(g.services) {
				group[id] = i
			}
		}
	}
	return group
}

// placedService is one decision service's box and the divider that splits it.
type placedService struct {
	id         string
	x, y, w, h float64
	dividerY   float64
}

// serviceBoxes draws each decision service around the members that were placed:
// the bounding box of its decisions plus a margin, with the divider line between
// the lowest output decision and the highest encapsulated one. Layering already
// guarantees that order (layout), so the line always has a gap to sit in.
//
// A service whose members all landed in one compartment keeps the other one open
// rather than empty-looking: the divider goes just inside the far edge, which is
// also what an author dragging a decision in there would produce.
func serviceBoxes(g drg, places []placed) []placedService {
	at := make(map[string]placed, len(places))
	bottom := 0.0
	for _, p := range places {
		at[p.node.id] = p
		if y := p.y + p.h; y > bottom {
			bottom = y
		}
	}
	// Where a service with nothing to draw around goes: its own row under the graph,
	// so it neither overlaps the picture nor disappears.
	emptyX := padding
	emptyY := bottom + gapY

	out := make([]placedService, 0, len(g.services))
	for _, svc := range g.services {
		box, ok := boundsAround(at, svc)
		if !ok {
			out = append(out, placedService{
				id: svc.id, x: emptyX, y: emptyY,
				w: emptyServiceW, h: emptyServiceH,
				dividerY: emptyY + emptyServiceH/2,
			})
			emptyX += emptyServiceW + gapX
			continue
		}
		out = append(out, box)
	}
	return out
}

// boundsAround computes one service's box from the members that were placed. It
// reports false when none were.
func boundsAround(at map[string]placed, svc drgService) (placedService, bool) {
	var (
		minX, minY      = 0.0, 0.0
		maxX, maxY      = 0.0, 0.0
		outputBottom    = 0.0
		encapsulatedTop = 0.0
		seen, hasOut    = false, false
		hasEncapsulated bool
	)
	consider := func(id string, isOutput bool) {
		p, ok := at[id]
		if !ok {
			return
		}
		if !seen {
			minX, minY, maxX, maxY = p.x, p.y, p.x+p.w, p.y+p.h
			seen = true
		} else {
			minX = math.Min(minX, p.x)
			minY = math.Min(minY, p.y)
			maxX = math.Max(maxX, p.x+p.w)
			maxY = math.Max(maxY, p.y+p.h)
		}
		if isOutput {
			if !hasOut || p.y+p.h > outputBottom {
				outputBottom = p.y + p.h
			}
			hasOut = true
			return
		}
		if !hasEncapsulated || p.y < encapsulatedTop {
			encapsulatedTop = p.y
		}
		hasEncapsulated = true
	}
	for _, id := range svc.outputs {
		consider(id, true)
	}
	for _, id := range svc.encapsulated {
		consider(id, false)
	}
	if !seen {
		return placedService{}, false
	}

	box := placedService{
		id: svc.id,
		x:  minX - serviceMargin, y: minY - serviceMargin,
		w: (maxX - minX) + 2*serviceMargin,
		h: (maxY - minY) + 2*serviceMargin,
	}
	switch {
	case hasOut && hasEncapsulated:
		box.dividerY = (outputBottom + encapsulatedTop) / 2
		// Layering normally puts every output decision above every encapsulated one,
		// so that midpoint is a gap. Two things defeat it: a requirement cycle, which
		// stops the depth walk, and a decision the document names in both
		// compartments, which cannot be on one side of a line. Neither has a divider
		// that classifies everything correctly, so the box's own middle is used — a
		// line inside the box beats one outside it.
		if outputBottom > encapsulatedTop {
			box.dividerY = box.y + box.h/2
		}
	case hasOut:
		box.dividerY = box.y + box.h - serviceMargin/2
	default:
		box.dividerY = box.y + serviceMargin/2
	}
	return box, true
}

// --- emitting ---

// generateDMNDI renders a laid-out graph as a <dmndi:DMNDI> block: a shape per
// node, then an edge per requirement drawn between the two boxes' centres. dmn-js
// re-routes an edge to the box borders when it renders, so naming the centres is
// enough to produce a correct picture without a routing model here.
func generateDMNDI(g drg) (string, bool) {
	places := layout(g)
	if len(places) == 0 {
		return "", false
	}
	at := make(map[string]placed, len(places))
	for _, p := range places {
		at[p.node.id] = p
	}

	var b strings.Builder
	fmt.Fprintf(&b, "  <dmndi:DMNDI xmlns:dmndi=%q xmlns:dc=%q xmlns:di=%q>\n", dmndiFor(g.modelNS), nsDC, nsDI)
	b.WriteString("    <dmndi:DMNDiagram id=\"DMNDiagram_atlas\">\n")
	// Decision services come first, and the order is load-bearing: a viewer that
	// does not treat the box as a container paints shapes in document order, so a
	// service written after its members would cover them.
	for _, svc := range serviceBoxes(g, places) {
		fmt.Fprintf(&b, "      <dmndi:DMNShape id=%q dmnElementRef=%q>\n", "DMNShape_"+xmlAttr(svc.id), xmlAttr(svc.id))
		fmt.Fprintf(&b, "        <dc:Bounds x=\"%g\" y=\"%g\" width=\"%g\" height=\"%g\"/>\n", svc.x, svc.y, svc.w, svc.h)
		b.WriteString("        <dmndi:DMNDecisionServiceDividerLine>\n")
		fmt.Fprintf(&b, "          <di:waypoint x=\"%g\" y=\"%g\"/>\n", svc.x, svc.dividerY)
		fmt.Fprintf(&b, "          <di:waypoint x=\"%g\" y=\"%g\"/>\n", svc.x+svc.w, svc.dividerY)
		b.WriteString("        </dmndi:DMNDecisionServiceDividerLine>\n")
		b.WriteString("      </dmndi:DMNShape>\n")
	}
	for _, p := range places {
		fmt.Fprintf(&b, "      <dmndi:DMNShape id=%q dmnElementRef=%q>\n", "DMNShape_"+xmlAttr(p.node.id), xmlAttr(p.node.id))
		fmt.Fprintf(&b, "        <dc:Bounds x=\"%g\" y=\"%g\" width=\"%g\" height=\"%g\"/>\n", p.x, p.y, p.w, p.h)
		b.WriteString("      </dmndi:DMNShape>\n")
	}
	for _, e := range g.edges {
		from, okFrom := at[e.source]
		to, okTo := at[e.target]
		if !okFrom || !okTo || e.id == "" {
			continue
		}
		fmt.Fprintf(&b, "      <dmndi:DMNEdge id=%q dmnElementRef=%q>\n", "DMNEdge_"+xmlAttr(e.id), xmlAttr(e.id))
		fmt.Fprintf(&b, "        <di:waypoint x=\"%g\" y=\"%g\"/>\n", from.x+from.w/2, from.y)
		fmt.Fprintf(&b, "        <di:waypoint x=\"%g\" y=\"%g\"/>\n", to.x+to.w/2, to.y+to.h)
		b.WriteString("      </dmndi:DMNEdge>\n")
	}
	b.WriteString("    </dmndi:DMNDiagram>\n  </dmndi:DMNDI>\n")
	return b.String(), true
}

// DMN diagram blocks to strip before writing a fresh one. Two shapes occur: a
// self-closing <DMNDI .../> and a full <DMNDI>…</DMNDI> container. The namespace
// prefix is arbitrary, so both patterns allow any. The self-closing form is removed
// first: its [^>]* stops at the first '>' and so can never swallow a container's
// contents, while the container's non-greedy body then matches each remaining block
// up to its own closing tag.
var (
	dmndiSelfClose  = regexp.MustCompile(`(?is)<\s*([a-z0-9_.]+:)?DMNDI\b[^>]*/\s*>`)
	dmndiBlock      = regexp.MustCompile(`(?is)<\s*([a-z0-9_.]+:)?DMNDI\b.*?</\s*([a-z0-9_.]+:)?DMNDI\s*>`)
	definitionsTail = regexp.MustCompile(`(?is)</\s*([a-z0-9_.]+:)?definitions\s*>`)
)

func stripDMNDI(src []byte) []byte {
	src = dmndiSelfClose.ReplaceAll(src, nil)
	return dmndiBlock.ReplaceAll(src, nil)
}

// injectBeforeDefinitionsClose splices di in just before the closing </definitions>
// tag, returning src unchanged when there is none to inject before.
func injectBeforeDefinitionsClose(src []byte, di string) []byte {
	loc := definitionsTail.FindIndex(src)
	if loc == nil {
		return src
	}
	out := make([]byte, 0, len(src)+len(di))
	out = append(out, src[:loc[0]]...)
	out = append(out, di...)
	out = append(out, src[loc[0]:]...)
	return out
}

// xmlAttr escapes a string for use inside an XML attribute value.
func xmlAttr(s string) string {
	return strings.NewReplacer(`&`, "&amp;", `<`, "&lt;", `>`, "&gt;", `"`, "&quot;").Replace(s)
}
