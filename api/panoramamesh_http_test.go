package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// meshNode and meshGraph mirror the panorama mesh payload. Declared here rather
// than imported so the test pins the wire contract a browser sees, not the Go type.
type meshNode struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Provenance  string `json:"provenance"`
	Application string `json:"application"`
	ProcessID   string `json:"processId"`
	Version     int32  `json:"version"`
	Children    int    `json:"children"`
}

type meshEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

type meshGraph struct {
	Nodes      []meshNode `json:"nodes"`
	Edges      []meshEdge `json:"edges"`
	Restricted int        `json:"restricted"`
	Clustered  bool       `json:"clustered"`
}

func getMesh(t *testing.T, ts *httptest.Server) meshGraph {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/panorama/mesh", "", "")
	if code != http.StatusOK {
		t.Fatalf("GET mesh status = %d, body = %s", code, body)
	}
	var g meshGraph
	if err := json.Unmarshal(body, &g); err != nil {
		t.Fatalf("decode mesh: %v (%s)", err, body)
	}
	return g
}

func meshNodeByID(t *testing.T, g meshGraph, id string) meshNode {
	t.Helper()
	for _, n := range g.Nodes {
		if n.ID == id {
			return n
		}
	}
	t.Fatalf("no node %q in %+v", id, g.Nodes)
	return meshNode{}
}

func meshHasEdge(g meshGraph, from, to, kind string) bool {
	for _, e := range g.Edges {
		if e.From == from && e.To == to && e.Kind == kind {
			return true
		}
	}
	return false
}

// TestPanoramaMeshOnAFreshServer is the cold-start claim ADR-0211 rests on: a
// server where nobody has modeled anything still answers, and answers with a
// well-formed graph rather than a null, an error, or a blank page.
//
// A fresh server is not empty. It carries the platform-managed "Atlas System"
// application (ADR-0122), so the mesh shows exactly one node and no processes —
// which is the honest picture and is pinned here so a change to the bootstrap does
// not silently change what a first-time user sees.
func TestPanoramaMeshOnAFreshServer(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodGet, "/api/v1/panorama/mesh", "", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", code, body)
	}
	var g meshGraph
	if err := json.Unmarshal(body, &g); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if g.Nodes == nil || g.Edges == nil {
		t.Errorf("fresh mesh has null collections: %s", body)
	}
	if len(g.Edges) != 0 {
		t.Errorf("fresh server produced %d edges, want none", len(g.Edges))
	}
	for _, n := range g.Nodes {
		if n.Kind != "application" {
			t.Errorf("fresh server produced a %s node (%q); only the bootstrap application should exist", n.Kind, n.ID)
		}
	}
	if n := meshNodeByID(t, g, "application:system"); n.Name != "Atlas System" {
		t.Errorf("bootstrap application node = %+v", n)
	}
}

// TestPanoramaMeshDerivesDeployedProcessesAndTheirCalls is the whole point of the
// derived altitude: deploy two processes wired by a call activity and the mesh
// shows them and the dependency between them, with nobody having drawn anything.
func TestPanoramaMeshDerivesDeployedProcessesAndTheirCalls(t *testing.T) {
	ts := newTestServer(t)

	// The caller alone: "child" is not deployed yet, so its call is unresolved and
	// "missing" never will be.
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", callerBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy caller status = %d, body = %s", code, body)
	}
	var caller struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &caller); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}

	g := getMesh(t, ts)
	callerID := meshProcessID(caller.Key)
	if n := meshNodeByID(t, g, callerID); n.Kind != "process" || n.ProcessID != "caller" || n.Provenance != "derived" {
		t.Errorf("caller node = %+v, want a derived process node for %q", n, "caller")
	}
	for _, pid := range []string{"child", "missing"} {
		if !meshHasEdge(g, callerID, "unresolved:"+pid, "calls") {
			t.Errorf("edge to unresolved %q missing from %+v", pid, g.Edges)
		}
	}

	// Deploying the child turns one unresolved call into a real dependency, without
	// anything about the caller changing.
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/deployments", childBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy child status = %d, body = %s", code, body)
	}
	var child struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &child); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}

	g = getMesh(t, ts)
	childID := meshProcessID(child.Key)
	if !meshHasEdge(g, callerID, childID, "calls") {
		t.Errorf("resolved call edge missing from %+v", g.Edges)
	}
	if meshHasEdge(g, callerID, "unresolved:child", "calls") {
		t.Errorf("stale unresolved edge to child survived the deploy: %+v", g.Edges)
	}
	// "missing" is still nowhere, and must stay visible as such.
	if !meshHasEdge(g, callerID, "unresolved:missing", "calls") {
		t.Errorf("edge to still-undeployed %q missing from %+v", "missing", g.Edges)
	}
}

// meshProcessID spells the node id the mesh gives a deployment, so the tests read
// the same way the payload does.
func meshProcessID(key uint64) string {
	return "process:" + strconv.FormatUint(key, 10)
}
