package catalog

import (
	"reflect"
	"testing"
)

// The release's exclusion map and the publish refusals nothing else exercised.

// TestExclusionsAreFrozenBothWaysRound: an incompatibility is symmetric, so the
// release answers "what does this item exclude" for either side in one lookup. A
// reader that found only one direction would report half the violations
// (ADR-0342).
func TestExclusionsAreFrozenBothWaysRound(t *testing.T) {
	ids := []string{"approve", "create", "audit", "vpn"}
	var items []Item
	for _, id := range ids {
		items = append(items, item(id))
	}
	rel := mustPublish(t, Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: ids}},
		Items:    items,
		Edges: []Edge{
			{From: "approve", To: "create", Kind: EdgeExcludes},
			{From: "audit", To: "approve", Kind: EdgeExcludes},
			// Written twice, once each way: still one pair.
			{From: "create", To: "approve", Kind: EdgeExcludes},
		},
	})
	want := map[string][]string{
		"approve": {"audit", "create"},
		"create":  {"approve"},
		"audit":   {"approve"},
	}
	if !reflect.DeepEqual(rel.Excludes, want) {
		t.Fatalf("Excludes = %v, want %v (both directions, sorted, none for vpn)", rel.Excludes, want)
	}
}

// TestAReleaseWithoutExclusionsCarriesNone: no entry at all rather than an empty
// map, which is what lets an order skip the conflict walk entirely.
func TestAReleaseWithoutExclusionsCarriesNone(t *testing.T) {
	rel := mustPublish(t, Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"a", "b"}}},
		Items:    []Item{item("a"), item("b")},
		Edges:    []Edge{requires("a", "b")},
	})
	if rel.Excludes != nil {
		t.Fatalf("Excludes = %v, want nil for a catalogue that excludes nothing", rel.Excludes)
	}
}

// TestAnItemExcludingItselfIsRefused: holding it once would already be a conflict.
// Somebody wrote the edge meaning something, so it is refused rather than dropped.
func TestAnItemExcludingItselfIsRefused(t *testing.T) {
	_, problems := Publish(Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"a"}}},
		Items:    []Item{item("a")},
		Edges:    []Edge{{From: "a", To: "a", Kind: EdgeExcludes}},
	})
	contains(t, problems, "excludes itself")
}

// TestABlankEligibleGroupIsRefusedOnce: a blank group matches nobody, so the product
// would be orderable by no one with nothing saying why (ADR-0347). Two blanks are one
// problem: the fix is the same.
func TestABlankEligibleGroupIsRefusedOnce(t *testing.T) {
	it := item("a")
	it.Eligible = []string{"grp_staff", " ", ""}
	_, problems := Publish(Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"a"}}},
		Items:    []Item{it},
	})
	contains(t, problems, "blank eligible group")
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want exactly one for the blank groups", problems)
	}
}

// TestANegativeCeilingIsRefused: a right that ended before it began would be reported
// overdue from its first moment. Zero is the ordinary "no end" and publishes.
func TestANegativeCeilingIsRefused(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"a"}}},
	}
	neg := item("a")
	neg.MaxDays = -1
	in.Items = []Item{neg}
	_, problems := Publish(in)
	contains(t, problems, "maxDays is negative")

	zero := item("a")
	in.Items = []Item{zero}
	mustPublish(t, in)
}

// TestEveryContradictoryStructuralPairIsReported: a pair drawn both as composition and
// as aggregation cannot be acted on by the basket, and each such pair is its own
// problem — fixing one must not leave the others to be discovered one publish at a
// time.
func TestEveryContradictoryStructuralPairIsReported(t *testing.T) {
	ids := []string{"w", "v", "a", "b"}
	var items []Item
	for _, id := range ids {
		items = append(items, item(id))
	}
	var edges []Edge
	for _, pair := range [][2]string{{"w", "b"}, {"w", "a"}, {"v", "a"}} {
		edges = append(edges,
			Edge{From: pair[0], To: pair[1], Kind: EdgeComposition},
			Edge{From: pair[0], To: pair[1], Kind: EdgeAggregation})
	}
	_, problems := Publish(Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: ids}},
		Items:    items,
		Edges:    edges,
	})
	var got [][2]string
	for _, p := range problems {
		for _, part := range []string{"a", "b"} {
			if p.Message == "contains "+part+" both as composition and as aggregation; "+
				"one is integral and the other optional, and the basket cannot be both" {
				got = append(got, [2]string{p.Item, part})
			}
		}
	}
	want := [][2]string{{"v", "a"}, {"w", "a"}, {"w", "b"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("contradictions reported = %v, want %v (one per pair, in order); all problems: %v",
			got, want, problems)
	}
}

// TestProblemsAreOrderedByCatalogueFirst: problems about no particular catalogue come
// before those about one, so two attempts at the same broken set diff cleanly.
func TestProblemsAreOrderedByCatalogueFirst(t *testing.T) {
	draft := item("a")
	draft.State = StateDraft
	_, problems := Publish(Input{
		CatalogID: "b",
		Catalogs: []Catalog{
			{ID: "b", Rank: 1, Languages: []string{"de"}, Items: []string{"a", "ghost"}},
			{ID: "a", Rank: 1, Languages: []string{"de"}, Items: []string{"a"}},
		},
		Items: []Item{draft},
	})
	var got []string
	for _, p := range problems {
		got = append(got, p.Catalog+"|"+p.Item+"|"+p.Message)
	}
	want := []string{
		"|a|state is draft, not active",
		"b||rank 1 is already held by catalogue a",
		"b||unknown item ghost",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("problems = %q, want %q", got, want)
	}
}
