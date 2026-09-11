package panorama

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMeshHandleQueryReportsSourcePosition(t *testing.T) {
	mesh := NewMesh(meshLoop(t), func(*http.Request) (Landscape, ReachOut, error) {
		return Landscape{}, nil, nil
	}, nil, 0, meshNow)
	body := bytes.NewBufferString(`{"query":"MATCH (a:application) WHERE a.name = RETURN a"}`)
	rec := httptest.NewRecorder()
	mesh.HandleQuery(rec, httptest.NewRequest(http.MethodPost, "/api/v1/panorama/query", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var payload struct{ Error QueryError `json:"error"` }
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Line != 1 || payload.Error.Column <= 1 || payload.Error.Code == "" {
		t.Errorf("diagnostic = %#v", payload.Error)
	}
}

func TestGraphQueryParameterTypeIsValidatedBeforeMatching(t *testing.T) {
	_, err := executeGraphQuery(Graph{}, QueryRequest{
		Query:      `MATCH (p:process) WHERE p.version = $version RETURN p`,
		Parameters: map[string]any{"version": "three"},
	}, DefaultQueryLimits())
	if err == nil || !strings.Contains(err.Error(), "expects number") {
		t.Fatalf("err = %v, want typed-parameter diagnostic", err)
	}
}

func TestGraphQueryOrderAndLimitAreDeterministic(t *testing.T) {
	graph := Graph{Nodes: []Node{
		{ID: "process:2", Kind: KindProcess, Name: "Zulu", Provenance: ProvenanceDerived},
		{ID: "process:1", Kind: KindProcess, Name: "Alpha", Provenance: ProvenanceDerived},
	}}
	got, err := executeGraphQuery(graph, QueryRequest{
		Query: `MATCH (p:process) RETURN p ORDER BY p.name ASC LIMIT 1`,
	}, DefaultQueryLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Graph.Nodes) != 1 || got.Graph.Nodes[0].Name != "Alpha" {
		t.Fatalf("nodes = %#v", got.Graph.Nodes)
	}
}

func TestGraphQueryVisitedEdgeLimitFailsClosed(t *testing.T) {
	limits := DefaultQueryLimits()
	limits.MaxVisitedEdges = 1
	_, err := executeGraphQuery(queryFixture(), QueryRequest{
		Query: `MATCH p = (a:application)-[*1..4]->(x) RETURN p`,
	}, limits)
	if err == nil || !strings.Contains(err.Error(), "visited edges") {
		t.Fatalf("err = %v, want visited-edge limit", err)
	}
}
