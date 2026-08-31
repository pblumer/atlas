package panorama

import (
	"encoding/json"
	"strings"
	"testing"
)

// app builds one visible application with the given id and name.
func app(id, name string) Application {
	return Application{ID: id, Name: name, CanView: true}
}

// proc builds one visible deployed process owned by an application.
func proc(key uint64, processID, name, appID string, calls ...Call) Process {
	return Process{
		Key: key, ProcessID: processID, Name: name, Version: 1,
		ApplicationID: appID, CanView: true, Calls: calls,
	}
}

// nodeByID finds a node, failing the test when it is absent.
func nodeByID(t *testing.T, g Graph, id string) Node {
	t.Helper()
	for _, n := range g.Nodes {
		if n.ID == id {
			return n
		}
	}
	t.Fatalf("no node %q in %#v", id, g.Nodes)
	return Node{}
}

// hasEdge reports whether the graph carries exactly this edge.
func hasEdge(g Graph, from, to, kind string) bool {
	for _, e := range g.Edges {
		if e.From == from && e.To == to && e.Kind == kind {
			return true
		}
	}
	return false
}

// kindsOf counts nodes per kind, so a test can assert a shape without pinning ids.
func kindsOf(g Graph) map[string]int {
	out := map[string]int{}
	for _, n := range g.Nodes {
		out[n.Kind]++
	}
	return out
}

// TestDeriveGraphEmptyLandscapeRendersEmptyGraph pins the cold-start case the whole
// slice exists for: a server where nobody has modeled anything still answers, and it
// answers with empty collections rather than nulls — the mesh renderer iterates them.
func TestDeriveGraphEmptyLandscapeRendersEmptyGraph(t *testing.T) {
	g := DeriveGraph(Landscape{}, Options{})

	if len(g.Nodes) != 0 || len(g.Edges) != 0 {
		t.Fatalf("empty landscape produced %d nodes and %d edges", len(g.Nodes), len(g.Edges))
	}
	body, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(body), `"nodes":[]`) || !strings.Contains(string(body), `"edges":[]`) {
		t.Errorf("empty graph marshals as %s — nodes and edges must be [], never null", body)
	}
}

// TestDeriveGraphContainsProcessesInTheirApplication covers the structural spine:
// an application node per application, a process node per deployment, and a
// containment edge between them. Everything derived carries derived provenance —
// nothing in this slice is modeled yet (ADR-0211 §2).
func TestDeriveGraphContainsProcessesInTheirApplication(t *testing.T) {
	g := DeriveGraph(Landscape{
		Applications: []Application{app("a1", "Billing")},
		Processes: []Process{
			proc(1, "invoice", "Invoice", "a1"),
			proc(2, "dunning", "Dunning", "a1"),
		},
	}, Options{})

	if got := kindsOf(g); got[KindApplication] != 1 || got[KindProcess] != 2 {
		t.Fatalf("node kinds = %v, want 1 application and 2 processes", got)
	}
	if !hasEdge(g, "application:a1", "process:1", EdgeContains) ||
		!hasEdge(g, "application:a1", "process:2", EdgeContains) {
		t.Errorf("containment edges missing from %#v", g.Edges)
	}
	for _, n := range g.Nodes {
		if n.Provenance != ProvenanceDerived {
			t.Errorf("node %q provenance = %q, want %q", n.ID, n.Provenance, ProvenanceDerived)
		}
	}
}

// TestDeriveGraphDrawsResolvedCallActivityEdges is the claim that makes the mesh
// worth building: a call activity is a dependency Atlas can point at, so it becomes
// an edge between the two deployments the call actually resolves to.
func TestDeriveGraphDrawsResolvedCallActivityEdges(t *testing.T) {
	g := DeriveGraph(Landscape{
		Applications: []Application{app("a1", "Billing")},
		Processes: []Process{
			proc(1, "invoice", "Invoice", "a1", Call{ElementID: "Call_1", CalledProcessID: "dunning", TargetKey: 2}),
			proc(2, "dunning", "Dunning", "a1"),
		},
	}, Options{})

	if !hasEdge(g, "process:1", "process:2", EdgeCalls) {
		t.Errorf("call edge missing from %#v", g.Edges)
	}
	if got := kindsOf(g); got[KindRestricted] != 0 || got[KindUnresolved] != 0 {
		t.Errorf("a fully resolved, visible call produced placeholders: %v", got)
	}
}

// TestDeriveGraphKeepsTheEdgeToARestrictedTarget is ADR-0211 §3's rule. Dropping the
// edge would render "this process depends on nothing", which is a false statement
// when it means "you may not see what it depends on" — and unlike a missing edge, a
// placeholder is visible incompleteness.
func TestDeriveGraphKeepsTheEdgeToARestrictedTarget(t *testing.T) {
	hidden := proc(2, "dunning", "Dunning", "a2")
	hidden.CanView = false

	g := DeriveGraph(Landscape{
		Applications: []Application{app("a1", "Billing"), {ID: "a2", Name: "Collections"}},
		Processes: []Process{
			proc(1, "invoice", "Invoice", "a1", Call{ElementID: "Call_1", CalledProcessID: "dunning", TargetKey: 2}),
			hidden,
		},
	}, Options{})

	if got := kindsOf(g); got[KindProcess] != 1 || got[KindRestricted] != 1 {
		t.Fatalf("node kinds = %v, want the caller's own process plus one placeholder", got)
	}
	if g.Restricted != 1 {
		t.Errorf("Restricted = %d, want 1 — the legend states the incompleteness", g.Restricted)
	}
	var placeholder Node
	for _, n := range g.Nodes {
		if n.Kind == KindRestricted {
			placeholder = n
		}
	}
	if !hasEdge(g, "process:1", placeholder.ID, EdgeCalls) {
		t.Errorf("edge to the restricted placeholder missing from %#v", g.Edges)
	}
	// The invisible application must not appear either — it is as hidden as its
	// process, and an application node would name it.
	if got := kindsOf(g); got[KindApplication] != 1 {
		t.Errorf("node kinds = %v, want only the visible application", got)
	}
}

// TestRestrictedPlaceholderDisclosesOnlyItsKind guards the disclosure bound the
// record accepted. Everything that would name the hidden resource — its display
// name, its BPMN process id, its version, its owning application — must be absent
// from the payload, not merely absent from the rendering.
func TestRestrictedPlaceholderDisclosesOnlyItsKind(t *testing.T) {
	hidden := proc(2, "payroll-sensitive", "Payroll", "a2")
	hidden.CanView = false

	g := DeriveGraph(Landscape{
		Applications: []Application{app("a1", "Billing"), {ID: "a2", Name: "HR Confidential"}},
		Processes: []Process{
			proc(1, "invoice", "Invoice", "a1", Call{ElementID: "Call_1", CalledProcessID: "payroll-sensitive", TargetKey: 2}),
			hidden,
		},
	}, Options{})

	body, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, leak := range []string{"payroll-sensitive", "Payroll", "HR Confidential", "a2"} {
		if strings.Contains(string(body), leak) {
			t.Errorf("restricted graph leaks %q in %s", leak, body)
		}
	}
}

// TestRestrictedPlaceholdersAreDistinctPerTarget: merging two hidden targets into
// one placeholder would draw a dependency that does not exist, and splitting one
// target across two placeholders would invent a second dependency. Identity is kept
// internally by target key and exposed only as an opaque per-response ordinal.
func TestRestrictedPlaceholdersAreDistinctPerTarget(t *testing.T) {
	hiddenA := proc(2, "alpha", "Alpha", "a2")
	hiddenA.CanView = false
	hiddenB := proc(3, "beta", "Beta", "a2")
	hiddenB.CanView = false

	g := DeriveGraph(Landscape{
		Applications: []Application{app("a1", "Billing"), {ID: "a2", Name: "Hidden"}},
		Processes: []Process{
			proc(1, "caller", "Caller", "a1",
				Call{ElementID: "Call_1", CalledProcessID: "alpha", TargetKey: 2},
				Call{ElementID: "Call_2", CalledProcessID: "beta", TargetKey: 3},
				Call{ElementID: "Call_3", CalledProcessID: "alpha", TargetKey: 2}),
			hiddenA, hiddenB,
		},
	}, Options{})

	if got := kindsOf(g)[KindRestricted]; got != 2 {
		t.Fatalf("restricted placeholders = %d, want 2 — one per distinct hidden target", got)
	}
	if g.Restricted != 2 {
		t.Errorf("Restricted = %d, want 2", g.Restricted)
	}
	// Three call activities, two of them to the same target: two distinct edges.
	calls := 0
	for _, e := range g.Edges {
		if e.Kind == EdgeCalls {
			calls++
		}
	}
	if calls != 2 {
		t.Errorf("call edges = %d, want 2 — repeated calls to one target are one edge", calls)
	}
}

// TestDeriveGraphMarksAnUndeployedCallTargetUnresolved separates "you may not see
// it" from "it is not here". Both are honest answers and they are different ones;
// collapsing them would tell an operator a missing deployment is a permission
// problem. The called process id is safe to carry: it is in the caller's own model.
func TestDeriveGraphMarksAnUndeployedCallTargetUnresolved(t *testing.T) {
	g := DeriveGraph(Landscape{
		Applications: []Application{app("a1", "Billing")},
		Processes: []Process{
			proc(1, "invoice", "Invoice", "a1", Call{ElementID: "Call_1", CalledProcessID: "nowhere"}),
		},
	}, Options{})

	if got := kindsOf(g); got[KindUnresolved] != 1 {
		t.Fatalf("node kinds = %v, want one unresolved node", got)
	}
	n := nodeByID(t, g, "unresolved:process:nowhere")
	if n.Name != "nowhere" {
		t.Errorf("unresolved node name = %q, want the called process id", n.Name)
	}
	if !hasEdge(g, "process:1", "unresolved:process:nowhere", EdgeCalls) {
		t.Errorf("edge to the unresolved target missing from %#v", g.Edges)
	}
}

// TestDeriveGraphIsDeterministic: the same landscape must produce byte-identical
// JSON however the inputs are ordered, or a polling UI redraws on noise and two
// responses cannot be diffed.
func TestDeriveGraphIsDeterministic(t *testing.T) {
	forward := Landscape{
		Applications: []Application{app("a1", "Billing"), app("a2", "Sales")},
		Processes: []Process{
			proc(1, "invoice", "Invoice", "a1", Call{ElementID: "Call_1", CalledProcessID: "order", TargetKey: 2}),
			proc(2, "order", "Order", "a2"),
		},
	}
	reversed := Landscape{
		Applications: []Application{forward.Applications[1], forward.Applications[0]},
		Processes:    []Process{forward.Processes[1], forward.Processes[0]},
	}

	a, err := json.Marshal(DeriveGraph(forward, Options{}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	b, err := json.Marshal(DeriveGraph(reversed, Options{}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(a) != string(b) {
		t.Errorf("input order changed the graph:\n %s\n %s", a, b)
	}
}

// TestDeriveGraphClustersOverTheSizeBudget is ADR-0211 §7's honest degrade: over
// budget the mesh collapses to applications and says so, rather than returning a
// hairball the browser cannot lay out or silently truncating to a graph that looks
// complete.
func TestDeriveGraphClustersOverTheSizeBudget(t *testing.T) {
	land := Landscape{Applications: []Application{app("a1", "Billing")}}
	for i := 1; i <= 10; i++ {
		land.Processes = append(land.Processes, proc(uint64(i), "p", "P", "a1"))
	}

	g := DeriveGraph(land, Options{MaxNodes: 5})

	if !g.Clustered {
		t.Error("Clustered = false — over budget the response must say it collapsed")
	}
	if got := kindsOf(g); got[KindProcess] != 0 || got[KindApplication] != 1 {
		t.Fatalf("node kinds = %v, want applications only", got)
	}
	if n := nodeByID(t, g, "application:a1"); n.Children != 10 {
		t.Errorf("Children = %d, want 10 — a collapsed node states what it stands for", n.Children)
	}
	if len(g.Edges) != 0 {
		t.Errorf("collapsed graph carries %d edges, want none between applications yet", len(g.Edges))
	}
}

// TestDeriveGraphWithinBudgetIsNotClustered pins the other side of the boundary, so
// the degrade cannot quietly become the normal path.
func TestDeriveGraphWithinBudgetIsNotClustered(t *testing.T) {
	g := DeriveGraph(Landscape{
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{proc(1, "invoice", "Invoice", "a1")},
	}, Options{MaxNodes: 5})

	if g.Clustered {
		t.Error("Clustered = true for a graph inside its budget")
	}
	if got := kindsOf(g)[KindProcess]; got != 1 {
		t.Errorf("process nodes = %d, want 1", got)
	}
}

// TestDeriveGraphOmitsAnApplicationTheCallerCannotView keeps the listing filter and
// the graph filter the same one: an application absent from a list must not reappear
// as a node here, and its processes go with it.
func TestDeriveGraphOmitsAnApplicationTheCallerCannotView(t *testing.T) {
	hiddenProc := proc(2, "secret", "Secret", "a2")
	hiddenProc.CanView = false

	g := DeriveGraph(Landscape{
		Applications: []Application{app("a1", "Billing"), {ID: "a2", Name: "Hidden"}},
		Processes:    []Process{proc(1, "invoice", "Invoice", "a1"), hiddenProc},
	}, Options{})

	if got := kindsOf(g); got[KindApplication] != 1 || got[KindProcess] != 1 {
		t.Fatalf("node kinds = %v, want one application and one process", got)
	}
	// Nothing references the hidden pair, so no placeholder is warranted either.
	if g.Restricted != 0 {
		t.Errorf("Restricted = %d, want 0 — an unreferenced hidden resource is simply absent", g.Restricted)
	}
}

// --- Part 2: connectors and decisions as dependency nodes ---

// TestDeriveGraphDrawsConnectorDependencies: a model names a connector by name
// only and cannot tell whether that name is configured anywhere (ADR-0036/0041).
// The mesh is on the outside, where the connector store is, so it can answer —
// which is the whole reason the references are enumerable (ADR-0158).
func TestDeriveGraphDrawsConnectorDependencies(t *testing.T) {
	p := proc(1, "invoice", "Invoice", "a1")
	p.Connectors = []ConnectorUse{{ElementID: "Task_1", Name: "billing-rest", TargetID: "c1"}}

	g := DeriveGraph(Landscape{
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{p},
		Connectors:   []Connector{{ID: "c1", Name: "billing-rest", Kind: "rest", CanView: true}},
	}, Options{})

	if n := nodeByID(t, g, "connector:c1"); n.Kind != KindConnector || n.Name != "billing-rest" {
		t.Errorf("connector node = %+v", n)
	}
	if !hasEdge(g, "process:1", "connector:c1", EdgeUses) {
		t.Errorf("connector edge missing from %#v", g.Edges)
	}
}

// TestConnectorNodeCarriesNoEndpointOrCredential is the disclosure bound. A
// connector record holds an endpoint and a credential reference; neither belongs
// in a landscape picture that anyone with modeler access can open, and an internal
// hostname is exactly what ADR-0211 §10 keeps out of an export. The assertion is on
// the marshalled payload, not on the rendering.
func TestConnectorNodeCarriesNoEndpointOrCredential(t *testing.T) {
	p := proc(1, "invoice", "Invoice", "a1")
	p.Connectors = []ConnectorUse{{ElementID: "Task_1", Name: "billing-rest", TargetID: "c1"}}

	g := DeriveGraph(Landscape{
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{p},
		Connectors: []Connector{{
			ID: "c1", Name: "billing-rest", Kind: "rest", CanView: true,
			Endpoint: "https://internal-billing.corp.example/api", CredentialsRef: "vault://billing",
		}},
	}, Options{})

	body, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, leak := range []string{"internal-billing", "corp.example", "vault://billing", "CredentialsRef", "endpoint"} {
		if strings.Contains(string(body), leak) {
			t.Errorf("connector node leaks %q in %s", leak, body)
		}
	}
}

// TestDeriveGraphMarksAnUnconfiguredConnectorUnresolved is the operational answer
// the model cannot give: this process asks for a connector nobody configured, so it
// would fail at run time. The name is safe to show — it is in the caller's own model.
func TestDeriveGraphMarksAnUnconfiguredConnectorUnresolved(t *testing.T) {
	p := proc(1, "invoice", "Invoice", "a1")
	p.Connectors = []ConnectorUse{{ElementID: "Task_1", Name: "nowhere-rest"}}

	g := DeriveGraph(Landscape{
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{p},
	}, Options{})

	n := nodeByID(t, g, "unresolved:connector:nowhere-rest")
	if n.Kind != KindUnresolved || n.Name != "nowhere-rest" {
		t.Errorf("unresolved connector node = %+v", n)
	}
	if !hasEdge(g, "process:1", n.ID, EdgeUses) {
		t.Errorf("edge to the unconfigured connector missing from %#v", g.Edges)
	}
}

// TestDeriveGraphKeepsTheEdgeToARestrictedConnector applies §3's rule to the second
// resource kind that has a sharing scope of its own (ADR-0205). Same rule, same
// placeholder, and a connector must not merge with a hidden process.
func TestDeriveGraphKeepsTheEdgeToARestrictedConnector(t *testing.T) {
	p := proc(1, "invoice", "Invoice", "a1")
	p.Connectors = []ConnectorUse{{ElementID: "Task_1", Name: "secret-rest", TargetID: "c1"}}

	g := DeriveGraph(Landscape{
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{p},
		Connectors:   []Connector{{ID: "c1", Name: "secret-rest", Kind: "rest"}},
	}, Options{})

	if got := kindsOf(g); got[KindConnector] != 0 || got[KindRestricted] != 1 {
		t.Fatalf("node kinds = %v, want the connector replaced by a placeholder", got)
	}
	if g.Restricted != 1 {
		t.Errorf("Restricted = %d, want 1", g.Restricted)
	}
	body, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(body), "secret-rest") {
		t.Errorf("restricted connector leaks its name in %s", body)
	}
}

// TestRestrictedProcessesAndConnectorsDoNotShareAPlaceholder: placeholders are
// keyed per resource, and a hidden process is not a hidden connector. Merging them
// would draw one dependency where there are two, on different things.
func TestRestrictedProcessesAndConnectorsDoNotShareAPlaceholder(t *testing.T) {
	hidden := proc(2, "dunning", "Dunning", "a2")
	hidden.CanView = false
	p := proc(1, "invoice", "Invoice", "a1", Call{ElementID: "Call_1", CalledProcessID: "dunning", TargetKey: 2})
	p.Connectors = []ConnectorUse{{ElementID: "Task_1", Name: "secret-rest", TargetID: "c1"}}

	g := DeriveGraph(Landscape{
		Applications: []Application{app("a1", "Billing"), {ID: "a2", Name: "Hidden"}},
		Processes:    []Process{p, hidden},
		Connectors:   []Connector{{ID: "c1", Name: "secret-rest", Kind: "rest"}},
	}, Options{})

	if got := kindsOf(g)[KindRestricted]; got != 2 {
		t.Errorf("restricted placeholders = %d, want one per hidden resource", got)
	}
	if g.Restricted != 2 {
		t.Errorf("Restricted = %d, want 2", g.Restricted)
	}
}

// TestDeriveGraphDrawsDecisionDependencies covers the third edge the compiler
// already enumerates. A remote (connector-mode) decision is deliberately not here:
// it arrives as a connector reference instead, which is where its dependency
// actually points.
func TestDeriveGraphDrawsDecisionDependencies(t *testing.T) {
	p := proc(1, "invoice", "Invoice", "a1")
	p.Decisions = []string{"credit-score"}

	g := DeriveGraph(Landscape{
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{p},
		Decisions:    []Decision{{ID: "credit-score", Name: "Credit score", CanView: true}},
	}, Options{})

	if n := nodeByID(t, g, "decision:credit-score"); n.Kind != KindDecision || n.Name != "Credit score" {
		t.Errorf("decision node = %+v", n)
	}
	if !hasEdge(g, "process:1", "decision:credit-score", EdgeUses) {
		t.Errorf("decision edge missing from %#v", g.Edges)
	}
}

// TestDeriveGraphDeduplicatesRepeatedDependencies: three service tasks on one
// connector are one dependency, not three. The mesh answers "what does this depend
// on", and repeating an edge would make a busy process look more coupled than it is.
func TestDeriveGraphDeduplicatesRepeatedDependencies(t *testing.T) {
	p := proc(1, "invoice", "Invoice", "a1")
	p.Connectors = []ConnectorUse{
		{ElementID: "Task_1", Name: "billing-rest", TargetID: "c1"},
		{ElementID: "Task_2", Name: "billing-rest", TargetID: "c1"},
		{ElementID: "Task_3", Name: "billing-rest", TargetID: "c1"},
	}

	g := DeriveGraph(Landscape{
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{p},
		Connectors:   []Connector{{ID: "c1", Name: "billing-rest", Kind: "rest", CanView: true}},
	}, Options{})

	uses := 0
	for _, e := range g.Edges {
		if e.Kind == EdgeUses {
			uses++
		}
	}
	if uses != 1 {
		t.Errorf("uses edges = %d, want 1", uses)
	}
}

// TestUnreferencedResourcesStayOutOfTheGraph: the mesh is the dependency picture,
// not an inventory. A configured connector nothing uses is not a landscape edge, and
// putting it on screen would bury the ones that are.
func TestUnreferencedResourcesStayOutOfTheGraph(t *testing.T) {
	g := DeriveGraph(Landscape{
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{proc(1, "invoice", "Invoice", "a1")},
		Connectors:   []Connector{{ID: "c1", Name: "unused-rest", Kind: "rest", CanView: true}},
		Decisions:    []Decision{{ID: "unused", Name: "Unused", CanView: true}},
	}, Options{})

	if got := kindsOf(g); got[KindConnector] != 0 || got[KindDecision] != 0 {
		t.Errorf("node kinds = %v, want no unreferenced connector or decision", got)
	}
}

// TestUnresolvedProcessAndConnectorIdsDoNotCollide pins the namespaced id scheme: a
// process id and a connector name can be the same string, and they are different
// findings about different things.
func TestUnresolvedProcessAndConnectorIdsDoNotCollide(t *testing.T) {
	p := proc(1, "invoice", "Invoice", "a1", Call{ElementID: "Call_1", CalledProcessID: "billing"})
	p.Connectors = []ConnectorUse{{ElementID: "Task_1", Name: "billing"}}

	g := DeriveGraph(Landscape{
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{p},
	}, Options{})

	if got := kindsOf(g)[KindUnresolved]; got != 2 {
		t.Fatalf("unresolved nodes = %d, want 2 — one process target and one connector", got)
	}
	nodeByID(t, g, "unresolved:process:billing")
	nodeByID(t, g, "unresolved:connector:billing")
}

// TestDeriveGraphHandlesHiddenAndMissingDecisions completes the decision edge for
// the same two cases the connector edge already covers. A decision reached through
// a business-rule task is a dependency like any other: it can sit behind a scope,
// and it can simply not be registered — and those stay two findings here too.
func TestDeriveGraphHandlesHiddenAndMissingDecisions(t *testing.T) {
	hidden := proc(1, "invoice", "Invoice", "a1")
	hidden.Decisions = []string{"payroll-band"}
	missing := proc(2, "dunning", "Dunning", "a1")
	missing.Decisions = []string{"never-registered"}

	g := DeriveGraph(Landscape{
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{hidden, missing},
		Decisions:    []Decision{{ID: "payroll-band", Name: "Payroll band"}},
	}, Options{})

	if got := kindsOf(g); got[KindDecision] != 0 || got[KindRestricted] != 1 || got[KindUnresolved] != 1 {
		t.Fatalf("node kinds = %v, want one placeholder and one unresolved decision", got)
	}
	if n := nodeByID(t, g, "unresolved:decision:never-registered"); n.Name != "never-registered" {
		t.Errorf("unresolved decision node = %+v", n)
	}
	body, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(body), "Payroll band") {
		t.Errorf("restricted decision leaks its name in %s", body)
	}
}
