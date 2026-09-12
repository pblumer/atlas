package infomodel

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
)

// mustExpr compiles a FEEL expression for a fixture association.
func mustExpr(t *testing.T, src string) *expr.Compiled {
	t.Helper()
	c, err := expr.CompileAuto(src)
	if err != nil {
		t.Fatalf("compile %q: %v", src, err)
	}
	return c
}

// salesVocabulary is the orderModel fixture as a resolved vocabulary.
func salesVocabulary(t *testing.T) *Vocabulary {
	t.Helper()
	m := orderModel()
	if res := Validate(m); !res.Valid {
		t.Fatalf("fixture model is invalid: %v", findingCodes(res))
	}
	return NewVocabulary([]Model{m})
}

// findingsFor runs the check and indexes the problems by rule.
func problemsByRule(ps []compiler.Problem) map[string][]compiler.Problem {
	out := map[string][]compiler.Problem{}
	for _, p := range ps {
		out[p.Rule] = append(out[p.Rule], p)
	}
	return out
}

// writerProcess builds Start → write → read → End over one data object, with the
// write's member target and the object's declared type as given.
func writerProcess(t *testing.T, itemType, targetPath string) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(1, "sales", 1)
	start := b.AddStartEvent()
	write := b.AddTask()
	read := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, write)
	b.Connect(write, read)
	b.Connect(read, end)
	b.AddDataObject("order", itemType, "received", false)
	b.AddDataOutputAssociation(write, "order", mustExpr(t, "amount"), "approved", targetPath)
	b.AddDataInputAssociation(read, "order", "orderCopy", nil)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// TestCheckDataFlowCleanModel is the baseline: a data object whose declared type
// is modeled, written through a member the class actually has, and read after it
// is written, produces nothing.
func TestCheckDataFlowCleanModel(t *testing.T) {
	ps := CheckDataFlow(writerProcess(t, "Order", "id"), salesVocabulary(t))
	if len(ps) != 0 {
		t.Errorf("a clean model produced %d problems: %+v", len(ps), ps)
	}
}

// TestCheckDataFlowResolvesItemSubjectRef is the point of the whole slice: the
// type slot BPMN leaves opaque now resolves, and a name nothing models is said so
// rather than passing silently.
func TestCheckDataFlowResolvesItemSubjectRef(t *testing.T) {
	ps := CheckDataFlow(writerProcess(t, "Ordr", ""), salesVocabulary(t))
	byRule := problemsByRule(ps)
	found := byRule[RuleDataUnresolvedType]
	if len(found) != 1 {
		t.Fatalf("unresolved type produced %+v", ps)
	}
	if found[0].Severity != compiler.SeverityWarning {
		t.Errorf("severity = %q, want warning — a type modeled later must not block a deploy", found[0].Severity)
	}
	if !strings.Contains(found[0].Message, "Ordr") || !strings.Contains(found[0].Message, "order") {
		t.Errorf("message %q names neither the type nor the data object", found[0].Message)
	}

	// An object with no declared type at all, in an application that does model its
	// data, is the same gap said differently.
	untyped := CheckDataFlow(writerProcess(t, "", ""), salesVocabulary(t))
	if len(problemsByRule(untyped)[RuleDataUntyped]) != 1 {
		t.Errorf("an untyped object in a modeled application produced %+v", untyped)
	}
	// With no information model at all there is nothing to resolve against and
	// nothing to say — this must not fire on every process in an instance that has
	// not started modeling.
	if got := CheckDataFlow(writerProcess(t, "", ""), NewVocabulary(nil)); len(got) != 0 {
		t.Errorf("an unmodeled application produced %+v", got)
	}
}

// TestCheckDataFlowMemberWrites is ADR-0060's named follow-up: a write that targets
// one member of a structured object is checked against the class that object is.
func TestCheckDataFlowMemberWrites(t *testing.T) {
	tests := []struct {
		name, path, wantRule string
	}{
		{"a member the class has", "id", ""},
		{"a member it does not", "amount", RuleDataUnknownMember},
		{"a member of a member", "shipTo.city", ""},
		{"a member of a member it does not have", "shipTo.zip", RuleDataUnknownMember},
		{"a path through a scalar", "id.length", RuleDataMemberThroughScalar},
		{"a path through an enumeration", "status.code", RuleDataMemberThroughScalar},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ps := CheckDataFlow(writerProcess(t, "Order", tt.path), salesVocabulary(t))
			byRule := problemsByRule(ps)
			if tt.wantRule == "" {
				if len(ps) != 0 {
					t.Fatalf("expected no problems, got %+v", ps)
				}
				return
			}
			found := byRule[tt.wantRule]
			if len(found) != 1 {
				t.Fatalf("expected one %s, got %+v", tt.wantRule, ps)
			}
			if found[0].Severity != compiler.SeverityError {
				t.Errorf("severity = %q, want error — this write cannot do what it says", found[0].Severity)
			}
			if !strings.Contains(found[0].Message, tt.path) {
				t.Errorf("message %q does not name the path it could not resolve", found[0].Message)
			}
		})
	}
}

// TestCheckDataFlowMemberThroughCollection covers the path ADR-0060 leaves for
// later: a dotted target that walks *into* a list writes the member of an object,
// not of each element, so it does not mean what it looks like.
func TestCheckDataFlowMemberThroughCollection(t *testing.T) {
	m := orderModel()
	m.Classes[1].Attributes = append(m.Classes[1].Attributes,
		Attribute{Name: "tags", Type: TypeString, Multiplicity: MultMany})
	m.Classes[1].Attributes = append(m.Classes[1].Attributes,
		Attribute{Name: "addresses", Type: "Address", Multiplicity: MultMany})
	vocab := NewVocabulary([]Model{m})

	ps := CheckDataFlow(writerProcess(t, "Order", "addresses.city"), vocab)
	found := problemsByRule(ps)[RuleDataMemberThroughCollection]
	if len(found) != 1 {
		t.Fatalf("expected a collection-path finding, got %+v", ps)
	}
	if found[0].Severity != compiler.SeverityWarning {
		t.Errorf("severity = %q, want warning — it is legal, it just does not index", found[0].Severity)
	}
}

// TestCheckDataFlowReadWithNoWriter is ADR-0053's headline example: "task Approve
// reads order but no upstream element produces it".
func TestCheckDataFlowReadWithNoWriter(t *testing.T) {
	b := compiler.NewBuilder(1, "sales", 1)
	start := b.AddStartEvent()
	read := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, read)
	b.Connect(read, end)
	b.AddDataObject("order", "Order", "received", false)
	b.AddDataInputAssociation(read, "order", "orderCopy", nil)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ps := CheckDataFlow(cp, salesVocabulary(t))
	found := problemsByRule(ps)[RuleDataNeverWritten]
	if len(found) != 1 {
		t.Fatalf("expected a never-written finding, got %+v", ps)
	}
	if !strings.Contains(found[0].Message, "order") {
		t.Errorf("message %q does not name the object", found[0].Message)
	}
	if found[0].Severity != compiler.SeverityWarning {
		t.Errorf("severity = %q, want warning — the read yields null, which may be intended", found[0].Severity)
	}
}

// TestCheckDataFlowReadBeforeWrite covers the harder half: the object *is* written
// somewhere, but not on any path that reaches this reader. A parallel split is the
// case that produces it, and the one a snapshot of the model cannot show.
func TestCheckDataFlowReadBeforeWrite(t *testing.T) {
	b := compiler.NewBuilder(1, "sales", 1)
	start := b.AddStartEvent()
	fork := b.AddParallelGateway()
	write := b.AddTask()
	read := b.AddTask()
	join := b.AddParallelGateway()
	end := b.AddEndEvent()
	b.Connect(start, fork)
	b.Connect(fork, write)
	b.Connect(fork, read)
	b.Connect(write, join)
	b.Connect(read, join)
	b.Connect(join, end)
	b.AddDataObject("order", "Order", "received", false)
	b.AddDataOutputAssociation(write, "order", mustExpr(t, "amount"), "approved", "")
	b.AddDataInputAssociation(read, "order", "orderCopy", nil)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ps := CheckDataFlow(cp, salesVocabulary(t))
	found := problemsByRule(ps)[RuleDataReadBeforeWrite]
	if len(found) != 1 {
		t.Fatalf("expected a read-before-write finding, got %+v", ps)
	}
	if !strings.Contains(found[0].Message, "order") {
		t.Errorf("message %q does not name the object", found[0].Message)
	}
	// And it must NOT also claim nothing writes it — something does.
	if len(problemsByRule(ps)[RuleDataNeverWritten]) != 0 {
		t.Errorf("both findings were raised for one object: %+v", ps)
	}
}

// TestCheckDataFlowSelfWriteDoesNotCount covers the ordering that makes the check
// worth trusting: an activity's own output association runs when it *completes*,
// so it does not satisfy the read its own input association makes on activation.
func TestCheckDataFlowSelfWriteDoesNotCount(t *testing.T) {
	b := compiler.NewBuilder(1, "sales", 1)
	start := b.AddStartEvent()
	both := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, both)
	b.Connect(both, end)
	b.AddDataObject("order", "Order", "received", false)
	b.AddDataInputAssociation(both, "order", "orderCopy", nil)
	b.AddDataOutputAssociation(both, "order", mustExpr(t, "amount"), "approved", "")
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(problemsByRule(CheckDataFlow(cp, salesVocabulary(t)))[RuleDataReadBeforeWrite]) != 1 {
		t.Error("an activity reading what it will only write on completion was accepted")
	}
}

// TestCheckDataFlowLoopWriterCounts is the other side of the same ordering: a
// writer that reaches the reader only round the back of a loop *does* precede it,
// from the second round on, so flagging it would be a false alarm on a correct model.
func TestCheckDataFlowLoopWriterCounts(t *testing.T) {
	b := compiler.NewBuilder(1, "sales", 1)
	start := b.AddStartEvent()
	write := b.AddTask()
	read := b.AddTask()
	gw := b.AddExclusiveGateway()
	end := b.AddEndEvent()
	b.Connect(start, write)
	b.Connect(write, read)
	b.Connect(read, gw)
	b.Connect(gw, write) // back edge
	b.Connect(gw, end)
	b.AddDataObject("order", "Order", "received", false)
	b.AddDataOutputAssociation(write, "order", mustExpr(t, "amount"), "approved", "")
	b.AddDataInputAssociation(read, "order", "orderCopy", nil)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if ps := CheckDataFlow(cp, salesVocabulary(t)); len(ps) != 0 {
		t.Errorf("a loop whose writer precedes its reader was flagged: %+v", ps)
	}
}

// TestCheckDataFlowWithoutAVocabularyStillChecksFlow pins the split: reachability
// is a property of the process alone, so it is reported whether or not anybody has
// modeled the data. Only the type checks need a vocabulary.
func TestCheckDataFlowWithoutAVocabularyStillChecksFlow(t *testing.T) {
	b := compiler.NewBuilder(1, "sales", 1)
	start := b.AddStartEvent()
	read := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, read)
	b.Connect(read, end)
	b.AddDataObject("order", "", "", false)
	b.AddDataInputAssociation(read, "order", "orderCopy", nil)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ps := CheckDataFlow(cp, NewVocabulary(nil))
	if len(problemsByRule(ps)[RuleDataNeverWritten]) != 1 {
		t.Errorf("flow checking needs no vocabulary, got %+v", ps)
	}
}

// TestCheckDataFlowNilProcess covers the defensive call: a caller with no compiled
// process gets no findings rather than a panic.
func TestCheckDataFlowNilProcess(t *testing.T) {
	if ps := CheckDataFlow(nil, salesVocabulary(t)); len(ps) != 0 {
		t.Errorf("a nil process produced %+v", ps)
	}
}

// memberWriteBPMN writes a member the class does not have, from a task with a
// BPMN id — so the finding has a real element to anchor to.
const memberWriteBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="sales" isExecutable="true">
    <dataObject id="DO_order" name="order" itemSubjectRef="Order"/>
    <dataObjectReference id="Ref_w" name="order" dataObjectRef="DO_order"/>
    <startEvent id="Start_1"/>
    <task id="Approve_1" name="Approve">
      <dataOutputAssociation id="doa"><targetRef>Ref_w</targetRef>
        <assignment><from>= 1</from><to>amount</to></assignment></dataOutputAssociation>
    </task>
    <endEvent id="End_1"/>
    <sequenceFlow id="f1" sourceRef="Start_1" targetRef="Approve_1"/>
    <sequenceFlow id="f2" sourceRef="Approve_1" targetRef="End_1"/>
  </process>
</definitions>`

// TestCheckDataFlowAnchorsToTheElement pins that a finding names the activity that
// carries the association. Without it the Problems panel can only print a list a
// modeler has to search by hand, which is the thing a panel exists to replace.
func TestCheckDataFlowAnchorsToTheElement(t *testing.T) {
	cp, err := compiler.Parse(1, 1, strings.NewReader(memberWriteBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ps := CheckDataFlow(cp, salesVocabulary(t))
	found := problemsByRule(ps)[RuleDataUnknownMember]
	if len(found) != 1 {
		t.Fatalf("expected one unknown-member finding, got %+v", ps)
	}
	if found[0].Element != "Approve_1" {
		t.Errorf("Element = %q, want Approve_1 — the task carrying the write", found[0].Element)
	}
}

// storeProcess builds a process that names one data store.
func storeProcess(t *testing.T, storeName string) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(1, "sales", 1)
	start := b.AddStartEvent()
	end := b.AddEndEvent()
	b.Connect(start, end)
	b.AddDataStore(storeName, "Ref_store")
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// TestCheckDataFlowResolvesDataStores covers the claim a process makes when it draws
// a data store: that something outside it keeps this class. The store is declared
// once for the application, so this is where the process's claim meets it.
func TestCheckDataFlowResolvesDataStores(t *testing.T) {
	m := orderModel()
	m.Stores = []DataStore{{ID: "s1", Name: "Orders", Class: "Order", Worker: "clio-main", Mode: StoreModeRead}}
	if res := Validate(m); !res.Valid {
		t.Fatalf("fixture is invalid: %v", findingCodes(res))
	}
	vocab := NewVocabulary([]Model{m})

	if ps := CheckDataFlow(storeProcess(t, "Orders"), vocab); len(ps) != 0 {
		t.Errorf("a declared, wired store produced %+v", ps)
	}

	ps := CheckDataFlow(storeProcess(t, "Invoices"), vocab)
	found := problemsByRule(ps)[RuleDataUnknownStore]
	if len(found) != 1 {
		t.Fatalf("an undeclared store produced %+v", ps)
	}
	if found[0].Severity != compiler.SeverityWarning {
		t.Errorf("severity = %q, want warning — a store modelled later must not block a deploy", found[0].Severity)
	}
	if found[0].Element != "Ref_store" || !strings.Contains(found[0].Message, "Invoices") {
		t.Errorf("finding = %+v, want it anchored and naming the store", found[0])
	}
}

// TestCheckDataFlowStoreWithoutAWorker covers the second claim: a store may be
// modelled before anybody wires it, and a deploy says so rather than letting the
// first read find out.
func TestCheckDataFlowStoreWithoutAWorker(t *testing.T) {
	m := orderModel()
	m.Stores = []DataStore{{ID: "s1", Name: "Orders", Class: "Order", Mode: StoreModeRead}}
	if res := Validate(m); !res.Valid {
		t.Fatalf("fixture is invalid: %v", findingCodes(res))
	}
	ps := CheckDataFlow(storeProcess(t, "Orders"), NewVocabulary([]Model{m}))
	found := problemsByRule(ps)[RuleDataStoreUnbound]
	if len(found) != 1 {
		t.Fatalf("an unwired store produced %+v", ps)
	}
	if !strings.Contains(found[0].Message, "Order") {
		t.Errorf("message %q does not name the class the store holds", found[0].Message)
	}
}

// TestCheckDataFlowStoresNeedAVocabulary pins the same silence the type checks keep:
// an application that models nothing is not told its stores are undeclared, because
// there is nothing to declare them in yet.
func TestCheckDataFlowStoresNeedAVocabulary(t *testing.T) {
	if ps := CheckDataFlow(storeProcess(t, "Orders"), NewVocabulary(nil)); len(ps) != 0 {
		t.Errorf("an unmodelled application produced %+v", ps)
	}
}

// --- lifecycles (ADR-0259) -------------------------------------------------
//
// The data state a process writes resolves against the class's lifecycle exactly as
// its itemSubjectRef resolves against a class. Both checks are warnings and neither
// refuses a deploy: a model is routinely drawn before the vocabulary it names exists.

// lifecycleVocabulary is the sales model with a lifecycle on Order.
func lifecycleVocabulary(t *testing.T, lc *Lifecycle) *Vocabulary {
	t.Helper()
	m := orderModel()
	for i := range m.Classes {
		if m.Classes[i].Name == "Order" {
			m.Classes[i].Lifecycle = lc
		}
	}
	if res := Validate(m); !res.Valid {
		t.Fatalf("fixture model is invalid: %v", findingCodes(res))
	}
	return NewVocabulary([]Model{m})
}

// The fixture seeds `order` in "received" and writes it to "approved".
func receivedToApproved() *Lifecycle {
	return &Lifecycle{
		States: []LifecycleState{
			{Name: "received", Initial: true}, {Name: "approved"}, {Name: "shipped", Final: true},
		},
		Transitions: []LifecycleTransition{
			{ID: "t1", From: "received", To: "approved"},
			{ID: "t2", From: "approved", To: "shipped"},
		},
	}
}

func TestCheckDataFlowLifecycleClean(t *testing.T) {
	ps := CheckDataFlow(writerProcess(t, "Order", ""), lifecycleVocabulary(t, receivedToApproved()))
	by := problemsByRule(ps)
	for _, rule := range []string{RuleDataUnknownState, RuleDataIllegalTransition} {
		if len(by[rule]) != 0 {
			t.Errorf("%s on a lifecycle the process obeys: %+v", rule, by[rule])
		}
	}
}

// A class with no lifecycle is the normal case: the data state stays the free string
// BPMN made it, and nothing is said about it.
func TestCheckDataFlowWithoutALifecycleSaysNothingAboutStates(t *testing.T) {
	ps := CheckDataFlow(writerProcess(t, "Order", ""), salesVocabulary(t))
	by := problemsByRule(ps)
	if len(by[RuleDataUnknownState])+len(by[RuleDataIllegalTransition]) != 0 {
		t.Errorf("a class with no lifecycle drew a state finding: %+v", ps)
	}
}

// The typo check, and the one that will find something on the first real application.
func TestCheckDataFlowUnknownState(t *testing.T) {
	lc := receivedToApproved()
	lc.States[1].Name = "Approved" // the process writes "approved"
	lc.Transitions[0].To, lc.Transitions[1].From = "Approved", "Approved"
	ps := CheckDataFlow(writerProcess(t, "Order", ""), lifecycleVocabulary(t, lc))
	by := problemsByRule(ps)
	if len(by[RuleDataUnknownState]) != 1 {
		t.Fatalf("want one unknown-state, got %+v", by[RuleDataUnknownState])
	}
	p := by[RuleDataUnknownState][0]
	if p.Severity != compiler.SeverityWarning {
		t.Errorf("severity = %v, want a warning — a lifecycle is drawn after the process", p.Severity)
	}
	// It has to name the state, or the reader is left diffing two lists by eye.
	if !strings.Contains(p.Message, "approved") || !strings.Contains(p.Message, "Order") {
		t.Errorf("message names neither the state nor the class: %q", p.Message)
	}
}

// The object's seeded state is resolved too: an object created in a state the class
// does not declare is the same defect one step earlier.
func TestCheckDataFlowUnknownInitialState(t *testing.T) {
	lc := receivedToApproved()
	lc.States[0].Name = "neu" // the process seeds "received"
	lc.Transitions[0].From = "neu"
	ps := CheckDataFlow(writerProcess(t, "Order", ""), lifecycleVocabulary(t, lc))
	found := false
	for _, p := range problemsByRule(ps)[RuleDataUnknownState] {
		if strings.Contains(p.Message, "received") {
			found = true
		}
	}
	if !found {
		t.Errorf("the seeded state was not resolved: %+v", ps)
	}
}

// A move the lifecycle does not allow, where it does declare both ends.
func TestCheckDataFlowIllegalTransition(t *testing.T) {
	lc := receivedToApproved()
	lc.Transitions = []LifecycleTransition{{ID: "t2", From: "approved", To: "shipped"}}
	ps := CheckDataFlow(writerProcess(t, "Order", ""), lifecycleVocabulary(t, lc))
	by := problemsByRule(ps)
	if len(by[RuleDataIllegalTransition]) != 1 {
		t.Fatalf("want one illegal-transition, got %+v", by[RuleDataIllegalTransition])
	}
	p := by[RuleDataIllegalTransition][0]
	if p.Severity != compiler.SeverityWarning {
		t.Errorf("severity = %v, want a warning", p.Severity)
	}
	if !strings.Contains(p.Message, "received") || !strings.Contains(p.Message, "approved") {
		t.Errorf("the message names neither end of the move it refuses: %q", p.Message)
	}
}

// A state the lifecycle does not declare is reported once, as unknown — not twice,
// with a second complaint that no transition reaches the thing that is not there.
func TestCheckDataFlowUnknownStateIsNotAlsoAnIllegalTransition(t *testing.T) {
	lc := receivedToApproved()
	lc.States = lc.States[:1] // only "received" survives
	lc.Transitions = nil
	by := problemsByRule(CheckDataFlow(writerProcess(t, "Order", ""), lifecycleVocabulary(t, lc)))
	if len(by[RuleDataIllegalTransition]) != 0 {
		t.Errorf("an undeclared state was reported twice: %+v", by[RuleDataIllegalTransition])
	}
}

// TestCheckDataFlowChecksEveryWriteOnOneArrow is the guard that matters most for the
// several-writes-per-arrow shape: the check must not stop at the first assignment.
// One arrow with a good member and a bad one is a deploy that has to fail, and the
// message has to name the member that is wrong rather than the arrow that carries it
// (ADR-draft-a-write-arrow-may-set-several-members, ADR-0230 for the class).
func TestCheckDataFlowChecksEveryWriteOnOneArrow(t *testing.T) {
	b := compiler.NewBuilder(1, "sales", 1)
	start := b.AddStartEvent()
	write := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, write)
	b.Connect(write, end)
	b.AddDataObject("order", "Order", "received", false)
	b.AddDataOutputAssociationWrites(write, "order", "approved", []compiler.DataWrite{
		{Value: mustExpr(t, "amount"), TargetPath: "id"},          // the class has it
		{Value: mustExpr(t, "amount"), TargetPath: "shipTo.city"}, // and this one
		{Value: mustExpr(t, "amount"), TargetPath: "nmae"},        // and not this one
	})
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	found := problemsByRule(CheckDataFlow(cp, salesVocabulary(t)))[RuleDataUnknownMember]
	if len(found) != 1 {
		t.Fatalf("expected one unknown-member problem, got %+v", CheckDataFlow(cp, salesVocabulary(t)))
	}
	if !strings.Contains(found[0].Message, "nmae") {
		t.Errorf("message %q does not name the member that is wrong", found[0].Message)
	}
}
