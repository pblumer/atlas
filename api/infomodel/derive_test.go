package infomodel

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// Deriving the information model from the processes that use it
// (ADR-0301, §1 and §2).
//
// The derived model is what is *built*: read off the data objects, the writes and the
// graph. What a person models by hand is a different statement — a target, not yet
// reality — so these tests are never about the two agreeing. They are about the
// derived half being read correctly, and about it saying plainly what it could not
// read, which is the half that decides whether it is honest at all.

// classNamedIn reads one derived class out of a model, or fails.
func classNamedIn(t *testing.T, d Derivation, name string) Class {
	t.Helper()
	for _, c := range d.Model.Classes {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no derived class %q; got %v", name, classNames(d))
	return Class{}
}

func classNames(d Derivation) []string {
	out := []string{}
	for _, c := range d.Model.Classes {
		out = append(out, c.Name)
	}
	return out
}

func attrNames(c Class) []string {
	out := []string{}
	for _, a := range c.Attributes {
		out = append(out, a.Name)
	}
	return out
}

func gapsOfKind(d Derivation, kind string) []Gap {
	out := []Gap{}
	for _, g := range d.Gaps {
		if g.Kind == kind {
			out = append(out, g)
		}
	}
	return out
}

// orderProcess writes an order through two members and two states — the shape almost
// every real process has, and everything §1 says can be read.
func orderProcess(t *testing.T) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(1, "sales", 1)
	start := b.AddStartEvent()
	take := b.AddTask()
	approve := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, take)
	b.Connect(take, approve)
	b.Connect(approve, end)
	b.AddDataObject("order", "Order", "received", false)
	b.AddDataOutputAssociation(take, "order", mustExpr(t, "amount"), "received", "id")
	b.AddDataOutputAssociation(approve, "order", mustExpr(t, "amount"), "approved", "total")
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

func TestDerivingAClassFromTheTypeADataObjectDeclares(t *testing.T) {
	d := Derive([]*compiler.CompiledProcess{orderProcess(t)})
	c := classNamedIn(t, d, "Order")
	if c.Stereotype != StereotypeBusinessObject {
		t.Errorf("stereotype = %q, want a business object", c.Stereotype)
	}
	// The members are the paths the writes target, and nothing else: a write of the
	// whole value says nothing about what is inside it.
	if got := attrNames(c); len(got) != 2 || got[0] != "id" || got[1] != "total" {
		t.Errorf("attributes = %v, want [id total]", got)
	}
}

func TestAWriteOfTheWholeValueTeachesNothingAboutItsMembers(t *testing.T) {
	b := compiler.NewBuilder(1, "sales", 1)
	start := b.AddStartEvent()
	task := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	b.AddDataObject("order", "Order", "received", false)
	b.AddDataOutputAssociation(task, "order", mustExpr(t, "amount"), "approved", "") // no path
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	d := Derive([]*compiler.CompiledProcess{cp})
	c := classNamedIn(t, d, "Order")
	if len(c.Attributes) != 0 {
		t.Errorf("attributes = %v, want none — the write replaced the value, it did not name a member", attrNames(c))
	}
	// The class is still worth having: it exists, it is handled, and it has states.
	if len(c.Lifecycle.States) == 0 {
		t.Error("a class with no readable members still has the states it moves through")
	}
}

func TestAClassWithNoDeclaredTypeIsNamedAfterItsObjectAndSaysSo(t *testing.T) {
	// The commonest real case, and the one most likely to be named wrongly: the object
	// is `identitaet` and the class a person would write is `Identitaet`.
	b := compiler.NewBuilder(1, "sales", 1)
	start := b.AddStartEvent()
	task := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	b.AddDataObject("identitaet", "", "ERFASST", false)
	b.AddDataOutputAssociation(task, "identitaet", mustExpr(t, "amount"), "AKTIV", "nachname")
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	d := Derive([]*compiler.CompiledProcess{cp})
	classNamedIn(t, d, "identitaet") // named after the object, because nothing else names it
	named := gapsOfKind(d, GapNamedAfterObject)
	if len(named) != 1 || named[0].Class != "identitaet" {
		t.Fatalf("named-after-object gaps = %+v, want one for identitaet", named)
	}
	if !strings.Contains(named[0].Note, "itemSubjectRef") {
		t.Errorf("the note does not say what would fix it: %q", named[0].Note)
	}
	// A class that *does* declare its type says nothing of the kind.
	d = Derive([]*compiler.CompiledProcess{orderProcess(t)})
	if got := gapsOfKind(d, GapNamedAfterObject); len(got) != 0 {
		t.Errorf("a declared type produced %+v", got)
	}
}

func TestADottedPathSaysAMemberIsStructuredAndNotWhatItIs(t *testing.T) {
	// `customer.name` says an Order has a customer with members. Nothing in BPMN names
	// the class that customer is — so the attribute is derived and its type is not.
	b := compiler.NewBuilder(1, "sales", 1)
	start := b.AddStartEvent()
	task := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	b.AddDataObject("order", "Order", "received", false)
	b.AddDataOutputAssociation(task, "order", mustExpr(t, "amount"), "approved", "customer.name")
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	d := Derive([]*compiler.CompiledProcess{cp})
	c := classNamedIn(t, d, "Order")
	if got := attrNames(c); len(got) != 1 || got[0] != "customer" {
		t.Errorf("attributes = %v, want [customer] — the first segment, which is the member of this class", got)
	}
	structured := gapsOfKind(d, GapStructuredMember)
	if len(structured) != 1 || structured[0].Class != "Order" {
		t.Fatalf("structured-member gaps = %+v, want one for Order", structured)
	}
	if !strings.Contains(structured[0].Note, "customer") {
		t.Errorf("the note does not name the member: %q", structured[0].Note)
	}
}

func TestTheStatesAndWhereInstancesStartAreRead(t *testing.T) {
	d := Derive([]*compiler.CompiledProcess{orderProcess(t)})
	c := classNamedIn(t, d, "Order")
	names := []string{}
	initial := ""
	for _, s := range c.Lifecycle.States {
		names = append(names, s.Name)
		if s.Initial {
			initial = s.Name
		}
	}
	if len(names) != 2 || names[0] != "received" || names[1] != "approved" {
		t.Errorf("states = %v, want [received approved] in the order they are reached", names)
	}
	if initial != "received" {
		t.Errorf("initial = %q, want received — the state the object is created in", initial)
	}
	// Final is a statement about intent and the graph does not carry one.
	for _, s := range c.Lifecycle.States {
		if s.Final {
			t.Errorf("%s: final = true, but nothing in a process says a state is final", s.Name)
		}
	}
}

func TestATransitionIsAPairOfWritesTheGraphAllows(t *testing.T) {
	d := Derive([]*compiler.CompiledProcess{orderProcess(t)})
	c := classNamedIn(t, d, "Order")
	if len(c.Lifecycle.Transitions) != 1 {
		t.Fatalf("transitions = %+v, want one", c.Lifecycle.Transitions)
	}
	tr := c.Lifecycle.Transitions[0]
	if tr.From != "received" || tr.To != "approved" {
		t.Errorf("transition = %s → %s, want received → approved", tr.From, tr.To)
	}
}

func TestAWriteThatCannotFollowAnotherIsNotATransition(t *testing.T) {
	// Two branches of a fork, each writing a different state. Neither can reach the
	// other, so neither is a move from the other — reading the pair as a transition
	// would invent a path the process does not have.
	b := compiler.NewBuilder(1, "sales", 1)
	start := b.AddStartEvent()
	fork := b.AddParallelGateway()
	left := b.AddTask()
	right := b.AddTask()
	join := b.AddParallelGateway()
	end := b.AddEndEvent()
	b.Connect(start, fork)
	b.Connect(fork, left)
	b.Connect(fork, right)
	b.Connect(left, join)
	b.Connect(right, join)
	b.Connect(join, end)
	b.AddDataObject("order", "Order", "received", false)
	b.AddDataOutputAssociation(left, "order", mustExpr(t, "amount"), "approved", "")
	b.AddDataOutputAssociation(right, "order", mustExpr(t, "amount"), "rejected", "")
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	d := Derive([]*compiler.CompiledProcess{cp})
	c := classNamedIn(t, d, "Order")
	for _, tr := range c.Lifecycle.Transitions {
		if (tr.From == "approved" && tr.To == "rejected") || (tr.From == "rejected" && tr.To == "approved") {
			t.Errorf("derived %s → %s, but neither branch can reach the other", tr.From, tr.To)
		}
	}
	// Both are still reachable from where the object starts.
	if len(c.Lifecycle.Transitions) != 2 {
		t.Errorf("transitions = %+v, want the two out of received", c.Lifecycle.Transitions)
	}
}

func TestAWriteThatChangesNoStateStillNamesAMember(t *testing.T) {
	// The commonest write of all: fill a field, leave the thing where it is. It teaches
	// the class a member and must not invent a state or a move to go with it.
	b := compiler.NewBuilder(1, "sales", 1)
	start := b.AddStartEvent()
	task := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	b.AddDataObject("order", "Order", "received", false)
	b.AddDataOutputAssociation(task, "order", mustExpr(t, "amount"), "", "total") // no state
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	d := Derive([]*compiler.CompiledProcess{cp})
	c := classNamedIn(t, d, "Order")
	if got := attrNames(c); len(got) != 1 || got[0] != "total" {
		t.Errorf("attributes = %v, want [total]", got)
	}
	if len(c.Lifecycle.States) != 1 || c.Lifecycle.States[0].Name != "received" {
		t.Errorf("states = %+v, want only the one the object is created in", c.Lifecycle.States)
	}
	if len(c.Lifecycle.Transitions) != 0 {
		t.Errorf("transitions = %+v, want none — nothing moved", c.Lifecycle.Transitions)
	}
}

func TestAStructuredMemberIsSaidOnceHoweverOftenItIsWrittenInto(t *testing.T) {
	// Two writes into the same member prove the same one fact. Saying it twice would
	// read as two problems, and the qualifications are only useful while they are few.
	b := compiler.NewBuilder(1, "sales", 1)
	start := b.AddStartEvent()
	first := b.AddTask()
	second := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, first)
	b.Connect(first, second)
	b.Connect(second, end)
	b.AddDataObject("order", "Order", "received", false)
	b.AddDataOutputAssociation(first, "order", mustExpr(t, "amount"), "", "customer.name")
	b.AddDataOutputAssociation(second, "order", mustExpr(t, "amount"), "", "customer.email")
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	d := Derive([]*compiler.CompiledProcess{cp})
	c := classNamedIn(t, d, "Order")
	if got := attrNames(c); len(got) != 1 || got[0] != "customer" {
		t.Errorf("attributes = %v, want [customer] once", got)
	}
	if got := gapsOfKind(d, GapStructuredMember); len(got) != 1 {
		t.Errorf("structured-member gaps = %+v, want one", got)
	}
}

func TestAClassNothingGivesAStateHasNoMachineToDraw(t *testing.T) {
	// A data object nobody puts in a state is still a class — it has members and it is
	// handled. It has no life, and an empty machine drawn for it would be a claim that
	// it has one state, which is not what the processes say.
	b := compiler.NewBuilder(1, "sales", 1)
	start := b.AddStartEvent()
	task := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	b.AddDataObject("notiz", "Notiz", "", false) // no data state anywhere
	b.AddDataOutputAssociation(task, "notiz", mustExpr(t, "amount"), "", "text")
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	d := Derive([]*compiler.CompiledProcess{cp})
	c := classNamedIn(t, d, "Notiz")
	if c.Lifecycle != nil {
		t.Errorf("lifecycle = %+v, want none — no process ever gives it a state", c.Lifecycle)
	}
	if got := attrNames(c); len(got) != 1 || got[0] != "text" {
		t.Errorf("attributes = %v, want [text] — it is still a class", got)
	}
}

func TestWhereNothingDeclaresAStartTheFirstStateReachedStandsIn(t *testing.T) {
	// A data object with no dataState of its own, written through three states. A
	// machine with no start is one the document refuses, so the earliest state anything
	// reached stands in — which is merely the earliest known, not a claim about intent.
	b := compiler.NewBuilder(1, "sales", 1)
	start := b.AddStartEvent()
	draft := b.AddTask()
	review := b.AddTask()
	done := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, draft)
	b.Connect(draft, review)
	b.Connect(review, done)
	b.Connect(done, end)
	b.AddDataObject("antrag", "Antrag", "", false) // created in no state at all
	b.AddDataOutputAssociation(draft, "antrag", mustExpr(t, "amount"), "erfasst", "")
	b.AddDataOutputAssociation(review, "antrag", mustExpr(t, "amount"), "geprueft", "")
	b.AddDataOutputAssociation(done, "antrag", mustExpr(t, "amount"), "entschieden", "")
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	d := Derive([]*compiler.CompiledProcess{cp})
	c := classNamedIn(t, d, "Antrag")
	initial := ""
	for _, st := range c.Lifecycle.States {
		if st.Initial {
			if initial != "" {
				t.Fatalf("two initial states: %q and %q", initial, st.Name)
			}
			initial = st.Name
		}
	}
	if initial != "erfasst" {
		t.Errorf("initial = %q, want erfasst — the first state anything reached", initial)
	}
	// And the moves come out in a settled order, so two readings of the same processes
	// draw the same picture rather than one that looks like it changed.
	got := []string{}
	for _, tr := range c.Lifecycle.Transitions {
		got = append(got, tr.From+"→"+tr.To)
	}
	want := "erfasst→entschieden,erfasst→geprueft,geprueft→entschieden"
	if strings.Join(got, ",") != want {
		t.Errorf("transitions = %v, want %s", got, want)
	}
}

func TestADataObjectWithNeitherANameNorATypeIsNoClass(t *testing.T) {
	b := compiler.NewBuilder(1, "sales", 1)
	start := b.AddStartEvent()
	end := b.AddEndEvent()
	b.Connect(start, end)
	b.AddDataObject("", "", "received", false)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	d := Derive([]*compiler.CompiledProcess{cp})
	if len(d.Model.Classes) != 0 {
		t.Errorf("classes = %v, want none — there is nothing to call it", classNames(d))
	}
}

func TestOneClassIsBuiltFromEveryProcessThatTouchesIt(t *testing.T) {
	// The point of deriving per application rather than per process: an order that one
	// process approves and another cancels is one class with both.
	canceller := stateWriter(t, "Order", "received", "cancelled")
	d := Derive([]*compiler.CompiledProcess{orderProcess(t), canceller})
	c := classNamedIn(t, d, "Order")
	names := map[string]bool{}
	for _, s := range c.Lifecycle.States {
		names[s.Name] = true
	}
	for _, want := range []string{"received", "approved", "cancelled"} {
		if !names[want] {
			t.Errorf("state %q is missing; got %v", want, names)
		}
	}
	if len(d.Model.Classes) != 1 {
		t.Errorf("classes = %v, want one — the same class, not one per process", classNames(d))
	}
}

func TestTwoObjectsInOneProcessDoNotBorrowEachOthersWrites(t *testing.T) {
	// The reading walks every node once per data object, so the association's own
	// object is what decides whose member and whose state a write is. Without that,
	// one process carrying two objects gives each of them the other's whole life —
	// and the picture is wrong in the way hardest to notice, because it looks full.
	b := compiler.NewBuilder(1, "sales", 1)
	start := b.AddStartEvent()
	take := b.AddTask()
	bill := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, take)
	b.Connect(take, bill)
	b.Connect(bill, end)
	b.AddDataObject("order", "Order", "received", false)
	b.AddDataObject("invoice", "Invoice", "issued", false)
	b.AddDataOutputAssociation(take, "order", mustExpr(t, "amount"), "approved", "total")
	b.AddDataOutputAssociation(bill, "invoice", mustExpr(t, "amount"), "paid", "reference")
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	d := Derive([]*compiler.CompiledProcess{cp})
	order := classNamedIn(t, d, "Order")
	invoice := classNamedIn(t, d, "Invoice")
	if got := attrNames(order); len(got) != 1 || got[0] != "total" {
		t.Errorf("Order attributes = %v, want [total] — `reference` is written into the invoice", got)
	}
	if got := attrNames(invoice); len(got) != 1 || got[0] != "reference" {
		t.Errorf("Invoice attributes = %v, want [reference] — `total` is written into the order", got)
	}
	for _, tt := range []struct {
		c    Class
		want []string
	}{{order, []string{"received", "approved"}}, {invoice, []string{"issued", "paid"}}} {
		got := []string{}
		for _, st := range tt.c.Lifecycle.States {
			got = append(got, st.Name)
		}
		if strings.Join(got, ",") != strings.Join(tt.want, ",") {
			t.Errorf("%s states = %v, want %v — each object has its own life", tt.c.Name, got, tt.want)
		}
	}
	// And the moves stay inside one class: `approved → paid` is two things happening
	// in order, not one thing changing state.
	for _, tr := range order.Lifecycle.Transitions {
		if tr.To == "paid" || tr.From == "paid" {
			t.Errorf("the order derived a move through %s → %s, which belongs to the invoice", tr.From, tr.To)
		}
	}
}

func TestEveryDerivedClassIsKeylessAndTheReasonIsStatedOnce(t *testing.T) {
	// The one fact ADR-0230 exists for, and the one derivation can never produce. It is
	// stated once for the reading rather than repeated per class, and every class is
	// keyless in the model itself so nothing downstream can believe otherwise.
	d := Derive([]*compiler.CompiledProcess{orderProcess(t), stateWriter(t, "Invoice", "issued", "paid")})
	if len(d.Model.Classes) != 2 {
		t.Fatalf("classes = %v, want two", classNames(d))
	}
	for _, c := range d.Model.Classes {
		if len(c.Identity) != 0 {
			t.Errorf("%s: identity = %v, want none — nothing in BPMN says which attribute identifies a thing", c.Name, c.Identity)
		}
	}
	keyless := gapsOfKind(d, GapNoBusinessKey)
	if len(keyless) != 1 {
		t.Fatalf("no-business-key gaps = %d, want exactly one for the whole reading", len(keyless))
	}
	if keyless[0].Class != "" {
		t.Errorf("it is a fact about the reading, not about one class: %+v", keyless[0])
	}
}

func TestDerivingFromNothingIsAnEmptyReadingAndNotAnEmptyClaim(t *testing.T) {
	// A nil in the set is a process that could not be read, not a reason to fall over.
	d := Derive([]*compiler.CompiledProcess{nil})
	if len(d.Model.Classes) != 0 || len(d.Gaps) != 0 {
		t.Errorf("a nil process derived %v and %+v", classNames(d), d.Gaps)
	}
	d = Derive(nil)
	if len(d.Model.Classes) != 0 {
		t.Errorf("classes = %v, want none", classNames(d))
	}
	// No classes means nothing to say about business keys either: the sentence exists
	// to qualify a drawing, and there is no drawing.
	if len(d.Gaps) != 0 {
		t.Errorf("gaps = %+v, want none", d.Gaps)
	}
}

func TestDerivedClassesAreLaidOutSoTheDrawingIsReadable(t *testing.T) {
	// The canvas draws what the document says; a model with every class at 0,0 is a
	// pile. Derivation has no author's layout to preserve, so it makes one.
	d := Derive([]*compiler.CompiledProcess{
		orderProcess(t), stateWriter(t, "Invoice", "issued", "paid"), stateWriter(t, "Note", "open"),
	})
	seen := map[[2]float64]bool{}
	for _, c := range d.Model.Classes {
		at := [2]float64{c.X, c.Y}
		if seen[at] {
			t.Errorf("%s sits on top of another class at %v", c.Name, at)
		}
		seen[at] = true
	}
}

func TestADerivedModelDoesNotValidateAndThatIsTheHonestAnswer(t *testing.T) {
	// Validate's rules are about a document somebody saves: an untyped attribute there
	// is an oversight to fix. A derived reading is not a document, and an untyped
	// attribute in it is exactly what is known — the member exists, its type does not
	// follow from a FEEL expression. So it is expected to fail that rule, and the
	// reading is never put through Validate.
	//
	// What must hold is narrower and load-bearing: the *lifecycle* rules, because a
	// machine with no start, with two, or with a transition to a state that is not
	// there is a drawing the canvas cannot open.
	d := Derive([]*compiler.CompiledProcess{orderProcess(t), stateWriter(t, "Invoice", "issued", "paid")})
	res := Validate(d.Model)

	lifecycleCodes := map[string]bool{
		CodeLifecycleNotAllowed: true, CodeLifecycleEmpty: true, CodeMissingStateName: true,
		CodeDuplicateStateName: true, CodeNoInitialState: true, CodeManyInitialStates: true,
		CodeUnknownTransitionState: true, CodeDuplicateTransitionID: true,
		CodeTransitionLeavesFinalState: true,
	}
	for _, f := range res.Findings {
		if lifecycleCodes[f.Code] {
			t.Errorf("a derived lifecycle breaks the notation: %s — %s", f.Code, f.Message)
		}
		if f.Code != CodeUnknownType && !lifecycleCodes[f.Code] {
			t.Errorf("an unexpected finding on a derived model: %s — %s", f.Code, f.Message)
		}
	}
	// And the untyped attributes are the reason, said once in the reading itself.
	if len(gapsOfKind(d, GapNoAttributeTypes)) != 1 {
		t.Error("the reading does not say its attributes are untyped")
	}
}
