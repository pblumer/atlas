package catalog

import (
	"strings"
	"testing"
)

// Publishing is where the work happens (I5). These tests state what a release
// proves, because every one of these checks is a question that would otherwise be
// asked when somebody orders — and a cycle in a catalogue is a modelling error that
// must surface for the person who published it, not as an incident at 23:00 for
// whoever ordered.

// item builds a publishable item with both processes bound and one text.
func item(id string, edges ...string) Item {
	return Item{
		ID: id, HomeCatalog: "cat", State: StateActive,
		Texts:              map[string]string{"de": id},
		Approval:           Approval{Kind: KindNone},
		ProvisionProcess:   "prov-" + id,
		DeprovisionProcess: "deprov-" + id,
	}
}

// requires builds a precedence edge: from cannot be provisioned before to is.
func requires(from, to string) Edge { return Edge{From: from, To: to, Kind: EdgeRequires} }

func contains(t *testing.T, got []Problem, want string) {
	t.Helper()
	for _, p := range got {
		if strings.Contains(p.Message, want) {
			return
		}
	}
	t.Fatalf("no problem mentioning %q; got %v", want, got)
}

func mustPublish(t *testing.T, in Input) Release {
	t.Helper()
	rel, problems := Publish(in)
	if len(problems) != 0 {
		t.Fatalf("Publish: %v", problems)
	}
	return rel
}

// TestOrderPutsPreconditionsFirst is the property the whole precomputation exists
// for: the release carries a sequence, so ordering never walks the graph.
func TestOrderPutsPreconditionsFirst(t *testing.T) {
	// vpn requires laptop; mailbox requires account; nothing else relates.
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"},
			Items: []string{"laptop", "vpn", "account", "mailbox"}}},
		Items: []Item{item("laptop"), item("vpn"), item("account"), item("mailbox")},
		Edges: []Edge{requires("vpn", "laptop"), requires("mailbox", "account")},
	}
	rel := mustPublish(t, in)

	pos := map[string]int{}
	for i, id := range rel.Order {
		pos[id] = i
	}
	if len(rel.Order) != 4 {
		t.Fatalf("order has %d items, want 4: %v", len(rel.Order), rel.Order)
	}
	if pos["laptop"] > pos["vpn"] {
		t.Errorf("laptop must precede vpn: %v", rel.Order)
	}
	if pos["account"] > pos["mailbox"] {
		t.Errorf("account must precede mailbox: %v", rel.Order)
	}
}

// TestOrderIsDeterministic: two publishes of the same catalogue produce the same
// sequence. A release that reordered between publishes would make a diff of two
// releases unreadable, and the same order would fulfil differently twice.
func TestOrderIsDeterministic(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"},
			Items: []string{"c", "a", "b", "d"}}},
		Items: []Item{item("c"), item("a"), item("b"), item("d")},
		Edges: []Edge{requires("d", "a")},
	}
	first := mustPublish(t, in)
	for i := 0; i < 5; i++ {
		again := mustPublish(t, in)
		if strings.Join(again.Order, ",") != strings.Join(first.Order, ",") {
			t.Fatalf("order %v differs from %v", again.Order, first.Order)
		}
	}
}

// TestCycleIsRefused: the reason the sort happens at publish time at all.
func TestCycleIsRefused(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"},
			Items: []string{"a", "b", "c"}}},
		Items: []Item{item("a"), item("b"), item("c")},
		Edges: []Edge{requires("a", "b"), requires("b", "c"), requires("c", "a")},
	}
	_, problems := Publish(in)
	contains(t, problems, "cycle")
}

// TestStructuralCycleIsRefused: a product cannot contain itself either, and the
// structural edges are a different graph from the precedence ones.
func TestStructuralCycleIsRefused(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"a", "b"}}},
		Items:    []Item{item("a"), item("b")},
		Edges: []Edge{
			{From: "a", To: "b", Kind: EdgeComposition},
			{From: "b", To: "a", Kind: EdgeComposition},
		},
	}
	_, problems := Publish(in)
	contains(t, problems, "cycle")
}

// TestPrecedenceAndStructureAreSeparateGraphs: a part that is also a precondition
// of its own whole is not a cycle. Structure says "belongs to", precedence says
// "after" — conflating them would refuse an ordinary catalogue.
func TestPrecedenceAndStructureAreSeparateGraphs(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"},
			Items: []string{"workplace", "account"}}},
		Items: []Item{item("workplace"), item("account")},
		Edges: []Edge{
			{From: "workplace", To: "account", Kind: EdgeComposition},
			requires("workplace", "account"),
		},
	}
	mustPublish(t, in)
}

// TestBothProcessesAreRequired: a catalogue that can only grant is not a lifecycle.
func TestBothProcessesAreRequired(t *testing.T) {
	noDeprovision := item("a")
	noDeprovision.DeprovisionProcess = ""
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"a"}}},
		Items:    []Item{noDeprovision},
	}
	_, problems := Publish(in)
	contains(t, problems, "deprovision")

	noProvision := item("a")
	noProvision.ProvisionProcess = ""
	in.Items = []Item{noProvision}
	_, problems = Publish(in)
	contains(t, problems, "provision")
}

// TestEveryDeclaredLanguageIsTranslated: a customer must not meet a product in a
// language the catalogue promised and does not have.
func TestEveryDeclaredLanguageIsTranslated(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de", "fr"}, Items: []string{"a"}}},
		Items:    []Item{item("a")}, // has "de" only
	}
	_, problems := Publish(in)
	contains(t, problems, "fr")
}

// TestRankTieIsRefused: ranks resolve which catalogue a user sees, so a tie is a
// coin toss dressed as a rule.
func TestRankTieIsRefused(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{
			{ID: "a", Rank: 5, Languages: []string{"de"}},
			{ID: "b", Rank: 5, Languages: []string{"de"}},
		},
	}
	_, problems := Publish(in)
	contains(t, problems, "rank")
}

// TestUnknownItemReferenceIsRefused: a catalogue naming an item that does not
// exist would be an empty tile in a shop.
func TestUnknownItemReferenceIsRefused(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"ghost"}}},
	}
	_, problems := Publish(in)
	contains(t, problems, "ghost")
}

// TestEdgeToUnknownItemIsRefused: an edge into nothing would silently drop a
// precondition, which is the one failure the precomputed order must not have.
func TestEdgeToUnknownItemIsRefused(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"a"}}},
		Items:    []Item{item("a")},
		Edges:    []Edge{requires("a", "ghost")},
	}
	_, problems := Publish(in)
	contains(t, problems, "ghost")
}

// TestDraftAndWithdrawnCannotBePublished: withdrawn items stay resolvable forever
// but must not be orderable, and a draft is not finished.
func TestDraftAndWithdrawnCannotBePublished(t *testing.T) {
	for _, state := range []State{StateDraft, StateWithdrawn} {
		it := item("a")
		it.State = state
		in := Input{
			Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"a"}}},
			Items:    []Item{it},
		}
		_, problems := Publish(in)
		contains(t, problems, string(state))
	}
}

// TestApprovalRuleNeedingATargetMustHaveOne: "fixed" with nobody named would
// route an approval into nothing, and the order would park forever.
func TestApprovalRuleNeedingATargetMustHaveOne(t *testing.T) {
	for _, kind := range []ApprovalKind{KindFixed, KindRole} {
		it := item("a")
		it.Approval = Approval{Kind: kind}
		in := Input{
			Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"a"}}},
			Items:    []Item{it},
		}
		_, problems := Publish(in)
		contains(t, problems, "ref")
	}
}

// TestSuperiorAndNoneNeedNoTarget is the other half: the two kinds that resolve
// their approver from the order itself must not be asked for one.
func TestSuperiorAndNoneNeedNoTarget(t *testing.T) {
	for _, kind := range []ApprovalKind{KindNone, KindSuperior} {
		it := item("a")
		it.Approval = Approval{Kind: kind}
		in := Input{
			Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"a"}}},
			Items:    []Item{it},
		}
		mustPublish(t, in)
	}
}

// TestReleaseNamesItsUnapprovedItems: the standing list that keeps the escalation
// path visible when one role both defines approval rules and publishes.
func TestReleaseNamesItsUnapprovedItems(t *testing.T) {
	free, gated := item("free"), item("gated")
	gated.Approval = Approval{Kind: KindSuperior}
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"},
			Items: []string{"free", "gated"}}},
		Items: []Item{free, gated},
	}
	rel := mustPublish(t, in)
	if len(rel.WithoutApproval) != 1 || rel.WithoutApproval[0] != "free" {
		t.Fatalf("WithoutApproval = %v, want [free]", rel.WithoutApproval)
	}
}

// TestPublishReportsEveryProblemAtOnce: an author fixing one refusal per attempt
// is how a validation gate becomes a thing people work around.
func TestPublishReportsEveryProblemAtOnce(t *testing.T) {
	broken := item("a")
	broken.ProvisionProcess = ""
	broken.DeprovisionProcess = ""
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de", "fr"}, Items: []string{"a"}}},
		Items:    []Item{broken},
	}
	_, problems := Publish(in)
	if len(problems) < 3 {
		t.Fatalf("want at least 3 problems (provision, deprovision, fr), got %v", problems)
	}
}

// TestProblemsAreDeterministic: the same broken catalogue reports the same list in
// the same order, so a reviewer diffing two attempts sees what changed.
func TestProblemsAreDeterministic(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{
			{ID: "b", Rank: 2, Languages: []string{"de", "fr"}, Items: []string{"x", "y"}},
			{ID: "a", Rank: 1, Languages: []string{"de"}, Items: []string{"ghost"}},
		},
		Items: []Item{item("x"), item("y")},
	}
	_, first := Publish(in)
	for i := 0; i < 5; i++ {
		_, again := Publish(in)
		if len(again) != len(first) {
			t.Fatalf("problem count %d differs from %d", len(again), len(first))
		}
		for j := range again {
			if again[j] != first[j] {
				t.Fatalf("problem %d = %v, want %v", j, again[j], first[j])
			}
		}
	}
}

// TestEmptyCatalogPublishes: nothing about an empty catalogue is wrong, and
// refusing one would block the first step of building any catalogue at all.
func TestEmptyCatalogPublishes(t *testing.T) {
	rel := mustPublish(t, Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}}},
	})
	if len(rel.Order) != 0 {
		t.Fatalf("Order = %v, want empty", rel.Order)
	}
}

// TestItemInSeveralCatalogsIsOrderedOnce: the same service legitimately appears in
// several products and several catalogues; the fulfilment sequence names it once.
func TestItemInSeveralCatalogsIsOrderedOnce(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{
			{ID: "a", Rank: 1, Languages: []string{"de"}, Items: []string{"shared", "x"}},
			{ID: "b", Rank: 2, Languages: []string{"de"}, Items: []string{"shared", "y"}},
		},
		Items: []Item{item("shared"), item("x"), item("y")},
	}
	rel := mustPublish(t, in)
	seen := 0
	for _, id := range rel.Order {
		if id == "shared" {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("shared appears %d times in %v, want once", seen, rel.Order)
	}
}

// TestProblemStringNamesWhereItIs: a refusal list is read by a person fixing a
// catalogue, so each line has to say which item or catalogue it is about.
func TestProblemStringNamesWhereItIs(t *testing.T) {
	tests := []struct {
		name string
		p    Problem
		want string
	}{
		{"item wins over catalogue",
			Problem{Catalog: "cat", Item: "vpn", Message: "no provision process bound"},
			"item vpn: no provision process bound"},
		{"catalogue when there is no item",
			Problem{Catalog: "cat", Message: "unknown item ghost"},
			"catalogue cat: unknown item ghost"},
		{"neither, for a whole-input problem",
			Problem{Message: "precedence cycle: a, b"},
			"precedence cycle: a, b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.String(); got != tt.want {
				t.Fatalf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestOrderableWindowMustNotEndBeforeItBegins: a window that closes before it
// opens is an item nobody can ever order, published without complaint.
func TestOrderableWindowMustNotEndBeforeItBegins(t *testing.T) {
	it := item("a")
	it.Lifecycle = Lifecycle{From: 2000, Until: 1000}
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"a"}}},
		Items:    []Item{it},
	}
	_, problems := Publish(in)
	contains(t, problems, "ends before it begins")

	// One open side is the ordinary case and must publish.
	for _, lc := range []Lifecycle{{}, {From: 1000}, {Until: 2000}, {From: 1000, Until: 2000}} {
		ok := item("a")
		ok.Lifecycle = lc
		in.Items = []Item{ok}
		mustPublish(t, in)
	}
}

// TestEdgeFromUnknownItemIsRefused is the other half of the edge check: a
// precondition hanging off an item that does not exist.
func TestEdgeFromUnknownItemIsRefused(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"a"}}},
		Items:    []Item{item("a")},
		Edges:    []Edge{requires("ghost", "a")},
	}
	_, problems := Publish(in)
	contains(t, problems, "edge from unknown item ghost")
}

// TestStructuralKindsAreStructural pins the classification the two graphs are
// separated by. Reading requires as structural is how a workplace that contains an
// account and needs one first becomes a refused cycle.
func TestStructuralKindsAreStructural(t *testing.T) {
	for kind, want := range map[EdgeKind]bool{
		EdgeComposition: true, EdgeAggregation: true, EdgeRequires: false,
	} {
		if got := kind.Structural(); got != want {
			t.Errorf("%s.Structural() = %v, want %v", kind, got, want)
		}
	}
}

// TestLongPrecedenceChainKeepsItsOrder: three links deep, given in the wrong
// order, still comes back with every precondition ahead of its dependent.
func TestLongPrecedenceChainKeepsItsOrder(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"},
			Items: []string{"d", "c", "b", "a"}}},
		Items: []Item{item("d"), item("c"), item("b"), item("a")},
		Edges: []Edge{requires("d", "c"), requires("c", "b"), requires("b", "a")},
	}
	rel := mustPublish(t, in)
	if got := strings.Join(rel.Order, ","); got != "a,b,c,d" {
		t.Fatalf("Order = %s, want a,b,c,d", got)
	}
}
