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

// waveOf reports which wave an item was scheduled into, or -1.
func waveOf(rel Release, id string) int {
	for i, wave := range rel.Waves {
		for _, got := range wave {
			if got == id {
				return i
			}
		}
	}
	return -1
}

// scheduled counts how many times an item appears across every wave.
func scheduled(rel Release, id string) int {
	n := 0
	for _, wave := range rel.Waves {
		for _, got := range wave {
			if got == id {
				n++
			}
		}
	}
	return n
}

// flatten renders the schedule as "a,b|c" so two can be compared as one string.
func flatten(rel Release) string {
	var waves []string
	for _, wave := range rel.Waves {
		waves = append(waves, strings.Join(wave, ","))
	}
	return strings.Join(waves, "|")
}

func mustPublish(t *testing.T, in Input) Release {
	t.Helper()
	rel, problems := Publish(in)
	if len(problems) != 0 {
		t.Fatalf("Publish: %v", problems)
	}
	return rel
}

// TestPreconditionsLandInEarlierWaves is the property the whole precomputation
// exists for: the release carries a schedule, so ordering never walks the graph.
func TestPreconditionsLandInEarlierWaves(t *testing.T) {
	// vpn requires laptop; mailbox requires account; nothing else relates.
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"},
			Items: []string{"laptop", "vpn", "account", "mailbox"}}},
		Items: []Item{item("laptop"), item("vpn"), item("account"), item("mailbox")},
		Edges: []Edge{requires("vpn", "laptop"), requires("mailbox", "account")},
	}
	rel := mustPublish(t, in)

	if waveOf(rel, "laptop") >= waveOf(rel, "vpn") {
		t.Errorf("laptop must be in an earlier wave than vpn: %v", rel.Waves)
	}
	if waveOf(rel, "account") >= waveOf(rel, "mailbox") {
		t.Errorf("account must be in an earlier wave than mailbox: %v", rel.Waves)
	}
}

// TestIndependentItemsShareAWave is why a schedule replaced a flat sequence.
// Order 20 says a failing line must not stop lines that do not depend on it, and
// a flat list cannot express that — it has already thrown away the reason each
// item sits where it does. Items that need nothing from each other run together,
// so one failure stops its own successors and nothing else.
func TestIndependentItemsShareAWave(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"},
			Items: []string{"laptop", "vpn", "account", "mailbox"}}},
		Items: []Item{item("laptop"), item("vpn"), item("account"), item("mailbox")},
		Edges: []Edge{requires("vpn", "laptop"), requires("mailbox", "account")},
	}
	rel := mustPublish(t, in)

	if len(rel.Waves) != 2 {
		t.Fatalf("want 2 waves, got %d: %v", len(rel.Waves), rel.Waves)
	}
	if got := strings.Join(rel.Waves[0], ","); got != "account,laptop" {
		t.Errorf("first wave = %s, want account,laptop", got)
	}
	if got := strings.Join(rel.Waves[1], ","); got != "mailbox,vpn" {
		t.Errorf("second wave = %s, want mailbox,vpn", got)
	}
}

// TestItemWaitsForItsLatestPrecondition: two preconditions in different waves
// means the dependent goes after the later one, not after the first satisfied.
func TestItemWaitsForItsLatestPrecondition(t *testing.T) {
	// desk needs nothing; account needs nothing; laptop needs desk;
	// workplace needs account (wave 0) and laptop (wave 1) -> wave 2.
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"},
			Items: []string{"desk", "account", "laptop", "workplace"}}},
		Items: []Item{item("desk"), item("account"), item("laptop"), item("workplace")},
		Edges: []Edge{
			requires("laptop", "desk"),
			requires("workplace", "account"),
			requires("workplace", "laptop"),
		},
	}
	rel := mustPublish(t, in)
	if got := waveOf(rel, "workplace"); got != 2 {
		t.Fatalf("workplace in wave %d, want 2: %v", got, rel.Waves)
	}
}

// TestScheduleIsDeterministic: two publishes of the same catalogue produce the
// same schedule. One that reordered between publishes would make a diff of two
// releases unreadable, and the same order would fulfil differently twice.
func TestScheduleIsDeterministic(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"},
			Items: []string{"c", "a", "b", "d"}}},
		Items: []Item{item("c"), item("a"), item("b"), item("d")},
		Edges: []Edge{requires("d", "a")},
	}
	first := mustPublish(t, in)
	for i := 0; i < 5; i++ {
		if again := flatten(mustPublish(t, in)); again != flatten(first) {
			t.Fatalf("schedule %s differs from %s", again, flatten(first))
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
	if len(rel.Waves) != 0 {
		t.Fatalf("Waves = %v, want empty", rel.Waves)
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
	if seen := scheduled(rel, "shared"); seen != 1 {
		t.Fatalf("shared scheduled %d times in %v, want once", seen, rel.Waves)
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

// TestLongPrecedenceChainBecomesOneItemPerWave: three links deep, given in the
// wrong order, yields four waves of one — nothing here can run alongside
// anything else, and the schedule says so.
func TestLongPrecedenceChainBecomesOneItemPerWave(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"},
			Items: []string{"d", "c", "b", "a"}}},
		Items: []Item{item("d"), item("c"), item("b"), item("a")},
		Edges: []Edge{requires("d", "c"), requires("c", "b"), requires("b", "a")},
	}
	rel := mustPublish(t, in)
	if got := flatten(rel); got != "a|b|c|d" {
		t.Fatalf("schedule = %s, want a|b|c|d (one item per wave)", got)
	}
}
