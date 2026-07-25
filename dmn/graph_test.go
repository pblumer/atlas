package dmn_test

import (
	"context"
	"os"
	"path/filepath"
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
