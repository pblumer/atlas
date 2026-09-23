package panorama

import "testing"

// ADR-0402's estate altitude, as the assembly that decides what it says. The fetch and the
// screen come after; what is decided here is the picture's arithmetic, and all four of the
// record's rules are testable without a network.

// oneEstate is the ordinary shape: this runtime, two peers, one of them holding an
// application this server promoted there.
func oneEstate() (EstateDomain, []EstateDomain) {
	local := EstateDomain{
		ID: "local", Name: "Zurich", RuntimeID: "rt-zh", Holds: 120, State: StateHealthy,
	}
	return local, []EstateDomain{
		{
			ID: "tgt-test", Name: "Test", RuntimeID: "rt-test", Holds: 80, State: StateHealthy,
			Applications: map[string]string{"app-orders": "remote-orders"},
			DrawnBy:      "peer-test",
		},
		{ID: "tgt-prod", Name: "Production", State: StateUnreachable, Reason: "connection refused"},
	}
}

// TestAnEstateIsOneNodePerDomain is §2's budget: *"the estate view's own budget is the number
// of domains, which is operator configuration"*. Not L0 repeated — one node per domain, and
// this runtime is a domain like any other, because an estate that draws its peers and omits
// itself is a picture of somebody else's estate.
func TestAnEstateIsOneNodePerDomain(t *testing.T) {
	g := DeriveEstate(oneEstate())
	if len(g.Nodes) != 3 {
		t.Fatalf("nodes = %d, want 3 — one per domain, this runtime included: %+v", len(g.Nodes), g.Nodes)
	}
	for _, n := range g.Nodes {
		if n.Kind != KindDomain {
			t.Errorf("node %s is kind %q, want %q — an estate has one altitude", n.ID, n.Kind, KindDomain)
		}
		if n.Provenance != ProvenanceDerived {
			t.Errorf("node %s is %q, want derived: nothing here is modelled", n.ID, n.Provenance)
		}
	}
	if g.Nodes[0].ID != domainNodeID("local") {
		t.Errorf("the first node is %s, want this runtime's domain — a reader starts where they are", g.Nodes[0].ID)
	}
}

// TestTheOnlyEdgeIsAPromotionSomebodyRecorded is §4. The join is not a name match and not a
// drawn arrow: it is `deploymentTarget.Bindings`, written when a promotion succeeded.
func TestTheOnlyEdgeIsAPromotionSomebodyRecorded(t *testing.T) {
	g := DeriveEstate(oneEstate())
	if len(g.Edges) != 1 {
		t.Fatalf("edges = %d, want exactly the recorded promotion: %+v", len(g.Edges), g.Edges)
	}
	e := g.Edges[0]
	if e.From != domainNodeID("local") || e.To != domainNodeID("tgt-test") || e.Kind != EdgePromotes {
		t.Errorf("edge = %+v, want local → tgt-test of kind %q", e, EdgePromotes)
	}
}

// TestADomainWithNoRecordedPromotionGetsNoEdge is the same rule from the other side, and it is
// the one that keeps the picture honest: an estate that has never promoted anything is a set
// of unjoined domains, which ADR-0401 §3 states plainly as *"the truth about such an estate"*.
func TestADomainWithNoRecordedPromotionGetsNoEdge(t *testing.T) {
	local, peers := oneEstate()
	for i := range peers {
		peers[i].Applications = nil
	}
	g := DeriveEstate(local, peers)
	if len(g.Edges) != 0 {
		t.Errorf("edges = %+v, want none: nothing was promoted anywhere", g.Edges)
	}
	if len(g.Nodes) != 3 {
		t.Errorf("nodes = %d, want 3 — unjoined is not invisible", len(g.Nodes))
	}
}

// TestAPeerThatDoesNotServeTheViewIsItsOwnState is §3's fifth case. An older build whose
// descriptor advertises no `panorama.mesh` is neither unreachable nor stale: reading it as
// unreachable sends an operator to look at a network, and reading it as stale implies there was
// once an answer.
func TestAPeerThatDoesNotServeTheViewIsItsOwnState(t *testing.T) {
	local, peers := oneEstate()
	peers[1] = EstateDomain{ID: "tgt-old", Name: "Old", State: StateUnserved,
		Reason: "this build serves no landscape"}
	g := DeriveEstate(local, peers)

	n := estateNode(t, g, domainNodeID("tgt-old"))
	if n.State != StateUnserved {
		t.Errorf("state = %q, want %q", n.State, StateUnserved)
	}
	if n.State == StateUnreachable || n.State == StateStale {
		t.Error("the version boundary is folded into a state that sends an operator somewhere else")
	}
	if n.Severity != SeverityUnknown {
		t.Errorf("severity = %q, want %q — a version boundary is not a fault", n.Severity, SeverityUnknown)
	}
}

// TestAnUnreachablePeerIsAShapeNotAGap is the other half of §3, and the reason the node exists
// at all: a domain nobody can reach is still part of the estate, and drawing nothing would say
// the estate is smaller than it is.
func TestAnUnreachablePeerIsAShapeNotAGap(t *testing.T) {
	g := DeriveEstate(oneEstate())
	n := estateNode(t, g, domainNodeID("tgt-prod"))
	if n.State != StateUnreachable {
		t.Errorf("state = %q, want %q", n.State, StateUnreachable)
	}
	if n.Holds != 0 {
		t.Errorf("an unreachable domain reports %d nodes, want none — it answered nothing", n.Holds)
	}
	if n.Reason == "" {
		t.Error("an unreachable domain carries no reason, so a reader cannot tell why")
	}
}

// TestADomainSaysHowMuchItsCredentialCouldNotSee is the other half of §1's disclosure.
// DrawnBy names who looked; this says how much of the domain that look did not reach — and
// the two counts must not be confused, because [Graph.Restricted] is what *this* reader may
// not see in this picture while a domain's own is what somebody else's credential could not
// see in somebody else's (ADR-0410).
func TestADomainSaysHowMuchItsCredentialCouldNotSee(t *testing.T) {
	local, peers := oneEstate()
	peers[0].Restricted = 9
	g := DeriveEstate(local, peers)

	if got := estateNode(t, g, domainNodeID("tgt-test")).Restricted; got != 9 {
		t.Errorf("the domain reports %d restricted, want the 9 its own answer carried", got)
	}
	if g.Restricted != 0 {
		t.Errorf("the estate picture reports %d restricted of its own; a domain's count is "+
			"not the reader's", g.Restricted)
	}
	if got := estateNode(t, g, domainNodeID("local")).Restricted; got != 0 {
		t.Errorf("this runtime reports %d restricted, want none: nothing was withheld from "+
			"the estate assembly, and what the reader may not see is the landscape's own count", got)
	}
}

// TestEachDomainSaysWhoseCredentialDrewIt is §1's disclosure requirement: *"the picture states
// whose credential drew each subgraph"*. An incompleteness that is stated is a fact; one that
// is not is a discovery.
func TestEachDomainSaysWhoseCredentialDrewIt(t *testing.T) {
	g := DeriveEstate(oneEstate())
	if got := estateNode(t, g, domainNodeID("tgt-test")).DrawnBy; got != "peer-test" {
		t.Errorf("the answering domain says it was drawn by %q, want peer-test", got)
	}
	if got := estateNode(t, g, domainNodeID("local")).DrawnBy; got != "" {
		t.Errorf("this runtime says it was drawn by %q; it was drawn by the reader, not a credential", got)
	}
}

// TestADomainCarriesItsRuntimeIdOnlyWhereItReportedOne keeps ADR-0401 §1's naming honest. A
// runtime entity's estate-wide name is (runtimeId, key), but a peer that never answered has no
// runtime id — so the node is addressed by the target id an operator configured, and the
// runtime id is carried where it was actually reported rather than invented.
func TestADomainCarriesItsRuntimeIdOnlyWhereItReportedOne(t *testing.T) {
	g := DeriveEstate(oneEstate())
	if got := estateNode(t, g, domainNodeID("tgt-test")).RuntimeID; got != "rt-test" {
		t.Errorf("answering domain's runtime id = %q, want rt-test", got)
	}
	if got := estateNode(t, g, domainNodeID("tgt-prod")).RuntimeID; got != "" {
		t.Errorf("unreachable domain claims runtime id %q, which it never reported", got)
	}
}

// TestTheEstateIsOrderedTheSameWayTwice is the property every derived picture here owes: map
// iteration must not reach the payload, or a reader's diff between two reads is noise.
func TestTheEstateIsOrderedTheSameWayTwice(t *testing.T) {
	local, peers := oneEstate()
	peers = append(peers, EstateDomain{ID: "tgt-a", Name: "A", State: StateHealthy,
		Applications: map[string]string{"app-b": "x", "app-a": "y"}})
	first := DeriveEstate(local, peers)
	for range 5 {
		again := DeriveEstate(local, peers)
		if len(again.Nodes) != len(first.Nodes) || len(again.Edges) != len(first.Edges) {
			t.Fatalf("sizes differ between reads: %d/%d then %d/%d",
				len(first.Nodes), len(first.Edges), len(again.Nodes), len(again.Edges))
		}
		for i := range first.Nodes {
			if again.Nodes[i].ID != first.Nodes[i].ID {
				t.Fatalf("node %d is %s then %s", i, first.Nodes[i].ID, again.Nodes[i].ID)
			}
		}
		for i := range first.Edges {
			if again.Edges[i] != first.Edges[i] {
				t.Fatalf("edge %d is %+v then %+v", i, first.Edges[i], again.Edges[i])
			}
		}
	}
}

// TestOnePromotedApplicationIsOneEdgeHoweverManyTimesItWasPromoted mirrors the L0 rule that an
// edge's identity is its endpoints and its kind. Two applications promoted to one domain are
// one join between two domains, not two lines a reader has to add up.
func TestOnePromotedApplicationIsOneEdgeHoweverManyTimesItWasPromoted(t *testing.T) {
	local, peers := oneEstate()
	peers[0].Applications = map[string]string{"app-orders": "r1", "app-billing": "r2"}
	g := DeriveEstate(local, peers)
	if len(g.Edges) != 1 {
		t.Fatalf("edges = %+v, want one join between two domains", g.Edges)
	}
	if got := g.Edges[0].Promoted; got != 2 {
		t.Errorf("the join stands for %d promoted applications, want 2 — the count is on the edge", got)
	}
}

func estateNode(t *testing.T, g Graph, id string) Node {
	t.Helper()
	for _, n := range g.Nodes {
		if n.ID == id {
			return n
		}
	}
	t.Fatalf("no node %s in %+v", id, g.Nodes)
	return Node{}
}
