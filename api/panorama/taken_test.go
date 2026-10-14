package panorama

import "testing"

// ADR-0400: every derived edge is anchored on exactly one compiled element, and
// ADR-0080 has maintained a cumulative visit count per (definition, element) since
// long before this picture existed. So the traversal count of an edge is a join, not
// a new index — and these tests pin the four things that join has to get right.
//
// The hard one is the second: edges are deduplicated by their identity triple, because
// three call activities to one target are one dependency. Two business rule tasks
// calling one decision are therefore one edge, and its count is the *sum* of theirs.
// A count carried in the deduplication key instead would make them two edges; a count
// written by the last contributor would lose the others.

// edgeOf finds exactly one edge by its identity triple, failing otherwise. Distinct
// from hasEdge: this test needs the edge itself, and needs to know it is not two.
func edgeOf(t *testing.T, g Graph, from, to, kind string) Edge {
	t.Helper()
	var found []Edge
	for _, e := range g.Edges {
		if e.From == from && e.To == to && e.Kind == kind {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("edges %s -%s-> %s = %d, want exactly 1: %#v", from, kind, to, len(found), g.Edges)
	}
	return found[0]
}

// takenOf reports an edge's count, failing when it carries none.
func takenOf(t *testing.T, e Edge) int64 {
	t.Helper()
	if e.Taken == nil {
		t.Fatalf("edge %s -%s-> %s carries no count, want one", e.From, e.Kind, e.To)
	}
	return *e.Taken
}

// TestCallEdgeCarriesItsTraversalCount is the ordinary case: one call activity, one
// count, and the window it was counted over.
func TestCallEdgeCarriesItsTraversalCount(t *testing.T) {
	caller := proc(1, "invoice", "Invoice", "a1",
		Call{ElementID: "Call_1", ElementIndex: 7, CalledProcessID: "dunning", TargetKey: 2})
	caller.DeployedAt = 1_700_000_000
	caller.ElementVisits = map[int32]int64{7: 9000}

	g := DeriveGraph(Landscape{
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{caller, proc(2, "dunning", "Dunning", "a1")},
	}, Options{})

	e := edgeOf(t, g, "process:1", "process:2", EdgeCalls)
	if got := takenOf(t, e); got != 9000 {
		t.Errorf("Taken = %d, want 9000", got)
	}
	if e.TakenSince != 1_700_000_000 {
		t.Errorf("TakenSince = %d, want the deployment stamp 1700000000 — a count without its window says nothing (ADR-0400)", e.TakenSince)
	}
}

// TestOneEdgeSumsTheCountsOfEveryElementBehindIt is the deduplication case. Two
// business rule tasks calling one decision are one dependency and two places it is
// reached from; the edge's count is 40 + 2, not 40, not 2, and not two edges.
func TestOneEdgeSumsTheCountsOfEveryElementBehindIt(t *testing.T) {
	caller := proc(1, "onboarding", "Onboarding", "a1")
	caller.Decisions = []DecisionUse{
		{DecisionID: "eligibility", ElementIndex: 3},
		{DecisionID: "eligibility", ElementIndex: 5},
	}
	caller.DeployedAt = 1_700_000_000
	caller.ElementVisits = map[int32]int64{3: 40, 5: 2}

	g := DeriveGraph(Landscape{
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{caller},
		Decisions:    []Decision{{ID: "eligibility", Name: "Eligibility", CanView: true}},
	}, Options{})

	e := edgeOf(t, g, "process:1", "decision:eligibility", EdgeUses)
	if got := takenOf(t, e); got != 42 {
		t.Errorf("Taken = %d, want 42 — the sum over both business rule tasks", got)
	}
}

// TestAnEdgeNothingHasTakenCountsZeroAndSaysSinceWhen is the finding ADR-0400 is
// emphatic about. Zero is a real answer and must arrive as one, with the window that
// qualifies it — never as an absence, which would read as "nothing is known", and
// never as a verdict, which "dead" would be.
func TestAnEdgeNothingHasTakenCountsZeroAndSaysSinceWhen(t *testing.T) {
	caller := proc(1, "invoice", "Invoice", "a1",
		Call{ElementID: "Call_1", ElementIndex: 7, CalledProcessID: "dunning", TargetKey: 2})
	caller.DeployedAt = 1_700_000_000
	// The counter was read and holds nothing for this element: a compensation branch
	// that has not fired since the definition was deployed.
	caller.ElementVisits = map[int32]int64{}

	g := DeriveGraph(Landscape{
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{caller, proc(2, "dunning", "Dunning", "a1")},
	}, Options{})

	e := edgeOf(t, g, "process:1", "process:2", EdgeCalls)
	if got := takenOf(t, e); got != 0 {
		t.Errorf("Taken = %d, want 0", got)
	}
	if e.TakenSince != 1_700_000_000 {
		t.Errorf("TakenSince = %d, want the deployment stamp — a zero without a window is what gets misread as dead", e.TakenSince)
	}
}

// TestAnEdgeCarriesNoCountWhenTheCounterWasNotRead keeps "nothing is known" apart from
// "nothing happened". The collector leaves the map nil when it could not read the
// counters, exactly as it leaves Runtime nil, and an edge must then carry no count at
// all rather than a zero somebody would act on.
func TestAnEdgeCarriesNoCountWhenTheCounterWasNotRead(t *testing.T) {
	caller := proc(1, "invoice", "Invoice", "a1",
		Call{ElementID: "Call_1", ElementIndex: 7, CalledProcessID: "dunning", TargetKey: 2})
	caller.DeployedAt = 1_700_000_000
	caller.ElementVisits = nil

	g := DeriveGraph(Landscape{
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{caller, proc(2, "dunning", "Dunning", "a1")},
	}, Options{})

	e := edgeOf(t, g, "process:1", "process:2", EdgeCalls)
	if e.Taken != nil {
		t.Errorf("Taken = %d, want absent — an unread counter and an unused edge are different facts", *e.Taken)
	}
	if e.TakenSince != 0 {
		t.Errorf("TakenSince = %d, want 0 alongside an absent count", e.TakenSince)
	}
}

// TestAWorkerEdgeCarriesItsTraversalCount covers the third anchored edge kind, so the
// join is not accidentally call-activity-only.
func TestAWorkerEdgeCarriesItsTraversalCount(t *testing.T) {
	caller := proc(1, "invoice", "Invoice", "a1")
	caller.Workers = []WorkerUse{{ElementID: "Task_1", ElementIndex: 4, Name: "smtp", TargetID: "w1"}}
	caller.DeployedAt = 1_700_000_000
	caller.ElementVisits = map[int32]int64{4: 17}

	g := DeriveGraph(Landscape{
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{caller},
		Workers:      []Worker{{ID: "w1", Name: "SMTP", Type: "mail", CanView: true}},
	}, Options{})

	e := edgeOf(t, g, "process:1", "worker:w1", EdgeUses)
	if got := takenOf(t, e); got != 17 {
		t.Errorf("Taken = %d, want 17", got)
	}
}
