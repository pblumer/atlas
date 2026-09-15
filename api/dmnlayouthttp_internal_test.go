package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The diagram a DMN model is read with, and the one an author asks for
// (ADR-0325).

// diagramlessDMN is the shape almost every model arrives in: an agent, temis or a
// hand writes the logic, and nothing writes the picture.
const diagramlessDMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="defs_plain" name="Plain" namespace="http://atlas/dmn">
  <inputData id="amount" name="amount"/>
  <decision id="verdict" name="verdict">
    <informationRequirement id="ir1"><requiredInput href="#amount"/></informationRequirement>
    <literalExpression id="le1"><text>1</text></literalExpression>
  </decision>
</definitions>`

// A model stored without a diagram opens in the editor with one, so the graph it
// describes is visible and can be rewired. Before this, dmn-js drew the decision
// and dropped the input data and the arrow between them.
func TestAModelWithNoDiagramIsServedWithOne(t *testing.T) {
	srv, dir := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	if err := os.WriteFile(filepath.Join(dir, "dmn-models", "plain.dmn"), []byte(diagramlessDMN), 0o644); err != nil {
		t.Fatalf("seed model: %v", err)
	}

	code, b := x.do(http.MethodGet, "/api/v1/dmn-models/plain/xml", "")
	if code != http.StatusOK {
		t.Fatalf("read model: %d %s", code, b)
	}
	got := string(b)
	for _, want := range []string{`dmnElementRef="amount"`, `dmnElementRef="verdict"`, `dmnElementRef="ir1"`} {
		if !strings.Contains(got, want) {
			t.Errorf("served model does not draw %s:\n%s", want, got)
		}
	}
	// The stored file is not rewritten: the diagram is added on the way out, so
	// nothing about what is on disk changes by looking at it.
	stored, err := os.ReadFile(filepath.Join(dir, "dmn-models", "plain.dmn"))
	if err != nil || strings.Contains(string(stored), "DMNDI") {
		t.Fatalf("the stored model was rewritten by a read: %v\n%s", err, stored)
	}
}

// The DRG viewer draws from the graph rather than the XML, and it gets bounds for
// every node too — one generator, so the viewer and the editor place the same
// model the same way.
func TestTheViewerGraphCarriesBoundsForEveryNode(t *testing.T) {
	srv, dir := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	if err := os.WriteFile(filepath.Join(dir, "dmn-models", "plain.dmn"), []byte(diagramlessDMN), 0o644); err != nil {
		t.Fatalf("seed model: %v", err)
	}
	x.addRef("", "Plain", "plain")

	_, rb := x.do(http.MethodGet, "/api/v1/dmnrefs", "")
	var refs []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rb, &refs); err != nil {
		t.Fatalf("decode refs: %v (%s)", err, rb)
	}
	if len(refs) != 1 {
		t.Fatalf("refs = %+v, want the one just added", refs)
	}
	code, b := x.do(http.MethodGet, "/api/v1/dmnrefs/"+refs[0].ID+"/graph", "")
	if code != http.StatusOK {
		t.Fatalf("graph: %d %s", code, b)
	}
	var g struct {
		Nodes []struct {
			ID     string  `json:"id"`
			Width  float64 `json:"width"`
			Height float64 `json:"height"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatalf("decode graph: %v (%s)", err, b)
	}
	if len(g.Nodes) != 2 {
		t.Fatalf("nodes = %+v, want the input data and the decision", g.Nodes)
	}
	for _, n := range g.Nodes {
		if n.Width == 0 || n.Height == 0 {
			t.Errorf("node %q came back with no bounds; the viewer would have to invent a layout", n.ID)
		}
	}
}

// Auto-layout is the author asking for the graph to be re-flowed, so unlike a read
// it moves what is already placed.
func TestTheAutoLayoutRouteReflowsWhatIsThere(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	placed := strings.Replace(diagramlessDMN, "</definitions>", `  <dmndi:DMNDI xmlns:dmndi="https://www.omg.org/spec/DMN/20191111/DMNDI/" xmlns:dc="http://www.omg.org/spec/DMN/20180521/DC/">
    <dmndi:DMNDiagram id="D1">
      <dmndi:DMNShape id="s1" dmnElementRef="amount"><dc:Bounds x="4000" y="4000" width="125" height="45"/></dmndi:DMNShape>
      <dmndi:DMNShape id="s2" dmnElementRef="verdict"><dc:Bounds x="4000" y="3000" width="180" height="80"/></dmndi:DMNShape>
    </dmndi:DMNDiagram>
  </dmndi:DMNDI>
</definitions>`, 1)

	code, b := x.do(http.MethodPost, "/api/v1/dmn-layout", placed)
	if code != http.StatusOK {
		t.Fatalf("auto-layout: %d %s", code, b)
	}
	if strings.Contains(string(b), `x="4000"`) {
		t.Fatalf("the old coordinates survived an explicit re-flow:\n%s", b)
	}
	if !strings.Contains(string(b), `dmnElementRef="ir1"`) {
		t.Fatalf("the requirement was not drawn:\n%s", b)
	}
	// Only the picture changed.
	if !strings.Contains(string(b), `<literalExpression id="le1">`) {
		t.Fatalf("the semantic model was altered:\n%s", b)
	}
}

// An empty body is the caller's mistake, and a model that cannot be laid out comes
// back as it went in rather than stripped of what it had.
func TestTheAutoLayoutRouteRefusesNothingAndManglesNothing(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	if code, b := x.do(http.MethodPost, "/api/v1/dmn-layout", ""); code != http.StatusBadRequest {
		t.Fatalf("empty body = %d %s, want 400", code, b)
	}
	const notAModel = "<definitions"
	code, b := x.do(http.MethodPost, "/api/v1/dmn-layout", notAModel)
	if code != http.StatusOK || string(b) != notAModel {
		t.Fatalf("auto-layout of unparseable XML = %d %q, want it returned unchanged", code, b)
	}
}
