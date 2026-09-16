package infomodel

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// Reading the difference between what is built and what is planned
// (ADR-0310).
//
// The list will be read as work, so the thing these tests are most careful about is
// what must *not* appear on it. A false backlog item is worse than a missing one: the
// first person to find three inventions on it stops reading the other twelve.

// planned/built name the findings on each side, for terse assertions.
func sideNames(fs []DiffFinding) []string {
	out := []string{}
	for _, f := range fs {
		switch {
		case f.Kind == KindDiffTransition:
			out = append(out, f.Class+": "+f.From+"→"+f.To)
		case f.Name != "":
			out = append(out, f.Class+": "+f.Name)
		default:
			out = append(out, f.Class)
		}
	}
	return out
}

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// orderWriter is a process that carries an Order through received → approved, writing
// one member. It is the "built" side of every case below.
func orderWriter(t *testing.T) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(1, "sales", 1)
	start := b.AddStartEvent()
	take := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, take)
	b.Connect(take, end)
	b.AddDataObject("order", "Order", "received", false)
	b.AddDataOutputAssociation(take, "order", mustExpr(t, "amount"), "approved", "total")
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// authored is the model a person wrote: an Order that can also be cancelled, with a
// member no process writes.
func authored(classes ...Class) *Vocabulary {
	return NewVocabulary([]Model{{ID: "m1", Name: "Sales", Classes: classes}})
}

func orderClass() Class {
	return Class{
		ID: "c1", Name: "Order", Stereotype: StereotypeBusinessObject, Identity: []string{"id"},
		Attributes: []Attribute{
			{Name: "id", Type: TypeString, Multiplicity: MultOne},
			{Name: "total", Type: TypeNumber, Multiplicity: MultOne},
			{Name: "cancelledOn", Type: TypeDate, Multiplicity: MultOptional},
		},
		Lifecycle: &Lifecycle{
			States: []LifecycleState{
				{Name: "received", Initial: true},
				{Name: "approved"},
				{Name: "cancelled", Final: true},
			},
			Transitions: []LifecycleTransition{
				{ID: "t1", From: "received", To: "approved"},
				{ID: "t2", From: "received", To: "cancelled"},
			},
		},
	}
}

func TestPlannedButNotBuiltIsTheBacklog(t *testing.T) {
	d := Difference([]*compiler.CompiledProcess{orderWriter(t)}, authored(orderClass()))
	planned := sideNames(d.Planned)

	// The state the class declares and nothing writes — the case ADR-0259 already
	// reports from the other side as data.unreachable-state.
	if !has(planned, "Order: cancelled") {
		t.Errorf("the unreached state is not on the backlog; got %v", planned)
	}
	// The member nothing writes.
	if !has(planned, "Order: cancelledOn") {
		t.Errorf("the unwritten member is not on the backlog; got %v", planned)
	}
	// But NOT the move into that state. `received → cancelled` is the same work as
	// `cancelled` itself — you build the state by making the move — and one job listed
	// twice is how a backlog stops being read.
	if has(planned, "Order: received→cancelled") {
		t.Error("the move into an unreached state was reported as well as the state: one job, two rows")
	}
	// received → approved *is* built, so it is on neither side.
	if has(planned, "Order: received→approved") {
		t.Error("a transition the processes do make was reported as planned")
	}
}

// The transition case that *is* its own work: both states are built, and the move
// between them is not. Nothing else reports it, so nothing else would.
func TestAMoveBetweenTwoBuiltStatesIsItsOwnWork(t *testing.T) {
	c := orderClass()
	c.Lifecycle.Transitions = append(c.Lifecycle.Transitions,
		LifecycleTransition{ID: "t3", From: "approved", To: "received"})
	d := Difference([]*compiler.CompiledProcess{orderWriter(t)}, authored(c))
	if !has(sideNames(d.Planned), "Order: approved→received") {
		t.Errorf("a move between two built states was not reported; got %v", sideNames(d.Planned))
	}
}

func TestBuiltButNotDescribedIsTheOtherDirection(t *testing.T) {
	// The model knows nothing of the member or the state the process writes.
	bare := Class{ID: "c1", Name: "Order", Stereotype: StereotypeBusinessObject,
		Identity:   []string{"id"},
		Attributes: []Attribute{{Name: "id", Type: TypeString, Multiplicity: MultOne}}}
	d := Difference([]*compiler.CompiledProcess{orderWriter(t)}, authored(bare))
	built := sideNames(d.Built)

	if !has(built, "Order: total") {
		t.Errorf("a member the processes write is not reported as undescribed; got %v", built)
	}
	for _, want := range []string{"Order: received", "Order: approved"} {
		if !has(built, want) {
			t.Errorf("%q is written and undescribed, and was not reported; got %v", want, built)
		}
	}
	// The two directions are never mixed: a reader acts on them differently.
	if len(d.Planned) != 0 {
		t.Errorf("nothing here is planned-not-built, yet: %v", sideNames(d.Planned))
	}
}

func TestAClassOnlyOneSideKnowsIsReportedAsAWhole(t *testing.T) {
	// A class the model declares and no process carries: planned, and reported once as
	// the class rather than once per member it also does not have.
	withInvoice := authored(orderClass(), Class{
		ID: "c2", Name: "Invoice", Stereotype: StereotypeBusinessObject,
		Attributes: []Attribute{{Name: "no", Type: TypeString, Multiplicity: MultOne}}})
	d := Difference([]*compiler.CompiledProcess{orderWriter(t)}, withInvoice)
	planned := sideNames(d.Planned)
	if !has(planned, "Invoice") {
		t.Fatalf("the unbuilt class is missing; got %v", planned)
	}
	if has(planned, "Invoice: no") {
		t.Error("an unbuilt class also reported its members — one finding, not one per member")
	}
}

// The rule the whole list's credibility rests on. Derivation cannot see these
// (ADR-0301 §2), so a difference in them is a fact about derivation rather than about
// the system — and every one of them would otherwise be on every class, for ever.
func TestWhatDerivationCannotSeeIsNeverAFinding(t *testing.T) {
	d := Difference([]*compiler.CompiledProcess{orderWriter(t)}, authored(orderClass()))
	all := append(append([]DiffFinding{}, d.Planned...), d.Built...)
	for _, f := range all {
		for _, forbidden := range []string{"id", "key", "type", "multiplicity", "final", "documentation"} {
			if f.Name == forbidden {
				t.Errorf("%s reported %q, which derivation can never see", f.Side, f.Name)
			}
		}
	}
	// `id` is the business key and the class declares it; no process writes it, so a
	// naive member comparison would put it on the backlog. It must not be there.
	if has(sideNames(d.Planned), "Order: id") {
		t.Error("the business key was reported as work — it is the one fact derivation cannot produce")
	}
	// And the reading says what it did not look at, where it lists.
	if len(d.Excluded) == 0 {
		t.Fatal("the reading names none of its exclusions; a short list then reads as a clean bill")
	}
	joined := strings.Join(d.Excluded, " ")
	for _, want := range []string{"business key", "type"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the exclusions do not mention %q: %v", want, d.Excluded)
		}
	}
}

// The defect a real model exposed, and the reason this rule exists at all.
//
// A process that writes `= {id: …, nachname: …, …}` writes every field at once. The
// derived class then has no members, and a naive comparison reports every member the
// model declares as "planned, not built" — five rows of work the process demonstrably
// already does. Three inventions are enough for somebody to stop reading the list.
func TestAWholeObjectWriteWithholdsTheMemberComparisonRatherThanInventingWork(t *testing.T) {
	b := compiler.NewBuilder(1, "sales", 1)
	start := b.AddStartEvent()
	task := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	b.AddDataObject("order", "Order", "received", false)
	b.AddDataOutputAssociation(task, "order", mustExpr(t, "amount"), "approved", "")
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	d := Difference([]*compiler.CompiledProcess{cp}, authored(orderClass()))
	for _, f := range d.Planned {
		if f.Kind == KindDiffMember {
			t.Errorf("a member was reported as unbuilt against a whole-object write: %+v", f)
		}
	}
	// The states are still compared: a data state is written on the object, not inside
	// its value, so a whole-object write hides nothing about them.
	if !has(sideNames(d.Planned), "Order: cancelled") {
		t.Errorf("the unreached state went missing with the members: %v", sideNames(d.Planned))
	}
	// And the silence is stated. A reader who does not know the members were skipped
	// reads their absence as agreement.
	joined := strings.Join(d.Excluded, " ")
	if !strings.Contains(joined, "Order") || !strings.Contains(joined, "whole") {
		t.Errorf("the exclusions do not say the members of Order were not compared: %v", d.Excluded)
	}
}

func TestAnEnumerationIsNeverMissingFromTheProcesses(t *testing.T) {
	// An enumeration is machinery of the model — an attribute's type, or the states a
	// lifecycle takes (ADR-0306). No process ever carries one as a data object, so
	// comparing it would put every enumeration on the backlog for ever.
	withEnum := authored(orderClass(), Class{
		ID: "c3", Name: "OrderStatus", Stereotype: StereotypeEnumeration,
		Literals: []string{"received", "approved"}})
	d := Difference([]*compiler.CompiledProcess{orderWriter(t)}, withEnum)
	if has(sideNames(d.Planned), "OrderStatus") {
		t.Error("an enumeration was reported as planned-not-built")
	}
}

func TestAnApplicationThatModelsNothingIsNotABacklog(t *testing.T) {
	// Nothing has been planned, so nothing is missing from the plan. A first-time user
	// would otherwise meet a wall of findings that are only the absence of a document
	// they have not started.
	d := Difference([]*compiler.CompiledProcess{orderWriter(t)}, NewVocabulary(nil))
	if d.Modeled {
		t.Error("an application with no model reports itself as modelled")
	}
	if len(d.Planned)+len(d.Built) != 0 {
		t.Errorf("findings against no model at all: %v / %v", sideNames(d.Planned), sideNames(d.Built))
	}
}

func TestAModelThatMatchesItsProcessesHasNoDifference(t *testing.T) {
	// The case that says the reading is not simply always full: a model describing
	// exactly what the processes do produces nothing on either side.
	exact := Class{
		ID: "c1", Name: "Order", Stereotype: StereotypeBusinessObject, Identity: []string{"id"},
		Attributes: []Attribute{
			{Name: "id", Type: TypeString, Multiplicity: MultOne},
			{Name: "total", Type: TypeNumber, Multiplicity: MultOne},
		},
		Lifecycle: &Lifecycle{
			States:      []LifecycleState{{Name: "received", Initial: true}, {Name: "approved"}},
			Transitions: []LifecycleTransition{{ID: "t1", From: "received", To: "approved"}},
		},
	}
	d := Difference([]*compiler.CompiledProcess{orderWriter(t)}, authored(exact))
	if len(d.Planned)+len(d.Built) != 0 {
		t.Errorf("a model that matches its processes still differed: %v / %v",
			sideNames(d.Planned), sideNames(d.Built))
	}
	if !d.Modeled {
		t.Error("a model exists, and the reading says it does not")
	}
}

func TestEveryFindingSaysWhichDocumentToChange(t *testing.T) {
	// A row is only work if the reader can tell where the work is. The note names the
	// side, so "add it to the model" and "make a process do it" are never confused.
	d := Difference([]*compiler.CompiledProcess{orderWriter(t)}, authored(orderClass()))
	for _, f := range d.Planned {
		if f.Side != SidePlanned {
			t.Errorf("a finding on the planned side is labelled %q", f.Side)
		}
		if strings.TrimSpace(f.Note) == "" {
			t.Errorf("%+v has no note, so nothing says what it means", f)
		}
	}
	for _, f := range d.Built {
		if f.Side != SideBuilt {
			t.Errorf("a finding on the built side is labelled %q", f.Side)
		}
	}
}

func TestTheReadingIsStableAcrossTwoCalls(t *testing.T) {
	// Stateless and ordered: nothing is remembered between readings — which is why the
	// identity ADR-0301 §4 asked for is not needed — and two readings of the same
	// inputs are the same list rather than the same set in a new order.
	cps := []*compiler.CompiledProcess{orderWriter(t)}
	a := Difference(cps, authored(orderClass()))
	b := Difference(cps, authored(orderClass()))
	if strings.Join(sideNames(a.Planned), "|") != strings.Join(sideNames(b.Planned), "|") {
		t.Errorf("two readings differ:\n %v\n %v", sideNames(a.Planned), sideNames(b.Planned))
	}
}
