package dmn

import "testing"

// The registry freezes each model's requirements graph at registration, so an
// operator asking how a case was decided can be shown the decision without
// anything being compiled on the run loop (invariants I1/I5).

func TestTheGraphIsTheModelTheDeploymentEvaluates(t *testing.T) {
	r := NewRegistry()
	if err := r.DeployDecision(11, []byte(serviceXML)); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	g, ok := r.Graph(11, "Praemienrechnung")
	if !ok {
		t.Fatalf("Graph(11, service) reported nothing; the deployment provides it")
	}
	// Every decision in the model, plus its input datum — the picture the DRG is.
	want := map[string]bool{"basispraemie": false, "zuschlaege": false, "praemie": false, "jahreskilometer": false}
	for _, n := range g.Nodes {
		key := n.VarName
		if key == "" {
			key = n.Name
		}
		if _, declared := want[key]; declared {
			want[key] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("the graph has no node for %q; nodes = %+v", name, g.Nodes)
		}
	}
	if len(g.Edges) == 0 {
		t.Errorf("the graph has no requirements, but praemie requires two decisions")
	}
}

// A model with no diagram of its own is still drawable: the graph is completed on
// the way in, so no client ever has to invent a layout (ADR-0325). serviceXML
// carries no DMNDI, which is the normal shape for a model written over MCP.
func TestAModelWithoutADiagramIsGivenOne(t *testing.T) {
	r := NewRegistry()
	if err := r.DeployDecision(12, []byte(serviceXML)); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	g, _ := r.Graph(12, "praemie")
	if !hasBounds(g) {
		t.Fatalf("no node carries bounds, so the viewer must lay the graph out itself: %+v", g.Nodes)
	}
	for _, n := range g.Nodes {
		if n.Width <= 0 || n.Height <= 0 {
			t.Errorf("node %q is unsized (%v×%v) while the rest of the graph is drawn", n.ID, n.Width, n.Height)
		}
	}
}

// Asking for a decision no deployment provides is answered, not guessed at: an
// empty graph and false, so a caller says "this is gone" rather than drawing
// somebody else's model.
func TestAnUnknownDecisionHasNoGraph(t *testing.T) {
	r := NewRegistry()
	if err := r.DeployDecision(13, []byte(serviceXML)); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	g, ok := r.Graph(13, "Nichts")
	if ok {
		t.Errorf("Graph(unknown decision) reported a graph: %+v", g)
	}
	if g.Nodes == nil || g.Edges == nil {
		t.Errorf("the empty graph carries nil slices, which serialise as null rather than []")
	}
}

// A definition whose deployment is gone still has evaluations in its history, and
// they are still worth drawing. The lookup falls back to the newest model providing
// the decision — the same model a latest-bound task would evaluate.
func TestAGoneDeploymentFallsBackToTheNewestModel(t *testing.T) {
	r := NewRegistry()
	if err := r.DeployDecision(14, []byte(serviceXML)); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if _, ok := r.Graph(999, "praemie"); !ok {
		t.Errorf("Graph(unknown def) reported nothing; the decision is still deployed elsewhere")
	}
}

// IsService is what lets the viewer say the true thing about a missing trace. A
// service evaluation records none (ADR-0398), and "no rules were recorded" is a
// different sentence from "no rules fired".
func TestAServiceIsDistinguishedFromTheDecisionsInsideIt(t *testing.T) {
	r := NewRegistry()
	if err := r.DeployDecision(15, []byte(serviceXML)); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if !r.IsService(15, "Praemienrechnung") {
		t.Errorf("IsService(service) = false, want true")
	}
	for _, decision := range []string{"praemie", "zuschlaege", "basispraemie"} {
		if r.IsService(15, decision) {
			t.Errorf("IsService(%q) = true, but it is a decision inside the service, not the service", decision)
		}
	}
	if r.IsService(15, "Nichts") {
		t.Errorf("IsService(unknown) = true, want false")
	}
}
