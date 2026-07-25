package dmn

import (
	"context"
	"errors"
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
	defs, diags, err := v.engine.Compile(ctx, xml)
	if err != nil {
		empty.Resolved, empty.Message = true, err.Error()
		return empty, nil
	}
	if diags.HasErrors() {
		empty.Resolved, empty.Message = true, formatDiagnostics(diags)
		return empty, nil
	}
	g := defs.Graph()
	mg := ModelGraph{Resolved: true, Valid: true, ModelName: defs.ModelName(), Nodes: []GraphNode{}, Edges: []GraphEdge{}}
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
	return mg, nil
}
