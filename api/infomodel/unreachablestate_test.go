package infomodel

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// The third lifecycle check of ADR-0259 §3, and the one that could not join the other
// two: whether a declared state is ever reached is a question about the *application*,
// and CheckDataFlow reads one compiled process. "Nothing ever cancels an order" is
// false until every process has been looked at, so this is asked once over the set.

// cancellableOrder declares a way out that the fixture process never takes.
func cancellableOrder() *Lifecycle {
	return &Lifecycle{
		States: []LifecycleState{
			{Name: "received", Initial: true},
			{Name: "approved"},
			{Name: "cancelled", Final: true},
		},
		Transitions: []LifecycleTransition{
			{ID: "t1", From: "received", To: "approved"},
			{ID: "t2", From: "received", To: "cancelled"},
		},
	}
}

// stateWriter builds a process that seeds `order` in `initial` and writes it into
// each of `targets` in turn, so a test can say exactly which states an application
// reaches.
func stateWriter(t *testing.T, itemType, initial string, targets ...string) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(1, "sales", 1)
	prev := b.AddStartEvent()
	b.AddDataObject("order", itemType, initial, false)
	for _, target := range targets {
		node := b.AddTask()
		b.Connect(prev, node)
		b.AddDataOutputAssociation(node, "order", mustExpr(t, "amount"), target, "")
		prev = node
	}
	end := b.AddEndEvent()
	b.Connect(prev, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// unreachableIn returns the one unreachable-state problem, or fails.
func unreachableIn(t *testing.T, ps []compiler.Problem) compiler.Problem {
	t.Helper()
	var found []compiler.Problem
	for _, p := range ps {
		if p.Rule == RuleDataUnreachableState {
			found = append(found, p)
		}
	}
	if len(found) != 1 {
		t.Fatalf("unreachable-state problems = %d, want 1: %+v", len(found), ps)
	}
	return found[0]
}

func TestAStateNothingWritesIsReported(t *testing.T) {
	vocab := lifecycleVocabulary(t, cancellableOrder())
	// The application handles orders and never cancels one. Either a process is
	// missing or the model is aspirational, and both are worth saying.
	ps := CheckApplication([]*compiler.CompiledProcess{
		stateWriter(t, "Order", "received", "approved"),
	}, vocab)

	p := unreachableIn(t, ps)
	if p.Severity != compiler.SeverityWarning {
		t.Errorf("severity = %v, want a warning — this never refuses a deploy", p.Severity)
	}
	// It is a fact about the model and not about any one element, so it names no
	// element: blaming whichever process happened to be deployed would be wrong.
	if p.Element != "" {
		t.Errorf("element = %q, want empty", p.Element)
	}
	if !strings.Contains(p.Message, "cancelled") || !strings.Contains(p.Message, "Order") {
		t.Errorf("message does not name the class and the state: %q", p.Message)
	}
	for _, reached := range []string{"received", "approved"} {
		if strings.Contains(p.Message, reached) {
			t.Errorf("message names %q, which the application does reach: %q", reached, p.Message)
		}
	}
}

func TestEveryStateReachedSaysNothing(t *testing.T) {
	vocab := lifecycleVocabulary(t, cancellableOrder())
	ps := CheckApplication([]*compiler.CompiledProcess{
		stateWriter(t, "Order", "received", "approved"),
		stateWriter(t, "Order", "received", "cancelled"),
	}, vocab)
	for _, p := range ps {
		if p.Rule == RuleDataUnreachableState {
			t.Errorf("a fully reached lifecycle produced %+v", p)
		}
	}
}

func TestTheAnswerIsTheWholeApplicationAndNotOneProcess(t *testing.T) {
	// This is the entire reason the check could not join the other two. Read against
	// the first process alone, `cancelled` is unreachable; read against the pair, it
	// is not. A per-process check would have to be wrong or repeat itself.
	vocab := lifecycleVocabulary(t, cancellableOrder())
	alone := stateWriter(t, "Order", "received", "approved")
	canceller := stateWriter(t, "Order", "received", "cancelled")

	if ps := CheckApplication([]*compiler.CompiledProcess{alone}, vocab); len(ps) == 0 {
		t.Error("one process that never cancels: want the state reported")
	}
	if ps := CheckApplication([]*compiler.CompiledProcess{alone, canceller}, vocab); len(ps) != 0 {
		t.Errorf("the same process beside one that does cancel: %+v, want nothing", ps)
	}
}

func TestAClassNoProcessHandlesIsNotYetAnswerable(t *testing.T) {
	// A lifecycle drawn before the process that will write it is the normal order of
	// work, not a defect. Reporting every state of it the moment it is drawn would
	// make this the check people turn off.
	vocab := lifecycleVocabulary(t, cancellableOrder())
	ps := CheckApplication([]*compiler.CompiledProcess{
		stateWriter(t, "Invoice", "issued", "paid"), // nothing here is an Order
	}, vocab)
	for _, p := range ps {
		if p.Rule == RuleDataUnreachableState {
			t.Errorf("a class no process handles produced %+v", p)
		}
	}
	// And an application that deploys nothing at all says nothing at all.
	if ps := CheckApplication(nil, vocab); len(ps) != 0 {
		t.Errorf("no processes produced %+v", ps)
	}
}

func TestTheStateInstancesStartInCountsAsReached(t *testing.T) {
	// The seeded state is where every instance begins, so it is written as surely as
	// any output association writes one.
	vocab := lifecycleVocabulary(t, receivedToApproved())
	ps := CheckApplication([]*compiler.CompiledProcess{
		stateWriter(t, "Order", "received", "approved", "shipped"),
	}, vocab)
	for _, p := range ps {
		if p.Rule == RuleDataUnreachableState {
			t.Errorf("the seeded state was counted as unreached: %+v", p)
		}
	}
}

func TestAnObjectWithNoStartingStateLeavesTheFirstStateUnreached(t *testing.T) {
	// The lifecycle says instances begin `received` and the data object carries no
	// data state at all, so nothing ever puts one there. The remedy is one field in
	// the Modeler, which is exactly why it is worth saying.
	vocab := lifecycleVocabulary(t, receivedToApproved())
	ps := CheckApplication([]*compiler.CompiledProcess{
		stateWriter(t, "Order", "", "approved", "shipped"),
	}, vocab)
	p := unreachableIn(t, ps)
	if !strings.Contains(p.Message, "received") {
		t.Errorf("message does not name the unreached starting state: %q", p.Message)
	}
}

func TestSeveralUnreachedStatesAreOneFindingAndNotSeveral(t *testing.T) {
	// One sentence about one class. Three findings for three states would read as
	// three problems, and it is one: this lifecycle is ahead of its processes.
	vocab := lifecycleVocabulary(t, cancellableOrder())
	ps := CheckApplication([]*compiler.CompiledProcess{
		stateWriter(t, "Order", "received"), // reaches only where it starts
	}, vocab)
	p := unreachableIn(t, ps)
	for _, want := range []string{"approved", "cancelled"} {
		if !strings.Contains(p.Message, want) {
			t.Errorf("message does not name %q: %q", want, p.Message)
		}
	}
}

func TestAStateWrittenForAnotherClassDoesNotCount(t *testing.T) {
	// States are matched by string, and two classes may both use "approved". A write
	// against an Invoice says nothing about whether an Order ever gets there.
	vocab := lifecycleVocabulary(t, cancellableOrder())
	ps := CheckApplication([]*compiler.CompiledProcess{
		stateWriter(t, "Order", "received"),
		stateWriter(t, "Invoice", "received", "approved", "cancelled"),
	}, vocab)
	p := unreachableIn(t, ps)
	for _, want := range []string{"approved", "cancelled"} {
		if !strings.Contains(p.Message, want) {
			t.Errorf("another class's write was counted for Order: %q", p.Message)
		}
	}
}

func TestAClassWithNoLifecycleIsSilentHereToo(t *testing.T) {
	// nil is the normal case and stays silent everywhere, this check included.
	ps := CheckApplication([]*compiler.CompiledProcess{
		stateWriter(t, "Order", "received", "approved"),
	}, salesVocabulary(t))
	if len(ps) != 0 {
		t.Errorf("a class with no lifecycle produced %+v", ps)
	}
	// And an application with no information model has nothing to read against.
	if ps := CheckApplication([]*compiler.CompiledProcess{
		stateWriter(t, "Order", "received", "approved"),
	}, NewVocabulary(nil)); len(ps) != 0 {
		t.Errorf("no vocabulary produced %+v", ps)
	}
}

func TestAWriteIsCountedForItsOwnObjectAndNotAnyOther(t *testing.T) {
	// Two objects of different classes in *one* process. Every output association of
	// that process is walked for every object, so a write has to be matched to the
	// object it targets — otherwise the invoice's write would mark "approved" reached
	// for the order too, and the finding this check exists for would vanish.
	b := compiler.NewBuilder(1, "billing", 1)
	start := b.AddStartEvent()
	task := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	b.AddDataObject("order", "Order", "received", false)
	b.AddDataObject("invoice", "Invoice", "received", false)
	b.AddDataOutputAssociation(task, "invoice", mustExpr(t, "amount"), "approved", "")
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ps := CheckApplication([]*compiler.CompiledProcess{cp}, lifecycleVocabulary(t, cancellableOrder()))
	p := unreachableIn(t, ps)
	if !strings.Contains(p.Message, "approved") {
		t.Errorf("the invoice's write was credited to the order: %q", p.Message)
	}
}
