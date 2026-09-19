package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

// The decision graph endpoint is what the Operations viewers open when a business
// rule task is double-clicked: one evaluation, with the requirements graph of the
// model it ran against, so an operator can be shown the decision rather than told
// about it.
//
// These drive the HTTP surface, because that is the seam the viewers use.

// decisionGraphBody mirrors the served document. It is spelled out here rather than
// reusing the handler's type so a change to the wire shape has to be made twice —
// once in the server and once in what a client is promised.
type decisionGraphBody struct {
	At         int64           `json:"at"`
	ElementID  string          `json:"elementId"`
	DecisionID string          `json:"decisionId"`
	ModelName  string          `json:"modelName"`
	Service    bool            `json:"service"`
	Inputs     json.RawMessage `json:"inputs"`
	Outputs    json.RawMessage `json:"outputs"`
	Trace      json.RawMessage `json:"trace"`
	Nodes      []struct {
		ID       string  `json:"id"`
		Type     string  `json:"type"`
		Name     string  `json:"name"`
		VarName  string  `json:"varName"`
		HasTable bool    `json:"hasTable"`
		Width    float64 `json:"width"`
	} `json:"nodes"`
	Edges []struct {
		Type   string `json:"type"`
		Source string `json:"source"`
		Target string `json:"target"`
	} `json:"edges"`
}

// dinnerDecisionEvaluation runs the dinner process to completion and returns the
// instance key and the single evaluation its business rule task made.
func dinnerDecisionEvaluation(t *testing.T) (deployTestHarness, uint64, int64) {
	t.Helper()
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	pid := x.mkProject("Dinner")
	x.saveDraft(pid, dinnerBPMN)
	x.addRef(pid, "Dish decision", "dish")
	code, b := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", "")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, b)
	}
	var rep projectDeployResp
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	if code, b := x.do(http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", rep.Definitions[0].Key), "{}"); code != http.StatusOK {
		t.Fatalf("create instance status=%d body=%s", code, b)
	}

	_, list := x.do(http.MethodGet, "/api/v1/instances", "")
	var instances []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(listRows(t, list), &instances); err != nil {
		t.Fatalf("decode instances: %v", err)
	}
	if len(instances) == 0 {
		t.Fatalf("no instances listed: %s", list)
	}
	_, b = x.do(http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/decisions", instances[0].Key), "")
	var decisions []struct {
		At int64 `json:"at"`
	}
	if err := json.Unmarshal(b, &decisions); err != nil {
		t.Fatalf("decode decisions: %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("decisions = %d, want 1; body=%s", len(decisions), b)
	}
	return x, instances[0].Key, decisions[0].At
}

// TestDecisionGraphServesTheEvaluationWithItsModel is the whole point of the
// endpoint: one read gives the viewer both halves — the case (inputs, outputs,
// trace) and the picture of the decision that produced it.
func TestDecisionGraphServesTheEvaluationWithItsModel(t *testing.T) {
	x, instanceKey, at := dinnerDecisionEvaluation(t)

	code, b := x.do(http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/decisions/%d/graph", instanceKey, at), "")
	if code != http.StatusOK {
		t.Fatalf("graph status=%d body=%s", code, b)
	}
	var g decisionGraphBody
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatalf("decode graph: %v", err)
	}
	if g.DecisionID != "Dish" {
		t.Errorf("decisionId = %q, want Dish", g.DecisionID)
	}
	if g.At != at {
		t.Errorf("at = %d, want the evaluation asked for (%d)", g.At, at)
	}
	if g.ElementID != "decide" {
		t.Errorf("elementId = %q, want decide — the viewer needs the diagram id to name the task", g.ElementID)
	}
	if string(g.Inputs) != `{"Season":"Winter"}` {
		t.Errorf("inputs = %s, want the input context the evaluation saw", g.Inputs)
	}
	if string(g.Outputs) != `{"Dish":"Roastbeef"}` {
		t.Errorf("outputs = %s, want what the decision returned", g.Outputs)
	}
	if len(g.Trace) == 0 {
		t.Errorf("trace is empty, want the rules that fired; body=%s", b)
	}
	if g.Service {
		t.Errorf("service = true, want false — Dish is a decision, not a decision service")
	}

	// The graph itself: the decision and the input datum it requires, joined by the
	// requirement between them. Without these the window has nothing to draw.
	var decision, input bool
	for _, n := range g.Nodes {
		switch n.Type {
		case "decision":
			if n.Name == "Dish" {
				decision = true
				if !n.HasTable {
					t.Errorf("the Dish node reports no decision table, but it is one")
				}
			}
		case "inputData":
			input = true
		}
	}
	if !decision || !input {
		t.Fatalf("nodes = %+v, want at least the Dish decision and an input datum", g.Nodes)
	}
	if len(g.Edges) == 0 {
		t.Errorf("edges are empty, want the requirement joining the input datum to the decision")
	}
}

// TestDecisionGraphNodesCarryADiagram covers ADR-0325's promise at this seam: the
// viewer is never handed a graph it has to invent a layout for. The dish model
// carries no DMNDI of its own, so the bounds can only be there because the server
// completed it before freezing the graph.
func TestDecisionGraphNodesCarryADiagram(t *testing.T) {
	x, instanceKey, at := dinnerDecisionEvaluation(t)

	_, b := x.do(http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/decisions/%d/graph", instanceKey, at), "")
	var g decisionGraphBody
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatalf("decode graph: %v", err)
	}
	for _, n := range g.Nodes {
		if n.Width <= 0 {
			t.Fatalf("node %q has no width, so the client must lay the graph out itself; body=%s", n.ID, b)
		}
	}
}

// TestDecisionGraphRefusesWhatItCannotAnswer: the caller named one specific
// evaluation, and there is no such thing as an empty one. This is deliberately
// unlike the /decisions list beside it, which answers an unknown instance with an
// empty array — that read asks "what did this instance decide", which has an
// answer, and this one asks "show me this decision", which does not.
func TestDecisionGraphRefusesWhatItCannotAnswer(t *testing.T) {
	x, instanceKey, at := dinnerDecisionEvaluation(t)

	for _, c := range []struct {
		name string
		path string
		want int
	}{
		{"an instance that never ran", "/api/v1/instances/123456/decisions/1/graph", http.StatusNotFound},
		{"a timestamp nothing was recorded at", fmt.Sprintf("/api/v1/instances/%d/decisions/%d/graph", instanceKey, at+1), http.StatusNotFound},
		{"an instance key that is not a number", "/api/v1/instances/nope/decisions/1/graph", http.StatusBadRequest},
		{"a timestamp that is not a number", fmt.Sprintf("/api/v1/instances/%d/decisions/later/graph", instanceKey), http.StatusBadRequest},
	} {
		t.Run(c.name, func(t *testing.T) {
			if code, b := x.do(http.MethodGet, c.path, ""); code != c.want {
				t.Errorf("status = %d, want %d; body=%s", code, c.want, b)
			}
		})
	}
}

// TestAnEvaluationCarriesAKeyAJavaScriptClientCanHold is the near-miss this feature
// was built through. A nanosecond timestamp is past 2^53, so a browser parsing the
// `at` number rounds it — 1789824241612033700 becomes …033800 — and every link built
// from the parsed value would address an evaluation that does not exist. The listing
// therefore serves the timestamp as a string too, and that string is what the graph
// endpoint is keyed on.
func TestAnEvaluationCarriesAKeyAJavaScriptClientCanHold(t *testing.T) {
	x, instanceKey, at := dinnerDecisionEvaluation(t)

	_, b := x.do(http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/decisions", instanceKey), "")
	var rows []struct {
		At    int64  `json:"at"`
		AtKey string `json:"atKey"`
	}
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatalf("decode decisions: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("decisions = %d, want 1", len(rows))
	}
	want := strconv.FormatInt(at, 10)
	if rows[0].AtKey != want {
		t.Fatalf("atKey = %q, want %q — the exact digits, so a client never has to reconstruct them", rows[0].AtKey, want)
	}
	if rows[0].At != at {
		t.Errorf("at = %d, want %d", rows[0].At, at)
	}

	// The key the listing served addresses the evaluation, and the cross-instance
	// listing serves the same one — that is the other place the link is built from.
	if code, b := x.do(http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/decisions/%s/graph", instanceKey, rows[0].AtKey), ""); code != http.StatusOK {
		t.Errorf("graph by atKey status=%d, want 200; body=%s", code, b)
	}
	_, b = x.do(http.MethodGet, "/api/v1/decisions/Dish/evaluations", "")
	var cross []struct {
		AtKey string `json:"atKey"`
	}
	if err := json.Unmarshal(b, &cross); err != nil {
		t.Fatalf("decode evaluations: %v", err)
	}
	if len(cross) != 1 || cross[0].AtKey != want {
		t.Errorf("cross-instance atKey = %+v, want one row keyed %q", cross, want)
	}

	// And the rounding really is what a browser would do to the number beside it.
	if rounded := float64(at); int64(rounded) == at {
		t.Skipf("this instance's timestamp (%d) happens to survive a float64, so the hazard is not demonstrable here", at)
	}
}
