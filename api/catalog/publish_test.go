package catalog

import (
	"net/http"
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

// TestEveryDeclaredLanguageIsReportedAndNotRefused.
//
// This used to refuse, on the reasoning that a customer must not meet a product
// in a language the catalogue promised and does not have. They never did: the
// portal falls back to the language the catalogue has, so what the refusal
// actually stopped was a usable catalogue going live. It is reported instead —
// see translationgaps_test.go for the whole of that argument.
func TestEveryDeclaredLanguageIsReportedAndNotRefused(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de", "fr"}, Items: []string{"a"}}},
		Items:    []Item{item("a")}, // has "de" only
	}
	if _, problems := Publish(in); len(problems) != 0 {
		t.Errorf("a product named in one of two declared languages was refused: %+v", problems)
	}
	contains(t, TranslationGaps(in), "no name in fr")
}

// TestRankTieIsRefused: ranks resolve which catalogue a user sees, so a tie is a
// coin toss dressed as a rule.
func TestRankTieIsRefused(t *testing.T) {
	in := Input{
		CatalogID: "a",
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
	// Three refusals of three different kinds, so the test cannot pass on one
	// check reported twice. The third used to be the missing French text; that is
	// a gap now and not a refusal, so it is a blank category instead — still a
	// refusal, and still nothing to do with the two above it.
	broken.Category = "   "
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de", "fr"}, Items: []string{"a"}}},
		Items:    []Item{broken},
	}
	_, problems := Publish(in)
	if len(problems) < 3 {
		t.Fatalf("want at least 3 problems (provision, deprovision, blank category), got %v", problems)
	}
}

// TestProblemsAreDeterministic: the same broken catalogue reports the same list in
// the same order, so a reviewer diffing two attempts sees what changed.
func TestProblemsAreDeterministic(t *testing.T) {
	in := Input{
		CatalogID: "a",
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
		CatalogID: "a",
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

// A wave is the right unit to *run* in and the wrong unit to *start on*. Waves say
// what may go in parallel; they cannot say which lines a failure takes with it,
// because at a wave boundary they know only "the previous wave is done". The
// release therefore also carries each item's direct preconditions, and Blocked is
// what the fulfilment process asks when a line fails.

// TestReleaseCarriesDirectPreconditions: the edges the schedule was computed from
// survive into the release, so the orchestrator does not need the catalogue.
func TestReleaseCarriesDirectPreconditions(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"},
			Items: []string{"desk", "account", "laptop", "workplace"}}},
		Items: []Item{item("desk"), item("account"), item("laptop"), item("workplace")},
		Edges: []Edge{
			requires("laptop", "desk"),
			requires("workplace", "laptop"),
			requires("workplace", "account"),
		},
	}
	rel := mustPublish(t, in)

	if got := strings.Join(rel.Requires["workplace"], ","); got != "account,laptop" {
		t.Errorf("Requires[workplace] = %s, want account,laptop (sorted)", got)
	}
	if got := strings.Join(rel.Requires["laptop"], ","); got != "desk" {
		t.Errorf("Requires[laptop] = %s, want desk", got)
	}
	// An item that needs nothing carries no entry: an empty list is noise in a
	// document a person reads.
	if _, ok := rel.Requires["desk"]; ok {
		t.Errorf("Requires has an entry for desk, which needs nothing")
	}
}

// TestBlockedStopsOnlyWhatDependsOnTheFailure is the rule the whole second field
// exists for: a failing line stops its own successors and nothing else.
func TestBlockedStopsOnlyWhatDependsOnTheFailure(t *testing.T) {
	// laptop -> vpn, account -> mailbox. laptop fails; mailbox must still run.
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"},
			Items: []string{"laptop", "vpn", "account", "mailbox"}}},
		Items: []Item{item("laptop"), item("vpn"), item("account"), item("mailbox")},
		Edges: []Edge{requires("vpn", "laptop"), requires("mailbox", "account")},
	}
	rel := mustPublish(t, in)

	got := rel.Blocked("laptop")
	if strings.Join(got, ",") != "vpn" {
		t.Fatalf("Blocked(laptop) = %v, want [vpn] — mailbox depends on account, not laptop", got)
	}
}

// TestBlockedIsTransitive: a failure three links up stops the whole chain, not
// only the item directly behind it.
func TestBlockedIsTransitive(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"},
			Items: []string{"a", "b", "c", "d"}}},
		Items: []Item{item("a"), item("b"), item("c"), item("d")},
		Edges: []Edge{requires("b", "a"), requires("c", "b"), requires("d", "c")},
	}
	rel := mustPublish(t, in)
	if got := strings.Join(rel.Blocked("a"), ","); got != "b,c,d" {
		t.Fatalf("Blocked(a) = %s, want b,c,d", got)
	}
}

// TestBlockedExcludesTheFailureItself: the failed line has a failure of its own;
// reporting it as blocked would tell somebody to look in the wrong place.
func TestBlockedExcludesTheFailureItself(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"a", "b"}}},
		Items:    []Item{item("a"), item("b")},
		Edges:    []Edge{requires("b", "a")},
	}
	rel := mustPublish(t, in)
	for _, id := range rel.Blocked("a") {
		if id == "a" {
			t.Fatalf("Blocked(a) = %v, must not contain a itself", rel.Blocked("a"))
		}
	}
}

// TestBlockedTakesSeveralFailuresAtOnce: a wave can fail in more than one place,
// and the union must be reported once each rather than twice.
func TestBlockedTakesSeveralFailuresAtOnce(t *testing.T) {
	// Both x and y are preconditions of z; both fail.
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"},
			Items: []string{"x", "y", "z", "free"}}},
		Items: []Item{item("x"), item("y"), item("z"), item("free")},
		Edges: []Edge{requires("z", "x"), requires("z", "y")},
	}
	rel := mustPublish(t, in)
	if got := strings.Join(rel.Blocked("x", "y"), ","); got != "z" {
		t.Fatalf("Blocked(x, y) = %s, want z once", got)
	}
	if got := rel.Blocked("free"); len(got) != 0 {
		t.Fatalf("Blocked(free) = %v, want nothing", got)
	}
}

// TestBlockedOnNothingBlocksNothing: the ordinary path, where a wave settles
// clean.
func TestBlockedOnNothingBlocksNothing(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"a", "b"}}},
		Items:    []Item{item("a"), item("b")},
		Edges:    []Edge{requires("b", "a")},
	}
	rel := mustPublish(t, in)
	if got := rel.Blocked(); len(got) != 0 {
		t.Fatalf("Blocked() = %v, want nothing", got)
	}
	if got := rel.Blocked("unknown"); len(got) != 0 {
		t.Fatalf("Blocked(unknown) = %v, want nothing", got)
	}
}

// TestOneStructuralPairCannotBeBothKinds.
//
// [Release.Includes] and [Release.Options] are kept apart because they mean
// opposite things to a basket: an inclusion is a consequence of ordering the whole
// — integral, never deselectable — and an option is an offer. A pair carrying both
// structural kinds lands in both lists, so the same part is ordered without asking
// *and* offered as a choice, on one screen.
//
// It is reachable without anybody writing a contradiction. mergeEdges, behind the
// ArchiMate import, only ever adds — deliberately, because "an import is not a
// synchronisation" — so redrawing a composition as an aggregation in the model and
// importing again leaves the catalogue holding both, and the old edge is the one
// nobody remembers.
//
// Refused rather than resolved: picking one would publish a catalogue that does not
// say what its author drew, and there is no honest rule for which of the two they
// meant. Refused *here* because it is provable at publish, which is where anything
// provable at publish belongs (I5).
func TestOneStructuralPairCannotBeBothKinds(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"},
			Items: []string{"workplace", "laptop"}}},
		Items: []Item{item("workplace"), item("laptop")},
		Edges: []Edge{
			{From: "workplace", To: "laptop", Kind: EdgeComposition},
			{From: "workplace", To: "laptop", Kind: EdgeAggregation},
		},
	}
	rel, problems := Publish(in)
	if len(problems) == 0 {
		t.Fatalf("published a part that is integral and optional at once: includes=%v options=%v",
			rel.Includes, rel.Options)
	}
	contains(t, problems, "both as composition and as aggregation")
}

// TestTheSameStructuralEdgeTwiceIsNotAContradiction.
//
// The check above has to distinguish two kinds on one pair from one kind written
// twice. The second is what an import produces routinely — the same model read
// again — and refusing it would make re-importing an unchanged model an error.
func TestTheSameStructuralEdgeTwiceIsNotAContradiction(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"},
			Items: []string{"workplace", "laptop"}}},
		Items: []Item{item("workplace"), item("laptop")},
		Edges: []Edge{
			{From: "workplace", To: "laptop", Kind: EdgeComposition},
			{From: "workplace", To: "laptop", Kind: EdgeComposition},
		},
	}
	rel := mustPublish(t, in)
	if got := rel.Includes["workplace"]; len(got) == 0 {
		t.Errorf("the part was lost: includes=%v", rel.Includes)
	}
}

// TestTheTwoKindsAreFineOnDifferentPairs.
//
// A whole legitimately contains one part integrally and offers another alongside
// it — that is the ordinary shape of a bundle, and a check that looked at the
// parent alone rather than at the pair would refuse it.
func TestTheTwoKindsAreFineOnDifferentPairs(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"},
			Items: []string{"workplace", "laptop", "monitor"}}},
		Items: []Item{item("workplace"), item("laptop"), item("monitor")},
		Edges: []Edge{
			{From: "workplace", To: "laptop", Kind: EdgeComposition},
			{From: "workplace", To: "monitor", Kind: EdgeAggregation},
		},
	}
	rel := mustPublish(t, in)
	if len(rel.Includes["workplace"]) != 1 || len(rel.Options["workplace"]) != 1 {
		t.Errorf("a bundle with one integral part and one option was not published as such: "+
			"includes=%v options=%v", rel.Includes, rel.Options)
	}
}

// TestAnotherCataloguesProductsDoNotBlockThisOne.
//
// The defect this reproduces made the product unusable at the second catalogue.
// [Store.InputFor] passes every catalogue (a rank is unique across the set) but
// only *this* catalogue's items, and checkCatalogs resolved every catalogue's item
// references against that one list. So each of the others came back "unknown
// item", and a catalogue could be published only while every other catalogue was
// empty.
//
// The two messages it produced are the reason it read as a contradiction rather
// than as a bug: publishing A blamed B, publishing B blamed A, and neither named
// the catalogue the caller had asked to publish.
//
// End to end through the handler, because the defect was in the seam between the
// store's input and the checker's reading of it — either one alone looks right.
func TestAnotherCataloguesProductsDoNotBlockThisOne(t *testing.T) {
	s := serviceWithAdmin(t)
	a := makeCatalog(t, s, user("usr_a"))
	b := makeCatalog(t, s, user("usr_a"))
	as(t, s.HandleUpdateCatalog, user("usr_a"), "PATCH", `{"rank":2}`, "id", b.ID)
	as(t, s.HandleSaveItem, user("usr_a"), "POST", productBody("laptop", a.ID))
	as(t, s.HandleSaveItem, user("usr_a"), "POST", productBody("mailbox", b.ID))
	as(t, s.HandleUpdateCatalog, user("usr_a"), "PATCH", `{"items":["laptop"]}`, "id", a.ID)
	as(t, s.HandleUpdateCatalog, user("usr_a"), "PATCH", `{"items":["mailbox"]}`, "id", b.ID)

	// Both ways round, because the symmetry is the whole shape of the defect: one
	// of the two would have passed by accident if the other catalogue were empty.
	for _, c := range []struct {
		name string
		id   string
	}{{"A while B carries a product", a.ID}, {"B while A carries a product", b.ID}} {
		t.Run(c.name, func(t *testing.T) {
			rec := as(t, s.HandlePublish, user("usr_a"), "POST", "", "id", c.id)
			if rec.Code != http.StatusCreated {
				t.Fatalf("publish = %d, want 201; another catalogue's products are not this "+
					"catalogue's problem: %s", rec.Code, rec.Body)
			}
		})
	}
}

// TestThisCataloguesOwnUnknownProductIsStillRefused.
//
// The half that must not be lost with the fix. Scoping the check to the subject is
// right; scoping it away would publish a catalogue naming a product that does not
// exist, which is an empty tile in a shop.
func TestThisCataloguesOwnUnknownProductIsStillRefused(t *testing.T) {
	in := Input{
		CatalogID: "a",
		Catalogs: []Catalog{
			{ID: "a", Rank: 1, Languages: []string{"de"}, Items: []string{"ghost"}},
			{ID: "b", Rank: 2, Languages: []string{"de"}, Items: []string{"real"}},
		},
		Items: []Item{},
	}
	_, problems := Publish(in)
	contains(t, problems, "unknown item ghost")
	for _, p := range problems {
		if strings.Contains(p.Message, "real") {
			t.Errorf("the other catalogue's product was judged too: %v", p)
		}
	}
}

// TestAPublishThatDoesNotSayWhatItIsForIsRefused.
//
// An unnamed subject is read as "every catalogue here is the subject", which is
// true of one and false of any other number. Refusing the ambiguous case is what
// stops the reading from being a guess — and a guess here is the defect above,
// re-entered by the next caller who forgets the field.
func TestAPublishThatDoesNotSayWhatItIsForIsRefused(t *testing.T) {
	two := []Catalog{{ID: "a", Rank: 1, Languages: []string{"de"}}, {ID: "b", Rank: 2, Languages: []string{"de"}}}
	_, problems := Publish(Input{Catalogs: two})
	contains(t, problems, "does not say which")

	// One catalogue is not ambiguous: there is nothing else it could be for, and
	// refusing it would break every caller that has only ever had one.
	if _, problems := Publish(Input{Catalogs: two[:1]}); len(problems) != 0 {
		t.Errorf("a single catalogue needs no subject named: %v", problems)
	}
	// A subject that is not in the input is a caller error, not an empty catalogue.
	_, problems = Publish(Input{CatalogID: "gone", Catalogs: two})
	contains(t, problems, "is not in this input")
}

// A description is optional as a whole and all-or-nothing once there is one
// (#1069). The rule is not "every product needs a paragraph" — most do not — it is
// that a product which explains itself must explain itself to everybody the
// catalogue is published for.

// bilingual is an item a two-language catalogue accepts by name, so these tests
// fail on the description rule or not at all.
func bilingual(id string) Item {
	it := item(id)
	it.Texts = map[string]string{"de": id, "fr": id}
	return it
}

func TestAProductNeedsNoDescriptionAtAll(t *testing.T) {
	it := bilingual("vpn")

	_, problems := Publish(Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de", "fr"}, Items: []string{"vpn"}}},
		Items:    []Item{it},
	})

	if len(problems) != 0 {
		t.Fatalf("the fixture itself is unpublishable: %+v", problems)
	}
}

// TestADescriptionInOneLanguageIsReportedAndNotRefused.
//
// The name's rule, one field down, and relaxed with it: the portal falls back to
// the description it has rather than showing an empty panel, so the half is worth
// publishing and worth saying. See translationgaps_test.go.
func TestADescriptionInOneLanguageIsReportedAndNotRefused(t *testing.T) {
	it := bilingual("vpn")
	it.Descriptions = map[string]string{"de": "Verschlüsselter Zugang ins Firmennetz."}

	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de", "fr"}, Items: []string{"vpn"}}},
		Items:    []Item{it},
	}
	if _, problems := Publish(in); len(problems) != 0 {
		t.Errorf("a product described in one of two declared languages was refused: %+v", problems)
	}
	gaps := TranslationGaps(in)
	contains(t, gaps, "no description in fr")
	// And not a complaint about the language it does have.
	for _, g := range gaps {
		if strings.HasSuffix(g.Message, " de") {
			t.Errorf("the language that has a description was faulted: %+v", g)
		}
	}
}

// Whitespace is not a description. Read as one it would demand a translation of
// nothing in every other language the catalogue declares — turning a field
// somebody cleared into a wall in front of the release.
func TestADescriptionOfSpacesIsNoDescription(t *testing.T) {
	it := bilingual("vpn")
	it.Descriptions = map[string]string{"de": "   "}

	_, problems := Publish(Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de", "fr"}, Items: []string{"vpn"}}},
		Items:    []Item{it},
	})

	for _, p := range problems {
		if strings.Contains(p.Message, "description") {
			t.Errorf("blank spaces were read as a description: %+v", p)
		}
	}
}

// A description in every declared language is the case the rule exists to let
// through.
func TestADescriptionInEveryLanguagePublishes(t *testing.T) {
	it := bilingual("vpn")
	it.Descriptions = map[string]string{"de": "Zugang ins Firmennetz.", "fr": "Accès au réseau."}

	_, problems := Publish(Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de", "fr"}, Items: []string{"vpn"}}},
		Items:    []Item{it},
	})

	if len(problems) != 0 {
		t.Errorf("a fully translated description was refused: %+v", problems)
	}
}
