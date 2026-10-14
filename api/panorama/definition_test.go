package panorama

import "testing"

// ADR-0401 §2 splits one conflated thing into two: a *definition* is what a model is,
// independent of any server — process id plus version — and a *deployment* is that
// definition held by one runtime at one moment. The starmap's `process` node has been
// both at once, which is harmless with one server and unanswerable with several.
//
// What these tests pin is the split *and* its rendering rule, because the two are not
// the same decision. On a single runtime a definition has exactly one deployment,
// always: the collector reads `latestDeploymentByProcessID`, one node per process id.
// Drawing both halves there would put a permanent satellite beside every process —
// a node in a fixed 1:1 relation to another node is not a second thing, it is a field —
// and it would spend the measured 400-node budget (ADR-0211 §7, meshMaxNodes) to say
// nothing. So the definition is *derived* always and *drawn* only where it carries
// something a reader cannot get from the deployment: more than one deployment.
//
// That rule is what makes "this process runs in three domains" a drawing rather than a
// caption, and it is why these tests are mostly about when the node is absent.

// definitionsOf collects the definition nodes, so a test can assert on the rule
// without pinning ids.
func definitionsOf(g Graph) []Node {
	var out []Node
	for _, n := range g.Nodes {
		if n.Kind == KindDefinition {
			out = append(out, n)
		}
	}
	return out
}

// TestOneDeploymentDrawsNoDefinitionNode is the case every single-server installation
// is in, and the one the rule exists for. The definition is known — the deployment
// names it — and drawing it would cost a node per process against a measured budget to
// restate what the deployment already says.
func TestOneDeploymentDrawsNoDefinitionNode(t *testing.T) {
	g := DeriveGraph(Landscape{
		RuntimeID:    "rt-a",
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{proc(1, "invoice", "Invoice", "a1")},
	}, Options{})

	if got := definitionsOf(g); len(got) != 0 {
		t.Errorf("drew %d definition node(s) for a definition with one deployment, want none: %#v", len(got), got)
	}
	if got := kindsOf(g)[KindProcess]; got != 1 {
		t.Errorf("process nodes = %d, want the one deployment", got)
	}
}

// TestTwoDeploymentsOfOneDefinitionDrawTheDefinition is the drawing ADR-0401 §2 names
// and nothing today can produce: one model, two runtimes holding it. The definition
// becomes a node because it is now the thing the two have in common, and each
// deployment points back at it.
func TestTwoDeploymentsOfOneDefinitionDrawTheDefinition(t *testing.T) {
	here := proc(1, "invoice", "Invoice", "a1")
	there := proc(1, "invoice", "Invoice", "a1") // same key, minted by another runtime
	there.RuntimeID = "rt-b"

	g := DeriveGraph(Landscape{
		RuntimeID:    "rt-a",
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{here, there},
	}, Options{})

	defs := definitionsOf(g)
	if len(defs) != 1 {
		t.Fatalf("definition nodes = %d, want exactly 1 — two deployments of one model are one definition: %#v", len(defs), g.Nodes)
	}
	def := defs[0]
	if def.ProcessID != "invoice" || def.Version != 1 {
		t.Errorf("definition node = (%q, %d), want (invoice, 1) — process id plus version is the identity", def.ProcessID, def.Version)
	}
	if def.RuntimeID != "" {
		t.Errorf("definition node carries runtime %q; a definition is estate-wide and belongs to no runtime (ADR-0401 §2)", def.RuntimeID)
	}

	deployments := 0
	for _, n := range g.Nodes {
		if n.Kind != KindProcess {
			continue
		}
		deployments++
		if n.Definition != def.ID {
			t.Errorf("deployment %q names definition %q, want %q", n.ID, n.Definition, def.ID)
		}
		if !hasEdge(g, def.ID, n.ID, EdgeDeploys) {
			t.Errorf("no %s edge from %q to %q", EdgeDeploys, def.ID, n.ID)
		}
	}
	if deployments != 2 {
		t.Errorf("deployment nodes = %d, want 2", deployments)
	}
}

// TestTwoVersionsAreTwoDefinitions pins version into the identity. Two runtimes each
// holding a *different* version are two definitions with one deployment apiece, so the
// rule draws neither — and that is right: there is nothing in common to draw. The
// finding a reader wants here is drift, which is a comparison of two deployments and
// not a shared definition.
func TestTwoVersionsAreTwoDefinitions(t *testing.T) {
	here := proc(1, "invoice", "Invoice", "a1")
	there := proc(1, "invoice", "Invoice", "a1")
	there.RuntimeID = "rt-b"
	there.Version = 2

	g := DeriveGraph(Landscape{
		RuntimeID:    "rt-a",
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{here, there},
	}, Options{})

	if got := definitionsOf(g); len(got) != 0 {
		t.Errorf("drew %d definition node(s); two versions are two definitions, one deployment each: %#v", len(got), got)
	}
}

// TestGraphNamesTheRuntimeThatDerivedIt is ADR-0401 §1's other half. Every node id here
// carries a key, and a key means something only beside the runtime that minted it —
// "a bare key is never published estate-wide". The ids stay local and readable; what
// qualifies them is the document saying whose keys they are.
func TestGraphNamesTheRuntimeThatDerivedIt(t *testing.T) {
	g := DeriveGraph(Landscape{
		RuntimeID:    "rt-a",
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{proc(1, "invoice", "Invoice", "a1")},
	}, Options{})

	if g.RuntimeID != "rt-a" {
		t.Errorf("graph RuntimeID = %q, want rt-a — the keys in this document are that runtime's", g.RuntimeID)
	}
}

// TestADeploymentInheritsTheDerivingRuntime keeps the local case free of ceremony: a
// process the collector read off this server belongs to this server, so it does not
// have to say so twice. A process carrying its own runtime id came from somewhere else
// and keeps it.
func TestADeploymentInheritsTheDerivingRuntime(t *testing.T) {
	mine := proc(1, "invoice", "Invoice", "a1")
	theirs := proc(2, "dunning", "Dunning", "a1")
	theirs.RuntimeID = "rt-b"

	g := DeriveGraph(Landscape{
		RuntimeID:    "rt-a",
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{mine, theirs},
	}, Options{})

	if got := nodeByID(t, g, processNodeID(1)).RuntimeID; got != "rt-a" {
		t.Errorf("local deployment RuntimeID = %q, want the deriving runtime rt-a", got)
	}
	if got := nodeByID(t, g, processNodeID(2)).RuntimeID; got != "rt-b" {
		t.Errorf("foreign deployment RuntimeID = %q, want rt-b kept as read", got)
	}
}

// TestADefinitionIsNotDrawnWithoutARuntimeToTellThemApart guards the degenerate case
// the rule must not mistake for the interesting one. Two records of the same definition
// with the *same* runtime are one deployment seen twice, not two deployments, and a
// definition node there would assert an estate that does not exist.
func TestADefinitionIsNotDrawnWithoutARuntimeToTellThemApart(t *testing.T) {
	g := DeriveGraph(Landscape{
		RuntimeID:    "rt-a",
		Applications: []Application{app("a1", "Billing")},
		Processes: []Process{
			proc(1, "invoice", "Invoice", "a1"),
			proc(1, "invoice", "Invoice", "a1"),
		},
	}, Options{})

	if got := definitionsOf(g); len(got) != 0 {
		t.Errorf("drew %d definition node(s) for one deployment recorded twice: %#v", len(got), got)
	}
}

// TestACollapsedGraphStillNamesItsRuntime is the regression this slice nearly shipped.
//
// Both collapse constructors build a fresh Graph and copy what survives from the full
// one — Restricted, Clustered, ObservedAt. A runtime id left out of that list would make
// every over-budget installation silently unjoinable, and the absence would read as "the
// server could not name itself" rather than "this picture is large", which is a different
// fact with a different remedy. The two subjects collapse through different functions, so
// both are pinned.
func TestACollapsedGraphStillNamesItsRuntime(t *testing.T) {
	// Two applications with a process each, and a budget of one node: enough to be
	// over it whichever way the collapse goes.
	land := Landscape{
		RuntimeID:    "rt-a",
		Applications: []Application{app("a1", "Billing"), app("a2", "Dunning")},
		Processes: []Process{
			proc(1, "invoice", "Invoice", "a1"),
			proc(2, "chase", "Chase", "a2"),
		},
	}
	g := DeriveGraph(land, Options{MaxNodes: 1})
	if !g.Clustered {
		t.Fatalf("graph did not collapse at MaxNodes=1, so this test is not exercising the collapse: %d nodes", len(g.Nodes))
	}
	if g.RuntimeID != "rt-a" {
		t.Errorf("collapsed landscape RuntimeID = %q, want rt-a — a large picture is still this runtime's", g.RuntimeID)
	}

	// The product map collapses through its own constructor.
	products := Landscape{
		RuntimeID:    "rt-a",
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{proc(1, "provision", "Provision", "a1")},
		Catalogs:     []ProductCatalog{cat("c1", "Workplace", "laptop", "phone")},
		Products: []Product{
			prod("laptop", "Laptop", "c1", "", ""),
			prod("phone", "Phone", "c1", "", ""),
		},
	}
	pg := DeriveGraph(products, Options{Subject: SubjectProducts, MaxNodes: 1})
	if !pg.Clustered {
		t.Fatalf("product map did not collapse at MaxNodes=1: %d nodes", len(pg.Nodes))
	}
	if pg.RuntimeID != "rt-a" {
		t.Errorf("collapsed product map RuntimeID = %q, want rt-a", pg.RuntimeID)
	}
}
