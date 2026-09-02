package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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
	WorkerType  string `json:"workerType"`
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
		if !meshHasEdge(g, callerID, "unresolved:process:"+pid, "calls") {
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
	if meshHasEdge(g, callerID, "unresolved:process:child", "calls") {
		t.Errorf("stale unresolved edge to child survived the deploy: %+v", g.Edges)
	}
	// "missing" is still nowhere, and must stay visible as such.
	if !meshHasEdge(g, callerID, "unresolved:process:missing", "calls") {
		t.Errorf("edge to still-undeployed %q missing from %+v", "missing", g.Edges)
	}
}

// meshProcessID spells the node id the mesh gives a deployment, so the tests read
// the same way the payload does.
func meshProcessID(key uint64) string {
	return "process:" + strconv.FormatUint(key, 10)
}

// workerMeshBPMN names a mail worker the way every model names one (ADR-0036/0041):
// by name, with no endpoint and no secret. The attribute is still connector="…" —
// that is the model contract ADR-0203 deliberately leaves alone.
const workerMeshBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas.dev/schema/1.0/bpmn">
  <process id="notifier" name="Notifier" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="notify">
      <extensionElements><atlas:mailConnector connector="ops-mail" to="a@b.ch" subject="hi" body="hi"/></extensionElements>
    </serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="notify"/>
    <sequenceFlow id="f2" sourceRef="notify" targetRef="end"/>
  </process>
</definitions>`

// TestPanoramaMeshShowsAnUnconfiguredWorker is the finding a model cannot make
// about itself. It names a worker by name and carries nothing that says whether that
// name exists here, so a process can deploy clean and then park its first token
// (ADR-0158). The landscape is on the outside, next to the configured workers, and
// says so before anything runs.
func TestPanoramaMeshShowsAnUnconfiguredWorker(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", workerMeshBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status = %d, body = %s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}

	g := getMesh(t, ts)
	if n := meshNodeByID(t, g, "unresolved:worker:ops-mail"); n.Name != "ops-mail" {
		t.Errorf("unresolved worker node = %+v", n)
	}
	if !meshHasEdge(g, meshProcessID(dep.Key), "unresolved:worker:ops-mail", "uses") {
		t.Errorf("uses edge to the unconfigured worker missing from %+v", g.Edges)
	}
}

// TestPanoramaMeshNeverCarriesAWorkerEndpoint is the disclosure bound, asserted
// against a real configured worker on a real server rather than against the
// derivation alone: the endpoint reaches the store, and must not reach the wire.
func TestPanoramaMeshNeverCarriesAWorkerEndpoint(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/configured-workers",
		`{"name":"ops-mail","kind":"mail","endpoint":"smtp://internal-relay.corp.example:587","sender":"ops@example.test"}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create worker status = %d, body = %s", code, body)
	}
	if code, body = doReq(t, ts, http.MethodPost, "/api/v1/deployments", workerMeshBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy status = %d, body = %s", code, body)
	}

	code, raw := doReq(t, ts, http.MethodGet, "/api/v1/panorama/mesh", "", "")
	if code != http.StatusOK {
		t.Fatalf("mesh status = %d, body = %s", code, raw)
	}
	for _, leak := range []string{"internal-relay", "corp.example", "smtp://", "587"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("mesh payload leaks %q: %s", leak, raw)
		}
	}
	// The dependency is still drawn — the endpoint is what stays out, not the edge.
	var g meshGraph
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var found bool
	for _, n := range g.Nodes {
		if n.Kind == "worker" && n.Name == "ops-mail" {
			found = true
			// The Worker Type is what a worker node carries besides its name, and
			// it is named workerType on the wire: this surface is new, so it speaks
			// the vocabulary of ADR-0203 rather than the store's older spelling.
			if n.WorkerType != "mail" {
				t.Errorf("worker node = %+v, want workerType %q", n, "mail")
			}
		}
	}
	if !found {
		t.Errorf("configured worker missing from %+v", g.Nodes)
	}
}

// TestLandscapeDrawsItsPeersAndSaysWhatTheyAre is what makes unreachable and stale
// producible on the landscape at all.
//
// Before this the mesh contacted nothing, so it declared both states unproducible —
// true, and useless: an operator scanning the picture for trouble could not see that
// a whole peer had gone away. A deployment target that does not answer is now a node
// on the landscape with a finding on it.
func TestLandscapeDrawsItsPeersAndSaysWhatTheyAre(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodGet, "/api/v1/panorama/mesh", "", "")
	if code != http.StatusOK {
		t.Fatalf("mesh status = %d, body = %s", code, body)
	}
	var bare struct {
		Nodes  []struct{ ID, Kind string } `json:"nodes"`
		Status struct {
			Unavailable []struct{ State, Reason string } `json:"unavailable"`
		} `json:"status"`
	}
	if err := json.Unmarshal(body, &bare); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	// With no target configured the landscape reaches nothing, and says so — naming
	// what would change that rather than only that it cannot.
	if len(bare.Status.Unavailable) != 2 {
		t.Fatalf("a peerless landscape declares %#v, want unreachable and stale",
			bare.Status.Unavailable)
	}
	for _, u := range bare.Status.Unavailable {
		if !strings.Contains(u.Reason, "deployment target") {
			t.Errorf("%q does not say what would make it producible: %q", u.State, u.Reason)
		}
	}

	// Now configure one. Nothing is listening at that address, so asking it is the
	// case this whole slice is for.
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/targets",
		`{"name":"Production","baseUrl":"https://atlas.example.test"}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create target status = %d, body = %s", code, body)
	}

	code, body = doReq(t, ts, http.MethodGet, "/api/v1/panorama/mesh", "", "")
	if code != http.StatusOK {
		t.Fatalf("mesh status = %d, body = %s", code, body)
	}
	var peered struct {
		Nodes []struct {
			ID       string `json:"id"`
			Kind     string `json:"kind"`
			Name     string `json:"name"`
			State    string `json:"state"`
			Severity string `json:"severity"`
			Reason   string `json:"reason"`
		} `json:"nodes"`
		Status struct {
			Unavailable []struct{ State, Reason string } `json:"unavailable"`
		} `json:"status"`
	}
	if err := json.Unmarshal(body, &peered); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}

	var target *struct {
		ID       string `json:"id"`
		Kind     string `json:"kind"`
		Name     string `json:"name"`
		State    string `json:"state"`
		Severity string `json:"severity"`
		Reason   string `json:"reason"`
	}
	for i := range peered.Nodes {
		if peered.Nodes[i].Kind == "target" {
			target = &peered.Nodes[i]
		}
	}
	if target == nil {
		t.Fatalf("no target node on the landscape: %s", body)
	}
	if target.Name != "Production" {
		t.Errorf("target name = %q, want the operator's name for it", target.Name)
	}
	// Unreachable, not critical: "I could not reach it" and "it is broken" are
	// different findings (ADR-0211 §4).
	if target.State != "unreachable" || target.Severity != "attention" {
		t.Errorf("a peer that does not answer = %q/%q, want unreachable and attention",
			target.State, target.Severity)
	}
	if target.Reason == "" {
		t.Error("the target carries no reason; a finding without one is not actionable")
	}
	// And the payload stops declaring what it can now produce.
	if len(peered.Status.Unavailable) != 0 {
		t.Errorf("Unavailable = %#v on a landscape that drew a peer", peered.Status.Unavailable)
	}

	// The base URL never reaches the payload: it is this operator's map of where
	// their infrastructure lives, and the landscape is opened by anybody with
	// modeler access.
	for _, leak := range []string{"atlas.example.test", "https://", "credentialRef"} {
		if strings.Contains(string(body), leak) {
			t.Errorf("the landscape leaks %q: %s", leak, body)
		}
	}
}
