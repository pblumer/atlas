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

// dataObjectProcess builds Start → ServiceTask → End with one declared data
// object ("order", initial state "received"), so an instance parks on the service
// task with the data object seeded under its scope.
func dataObjectProcess(t testing.TB) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(defKey, "withdata", 1)
	start := b.AddStartEvent()
	task := b.AddServiceTask(jobName, 3)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	b.AddDataObject("order", "OrderType", "received", false)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// readDataObject returns a scope's data object by name, or nil.
func readDataObject(t *testing.T, s *state.Store, scope uint64, name string) *model.DataObjectValue {
	t.Helper()
	var out *model.DataObjectValue
	if err := s.DataObjectsOfScope(scope, func(v *model.DataObjectValue) error {
		if v.Name == name {
			c := *v
			out = &c
		}
		return nil
	}); err != nil {
		t.Fatalf("DataObjectsOfScope: %v", err)
	}
	return out
}

// dataObjectStates returns a scope's data-object state history, oldest first.
func dataObjectStates(t *testing.T, s *state.Store, scope uint64) []string {
	t.Helper()
	var out []string
	if err := s.DataObjectSnapshotHistory(scope, func(_ int64, _ uint64, v *model.DataObjectValue) error {
		out = append(out, v.State)
		return nil
	}); err != nil {
		t.Fatalf("DataObjectSnapshotHistory: %v", err)
	}
	return out
}

// TestDataObjectSeededAndRecovers is the recovery property for data objects
// (ADR-0053): creating an instance of a process that declares a data object seeds
// that object under the instance scope with its declared initial data state, and
// replaying the log into a fresh store rebuilds it identically — the value and
// state come only from the event, never recomputed (invariants I4/I6).
func TestDataObjectSeededAndRecovers(t *testing.T) {
	dir := t.TempDir()
	cp := dataObjectProcess(t)
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	scope := model.NewKey(1, 1) // the first minted key is the process instance
	live := readDataObject(t, h1.store, scope, "order")
	if live == nil {
		t.Fatal("live data object 'order' not seeded")
	}
	if live.State != "received" {
		t.Errorf("live state = %q, want received", live.State)
	}
	if live.ScopeKey != scope {
		t.Errorf("live scope = %d, want %d", live.ScopeKey, scope)
	}
	// The seeding is captured in the event-sourced state history.
	if got := dataObjectStates(t, h1.store, scope); len(got) != 1 || got[0] != "received" {
		t.Errorf("live state history = %v, want [received]", got)
	}
	h1.close(t)

	// Replay the same log into a fresh, empty store.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() {
		if err := store2.Close(); err != nil {
			t.Errorf("store2.Close: %v", err)
		}
		if err := log2.Close(); err != nil {
			t.Errorf("log2.Close: %v", err)
		}
	}()
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	replayed := readDataObject(t, store2, scope, "order")
	if replayed == nil || replayed.State != live.State || replayed.ScopeKey != live.ScopeKey || replayed.Name != live.Name {
		t.Fatalf("replayed data object = %+v, want %+v", replayed, live)
	}
	if got := dataObjectStates(t, store2, scope); len(got) != 1 || got[0] != "received" {
		t.Errorf("replayed state history = %v, want [received]", got)
	}
}

// associationProcess builds Start → Task(writes =amount into order[approved]) → End
// with a data object "order" seeded [received]. The plain task completes on
// activation, so it runs to completion with no worker, exercising the data-output
// association path (ADR-0058).
func associationProcess(t testing.TB) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(defKey, "assoc", 1)
	start := b.AddStartEvent()
	task := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	b.AddDataObject("order", "", "received", false)
	b.AddDataOutputAssociation(task, "order", mustCompile(t, "amount"), "approved")
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// TestDataOutputAssociationWritesAndRecovers is the recovery property for data
// output associations (ADR-0058): a task's association evaluates a FEEL expression
// over the instance's variables, writes the result into a data object, and advances
// the object's data state; replaying the log rebuilds the same value and state — the
// write is frozen into the event, never recomputed (invariants I4/I6).
func TestDataOutputAssociationWritesAndRecovers(t *testing.T) {
	dir := t.TempDir()
	cp := associationProcess(t)
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key, model.VariableValue{Name: "amount", Kind: model.VarNumber, Text: "42"})
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	scope := model.NewKey(1, 1)
	live := readDataObject(t, h1.store, scope, "order")
	if live == nil {
		t.Fatal("data object 'order' missing after run")
	}
	if live.State != "approved" {
		t.Errorf("live state = %q, want approved (association advanced it)", live.State)
	}
	if live.Kind != model.VarNumber || live.Text != "42" {
		t.Errorf("live value = kind %v text %q, want number 42 (from =amount)", live.Kind, live.Text)
	}
	// The whole transition is captured in the state history: seeded [received] then
	// the association's [approved].
	if got := dataObjectStates(t, h1.store, scope); len(got) != 2 || got[0] != "received" || got[1] != "approved" {
		t.Errorf("state history = %v, want [received approved]", got)
	}
	h1.close(t)

	// Replay the same log into a fresh, empty store.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() {
		if err := store2.Close(); err != nil {
			t.Errorf("store2.Close: %v", err)
		}
		if err := log2.Close(); err != nil {
			t.Errorf("log2.Close: %v", err)
		}
	}()
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	replayed := readDataObject(t, store2, scope, "order")
	if replayed == nil || replayed.State != live.State || replayed.Kind != live.Kind || replayed.Text != live.Text {
		t.Fatalf("replayed = %+v, want %+v", replayed, live)
	}
}

// TestDataOutputAssociationStateOnly checks a state-only association (no value
// expression) advances the data state while keeping the object's current value.
func TestDataOutputAssociationStateOnly(t *testing.T) {
	dir := t.TempDir()
	b := compiler.NewBuilder(defKey, "assocstate", 1)
	start := b.AddStartEvent()
	task := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	b.AddDataObject("order", "", "received", false)
	b.AddDataOutputAssociation(task, "order", nil, "validated") // state-only, no value
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	clock := &manualClock{}
	h := openHarness(t, dir)
	defer h.close(t)
	p := engine.New(1, h.log, h.store, clock)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	got := readDataObject(t, h.store, model.NewKey(1, 1), "order")
	if got == nil || got.State != "validated" {
		t.Fatalf("state-only association: got %+v, want state validated", got)
	}
	if got.Kind != model.VarNull {
		t.Errorf("value kind = %v, want null (unset value preserved)", got.Kind)
	}
}

// TestDataOutputAssociationKeepsState covers an association that writes a value but
// names no target data state: the object's value changes while its current data
// state is preserved (ADR-0058).
func TestDataOutputAssociationKeepsState(t *testing.T) {
	dir := t.TempDir()
	b := compiler.NewBuilder(defKey, "assockeep", 1)
	start := b.AddStartEvent()
	task := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	b.AddDataObject("order", "", "received", false)
	b.AddDataOutputAssociation(task, "order", mustCompile(t, "amount"), "") // value, no target state
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	h := openHarness(t, dir)
	defer h.close(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, model.VariableValue{Name: "amount", Kind: model.VarNumber, Text: "7"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	got := readDataObject(t, h.store, model.NewKey(1, 1), "order")
	if got == nil || got.Kind != model.VarNumber || got.Text != "7" {
		t.Fatalf("value = %+v, want number 7", got)
	}
	if got.State != "received" {
		t.Errorf("state = %q, want received (no target state keeps it)", got.State)
	}
}
