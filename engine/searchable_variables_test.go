package engine_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// searchableProcess is start → user task → end, declaring one variable searchable.
// The user task parks the instance so its start variables stay observable.
func searchableProcess(t testing.TB, names ...string) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(defKey, "identitaet", 1)
	start := b.AddStartEvent()
	task := b.AddUserTask("Review", "editor", "reviewers", "", 50, 0, 3)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	b.SetSearchableVariables(names)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// The fold cannot ask a compiled process anything, so the decision "does this write
// belong in the value index" is made at command time and stamped onto the event —
// exactly where the producer key is stamped, and for the same reason: no write path
// can be trusted to remember it. A declared name is marked, an undeclared one beside
// it is not.
func TestSearchableVariableIsStampedOnTheEvent(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := searchableProcess(t, "identityId")

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key,
		model.VariableValue{Name: "identityId", Kind: model.VarString, Text: "MT-1998"},
		model.VariableValue{Name: "nachname", Kind: model.VarString, Text: "Testperson"},
	)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	var piKey uint64
	if err := h.store.ActiveProcessInstances(func(key uint64, _ *model.ProcessInstanceValue) error {
		piKey = key
		return nil
	}); err != nil {
		t.Fatalf("ActiveProcessInstances: %v", err)
	}
	if piKey == 0 {
		t.Fatal("no instance was started")
	}

	if v := readVar(t, h.store, piKey, "identityId"); v == nil || !v.Indexed {
		t.Errorf("identityId = %+v, want a record marked for the index", v)
	}
	if v := readVar(t, h.store, piKey, "nachname"); v == nil || v.Indexed {
		t.Errorf("nachname = %+v, want an undeclared name left out of the index", v)
	}
}

// A process that declares nothing marks nothing — the feature has to cost such a
// process one comparison per write and no index entries at all.
func TestUndeclaredProcessMarksNothing(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := searchableProcess(t) // no declaration

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, model.VariableValue{Name: "identityId", Kind: model.VarString, Text: "MT-1998"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	var piKey uint64
	if err := h.store.ActiveProcessInstances(func(key uint64, _ *model.ProcessInstanceValue) error {
		piKey = key
		return nil
	}); err != nil {
		t.Fatalf("ActiveProcessInstances: %v", err)
	}
	if v := readVar(t, h.store, piKey, "identityId"); v == nil || v.Indexed {
		t.Errorf("identityId = %+v, want nothing marked on an undeclared process", v)
	}
}

// instancesByVar reads the value index back.
func instancesByVar(t *testing.T, s *state.Store, name, value string) []uint64 {
	t.Helper()
	var got []uint64
	if err := s.InstancesByVariable(name, value, false, func(piKey uint64) error {
		got = append(got, piKey)
		return nil
	}); err != nil {
		t.Fatalf("InstancesByVariable: %v", err)
	}
	return got
}

// TestVariableIndexSurvivesRecovery is the invariant a derived index lives or dies by
// (I4/I6): the state a replay rebuilds is the state that was built live. It matters
// more here than for a counter, because the decision *whether* to index is not in the
// store — it is frozen into the event, and this is what proves the freezing works.
func TestVariableIndexSurvivesRecovery(t *testing.T) {
	dir := t.TempDir()
	cp := searchableProcess(t, "identityId")
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key, model.VariableValue{Name: "identityId", Kind: model.VarString, Text: "MT-1998"})
	p1.CreateInstance(cp.Key, model.VariableValue{Name: "identityId", Kind: model.VarString, Text: "MT-1999"})
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	live := instancesByVar(t, h1.store, "identityId", "MT-1998")
	if len(live) != 1 {
		t.Fatalf("live index for MT-1998 = %v, want exactly one instance", live)
	}
	h1.close(t)

	// Replay the log into a fresh, empty store: the index rebuilds from the events.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() { _ = store2.Close(); _ = log2.Close() }()
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}

	if got := instancesByVar(t, store2, "identityId", "MT-1998"); !reflect.DeepEqual(got, live) {
		t.Errorf("replayed index for MT-1998 = %v, want %v", got, live)
	}
	if got := instancesByVar(t, store2, "identityId", "MT-1999"); len(got) != 1 {
		t.Errorf("replayed index for MT-1999 = %v, want exactly one instance", got)
	}
	// A value nobody wrote stays absent on both sides.
	if got := instancesByVar(t, store2, "identityId", "MT-2000"); got != nil {
		t.Errorf("replayed index invented %v for a value nobody wrote", got)
	}
}
