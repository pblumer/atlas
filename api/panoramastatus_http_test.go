package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// meshStatusGraph mirrors the severity half of the mesh payload (ADR-0211 §4).
// Declared beside the graph the other mesh tests use rather than folded into it,
// so those keep pinning exactly the contract they were written for.
type meshStatusNode struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	State        string `json:"state"`
	Severity     string `json:"severity"`
	Reason       string `json:"reason"`
	SeverityFrom string `json:"severityFrom"`
	Application  string `json:"application"`
}

type meshStatusGraph struct {
	Nodes  []meshStatusNode `json:"nodes"`
	Status struct {
		OK          int  `json:"ok"`
		Attention   int  `json:"attention"`
		Critical    int  `json:"critical"`
		Unknown     int  `json:"unknown"`
		Partial     bool `json:"partial"`
		Unavailable []struct {
			State  string `json:"state"`
			Reason string `json:"reason"`
		} `json:"unavailable"`
	} `json:"status"`
}

func getMeshStatus(t *testing.T, ts *httptest.Server) meshStatusGraph {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/panorama/mesh", "", "")
	if code != http.StatusOK {
		t.Fatalf("GET mesh status = %d, body = %s", code, body)
	}
	var g meshStatusGraph
	if err := json.Unmarshal(body, &g); err != nil {
		t.Fatalf("decode mesh: %v (%s)", err, body)
	}
	return g
}

func statusNode(t *testing.T, g meshStatusGraph, id string) meshStatusNode {
	t.Helper()
	for _, n := range g.Nodes {
		if n.ID == id {
			return n
		}
	}
	t.Fatalf("no node %q in %+v", id, g.Nodes)
	return meshStatusNode{}
}

// TestMeshAlwaysDeclaresTheStatesItCannotObserve is the claim the whole slice rests
// on. ADR-0211 §4 puts severity over ADR-0189 §6's seven states, and this build can
// produce five of them; the other two need a source outside the engine. A payload
// that simply omitted them would render an instance nothing is watching as green,
// so it names them and says why they are missing.
func TestMeshAlwaysDeclaresTheStatesItCannotObserve(t *testing.T) {
	g := getMeshStatus(t, newTestServer(t))

	declared := map[string]string{}
	for _, u := range g.Status.Unavailable {
		declared[u.State] = u.Reason
	}
	if len(declared) != 2 {
		t.Fatalf("unavailable = %+v, want unreachable and stale", g.Status.Unavailable)
	}
	for _, state := range []string{"unreachable", "stale"} {
		if declared[state] == "" {
			t.Errorf("state %q is not declared unavailable: %+v", state, g.Status.Unavailable)
		}
	}
}

// TestMeshReportsParkedWorkAsDegradedAndAttributesIt is the answer an operator
// opens the landscape for. A process whose token is parked behind an incident is
// not healthy, the picture says so, and the application above it says which process
// made it say so — an unattributed finding at the top is not actionable.
func TestMeshReportsParkedWorkAsDegradedAndAttributesIt(t *testing.T) {
	ts := newTestServer(t)

	// Deployed into an application, so the containment edge exists and the
	// aggregation this test is half about has something to aggregate over.
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications", `{"name":"Billing"}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create application: status=%d body=%s", code, body)
	}
	var appRec struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &appRec); err != nil || appRec.ID == "" {
		t.Fatalf("decode application: %v (%s)", err, body)
	}

	code, body = doReq(t, ts, http.MethodPost, "/api/v1/deployments?projectId="+appRec.ID,
		incidentUserTaskBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	processID := meshProcessID(dep.Key)

	// Deployed and never run: nothing is failing, so the honest answer is healthy
	// rather than unknown. The engine holds every incident it ever raised, so this
	// is a fact about itself it can answer without asking anything outside.
	before := statusNode(t, getMeshStatus(t, ts), processID)
	if before.State != "healthy" || before.Severity != "ok" {
		t.Fatalf("a process with nothing parked = %q/%q, want healthy/ok", before.State, before.Severity)
	}

	if code, body = doReq(t, ts, http.MethodPost,
		fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	var tasks []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &tasks); err != nil || len(tasks) != 1 {
		t.Fatalf("list tasks: %v (status=%d body=%s)", err, code, body)
	}
	if code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/fail", tasks[0].Key),
		`{"retries":0,"message":"boom"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("fail job: status=%d body=%s", code, body)
	}

	g := getMeshStatus(t, ts)
	after := statusNode(t, g, processID)
	if after.State != "degraded" || after.Severity != "attention" {
		t.Fatalf("a parked process = %q/%q, want degraded/attention", after.State, after.Severity)
	}
	if !strings.Contains(after.Reason, "1 token(s)") {
		t.Errorf("reason = %q, want the count of parked tokens", after.Reason)
	}
	// A parked token is not an outage: ADR-0211 §4 keeps "some work inside it
	// failed" below "it cannot do work at all".
	if after.Severity == "critical" {
		t.Error("one parked token is reported as critical")
	}
	if after.Application == "" {
		t.Fatalf("process node %+v is in no application; the aggregation is untested", after)
	}
	parent := statusNode(t, g, after.Application)
	if parent.Severity != "attention" || parent.SeverityFrom != processID {
		t.Errorf("application = %q from %q, want attention attributed to %q",
			parent.Severity, parent.SeverityFrom, processID)
	}
	if !strings.Contains(parent.Reason, "1 token(s)") {
		t.Errorf("application reason = %q, want the child's own words", parent.Reason)
	}
	if g.Status.Attention < 1 {
		t.Errorf("status summary = %+v, want at least one node needing attention", g.Status)
	}
}

// TestMeshReportsAWorkerThatCannotServeWorkAsCritical is the other half of what
// this server can see about itself. A disabled worker builds no client, so every
// task pointing at it parks — the dependency is out, not merely unwell, which is
// what ADR-0211 §4 reserves critical for.
func TestMeshReportsAWorkerThatCannotServeWorkAsCritical(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/configured-workers",
		`{"name":"ops-mail","kind":"mail","endpoint":"smtp://relay.test:25","sender":"ops@example.test","enabled":false}`,
		"application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create worker: status=%d body=%s", code, body)
	}
	if code, body = doReq(t, ts, http.MethodPost, "/api/v1/deployments", workerMeshBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}

	g := getMeshStatus(t, ts)
	var worker meshStatusNode
	for _, n := range g.Nodes {
		if n.Kind == "worker" && n.Name == "ops-mail" {
			worker = n
		}
	}
	if worker.ID == "" {
		t.Fatalf("no worker node for ops-mail in %+v", g.Nodes)
	}
	if worker.State != "not-ready" || worker.Severity != "critical" {
		t.Fatalf("a disabled worker = %q/%q, want not-ready/critical", worker.State, worker.Severity)
	}
	if !strings.Contains(worker.Reason, "disabled") {
		t.Errorf("reason = %q, want the same words the worker list gives", worker.Reason)
	}
	// The finding is on the worker and does not travel up the uses edge: ADR-0211
	// §4 aggregates containment only, so one broken worker cannot repaint the
	// landscape. Impact analysis is what answers the dependency question.
	for _, n := range g.Nodes {
		if n.Kind == "process" && n.Severity == "critical" {
			t.Errorf("process %q inherited critical from its worker dependency", n.ID)
		}
	}
}

// TestMeshReportsAUsableWorkerAsHealthy is the negative of the case above, and
// keeps the severity from being a constant: the same worker, enabled, is a
// dependency the engine can actually serve.
func TestMeshReportsAUsableWorkerAsHealthy(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/configured-workers",
		`{"name":"ops-mail","kind":"mail","endpoint":"smtp://relay.test:25","sender":"ops@example.test"}`,
		"application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create worker: status=%d body=%s", code, body)
	}
	if code, body = doReq(t, ts, http.MethodPost, "/api/v1/deployments", workerMeshBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}

	g := getMeshStatus(t, ts)
	for _, n := range g.Nodes {
		if n.Kind != "worker" || n.Name != "ops-mail" {
			continue
		}
		if n.State != "healthy" || n.Severity != "ok" {
			t.Fatalf("a usable worker = %q/%q, want healthy/ok", n.State, n.Severity)
		}
		return
	}
	t.Fatalf("no worker node for ops-mail in %+v", g.Nodes)
}
