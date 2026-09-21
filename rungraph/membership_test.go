package rungraph

import (
	"reflect"
	"testing"

	"github.com/pblumer/atlas/model"
)

// W2 of ADR-0404: "One pass computes membership; a query is a lookup."
//
// The pass is union-find over the CSR, which W1 already built — for this topology connected
// components are the decomposition worth having (§5, and the arithmetic there is why
// there is no community pass and no gonum). What W2 adds is the half that sentence promises
// and W1 does not have: the queries, answered against that membership rather than by walking
// the graph again.
//
// The distinction the tests below keep sharp is which query is a *lookup* and which is a
// *pass*. "Do these two nodes belong together" is two lookups and no walk, which is the
// claim the record makes. "Name every member of this component" is a different question with a
// different cost, and conflating the two is how a structure nobody needs gets built.

// threeComponents is one graph with a deliberate shape: a component of three whose members are
// *not* contiguous in ordinal space, and two singletons around it. The interleaving matters
// — W1 measured that ordinal contiguity holds only for sequential arrival, so a lookup that
// assumed a component occupies an unbroken range would pass on a tidy fixture and be wrong on
// real concurrent state.
func threeCount() *fakeSource {
	return &fakeSource{
		keys: []uint64{10, 20, 30, 40, 50},
		values: []*model.ElementInstanceValue{
			el(0, 0, 0, 0),  // 10 — alone
			el(0, 0, 0, 0),  // 20 ─┐
			el(0, 0, 0, 0),  // 30 — alone, sitting between two members of 20's component
			el(0, 20, 0, 0), // 40 → 20
			el(0, 40, 0, 0), // 50 → 40, so {20,40,50} with 30 interleaved
		},
	}
}

func mustMembership(t *testing.T, src Source) *Membership {
	t.Helper()
	g, err := Build(src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return g.Membership()
}

// TestMembershipAnswersByKeyNotByOrdinal is the API's whole point. A caller holds an element
// instance key — that is what the store, an event and an operator's URL carry — and never an
// ordinal, which is an internal dense numbering. An API in ordinals would make every caller
// do the translation, and the translation is the part that is easy to get wrong.
func TestMembershipAnswersByKeyNotByOrdinal(t *testing.T) {
	m := mustMembership(t, threeCount())

	if m.Len() != 5 {
		t.Fatalf("Len() = %d, want 5", m.Len())
	}
	// The component's name is the key of its smallest ordinal, which is the label W1 chose for
	// exactly this reason: it is stable under a rebuild that sees the same graph.
	for _, tc := range []struct {
		key       uint64
		component uint64
	}{
		{10, 10},
		{20, 20},
		{30, 30},
		{40, 20},
		{50, 20},
	} {
		got, ok := m.Of(tc.key)
		if !ok {
			t.Errorf("Of(%d) is not a node of the graph", tc.key)
			continue
		}
		if got != tc.component {
			t.Errorf("Of(%d) = %d, want component %d", tc.key, got, tc.component)
		}
	}
	if _, ok := m.Of(999); ok {
		t.Error("Of() answered for a key the graph does not hold")
	}
}

// TestSameComponentIsTwoLookupsAndNoWalk is the query ADR-0404 §5 promises as a lookup: whether
// two things are related. It is the question an impact analysis asks a million times, so it
// must not touch the CSR at all.
func TestSameComponentIsTwoLookupsAndNoWalk(t *testing.T) {
	m := mustMembership(t, threeCount())

	for _, tc := range []struct {
		a, b uint64
		want bool
	}{
		{20, 40, true},
		{40, 50, true},
		{20, 50, true},  // transitively, through 40 — which is what union-find bought
		{20, 30, false}, // adjacent in ordinal space, unrelated in the graph
		{10, 50, false},
		{10, 10, true}, // a node is in its own component
	} {
		if got := m.SameComponent(tc.a, tc.b); got != tc.want {
			t.Errorf("SameComponent(%d, %d) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
	// A key the graph does not hold belongs to no component, and in particular not to the
	// component of another key it has never been related to. Answering true here would make
	// every impact analysis report the whole estate as affected.
	// Both orders, because the two early returns are two code paths and a stale key is as
	// likely to arrive on the right of the comparison as on the left.
	if m.SameComponent(999, 20) || m.SameComponent(20, 999) || m.SameComponent(999, 998) {
		t.Error("SameComponent() related a key the graph does not hold")
	}
}

// TestMembersNamesThemInKeyOrder covers the enumeration, which is a pass rather than a lookup and
// is documented as one. The interleaved fixture is what makes the assertion mean something:
// the members of 20's component are ordinals 1, 3 and 4, so an implementation reading a
// contiguous range would return 30 as a member.
func TestMembersNamesThemInKeyOrder(t *testing.T) {
	m := mustMembership(t, threeCount())

	if got, want := m.Members(40), []uint64{20, 40, 50}; !reflect.DeepEqual(got, want) {
		t.Errorf("Members(40) = %v, want %v — asked from a member that is not the component's name", got, want)
	}
	if got, want := m.Members(20), []uint64{20, 40, 50}; !reflect.DeepEqual(got, want) {
		t.Errorf("Members(20) = %v, want %v", got, want)
	}
	if got, want := m.Members(30), []uint64{30}; !reflect.DeepEqual(got, want) {
		t.Errorf("Members(30) = %v, want %v — a singleton is a component of one", got, want)
	}
	if got := m.Members(999); got != nil {
		t.Errorf("Members(999) = %v, want nil for a key the graph does not hold", got)
	}
	// Members come back in ascending key order, because the caller's next move is to show
	// them or to read them back out of the store, and both want a stable order they did not
	// have to impose.
	if got, want := m.Members(50), []uint64{20, 40, 50}; !reflect.DeepEqual(got, want) {
		t.Errorf("Members(50) = %v, want %v in ascending key order", got, want)
	}
}

// TestSizeCountsWithoutBuildingTheList is the question a cloud asks per component — "how many"
// — and it must not pay for a slice it throws away. ADR-0404 §9's budget is the reason: a
// count per component over a hundred million nodes is the difference between one pass and one
// pass plus an allocation per component.
func TestSizeCountsWithoutBuildingTheList(t *testing.T) {
	m := mustMembership(t, threeCount())

	for _, tc := range []struct {
		key  uint64
		size int
	}{{10, 1}, {20, 3}, {30, 1}, {40, 3}, {50, 3}, {999, 0}} {
		if got := m.Size(tc.key); got != tc.size {
			t.Errorf("Size(%d) = %d, want %d", tc.key, got, tc.size)
		}
	}
	if got := m.Count(); got != 3 {
		t.Errorf("Count() = %d, want 3", got)
	}
}

// TestMembershipOfAnEmptyGraph keeps the zero case from being a panic. An installation with
// no running instance is the ordinary state of a fresh server, and every method has to answer
// rather than crash.
func TestMembershipOfAnEmptyGraph(t *testing.T) {
	m := mustMembership(t, &fakeSource{})

	if m.Len() != 0 || m.Count() != 0 {
		t.Errorf("empty graph: Len() = %d, Count() = %d, want 0 and 0", m.Len(), m.Count())
	}
	if _, ok := m.Of(10); ok {
		t.Error("Of() answered on an empty graph")
	}
	if m.SameComponent(10, 20) {
		t.Error("SameComponent() related two keys on an empty graph")
	}
	if got := m.Members(10); got != nil {
		t.Errorf("Members(10) = %v, want nil", got)
	}
	if got := m.Size(10); got != 0 {
		t.Errorf("Size(10) = %d, want 0", got)
	}
}

// TestMembershipShareTheLabelArrayWithNoSecondStructure is a budget assertion, and it is the
// reason this type is a wrapper rather than an index.
//
// ADR-0404 §2 budgets *one* union-find array — 420 MB at 110 million nodes — and W1's
// Components() already returns it flattened so a lookup is one read. A Membership that built
// its own index would double that, and the record's §9 is explicit that the whole graph is
// the exception rather than the entry: the query that has to be cheap at that size is the
// lookup, and the lookup is already O(1) against the array that exists.
func TestMembershipSharesTheLabelArrayWithNoSecondStructure(t *testing.T) {
	g, err := Build(threeCount())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	m := g.Membership()
	labels := m.Labels()
	if len(labels) != g.Ordinals.Len() {
		t.Fatalf("Labels() has %d entries for %d nodes", len(labels), g.Ordinals.Len())
	}
	// The membership *is* the label array: writing through it changes what the lookups say,
	// which is what proves there is no copy hiding behind the API.
	labels[0] = 4
	if got, _ := m.Of(10); got != 50 {
		t.Errorf("after writing the label array, Of(10) = %d, want the array's answer (50) — the API holds a copy", got)
	}
}
