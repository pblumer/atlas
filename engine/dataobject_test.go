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
	b.AddDataOutputAssociation(task, "order", mustCompile(t, "amount"), "approved", "")
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
	b.AddDataOutputAssociation(task, "order", nil, "validated", "") // state-only, no value
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
	b.AddDataOutputAssociation(task, "order", mustCompile(t, "amount"), "", "") // value, no target state
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

// TestDataInputAssociationReadsAndRecovers is the recovery property for data input
// associations (ADR-0059): data flows both ways — an output association writes a
// data object, a later activity's input association reads it (via a FEEL transform
// binding the object under its name) into a variable, and that activity's own FEEL
// uses it. Replaying the log rebuilds the same result; each read/write is frozen
// into its event (invariants I4/I6).
func TestDataInputAssociationReadsAndRecovers(t *testing.T) {
	dir := t.TempDir()
	b := compiler.NewBuilder(defKey, "inout", 1)
	start := b.AddStartEvent()
	write := b.AddTask()                                              // output: order = =amount
	read := b.AddScriptTask(mustCompile(t, "orderIn * 10"), "result") // reads orderIn (set by input assoc)
	end := b.AddEndEvent()
	b.Connect(start, write)
	b.Connect(write, read)
	b.Connect(read, end)
	b.AddDataObject("order", "", "received", false)
	b.AddDataOutputAssociation(write, "order", mustCompile(t, "amount"), "approved", "")
	b.AddDataInputAssociation(read, "order", "orderIn", mustCompile(t, "order")) // bind object → orderIn
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
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
	// orderIn was read from the data object; result = orderIn * 10.
	if got := readVar(t, h1.store, scope, "orderIn"); got == nil || got.Text != "42" {
		t.Fatalf("orderIn = %+v, want number 42 (read from data object)", got)
	}
	live := readVar(t, h1.store, scope, "result")
	if live == nil || live.Text != "420" {
		t.Fatalf("result = %+v, want 420 (orderIn*10)", live)
	}
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
	if replayed := readVar(t, store2, scope, "result"); replayed == nil || replayed.Text != live.Text {
		t.Fatalf("replayed result = %+v, want %+v", replayed, live)
	}
}

// TestDataInputAssociationCopiesValue covers the no-transform path: an input
// association with no <assignment> copies the data object's value verbatim into the
// target variable.
func TestDataInputAssociationCopiesValue(t *testing.T) {
	dir := t.TempDir()
	b := compiler.NewBuilder(defKey, "incopy", 1)
	start := b.AddStartEvent()
	write := b.AddTask()
	read := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, write)
	b.Connect(write, read)
	b.Connect(read, end)
	b.AddDataObject("order", "", "received", false)
	b.AddDataOutputAssociation(write, "order", mustCompile(t, "amount"), "approved", "")
	b.AddDataInputAssociation(read, "order", "orderCopy", nil) // copy verbatim
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
	if got := readVar(t, h.store, model.NewKey(1, 1), "orderCopy"); got == nil || got.Kind != model.VarNumber || got.Text != "7" {
		t.Fatalf("orderCopy = %+v, want number 7 (copied from data object)", got)
	}
}

// TestDataInputAssociationConstantTransform covers a transform that references
// neither a variable nor the object (a constant), so the FEEL scope starts empty
// (nil) before the source object is bound in — the object write still lands the
// constant into the variable (ADR-0059).
func TestDataInputAssociationConstantTransform(t *testing.T) {
	dir := t.TempDir()
	b := compiler.NewBuilder(defKey, "inconst", 1)
	start := b.AddStartEvent()
	write := b.AddTask()
	read := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, write)
	b.Connect(write, read)
	b.Connect(read, end)
	b.AddDataObject("order", "", "received", false)
	b.AddDataOutputAssociation(write, "order", mustCompile(t, "amount"), "approved", "")
	b.AddDataInputAssociation(read, "order", "flag", mustCompile(t, "99")) // constant transform
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
	p.CreateInstance(cp.Key, model.VariableValue{Name: "amount", Kind: model.VarNumber, Text: "1"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if got := readVar(t, h.store, model.NewKey(1, 1), "flag"); got == nil || got.Text != "99" {
		t.Fatalf("flag = %+v, want number 99 (constant transform)", got)
	}
}

// TestDataOutputAssociationFieldWritesAndRecovers is the recovery property for
// field-level writes (ADR-0060): two activities each set one member of the same
// structured data object; the object accrues both fields (creating it on the first
// write), and replaying the log rebuilds the merged canonical JSON identically.
func TestDataOutputAssociationFieldWritesAndRecovers(t *testing.T) {
	dir := t.TempDir()
	b := compiler.NewBuilder(defKey, "fields", 1)
	start := b.AddStartEvent()
	setName := b.AddTask()
	setAmount := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, setName)
	b.Connect(setName, setAmount)
	b.Connect(setAmount, end)
	b.AddDataObject("order", "", "received", false)
	// Each association writes ONE member of order, keeping the rest.
	b.AddDataOutputAssociation(setName, "order", mustCompile(t, "customerName"), "", "name")
	b.AddDataOutputAssociation(setAmount, "order", mustCompile(t, "amt"), "approved", "amount")
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key,
		model.VariableValue{Name: "customerName", Kind: model.VarString, Text: "Acme"},
		model.VariableValue{Name: "amt", Kind: model.VarNumber, Text: "42"})
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	scope := model.NewKey(1, 1)
	live := readDataObject(t, h1.store, scope, "order")
	if live == nil {
		t.Fatal("data object 'order' missing after run")
	}
	// Both fields accrued into one structured object (canonical: keys sorted).
	if live.Kind != model.VarJSON || live.Text != `{"amount":42,"name":"Acme"}` {
		t.Fatalf("order = kind %v text %q, want VarJSON {\"amount\":42,\"name\":\"Acme\"}", live.Kind, live.Text)
	}
	if live.State != "approved" {
		t.Errorf("state = %q, want approved (second write advanced it)", live.State)
	}
	h1.close(t)

	// Replay into a fresh store.
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
	if replayed == nil || replayed.Kind != live.Kind || replayed.Text != live.Text {
		t.Fatalf("replayed = %+v, want %+v", replayed, live)
	}
}

// TestDataOutputAssociationNestedAndOverwrite covers two field-write edge paths
// (ADR-0060): a nested member path creates the intermediate object, and a field
// write onto a value that is currently a scalar (not an object) starts from a fresh
// object rather than corrupting it.
func TestDataOutputAssociationNestedAndOverwrite(t *testing.T) {
	dir := t.TempDir()
	b := compiler.NewBuilder(defKey, "nested", 1)
	start := b.AddStartEvent()
	scalarWrite := b.AddTask() // order = 5 (whole-object scalar)
	nestedWrite := b.AddTask() // order.customer.name = "Acme" (nested field on a scalar)
	end := b.AddEndEvent()
	b.Connect(start, scalarWrite)
	b.Connect(scalarWrite, nestedWrite)
	b.Connect(nestedWrite, end)
	b.AddDataObject("order", "", "", false)
	b.AddDataOutputAssociation(scalarWrite, "order", mustCompile(t, "5"), "", "")                   // whole value = scalar 5
	b.AddDataOutputAssociation(nestedWrite, "order", mustCompile(t, `"Acme"`), "", "customer.name") // nested field
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
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	got := readDataObject(t, h.store, model.NewKey(1, 1), "order")
	// The scalar 5 is discarded (a field write onto a non-object starts fresh); the
	// nested path created the intermediate object.
	if got == nil || got.Kind != model.VarJSON || got.Text != `{"customer":{"name":"Acme"}}` {
		t.Fatalf("order = %+v, want VarJSON {\"customer\":{\"name\":\"Acme\"}}", got)
	}
}

// TestDataObjectWriteAttribution is the attribution property for data objects: the
// element instance whose processing wrote a value is recorded on the event, so the
// console can say *who wrote this* rather than diffing two snapshots and blaming
// both branches of a fork (the reasoning of ADR-0219, applied to data objects).
//
// The seeding at instance creation is attributed to nobody — no element ran — while
// the association's write names the task that made it. Both survive replay, because
// the producer is frozen into the event and never recomputed (I4/I6).
func TestDataObjectWriteAttribution(t *testing.T) {
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
	// The snapshot history holds both writes in order: the seed, then the association.
	var producers []uint64
	if err := h1.store.DataObjectSnapshotHistory(scope, func(_ int64, _ uint64, v *model.DataObjectValue) error {
		producers = append(producers, v.ProducerKey)
		return nil
	}); err != nil {
		t.Fatalf("DataObjectSnapshotHistory: %v", err)
	}
	if len(producers) != 2 {
		t.Fatalf("history has %d entries, want 2 (seed then association write)", len(producers))
	}
	if producers[0] != 0 {
		t.Errorf("seed producer = %d, want 0 — no element wrote the seeded object", producers[0])
	}
	if producers[1] == 0 {
		t.Fatal("association write is unattributed; want the task's element instance")
	}
	// The producer must resolve against the instance's retained element history — that
	// is what the console joins it to in order to name an element on the diagram. A
	// completed activity's live element instance is gone by now, so the retained facts,
	// not the live state, are what attribution has to be true against.
	found := false
	if err := h1.store.ElementReplayHistory(scope, func(_ int64, _ uint64, v state.ElementReplayValue) error {
		if v.ElementInstanceKey == producers[1] {
			found = true
		}
		return nil
	}); err != nil {
		t.Fatalf("ElementReplayHistory: %v", err)
	}
	if !found {
		t.Errorf("producer %d matches no element instance in the instance's history", producers[1])
	}
	live := readDataObject(t, h1.store, scope, "order")
	if live == nil || live.ProducerKey != producers[1] {
		t.Errorf("current value's producer = %+v, want %d", live, producers[1])
	}
	h1.close(t)

	// Replay the same log into a fresh, empty store: attribution is a recorded fact.
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
	if replayed := readDataObject(t, store2, scope, "order"); replayed == nil || replayed.ProducerKey != producers[1] {
		t.Fatalf("replayed producer = %+v, want %d", replayed, producers[1])
	}
}
