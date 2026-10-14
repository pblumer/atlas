package dmn

import (
	"context"
	"errors"

	tdmn "github.com/pblumer/temis/dmn"
)

// GraphNode is one element of a DMN model's decision requirements graph, for a
// read-only viewer (ADR-0014's non-goal is a DMN *editor*; viewing what a
// referenced model contains is in scope). Type is "decision", "inputData", or
// "businessKnowledgeModel". X/Y/Width/Height carry the authored DMNDI bounds when
// the model has a diagram (all zero otherwise, so the client auto-lays-out).
type GraphNode struct {
	ID       string  `json:"id"`
	Type     string  `json:"type"`
	Name     string  `json:"name"`
	DataType string  `json:"dataType,omitempty"`
	VarName  string  `json:"varName,omitempty"`
	HasTable bool    `json:"hasTable,omitempty"`
	X        float64 `json:"x,omitempty"`
	Y        float64 `json:"y,omitempty"`
	Width    float64 `json:"width,omitempty"`
	Height   float64 `json:"height,omitempty"`
}

// GraphEdge is one requirement, directed from the required (upstream) element to
// the one that requires it — matching the DMN arrow direction. Type is
// "informationRequirement" or "knowledgeRequirement".
type GraphEdge struct {
	Type   string `json:"type"`
	Source string `json:"source"`
	Target string `json:"target"`
}

// ModelGraph is a referenced DMN model's requirements graph plus its resolve/valid
// status, so a viewer can render the diagram or show why it can't. Nodes/Edges are
// empty (never null) unless the model resolved and compiled cleanly.
type ModelGraph struct {
	Resolved  bool        `json:"resolved"`
	Valid     bool        `json:"valid"`
	ModelName string      `json:"modelName,omitempty"`
	Message   string      `json:"message,omitempty"`
	Nodes     []GraphNode `json:"nodes"`
	Edges     []GraphEdge `json:"edges"`
}

// Graph resolves modelRef, compiles it, and returns its decision requirements
// graph for a read-only viewer. Like Validate it returns a non-nil error only for
// an infrastructure failure; an unresolved handle or an invalid model is a normal
// result carrying a Message (and no nodes), so the viewer can explain the state
// instead of erroring.
//
// Every node comes back with bounds: a model whose diagram does not cover it is
// completed on the way through, so the viewer never has to invent a layout of its
// own (ADR-0325).
func (v *Validator) Graph(ctx context.Context, modelRef string) (ModelGraph, error) {
	empty := ModelGraph{Nodes: []GraphNode{}, Edges: []GraphEdge{}}
	xml, err := v.resolver.Resolve(ctx, modelRef)
	if errors.Is(err, ErrNotFound) {
		empty.Message = "no temis model matches this reference"
		return empty, nil
	}
	if err != nil {
		return ModelGraph{}, err
	}
	// The viewer draws from the bounds this graph carries, so the model is completed
	// first and temis then reports the generated diagram as any other
	// (ADR-0325). One generator, so the viewer and
	// the editor place the same model in the same picture.
	xml = EnsureDiagram(xml)
	defs, diags, err := v.engine.Compile(ctx, xml)
	if err != nil {
		empty.Resolved, empty.Message = true, err.Error()
		return empty, nil
	}
	if diags.HasErrors() {
		empty.Resolved, empty.Message = true, formatDiagnostics(diags)
		return empty, nil
	}
	return graphOf(defs), nil
}

// modelGraph freezes a compiled model's requirements graph at registration time,
// so a runtime surface can draw the model an evaluation actually ran against
// without compiling anything (invariant I5) and without doing the work on the run
// loop, which is where the read that wants it lands.
//
// Bounds come from the document's own DMNDI where it has one. Where it has none —
// the common case for a model written over MCP or by temis, which produce logic
// and not a picture — the diagram is generated exactly as the read-only viewer
// generates it (ADR-0325), by completing the source and re-reading the completed
// model's graph. One generator, so a decision is drawn in the same place wherever
// it is looked at. A completion that does not compile is not an error worth
// failing a deploy over: the boundless graph stands and the client lays it out.
func modelGraph(engine *tdmn.Engine, defs *tdmn.Definitions, src []byte) ModelGraph {
	mg := graphOf(defs)
	if hasBounds(mg) || len(src) == 0 {
		return mg
	}
	completed, diags, err := engine.Compile(context.Background(), EnsureDiagram(src))
	if err != nil || diags.HasErrors() {
		return mg
	}
	if drawn := graphOf(completed); hasBounds(drawn) {
		return drawn
	}
	return mg
}

// graphOf maps temis's own graph onto the wire shape, which is the one thing
// [Validator.Graph] and [modelGraph] must not do differently.
func graphOf(defs *tdmn.Definitions) ModelGraph {
	mg := ModelGraph{Resolved: true, Valid: true, ModelName: defs.ModelName(), Nodes: []GraphNode{}, Edges: []GraphEdge{}}
	g := defs.Graph()
	for _, n := range g.Nodes {
		mg.Nodes = append(mg.Nodes, GraphNode{
			ID: n.ID, Type: n.Type, Name: n.Name, DataType: n.DataType,
			VarName: n.VarName, HasTable: n.HasTable,
			X: n.X, Y: n.Y, Width: n.Width, Height: n.Height,
		})
	}
	for _, e := range g.Edges {
		mg.Edges = append(mg.Edges, GraphEdge{Type: e.Type, Source: e.Source, Target: e.Target})
	}
	return mg
}

// hasBounds reports whether a graph carries a drawable diagram. One sized node is
// enough: a document with DMNDI sizes every shape it declares, and the viewer's
// own fallback keys off the same question.
func hasBounds(mg ModelGraph) bool {
	for _, n := range mg.Nodes {
		if n.Width > 0 {
			return true
		}
	}
	return false
}
