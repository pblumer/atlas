package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// A declaration is per version (ADR-0244), and an instance can change version under
// an operator's hand: a migration (ADR-0162) rebinds a running instance to another
// deployed version, which may declare more searchable names than the one it came
// from, or fewer. The values it already holds were stamped by the version that wrote
// them, so without this the migrated instance is missing from the index its new
// version's search reads — a wrong answer rather than a slow one, because a declared
// name is answered from the index alone.
//
// identProcess is start → user task → end, so the instance parks with its start
// variables observable, and declares whatever the caller passes.
func identProcess(t *testing.T, key uint64, version int32, names ...string) (*compiler.CompiledProcess, []int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "identitaet", version)
	start := b.AddStartEvent()
	task := b.AddUserTask("Review", compiler.Assignment{Literal: "editor"}, compiler.Assignment{Literal: "reviewers"}, "", 50, 0, 3)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	b.SetSearchableVariables(names)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, []int32{start, task, end}
}

// onlyInstance returns the single active instance's key.
func onlyInstance(t *testing.T, s *state.Store) uint64 {
	t.Helper()
	var piKey uint64
	if err := s.ActiveProcessInstances(func(key uint64, _ *model.ProcessInstanceValue) error {
		piKey = key
		return nil
	}); err != nil {
		t.Fatalf("ActiveProcessInstances: %v", err)
	}
	if piKey == 0 {
		t.Fatal("no active instance")
	}
	return piKey
}

// logRecords counts what the log holds, so a test can assert that a command wrote
// nothing at all rather than only that the state it would have changed looks right.
func logRecords(t *testing.T, h *harness) int {
	t.Helper()
	n := 0
	if err := h.log.Replay(func([]byte) error {
		n++
		return nil
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	return n
}

// migrate runs one instance from one compiled version to another with the identity
// element mapping both test processes share by construction.
func migrate(t *testing.T, p *engine.Processor, piKey uint64, from, to *compiler.CompiledProcess, fromIDs, toIDs []int32) {
	t.Helper()
	mapping := make(map[int32]int32, len(fromIDs))
	for i := range fromIDs {
		mapping[fromIDs[i]] = toIDs[i]
	}
	p.MigrateInstance(model.NewProcessMigration(piKey, from.Key, to.Key, mapping), "operator", "declare identityId")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after migrate: %v", err)
	}
}

// Migrating onto a version that declares a name indexes the values the instance
// already holds. The decision is still made at command time and frozen into events
// (I6): the fold is handed one event per variable whose membership changes, so replay
// reaches the same index without asking a compiled process anything.
func TestMigrationIndexesWhatTheTargetDeclares(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	v1, ids1 := identProcess(t, 7, 1)               // declares nothing
	v2, ids2 := identProcess(t, 8, 2, "identityId") // declares identityId

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(v1)
	p.Deploy(v2)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(v1.Key,
		model.VariableValue{Name: "identityId", Kind: model.VarString, Text: "MT-1998"},
		model.VariableValue{Name: "nachname", Kind: model.VarString, Text: "Testperson"},
	)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	piKey := onlyInstance(t, h.store)
	if got := instancesByVar(t, h.store, "identityId", "MT-1998"); len(got) != 0 {
		t.Fatalf("index before migration = %v, want empty: the source version declares nothing", got)
	}

	migrate(t, p, piKey, v1, v2, ids1, ids2)

	if got := instancesByVar(t, h.store, "identityId", "MT-1998"); len(got) != 1 || got[0] != piKey {
		t.Errorf("index after migration = %v, want the migrated instance %d", got, piKey)
	}
	if v := readVar(t, h.store, piKey, "identityId"); v == nil || !v.Indexed {
		t.Errorf("identityId = %+v, want the record marked for the index", v)
	}
	// The flag is what a later overwrite reads to move the entry, so an undeclared
	// name must stay unmarked: marking everything would leave entries nobody asked for.
	if v := readVar(t, h.store, piKey, "nachname"); v == nil || v.Indexed {
		t.Errorf("nachname = %+v, want an undeclared name left alone", v)
	}
}

// The other direction, and the reason the refresh compares rather than adds: migrating
// onto a version that no longer declares the name has to drop the entry and clear the
// flag. Leaving the entry would let the index answer for an instance whose version
// never promised to be searchable by it, and leaving the flag set would make the next
// write believe it has an entry to move.
func TestMigrationDropsWhatTheTargetNoLongerDeclares(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	v1, ids1 := identProcess(t, 7, 1, "identityId")
	v2, ids2 := identProcess(t, 8, 2) // declares nothing

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(v1)
	p.Deploy(v2)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(v1.Key, model.VariableValue{Name: "identityId", Kind: model.VarString, Text: "MT-1998"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	piKey := onlyInstance(t, h.store)
	if got := instancesByVar(t, h.store, "identityId", "MT-1998"); len(got) != 1 {
		t.Fatalf("index before migration = %v, want the instance", got)
	}

	migrate(t, p, piKey, v1, v2, ids1, ids2)

	if got := instancesByVar(t, h.store, "identityId", "MT-1998"); len(got) != 0 {
		t.Errorf("index after migration = %v, want empty: the target declares nothing", got)
	}
	if v := readVar(t, h.store, piKey, "identityId"); v == nil || v.Indexed {
		t.Errorf("identityId = %+v, want the mark cleared", v)
	}
}

// The repair path (the explicit reindex an operator asks for): a store written before
// the migration fold refreshed anything holds an instance whose variable is unmarked
// under a version that declares it. Nothing in the engine can produce that state now,
// so the test writes it — the same licence a recovery test takes when it simulates a
// crash — and then asks the command to fix it.
func TestReindexInstanceVariablesRepairsAnOlderStore(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, _ := identProcess(t, 7, 1, "identityId")

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, model.VariableValue{Name: "identityId", Kind: model.VarString, Text: "MT-1998"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	piKey := onlyInstance(t, h.store)

	// Rewind that instance to what an older store held: the value present, its
	// membership unmarked, and no index entry.
	tx := h.store.NewTransaction()
	stale := *readVar(t, h.store, piKey, "identityId")
	stale.Indexed = false
	if err := tx.PutVariable(&stale); err != nil {
		t.Fatalf("PutVariable: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := tx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := instancesByVar(t, h.store, "identityId", "MT-1998"); len(got) != 0 {
		t.Fatalf("index after the rewind = %v, want empty", got)
	}

	p.ReindexInstanceVariables(piKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after reindex: %v", err)
	}

	if got := instancesByVar(t, h.store, "identityId", "MT-1998"); len(got) != 1 || got[0] != piKey {
		t.Errorf("index after reindex = %v, want the instance %d", got, piKey)
	}

	// And it is idempotent: an instance already in step emits nothing, so an operator
	// can run the repair over a whole version twice without writing anything the second
	// time. The log is the assertion, because "nothing changed" is what has to be true.
	before := logRecords(t, h)
	p.ReindexInstanceVariables(piKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after second reindex: %v", err)
	}
	if after := logRecords(t, h); after != before {
		t.Errorf("a no-op reindex appended %d record(s); want none", after-before)
	}
}

// The index is derived state, so the property it lives or dies by is that a replay
// rebuilds what the live run built (I4/I6) — including the memberships a migration
// changed after the fact.
func TestMigratedVariableIndexSurvivesRecovery(t *testing.T) {
	dir := t.TempDir()
	v1, ids1 := identProcess(t, 7, 1)
	v2, ids2 := identProcess(t, 8, 2, "identityId")
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(v1)
	p1.Deploy(v2)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(v1.Key, model.VariableValue{Name: "identityId", Kind: model.VarString, Text: "MT-1998"})
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	piKey := onlyInstance(t, h1.store)
	migrate(t, p1, piKey, v1, v2, ids1, ids2)
	live := instancesByVar(t, h1.store, "identityId", "MT-1998")
	if len(live) != 1 {
		t.Fatalf("live index = %v, want the migrated instance", live)
	}
	h1.close(t)

	// Reopen on an empty state store: everything is rebuilt from the log alone.
	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clock)
	p2.Deploy(v1)
	p2.Deploy(v2)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	if replayed := instancesByVar(t, h2.store, "identityId", "MT-1998"); len(replayed) != len(live) || replayed[0] != live[0] {
		t.Errorf("index after replay = %v, want %v", replayed, live)
	}
	if v := readVar(t, h2.store, piKey, "identityId"); v == nil || !v.Indexed {
		t.Errorf("identityId after replay = %+v, want the record marked for the index", v)
	}
}
