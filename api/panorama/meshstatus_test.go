package panorama

import (
	"encoding/json"
	"strings"
	"testing"
)

// withStatus returns a copy of a process carrying an observation, so a status test
// reads as the one fact it is about.
func withStatus(p Process, state, reason string) Process {
	p.State, p.Reason = state, reason
	return p
}

// TestSeverityMapsEachStateThroughOneTable is the mapping ADR-0211 §4 requires to
// live in one place, asserted at that place. Two of these rows are fixed by the
// record itself and the test names why: *unreachable* and *stale* are "I do not
// know", not "it is broken", and a view that painted them the same red loses its
// credibility on the first network fault — at exactly the moment somebody is
// relying on it.
func TestSeverityMapsEachStateThroughOneTable(t *testing.T) {
	for state, want := range map[string]string{
		StateHealthy:     SeverityOK,
		StateDegraded:    SeverityAttention,
		StateNotReady:    SeverityCritical,
		StateUnreachable: SeverityAttention,
		StateStale:       SeverityAttention,
		StateUnbound:     SeverityUnknown,
		"":               SeverityUnknown,
		"invented":       SeverityUnknown,
	} {
		if got := severityOf(state); got != want {
			t.Errorf("severityOf(%q) = %q, want %q", state, got, want)
		}
	}
	for _, state := range []string{StateUnreachable, StateStale} {
		if severityOf(state) == SeverityCritical {
			t.Fatalf("%q maps to critical; ADR-0211 §4 forbids it", state)
		}
	}
}

// TestGraphDeclaresWhatItCannotObserve is the honesty this slice turns on. Two of
// ADR-0189 §6's seven states need a source outside this process, and this build has
// none — so the payload says so. Without that, an instance with nothing watching it
// renders as uniformly healthy, and a green picture that cannot go red is worse
// than no picture at all.
func TestGraphDeclaresWhatItCannotObserve(t *testing.T) {
	g := DeriveGraph(Landscape{
		Applications: []Application{app("a", "Billing")},
		Processes:    []Process{withStatus(proc(1, "p", "Invoice", "a"), StateHealthy, "No work is parked.")},
	}, Options{})

	declared := map[string]string{}
	for _, u := range g.Status.Unavailable {
		declared[u.State] = u.Reason
	}
	for _, state := range []string{StateUnreachable, StateStale} {
		if declared[state] == "" {
			t.Fatalf("state %q is not declared unavailable: %#v", state, g.Status.Unavailable)
		}
		// The reason has to point somewhere. Saying "this cannot be known" is only
		// half an answer when it *can* be known one surface over.
		if !strings.Contains(declared[state], "observation projection") {
			t.Errorf("%q says it cannot be produced but not where it can: %q", state, declared[state])
		}
	}
	// A state the server does produce must not be listed: declaring a capability
	// absent while exercising it would make the declaration worthless.
	if _, listed := declared[StateHealthy]; listed {
		t.Fatal("healthy is declared unavailable while nodes are reporting it")
	}
}

// TestApplicationTakesTheWorstOfItsProcessesAndSaysWhich pins both halves of
// ADR-0211 §4's aggregation rule. Worst-of is the easy half; the attribution is the
// half that matters, because an unattributed red parent tells an operator that
// something is wrong somewhere, which is not actionable and trains them to stop
// looking.
func TestApplicationTakesTheWorstOfItsProcessesAndSaysWhich(t *testing.T) {
	g := DeriveGraph(Landscape{
		Applications: []Application{app("a", "Billing")},
		Processes: []Process{
			withStatus(proc(1, "ok", "Healthy", "a"), StateHealthy, "No work is parked."),
			withStatus(proc(2, "bad", "Parked", "a"), StateDegraded, "4 token(s) are parked."),
			withStatus(proc(3, "meh", "Also fine", "a"), StateHealthy, "No work is parked."),
		},
	}, Options{})

	parent := nodeByID(t, g, applicationNodeID("a"))
	if parent.Severity != SeverityAttention {
		t.Fatalf("application severity = %q, want %q", parent.Severity, SeverityAttention)
	}
	if parent.SeverityFrom != processNodeID(2) {
		t.Errorf("severityFrom = %q, want the degraded process %q", parent.SeverityFrom, processNodeID(2))
	}
	if !strings.Contains(parent.Reason, "4 token(s)") {
		t.Errorf("inherited reason = %q, want the child's own words", parent.Reason)
	}
	// The child keeps its own severity as its own: nothing was inherited there.
	child := nodeByID(t, g, processNodeID(2))
	if child.SeverityFrom != "" {
		t.Errorf("a node with its own finding claims to have inherited it: %q", child.SeverityFrom)
	}
}

// TestCriticalOutranksAttentionWhenAggregating checks the ordering, not just that
// something is inherited: an application with one broken worker dependency and one
// merely degraded process must read as the broken one.
func TestCriticalOutranksAttentionWhenAggregating(t *testing.T) {
	g := DeriveGraph(Landscape{
		Applications: []Application{app("a", "Billing")},
		Processes: []Process{
			withStatus(proc(1, "warn", "Parked", "a"), StateDegraded, "1 token parked."),
			withStatus(proc(2, "down", "Stopped", "a"), StateNotReady, "It cannot serve work."),
		},
	}, Options{})

	parent := nodeByID(t, g, applicationNodeID("a"))
	if parent.Severity != SeverityCritical || parent.SeverityFrom != processNodeID(2) {
		t.Fatalf("application = %q from %q, want critical from the stopped process",
			parent.Severity, parent.SeverityFrom)
	}
}

// TestAnApplicationOfUnobservedProcessesStaysUnknown is ADR-0211 §4's neutral rule
// applied to aggregation. Unknown ranks *below* ok on purpose: a parent whose
// children are all unobserved is unobserved, and calling it well would be a claim
// nobody made.
func TestAnApplicationOfUnobservedProcessesStaysUnknown(t *testing.T) {
	g := DeriveGraph(Landscape{
		Applications: []Application{app("a", "Billing")},
		Processes:    []Process{proc(1, "p", "Invoice", "a")},
	}, Options{})

	for _, id := range []string{applicationNodeID("a"), processNodeID(1)} {
		n := nodeByID(t, g, id)
		if n.Severity != SeverityUnknown || n.State != StateUnbound {
			t.Fatalf("%s = %q/%q, want unbound/unknown", id, n.State, n.Severity)
		}
	}
	if g.Status.Unknown != 2 || g.Status.OK != 0 {
		t.Fatalf("status = %#v, want two unknown and nothing ok", g.Status)
	}
}

// TestSeverityDoesNotFollowDependencyEdges pins the line ADR-0211 §4's aggregation
// rule draws: containment aggregates, dependency does not. Propagating along uses
// would let one unserved worker repaint most of a few-hundred-node landscape, and a
// mesh that turns mostly red on a single fault is the mesh nobody believes on the
// second one. Impact analysis is the answer to the dependency question, on demand.
func TestSeverityDoesNotFollowDependencyEdges(t *testing.T) {
	p := withStatus(proc(1, "p", "Invoice", "a"), StateHealthy, "No work is parked.")
	p.Workers = []WorkerUse{{ElementID: "task", Name: "mailer", TargetID: "w1"}}
	g := DeriveGraph(Landscape{
		Applications: []Application{app("a", "Billing")},
		Processes:    []Process{p},
		Workers: []Worker{{
			ID: "w1", Name: "mailer", Type: "mail", CanView: true,
			State: StateNotReady, Reason: "no credential is configured",
		}},
	}, Options{})

	if worker := nodeByID(t, g, workerNodeID("w1")); worker.Severity != SeverityCritical {
		t.Fatalf("worker severity = %q, want critical", worker.Severity)
	}
	// The process depends on the broken worker and is still reported on its own
	// evidence. Its own tokens are not parked, and saying they are would be false.
	if process := nodeByID(t, g, processNodeID(1)); process.Severity != SeverityOK {
		t.Fatalf("process inherited from a dependency: %q", process.Severity)
	}
	if parent := nodeByID(t, g, applicationNodeID("a")); parent.Severity != SeverityOK {
		t.Fatalf("application inherited through a dependency: %q from %q",
			parent.Severity, parent.SeverityFrom)
	}
}

// TestRestrictedPlaceholderCarriesNoSeverity is the disclosure rule (ADR-0211 §3)
// applied to the new channel. Whether a resource outside this caller's access is
// broken is a fact about that resource; a placeholder that reported it would turn
// the mesh into a side channel around the sharing scope it exists to honor.
func TestRestrictedPlaceholderCarriesNoSeverity(t *testing.T) {
	hidden := withStatus(Process{
		Key: 2, ProcessID: "secret", Name: "Payroll", Version: 1,
		ApplicationID: "b", CanView: false,
	}, StateNotReady, "the payroll worker has no credential")
	g := DeriveGraph(Landscape{
		Applications: []Application{app("a", "Billing")},
		Processes: []Process{
			withStatus(proc(1, "p", "Invoice", "a", Call{ElementID: "call", CalledProcessID: "secret", TargetKey: 2}),
				StateHealthy, "No work is parked."),
			hidden,
		},
	}, Options{})

	placeholder := nodeByID(t, g, restrictedNodeID(1))
	if placeholder.Severity != SeverityUnknown || placeholder.State != StateUnbound {
		t.Fatalf("placeholder = %q/%q, want unbound/unknown", placeholder.State, placeholder.Severity)
	}
	if placeholder.Reason != "" {
		t.Fatalf("placeholder carries a reason: %q", placeholder.Reason)
	}
	raw, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "payroll worker") {
		t.Fatalf("the hidden resource's finding reached the payload: %s", raw)
	}
}

// TestUnresolvedNodeCarriesNoSeverity keeps one fact in one channel. An unresolved
// dependency is a structural finding the node's kind already states; giving it a
// severity as well would report it twice, in two channels that could then disagree
// — and there is no resource there to have observed in the first place.
func TestUnresolvedNodeCarriesNoSeverity(t *testing.T) {
	g := DeriveGraph(Landscape{
		Applications: []Application{app("a", "Billing")},
		Processes: []Process{withStatus(
			proc(1, "p", "Invoice", "a", Call{ElementID: "call", CalledProcessID: "gone"}),
			StateHealthy, "No work is parked.")},
	}, Options{})

	missing := nodeByID(t, g, unresolvedNodeID(KindProcess, "gone"))
	if missing.Severity != SeverityUnknown || missing.Reason != "" {
		t.Fatalf("unresolved node = %q/%q, want a neutral severity and no reason",
			missing.Severity, missing.Reason)
	}
}

// TestStatusCountsEveryNodeExactlyOnce keeps the summary a summary. A legend that
// says "2 critical" over a picture with three is worse than no legend.
func TestStatusCountsEveryNodeExactlyOnce(t *testing.T) {
	p := withStatus(proc(1, "p", "Invoice", "a"), StateDegraded, "1 token parked.")
	p.Workers = []WorkerUse{{ElementID: "task", Name: "mailer", TargetID: "w1"}}
	g := DeriveGraph(Landscape{
		Applications: []Application{app("a", "Billing")},
		Processes:    []Process{p},
		Workers: []Worker{{ID: "w1", Name: "mailer", Type: "mail", CanView: true,
			State: StateHealthy, Reason: "The engine holds a usable client."}},
	}, Options{})

	total := g.Status.OK + g.Status.Attention + g.Status.Critical + g.Status.Unknown
	if total != len(g.Nodes) {
		t.Fatalf("status totals %d over %d nodes: %#v", total, len(g.Nodes), g.Status)
	}
	if g.Status.Attention != 2 { // the process and the application that inherits it
		t.Fatalf("attention = %d, want the process and its application", g.Status.Attention)
	}
	if g.Status.Partial {
		t.Fatal("a complete scan reported itself partial")
	}
}

// TestPartialScanIsReportedRatherThanAssumedComplete is the difference between a
// floor and a verdict. When the incident scan stops early, every process it did not
// reach is published as healthy — which is only defensible if the payload says the
// count is incomplete.
func TestPartialScanIsReportedRatherThanAssumedComplete(t *testing.T) {
	land := Landscape{
		Applications:  []Application{app("a", "Billing")},
		Processes:     []Process{withStatus(proc(1, "p", "Invoice", "a"), StateHealthy, "No work is parked.")},
		PartialStatus: true,
	}
	if g := DeriveGraph(land, Options{}); !g.Status.Partial {
		t.Fatal("a truncated scan is published as a complete one")
	}
	// And it survives the collapse: an over-budget landscape is exactly where the
	// scan is most likely to have run out, so losing the flag there would lose it
	// when it matters most.
	if g := DeriveGraph(land, Options{MaxNodes: 1}); !g.Clustered || !g.Status.Partial {
		t.Fatalf("clustered status = %#v (clustered=%v), want partial preserved",
			g.Status, g.Clustered)
	}
}

// TestClusteredApplicationKeepsTheWorstOfWhatItHides is why the collapse is not a
// blank. An application that hid a stopped process behind a node count would be a
// worse picture than no picture, and the reason says how many it stands for rather
// than naming a node the caller cannot see on screen.
func TestClusteredApplicationKeepsTheWorstOfWhatItHides(t *testing.T) {
	g := DeriveGraph(Landscape{
		Applications: []Application{app("a", "Billing")},
		Processes: []Process{
			withStatus(proc(1, "ok", "Healthy", "a"), StateHealthy, "No work is parked."),
			withStatus(proc(2, "down", "Stopped", "a"), StateNotReady, "It cannot serve work."),
		},
	}, Options{MaxNodes: 1})

	if !g.Clustered {
		t.Fatal("landscape did not collapse")
	}
	parent := nodeByID(t, g, applicationNodeID("a"))
	if parent.Severity != SeverityCritical {
		t.Fatalf("collapsed application severity = %q, want critical", parent.Severity)
	}
	if !strings.Contains(parent.Reason, "2 collapsed") {
		t.Errorf("collapsed reason = %q, want the count it stands for", parent.Reason)
	}
	if parent.SeverityFrom != "" {
		t.Errorf("collapsed node attributes to %q, which is not on screen", parent.SeverityFrom)
	}
}

// TestApplyStatusSkipsEdgesWhoseEndsAreMissing covers the guards that keep
// aggregation from depending on a graph being whole. DeriveGraph always hands it a
// complete one, so this is the contract stated where the function can be held to
// it: applyStatus runs over whatever graph it is given, and the day something
// filters the mesh before status is applied — the browser already filters after —
// a dangling containment edge must be skipped rather than crash the response that
// was going to tell an operator what is broken.
func TestApplyStatusSkipsEdgesWhoseEndsAreMissing(t *testing.T) {
	g := Graph{
		Nodes: []Node{{ID: "application:a", Kind: KindApplication, State: StateHealthy}},
		Edges: []Edge{
			// A child that is not in the graph, and a parent that is not either.
			{From: "application:a", To: "process:404", Kind: EdgeContains},
			{From: "application:gone", To: "application:a", Kind: EdgeContains},
		},
	}
	applyStatus(&g, false)

	parent := nodeByID(t, g, "application:a")
	if parent.Severity != SeverityOK || parent.SeverityFrom != "" {
		t.Fatalf("application = %q from %q, want its own ok severity", parent.Severity, parent.SeverityFrom)
	}
	if g.Status.OK != 1 || g.Status.OK+g.Status.Attention+g.Status.Critical+g.Status.Unknown != len(g.Nodes) {
		t.Fatalf("status = %#v over %d node(s)", g.Status, len(g.Nodes))
	}
}

// TestAModeledButAbsentNodeIsUnwatchedRatherThanWell keeps the two halves of the
// picture from contradicting each other. A node the mesh drew because a model
// declares it is one Atlas does not have — so there is nothing to have observed,
// and reporting it as healthy would be the view vouching for a resource it just
// said is missing.
func TestAModeledButAbsentNodeIsUnwatchedRatherThanWell(t *testing.T) {
	g := DeriveGraph(Landscape{
		Applications: []Application{app("a", "Billing")},
		Processes:    []Process{withStatus(proc(1, "p", "Invoice", "a"), StateHealthy, "No work is parked.")},
	}, Options{Overlays: []Overlay{{
		ModelID: "m1", ModelName: "Landscape",
		Elements: []ModelElement{{
			ElementID: "e-ghost", ElementType: "ApplicationComponent", Name: "Reporting",
			Key: KeyApplicationID, Values: []string{"absent"},
		}},
	}}})

	ghost := nodeByID(t, g, overlayNodeID(KindApplication, "absent"))
	if ghost.Provenance != ProvenanceModeled {
		t.Fatalf("ghost provenance = %q, want modeled", ghost.Provenance)
	}
	if ghost.State != StateUnbound || ghost.Severity != SeverityUnknown {
		t.Fatalf("modeled-but-absent node = %q/%q, want unbound/unknown", ghost.State, ghost.Severity)
	}
	if ghost.Reason != "" {
		t.Errorf("a node Atlas does not have carries a finding about it: %q", ghost.Reason)
	}
}

// TestIncidentCountRidesOnTheNodeThatCanHaveOne. The count is what turns "degraded"
// into something an operator can rank: two degraded processes are not equally
// degraded, and the number behind them says which to look at first.
//
// It rides only on a process, and that is the point of the assertion below. An
// incident belongs to a token and only a process has tokens, so a node without a
// count is one that *cannot* have one — never one reported as having none. Rendering
// those alike is how a picture claims a decision node is free of incidents it was
// never able to hold.
func TestIncidentCountRidesOnTheNodeThatCanHaveOne(t *testing.T) {
	parked := proc(1, "invoice", "Invoice", "a1")
	parked = withStatus(parked, StateDegraded, "3 token(s) are parked behind an unresolved incident.")
	parked.Incidents = 3
	clean := withStatus(proc(2, "dunning", "Dunning", "a1"), StateHealthy, "No work is parked in this process.")

	g := DeriveGraph(Landscape{
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{parked, clean},
	}, Options{})

	if got := nodeByID(t, g, "process:1").Incidents; got != 3 {
		t.Errorf("Incidents = %d, want 3", got)
	}
	// Healthy with nothing parked: absent, which is the same wire shape as a node
	// that cannot hold one. Both are honest — neither has an incident — and the
	// difference between them is the state, which is already on the node.
	if got := nodeByID(t, g, "process:2").Incidents; got != 0 {
		t.Errorf("a process with nothing parked reports Incidents = %d, want 0", got)
	}
	for _, id := range []string{"application:a1"} {
		if got := nodeByID(t, g, id).Incidents; got != 0 {
			t.Errorf("%s carries Incidents = %d; only a process holds tokens", id, got)
		}
	}

	// The count is omitted from the wire when it is zero, so "no incidents" and
	// "cannot have incidents" arrive as the same absence rather than as a number
	// asserting a process's worth of nothing about a decision.
	encoded, err := json.Marshal(nodeByID(t, g, "process:2"))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(encoded), "incidents") {
		t.Errorf("a node with no incidents still names the field: %s", encoded)
	}
}

// TestCollapsedApplicationSumsTheIncidentsBehindIt. A collapsed application stands
// for every process under it, so it must report what is parked behind all of them.
// Carrying the worst child's count would understate the outage by exactly the
// amount somebody needs to know.
func TestCollapsedApplicationSumsTheIncidentsBehindIt(t *testing.T) {
	land := Landscape{Applications: []Application{app("a1", "Billing")}}
	for i := 1; i <= 6; i++ {
		p := withStatus(proc(uint64(i), "p", "P", "a1"), StateDegraded, "parked")
		p.Incidents = i
		land.Processes = append(land.Processes, p)
	}

	g := DeriveGraph(land, Options{MaxNodes: 3})

	if !g.Clustered {
		t.Fatal("Clustered = false; this test is about the collapsed shape")
	}
	if got := nodeByID(t, g, "application:a1").Incidents; got != 21 {
		t.Errorf("Incidents = %d, want 21 — the sum of what all six hold", got)
	}
}
