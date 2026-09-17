package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// forkVersions builds two versions of one process for the case ADR-0162's rebinding has
// to refuse: v1 parks a token on a service task, and v2 is restructured enough that
// carrying that token across is not what the operator wants — the work is to be picked
// up again at a *different* element (`resume`), which is what a fork is for.
//
// v1 writes a data object on its way to the parked task, so the fixture also proves the
// thing an operator most cares about: the instance's data crosses, not just its shape.
func forkVersions(t testing.TB) (v1, v2 *compiler.CompiledProcess, parked1, resume2 int32) {
	t.Helper()
	b1 := compiler.NewBuilder(defKey, "forking", 1)
	s1 := b1.AddStartEvent()
	writer := b1.AddTask()
	parked1 = b1.AddServiceTask(jobName, 3)
	e1 := b1.AddEndEvent()
	b1.Connect(s1, writer)
	b1.Connect(writer, parked1)
	b1.Connect(parked1, e1)
	b1.AddDataObject("order", "", "received", false)
	b1.AddDataOutputAssociation(writer, "order", mustCompile(t, "amount"), "approved", "")
	cp1, err := b1.Build()
	if err != nil {
		t.Fatalf("Build v1: %v", err)
	}

	b2 := compiler.NewBuilder(defKey+1, "forking", 2)
	s2 := b2.AddStartEvent()
	resume2 = b2.AddServiceTask("recheck", 3)
	task2 := b2.AddServiceTask(jobName, 3)
	e2 := b2.AddEndEvent()
	b2.Connect(s2, resume2)
	b2.Connect(resume2, task2)
	b2.Connect(task2, e2)
	b2.AddDataObject("order", "", "received", false)
	cp2, err := b2.Build()
	if err != nil {
		t.Fatalf("Build v2: %v", err)
	}
	return cp1, cp2, parked1, resume2
}

// activeInstances is every running instance in the store, keyed by instance key — the
// only way a test learns the key of an instance the engine minted for itself.
func activeInstances(t *testing.T, s *state.Store) map[uint64]model.ProcessInstanceValue {
	t.Helper()
	out := map[uint64]model.ProcessInstanceValue{}
	if err := s.ActiveProcessInstances(func(k uint64, v *model.ProcessInstanceValue) error {
		out[k] = *v
		return nil
	}); err != nil {
		t.Fatalf("ActiveProcessInstances: %v", err)
	}
	return out
}

// successorOf is the one active instance that continues piKey.
func successorOf(t *testing.T, s *state.Store, piKey uint64) (uint64, model.ProcessInstanceValue) {
	t.Helper()
	var (
		key uint64
		val model.ProcessInstanceValue
		n   int
	)
	for k, v := range activeInstances(t, s) {
		if v.PredecessorInstanceKey == piKey {
			key, val, n = k, v, n+1
		}
	}
	if n != 1 {
		t.Fatalf("instances continuing %d = %d, want exactly 1", piKey, n)
	}
	return key, val
}

func operatorActions(t *testing.T, s *state.Store, piKey uint64) []model.OperatorActionValue {
	t.Helper()
	var out []model.OperatorActionValue
	if err := s.OperatorActionHistory(piKey, func(_ int64, _ uint64, v *model.OperatorActionValue) error {
		out = append(out, *v)
		return nil
	}); err != nil {
		t.Fatalf("OperatorActionHistory: %v", err)
	}
	return out
}

// TestForkContinuesInstanceInNewVersion is the point of the record: an instance whose
// token cannot be rebound is ended where it is, and its work continues in a *new*
// instance of the target version — at the element an operator named, carrying the
// instance's variables and data objects, with each record naming the other.
func TestForkContinuesInstanceInNewVersion(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	v1, v2, parked1, resume2 := forkVersions(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(v1)
	p.Deploy(v2)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(v1.Key, model.VariableValue{Name: "amount", Kind: model.VarNumber, Text: "42"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	oldKey := model.NewKey(1, 1)
	if _, ei := parkedElement(t, h.store, oldKey); ei.ElementId != parked1 {
		t.Fatalf("the instance parked at element %d, want %d", ei.ElementId, parked1)
	}

	p.ForkInstance(oldKey, v2.Key, []int32{resume2}, "admin", "v2 replaces the task this token is parked on")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (fork): %v", err)
	}

	// The predecessor ended where it was, and its record says where the work went.
	old, ok, err := h.store.ProcessInstance(oldKey)
	if err != nil || !ok {
		t.Fatalf("ProcessInstance(old): %v (ok=%v)", err, ok)
	}
	if old.State != model.PITerminated {
		t.Errorf("predecessor state = %v, want terminated", old.State)
	}
	newKey, next := successorOf(t, h.store, oldKey)
	if old.SuccessorInstanceKey != newKey {
		t.Errorf("predecessor names successor %d, want %d", old.SuccessorInstanceKey, newKey)
	}
	if n := len(activeInstances(t, h.store)); n != 1 {
		t.Errorf("active instances = %d, want only the successor", n)
	}
	// Nothing of the old execution is left running: its tokens are gone and so is the
	// job its service task had waiting.
	var live int
	if err := h.store.ElementInstancesOfProcess(oldKey, func(uint64) error { live++; return nil }); err != nil {
		t.Fatalf("ElementInstancesOfProcess: %v", err)
	}
	if live != 0 {
		t.Errorf("predecessor still holds %d element instances, want none", live)
	}

	// The successor runs the target version and picked the work up where it was told to.
	if next.ProcessDefKey != v2.Key {
		t.Errorf("successor runs def %d, want %d", next.ProcessDefKey, v2.Key)
	}
	if _, ei := parkedElement(t, h.store, newKey); ei.ElementId != resume2 || ei.ProcessDefKey != v2.Key {
		t.Errorf("successor token is at def %d element %d, want %d/%d",
			ei.ProcessDefKey, ei.ElementId, v2.Key, resume2)
	}

	// The data crossed: root-scope variables and the data object with the value and
	// state the predecessor had reached, not the declaration's initial one.
	var amount *model.VariableValue
	if err := h.store.VariablesOfScope(newKey, func(v *model.VariableValue) error {
		if v.Name == "amount" {
			c := *v
			amount = &c
		}
		return nil
	}); err != nil {
		t.Fatalf("VariablesOfScope: %v", err)
	}
	if amount == nil || amount.Text != "42" {
		t.Errorf("successor variable amount = %+v, want 42 carried across", amount)
	}
	if obj := readDataObject(t, h.store, newKey, "order"); obj == nil || obj.Text != "42" || obj.State != "approved" {
		t.Errorf("successor data object order = %+v, want the predecessor's value and state", obj)
	}

	// Both halves are on the record, with who and why — the predecessor's says the work
	// left, the successor's says it arrived.
	out := operatorActions(t, h.store, oldKey)
	if len(out) != 1 || out[0].Kind != model.OperatorActionForkedTo || out[0].Actor != "admin" ||
		out[0].Reason == "" || out[0].FromProcessDefKey != v1.Key {
		t.Errorf("predecessor audit = %+v, want a forkedTo by admin with its reason", out)
	}
	in := operatorActions(t, h.store, newKey)
	if len(in) != 1 || in[0].Kind != model.OperatorActionForkedFrom || in[0].Actor != "admin" || in[0].Reason == "" {
		t.Errorf("successor audit = %+v, want a forkedFrom by admin with its reason", in)
	}
}

// TestForkSurvivesRecovery is the invariant the design is shaped around: both instances
// and the link between them are rebuilt by replaying the log alone (I4/I6). The
// successor's key is generated at command time and written into the events, so a
// restarted engine agrees about which instance continues which.
func TestForkSurvivesRecovery(t *testing.T) {
	dir := t.TempDir()
	v1, v2, _, resume2 := forkVersions(t)
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(v1)
	p1.Deploy(v2)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(v1.Key, model.VariableValue{Name: "amount", Kind: model.VarNumber, Text: "42"})
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	oldKey := model.NewKey(1, 1)
	p1.ForkInstance(oldKey, v2.Key, []int32{resume2}, "admin", "why")
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (fork): %v", err)
	}
	wantKey, wantNext := successorOf(t, h1.store, oldKey)
	_, wantEI := parkedElement(t, h1.store, wantKey)
	h1.close(t)

	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() {
		_ = store2.Close()
		_ = log2.Close()
	}()
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(v1)
	p2.Deploy(v2)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}

	gotKey, gotNext := successorOf(t, store2, oldKey)
	if gotKey != wantKey {
		t.Errorf("after replay the successor is %d, want %d", gotKey, wantKey)
	}
	if gotNext.ProcessDefKey != wantNext.ProcessDefKey || gotNext.PredecessorInstanceKey != oldKey {
		t.Errorf("replayed successor = %+v, want %+v", gotNext, wantNext)
	}
	old, ok, err := store2.ProcessInstance(oldKey)
	if err != nil || !ok {
		t.Fatalf("ProcessInstance(old) after replay: %v (ok=%v)", err, ok)
	}
	if old.State != model.PITerminated || old.SuccessorInstanceKey != wantKey {
		t.Errorf("replayed predecessor = %+v, want terminated naming %d", *old, wantKey)
	}
	if _, ei := parkedElement(t, store2, gotKey); ei.ElementId != wantEI.ElementId {
		t.Errorf("replayed successor token at element %d, want %d", ei.ElementId, wantEI.ElementId)
	}
}

// TestForkOfAFinishedInstanceIsANoOp is the command arriving too late: the instance it
// names ended between the API's answer and its turn on the run loop. Nothing is written
// — not a successor with nobody's work in it, and not an audit record for something that
// did not happen.
func TestForkOfAFinishedInstanceIsANoOp(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	v1, v2, _, resume2 := forkVersions(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(v1)
	p.Deploy(v2)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(v1.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	oldKey := model.NewKey(1, 1)
	p.CancelInstance(oldKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (cancel): %v", err)
	}

	p.ForkInstance(oldKey, v2.Key, []int32{resume2}, "admin", "too late")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (fork): %v", err)
	}
	if n := len(activeInstances(t, h.store)); n != 0 {
		t.Errorf("active instances = %d, want none — the fork created one for a dead instance", n)
	}
	old, ok, err := h.store.ProcessInstance(oldKey)
	if err != nil || !ok {
		t.Fatalf("ProcessInstance: %v (ok=%v)", err, ok)
	}
	if old.SuccessorInstanceKey != 0 {
		t.Errorf("cancelled instance names successor %d, want none", old.SuccessorInstanceKey)
	}
	if got := operatorActions(t, h.store, oldKey); len(got) != 0 {
		t.Errorf("no-op fork left audit records %+v, want none", got)
	}
}

// TestForkRefusesWhatItCannotDo covers the gate in front of the command: a fork that
// does not hold writes nothing at all, rather than ending an instance and leaving its
// work nowhere. Each case is checked by the state being exactly as it was.
func TestForkRefusesWhatItCannotDo(t *testing.T) {
	v1, v2, _, resume2 := forkVersions(t)
	cases := []struct {
		name   string
		target func() uint64
		resume []int32
	}{
		{"no resume point at all", func() uint64 { return v2.Key }, nil},
		{"the version it is already on", func() uint64 { return v1.Key }, []int32{resume2}},
		{"a target that is not deployed", func() uint64 { return defKey + 99 }, []int32{resume2}},
		{"an element the target version does not have", func() uint64 { return v2.Key }, []int32{999}},
		{"the same resume point twice", func() uint64 { return v2.Key }, []int32{resume2, resume2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := openHarness(t, t.TempDir())
			defer h.close(t)
			p := engine.New(1, h.log, h.store, &manualClock{})
			p.Deploy(v1)
			p.Deploy(v2)
			if err := p.Recover(); err != nil {
				t.Fatalf("Recover: %v", err)
			}
			p.CreateInstance(v1.Key)
			if err := p.RunUntilIdle(); err != nil {
				t.Fatalf("RunUntilIdle: %v", err)
			}
			oldKey := model.NewKey(1, 1)

			p.ForkInstance(oldKey, tc.target(), tc.resume, "admin", "because")
			if err := p.RunUntilIdle(); err != nil {
				t.Fatalf("RunUntilIdle (fork): %v", err)
			}

			live := activeInstances(t, h.store)
			if len(live) != 1 {
				t.Fatalf("active instances = %d, want the untouched original only", len(live))
			}
			if pi, ok := live[oldKey]; !ok || pi.State != model.PIActive || pi.SuccessorInstanceKey != 0 {
				t.Errorf("original instance = %+v (present=%v), want it still running and unlinked", pi, ok)
			}
			if got := operatorActions(t, h.store, oldKey); len(got) != 0 {
				t.Errorf("refused fork left audit records %+v, want none", got)
			}
		})
	}
}
