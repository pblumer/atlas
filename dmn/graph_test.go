package dmn_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// TestModelGraph proves the read-only viewer's data source: a compiled model
// yields its decision requirements graph — the input data, the decision, and the
// requirement edge between them.
func TestModelGraph(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "dish.dmn"), []byte(dishModel), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}
	v := dmn.NewValidator(dmn.DirResolver{Dir: dir})

	g, err := v.Graph(context.Background(), "dish")
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	if !g.Resolved || !g.Valid {
		t.Fatalf("graph = %+v, want resolved+valid", g)
	}
	var decision, input bool
	for _, n := range g.Nodes {
		if n.Type == "decision" && n.Name == "Dish" {
			decision = true
		}
		if n.Type == "inputData" && n.Name == "Season" {
			input = true
		}
	}
	if !decision || !input {
		t.Errorf("nodes = %+v, want a Dish decision and a Season inputData", g.Nodes)
	}
	if len(g.Edges) == 0 {
		t.Errorf("edges = %v, want at least the Season→Dish requirement", g.Edges)
	}
}

// TestModelGraphInvalid returns a resolved-but-invalid result carrying a message
// (not an error, and no nodes), so the viewer can explain the state.
func TestModelGraphInvalid(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.dmn"),
		[]byte(`<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="d" name="broken"><decision id="Bad" name="Bad"><literalExpression id="le"><text>1 +</text></literalExpression></decision></definitions>`), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}
	v := dmn.NewValidator(dmn.DirResolver{Dir: dir})
	g, err := v.Graph(context.Background(), "broken")
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	if g.Valid || g.Message == "" || len(g.Nodes) != 0 {
		t.Errorf("graph = %+v, want invalid with a message and no nodes", g)
	}
}

// TestModelGraphUnresolved returns a message (not an error) for a missing model.
func TestModelGraphUnresolved(t *testing.T) {
	v := dmn.NewValidator(dmn.DirResolver{Dir: t.TempDir()})
	g, err := v.Graph(context.Background(), "missing")
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	if g.Resolved || g.Message == "" {
		t.Errorf("graph = %+v, want unresolved with a message", g)
	}
}

// TestModelGraphResolverError surfaces an infrastructure resolver failure as an
// error (distinct from an unresolved handle, which is a message).
func TestModelGraphResolverError(t *testing.T) {
	v := dmn.NewValidator(errResolver{})
	if _, err := v.Graph(context.Background(), "x"); err == nil {
		t.Fatal("Graph with a failing resolver = nil error, want an error")
	}
}

// TestModelGraphMalformed covers the compile-error (not diagnostics) path: XML that
// is not a DMN document at all yields a resolved-but-invalid result with a message.
func TestModelGraphMalformed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "junk.dmn"), []byte("<not-dmn"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	v := dmn.NewValidator(dmn.DirResolver{Dir: dir})
	g, err := v.Graph(context.Background(), "junk")
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	if g.Valid || g.Message == "" {
		t.Errorf("graph of malformed XML = %+v, want invalid with a message", g)
	}
}

// everyElementModel declares one of each element the DRD notation has: the three
// kinds of node a graph reports, the four kinds of requirement, and the four
// elements — knowledge source, text annotation, association, decision service — that
// it does not.
const everyElementModel = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="every" name="Every" namespace="https://example.com/every">
  <inputData id="in1" name="Amount"><variable id="v1" name="amount" typeRef="number"/></inputData>
  <businessKnowledgeModel id="bkm1" name="Instalment"/>
  <knowledgeSource id="ks1" name="Policy"/>
  <decision id="dec1" name="Risk">
    <variable id="v2" name="risk" typeRef="string"/>
    <informationRequirement id="ir1"><requiredInput href="#in1"/></informationRequirement>
    <knowledgeRequirement id="kr1"><requiredKnowledge href="#bkm1"/></knowledgeRequirement>
    <authorityRequirement id="ar1"><requiredAuthority href="#ks1"/></authorityRequirement>
    <literalExpression id="le1"><text>"low"</text></literalExpression>
  </decision>
  <decisionService id="svc1" name="Rating"><outputDecision href="#dec1"/></decisionService>
  <textAnnotation id="ta1"><text>note</text></textAnnotation>
  <association id="as1"><sourceRef href="#dec1"/><targetRef href="#ta1"/></association>
</definitions>`

// TestModelGraphElementTypes pins the whole of what a requirements graph can put in
// front of a renderer, which is what makes the renderer's notation table complete.
//
// api/web/decision-graph.js draws a shape per node type and a line per edge type,
// and every kind it does not know falls through to the plain rectangle and the solid
// filled arrow — that is, to a decision and to an information requirement. So the
// day this set grows, something is being drawn as the wrong element and the picture
// says something untrue. The e2e notation table (e2e/decision-graph.spec.mjs) covers
// exactly the five kinds below; this is the test that says five is all of them.
func TestModelGraphElementTypes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "every.dmn"), []byte(everyElementModel), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}
	v := dmn.NewValidator(dmn.DirResolver{Dir: dir})

	g, err := v.Graph(context.Background(), "every")
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	if !g.Resolved || !g.Valid {
		t.Fatalf("graph = %+v, want resolved+valid", g)
	}

	nodes := map[string]bool{}
	for _, n := range g.Nodes {
		nodes[n.Type] = true
	}
	edges := map[string]bool{}
	for _, e := range g.Edges {
		edges[e.Type] = true
	}
	wantNodes := map[string]bool{"decision": true, "inputData": true, "businessKnowledgeModel": true}
	wantEdges := map[string]bool{"informationRequirement": true, "knowledgeRequirement": true}
	if !reflect.DeepEqual(nodes, wantNodes) {
		t.Errorf("node types = %v, want %v — the renderer draws a shape per type, so a new one needs a row in the notation table", nodes, wantNodes)
	}
	if !reflect.DeepEqual(edges, wantEdges) {
		t.Errorf("edge types = %v, want %v — the renderer draws a line per type, so a new one needs a row in the notation table", edges, wantEdges)
	}
}
