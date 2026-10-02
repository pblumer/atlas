package panorama

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func queryFixture() Graph {
	return Graph{
		ObservedAt: 1700000000,
		Nodes: []Node{
			{ID: "application:a1", Kind: KindApplication, Name: "Billing", Provenance: ProvenanceDerived},
			{ID: "process:1", Kind: KindProcess, Name: "Invoice", ProcessID: "invoice", Version: 3, Provenance: ProvenanceDerived, Application: "application:a1"},
			{ID: "worker:w1", Kind: KindWorker, Name: "SAP", WorkerType: "rest", Provenance: ProvenanceDerived},
			{ID: "restricted:1", Kind: KindRestricted, Provenance: ProvenanceDerived},
			// This node and edge deliberately model a malformed/hostile input graph. The
			// evaluator must still treat restricted nodes as terminal so a future graph
			// producer cannot turn a placeholder into an authorization bridge.
			{ID: "process:secret", Kind: KindProcess, Name: "Secret", Provenance: ProvenanceDerived},
		},
		Edges: []Edge{
			{From: "application:a1", To: "process:1", Kind: EdgeContains},
			{From: "process:1", To: "worker:w1", Kind: EdgeUses},
			{From: "process:1", To: "restricted:1", Kind: EdgeCalls},
			{From: "restricted:1", To: "process:secret", Kind: EdgeCalls},
		},
		Restricted: 1,
	}
}

func TestGraphQueryMatchesTypedPathAndParameter(t *testing.T) {
	got, err := executeGraphQuery(queryFixture(), QueryRequest{
		Query: `MATCH p = (a:application)-[:contains]->(p1:process) WHERE p1.processId = $process RETURN p`,
		Parameters: map[string]any{"process": "invoice"},
	}, DefaultQueryLimits())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !got.Complete {
		t.Fatalf("complete = false: %#v", got.Limit)
	}
	if len(got.Graph.Nodes) != 2 || len(got.Graph.Edges) != 1 {
		t.Fatalf("result = %d nodes/%d edges, want 2/1", len(got.Graph.Nodes), len(got.Graph.Edges))
	}
	if got.Graph.Nodes[0].ID != "application:a1" || got.Graph.Nodes[1].ID != "process:1" {
		t.Errorf("nodes = %#v", got.Graph.Nodes)
	}
	if got.Profile != QueryProfileV1 {
		t.Errorf("profile = %q, want %q", got.Profile, QueryProfileV1)
	}
	if got.Graph.ObservedAt != queryFixture().ObservedAt {
		t.Errorf("observedAt = %d", got.Graph.ObservedAt)
	}
}

func TestGraphQueryBooleanWhereAndLiteral(t *testing.T) {
	got, err := executeGraphQuery(queryFixture(), QueryRequest{
		Query: `MATCH p = (a:application)-[:contains]->(p1:process) WHERE p1.version >= 3 AND NOT p1.name = "Other" RETURN p`,
	}, DefaultQueryLimits())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(got.Graph.Edges) != 1 {
		t.Fatalf("edges = %d, want 1", len(got.Graph.Edges))
	}
}

func TestGraphQueryBoundedTraversalNeverCrossesRestrictedPlaceholder(t *testing.T) {
	got, err := executeGraphQuery(queryFixture(), QueryRequest{
		Query: `MATCH p = (a:application)-[*1..4]->(x) WHERE a.name = "Billing" RETURN p`,
	}, DefaultQueryLimits())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, n := range got.Graph.Nodes {
		if n.ID == "process:secret" {
			t.Fatalf("query crossed restricted placeholder and disclosed %q", n.ID)
		}
	}
	var restricted bool
	for _, n := range got.Graph.Nodes {
		restricted = restricted || n.Kind == KindRestricted
	}
	if !restricted {
		t.Fatal("the already-authorized restricted endpoint disappeared from the result")
	}
}

func TestGraphQueryRejectsUnboundedAndMutatingSyntax(t *testing.T) {
	for _, q := range []string{
		`MATCH p = (a)-[*]->(b) RETURN p`,
		`MATCH (a) CREATE (b) RETURN a`,
		`MATCH (a) DELETE a RETURN a`,
		`CALL db.labels()`,
		`LOAD CSV FROM "https://example.invalid/x.csv" AS row RETURN row`,
		`MATCH (a) RETURN count(a)`,
	} {
		t.Run(q, func(t *testing.T) {
			if _, err := executeGraphQuery(queryFixture(), QueryRequest{Query: q}, DefaultQueryLimits()); err == nil {
				t.Fatalf("query %q was accepted", q)
			}
		})
	}
}

func TestGraphQueryRejectsUnknownKindPropertyAndParameter(t *testing.T) {
	cases := []QueryRequest{
		{Query: `MATCH (a:not_a_kind) RETURN a`},
		{Query: `MATCH (a:application) WHERE a.endpoint = "secret" RETURN a`},
		{Query: `MATCH (a:application) WHERE a.name = $missing RETURN a`},
	}
	for _, req := range cases {
		if _, err := executeGraphQuery(queryFixture(), req, DefaultQueryLimits()); err == nil {
			t.Fatalf("request %#v was accepted", req)
		}
	}
}

func TestGraphQueryLimitIsExplicitNotSilentTruncation(t *testing.T) {
	limits := DefaultQueryLimits()
	limits.MaxReturnedNodes = 1
	_, err := executeGraphQuery(queryFixture(), QueryRequest{
		Query: `MATCH p = (a:application)-[:contains]->(p1:process) RETURN p`,
	}, limits)
	if err == nil || !strings.Contains(err.Error(), "returned nodes") {
		t.Fatalf("err = %v, want returned-node limit", err)
	}
}

func TestGraphQuerySchemaNamesOnlySafeQueryableProperties(t *testing.T) {
	schema := GraphQuerySchema()
	if schema.Profile != QueryProfileV1 {
		t.Fatalf("profile = %q", schema.Profile)
	}
	joined, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	text := string(joined)
	for _, secret := range []string{"endpoint", "credentialsRef"} {
		if strings.Contains(text, secret) {
			t.Errorf("schema exposes %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "processId") || !strings.Contains(text, "workerType") {
		t.Errorf("schema lacks useful public properties: %s", text)
	}
}

func TestMeshHandleQueryEvaluatesOnlyCollectedAuthorizedLandscape(t *testing.T) {
	mesh := NewMesh(meshLoop(t), func(*http.Request) (Landscape, ReachOut, error) {
		return Landscape{
			Applications: []Application{
				{ID: "visible", Name: "Visible", CanView: true},
				{ID: "hidden", Name: "Hidden", CanView: false},
			},
			Processes: []Process{
				{Key: 1, ProcessID: "visible", Name: "Visible process", ApplicationID: "visible", CanView: true},
				{Key: 2, ProcessID: "hidden", Name: "Hidden process", ApplicationID: "hidden", CanView: false},
			},
		}, nil, nil
	}, nil, 0, meshNow)

	body := bytes.NewBufferString(`{"query":"MATCH (n:process) RETURN n"}`)
	rec := httptest.NewRecorder()
	mesh.HandleQuery(rec, httptest.NewRequest(http.MethodPost, "/api/v1/panorama/query", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var result QueryResponse
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	for _, n := range result.Graph.Nodes {
		if n.Name == "Hidden process" {
			t.Fatalf("query disclosed caller-hidden node %#v", n)
		}
	}
}

func TestMeshHandleQuerySchema(t *testing.T) {
	mesh := NewMesh(meshLoop(t), func(*http.Request) (Landscape, ReachOut, error) {
		return Landscape{}, nil, nil
	}, nil, 0, meshNow)
	rec := httptest.NewRecorder()
	mesh.HandleQuerySchema(rec, httptest.NewRequest(http.MethodGet, "/api/v1/panorama/query/schema", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var schema QuerySchema
	if err := json.NewDecoder(rec.Body).Decode(&schema); err != nil {
		t.Fatal(err)
	}
	if schema.Profile != QueryProfileV1 {
		t.Errorf("profile = %q", schema.Profile)
	}
}
