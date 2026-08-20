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

// migrationVersions builds two versions of the same process whose element *indices*
// deliberately disagree: v1 is Start → Task → End, v2 inserts an audit task before it,
// so the task a token is parked on is index 1 in one version and index 2 in the other.
// A migration that quietly kept the index would leave the token on the new audit task —
// which is the failure the mapping exists to prevent, and the reason this fixture is
// shaped this way rather than with two identical graphs.
func migrationVersions(t testing.TB) (v1, v2 *compiler.CompiledProcess, task1, task2 int32) {
	t.Helper()
	b1 := compiler.NewBuilder(defKey, "migrating", 1)
	s1 := b1.AddStartEvent()
	task1 = b1.AddServiceTask(jobName, 3)
	e1 := b1.AddEndEvent()
	b1.Connect(s1, task1)
	b1.Connect(task1, e1)
	cp1, err := b1.Build()
	if err != nil {
		t.Fatalf("Build v1: %v", err)
	}

	b2 := compiler.NewBuilder(defKey+1, "migrating", 2)
	s2 := b2.AddStartEvent()
	audit := b2.AddServiceTask("audit", 3)
	task2 = b2.AddServiceTask(jobName, 3)
	e2 := b2.AddEndEvent()
	b2.Connect(s2, audit)
	b2.Connect(audit, task2)
	b2.Connect(task2, e2)
	cp2, err := b2.Build()
	if err != nil {
		t.Fatalf("Build v2: %v", err)
	}
	if task1 == task2 {
		t.Fatalf("fixture is pointless: the task has index %d in both versions", task1)
	}
	return cp1, cp2, task1, task2
}

// parkedElement returns the one live element instance of an instance, failing if there
// is not exactly one — every assertion below is about where that single token sits.
func parkedElement(t *testing.T, s *state.Store, piKey uint64) (uint64, *model.ElementInstanceValue) {
	t.Helper()
	var keys []uint64
	if err := s.ElementInstancesOfProcess(piKey, func(k uint64) error {
		keys = append(keys, k)
		return nil
	}); err != nil {
		t.Fatalf("ElementInstancesOfProcess: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("live element instances = %d, want exactly 1", len(keys))
	}
	ei, ok, err := s.GetElementInstance(keys[0])
	if err != nil || !ok {
		t.Fatalf("GetElementInstance(%d): %v (ok=%v)", keys[0], err, ok)
	}
	return keys[0], ei
}

// TestMigrateRebindsRunningInstance is the point of ADR-0162: an instance parked on a
// service task in v1 is rebound to v2 without being cancelled, and lands on the element
// the mapping names — not on the index it happened to carry.
func TestMigrateRebindsRunningInstance(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	v1, v2, task1, task2 := migrationVersions(t)
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
	piKey := model.NewKey(1, 1)

	elKey, before := parkedElement(t, h.store, piKey)
	if before.ProcessDefKey != v1.Key || before.ElementId != task1 {
		t.Fatalf("before migration the token is at def %d element %d, want %d/%d",
			before.ProcessDefKey, before.ElementId, v1.Key, task1)
	}

	p.MigrateInstance(
		model.NewProcessMigration(piKey, v1.Key, v2.Key, mappingFor(v1, v2)),
		"admin", "the mail template was wrong")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (migrate): %v", err)
	}

	pi, ok, err := h.store.ProcessInstance(piKey)
	if err != nil || !ok {
		t.Fatalf("ProcessInstance: %v (ok=%v)", err, ok)
	}
	if pi.ProcessDefKey != v2.Key {
		t.Errorf("instance is bound to def %d, want the target %d", pi.ProcessDefKey, v2.Key)
	}
	sameKey, after := parkedElement(t, h.store, piKey)
	if sameKey != elKey {
		t.Errorf("element instance key changed %d → %d; migration rebinds, it does not recreate", elKey, sameKey)
	}
	if after.ProcessDefKey != v2.Key || after.ElementId != task2 {
		t.Errorf("token is at def %d element %d, want %d/%d", after.ProcessDefKey, after.ElementId, v2.Key, task2)
	}
	// Everything that is keyed by element instance rather than element index rides
	// through untouched — which is why the keys are preserved in the first place.
	if after.TokenID != before.TokenID || after.FlowScopeKey != before.FlowScopeKey {
		t.Errorf("token identity changed: %+v → %+v", *before, *after)
	}

	// The live-token counter moved with it, so the Operations overlay attributes the
	// token to the version actually running it (ADR-0080).
	if got := liveTokens(t, h.store, v1.Key); len(got) != 0 {
		t.Errorf("v1 still counts live tokens %v, want none", got)
	}
	if got := liveTokens(t, h.store, v2.Key); got[task2] != 1 {
		t.Errorf("v2 live tokens = %v, want one on element %d", got, task2)
	}

	// The migration is on the record, with who and why — and with the definition it
	// came from, which is what lets a reader resolve the earlier steps (ADR-0159/0162).
	var actions []model.OperatorActionValue
	if err := h.store.OperatorActionHistory(piKey, func(_ int64, _ uint64, v *model.OperatorActionValue) error {
		actions = append(actions, *v)
		return nil
	}); err != nil {
		t.Fatalf("OperatorActionHistory: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("operator actions = %d, want the migration", len(actions))
	}
	if a := actions[0]; a.Kind != model.OperatorActionMigrate || a.Actor != "admin" ||
		a.Reason != "the mail template was wrong" || a.FromProcessDefKey != v1.Key {
		t.Errorf("audit record = %+v, want a migrate by admin from def %d with its reason", a, v1.Key)
	}
}

// TestMigrateSurvivesRecovery is the invariant the whole design is shaped around: the
// rebinding is reproduced by replaying the log alone, so a restarted engine agrees with
// the one that performed it (I4/I6). If the fold ever derived anything instead of
// reading the event, this is the test that would notice.
func TestMigrateSurvivesRecovery(t *testing.T) {
	dir := t.TempDir()
	v1, v2, _, task2 := migrationVersions(t)
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(v1)
	p1.Deploy(v2)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(v1.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	piKey := model.NewKey(1, 1)
	p1.MigrateInstance(model.NewProcessMigration(piKey, v1.Key, v2.Key, mappingFor(v1, v2)), "admin", "why")
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (migrate): %v", err)
	}
	wantKey, wantEI := parkedElement(t, h1.store, piKey)
	h1.close(t)

	// Replay the log into a fresh store: the same events, the same applier, so the same
	// state — including the migration.
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
	p2.Deploy(v1)
	p2.Deploy(v2)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}

	pi, ok, err := store2.ProcessInstance(piKey)
	if err != nil || !ok {
		t.Fatalf("replayed ProcessInstance: %v (ok=%v)", err, ok)
	}
	if pi.ProcessDefKey != v2.Key {
		t.Errorf("replayed instance is bound to def %d, want %d", pi.ProcessDefKey, v2.Key)
	}
	gotKey, gotEI := parkedElement(t, store2, piKey)
	if gotKey != wantKey || *gotEI != *wantEI {
		t.Errorf("replayed element instance %d=%+v, want %d=%+v", gotKey, *gotEI, wantKey, *wantEI)
	}
	if gotEI.ElementId != task2 {
		t.Errorf("replayed token is on element %d, want %d", gotEI.ElementId, task2)
	}
	if got := liveTokens(t, store2, v2.Key); got[task2] != 1 {
		t.Errorf("replayed live tokens on v2 = %v, want one on element %d", got, task2)
	}
	if got := liveTokens(t, store2, v1.Key); len(got) != 0 {
		t.Errorf("replayed live tokens still on v1: %v", got)
	}
}

// TestMigrateIsDroppedWhenTheMappingDoesNotHold covers the run-loop half of the gate.
// The API refuses a bad migration before submitting one, but the instance is free to
// move in between — so the handler re-checks, and a mapping that no longer covers the
// live token must leave the instance exactly as it was rather than rebinding half of it.
func TestMigrateIsDroppedWhenTheMappingDoesNotHold(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	v1, v2, _, _ := migrationVersions(t)
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
	piKey := model.NewKey(1, 1)
	_, before := parkedElement(t, h.store, piKey)

	// A mapping that covers the start event but not the task the token is parked on.
	p.MigrateInstance(model.NewProcessMigration(piKey, v1.Key, v2.Key, map[int32]int32{0: 0}), "admin", "why")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (migrate): %v", err)
	}

	pi, _, err := h.store.ProcessInstance(piKey)
	if err != nil {
		t.Fatalf("ProcessInstance: %v", err)
	}
	if pi.ProcessDefKey != v1.Key {
		t.Errorf("instance moved to def %d despite an unmappable token; want it left on %d", pi.ProcessDefKey, v1.Key)
	}
	if _, after := parkedElement(t, h.store, piKey); *after != *before {
		t.Errorf("element instance changed to %+v, want it untouched at %+v", *after, *before)
	}
	// Nothing was audited either: a refused migration did not happen.
	n := 0
	if err := h.store.OperatorActionHistory(piKey, func(int64, uint64, *model.OperatorActionValue) error {
		n++
		return nil
	}); err != nil {
		t.Fatalf("OperatorActionHistory: %v", err)
	}
	if n != 0 {
		t.Errorf("operator actions = %d, want none for a migration that was not applied", n)
	}
}

// TestMigrateToTheSameVersionIsANoOp covers the idempotency arm the fold relies on: a
// re-applied or duplicated migration must not move the per-definition counters twice.
func TestMigrateToTheSameVersionIsANoOp(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	v1, v2, _, task2 := migrationVersions(t)
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
	piKey := model.NewKey(1, 1)
	mig := model.NewProcessMigration(piKey, v1.Key, v2.Key, mappingFor(v1, v2))
	for i := 0; i < 3; i++ {
		p.MigrateInstance(mig, "admin", "why")
		if err := p.RunUntilIdle(); err != nil {
			t.Fatalf("RunUntilIdle (migrate %d): %v", i, err)
		}
	}
	// Only the first one described anything: the rest found the instance already on the
	// target and stopped before emitting.
	if got := liveTokens(t, h.store, v2.Key); got[task2] != 1 {
		t.Errorf("live tokens after three migrate commands = %v, want exactly one", got)
	}
	if n, _ := counts(t, h.store); n != 1 {
		t.Errorf("active instance count = %d, want 1", n)
	}
}

// mappingFor pairs the two versions node by node. The API derives its mapping from
// BPMN element ids — the identity a modeler controls — but a process built through
// compiler.Builder carries none, so these tests state the correspondence directly:
// v2 is v1 with one task inserted, so every node after the start event shifts by one.
func mappingFor(from, to *compiler.CompiledProcess) map[int32]int32 {
	mapping := map[int32]int32{0: 0} // the start event is index 0 in both
	for i := int32(1); i < from.NodeCount(); i++ {
		if i+1 < to.NodeCount() {
			mapping[i] = i + 1
		}
	}
	return mapping
}

// liveTokens reads the per-definition live-token counters (ADR-0080) as a map, so a
// test can say which version a token is attributed to.
func liveTokens(t *testing.T, s *state.Store, defKey uint64) map[int32]int64 {
	t.Helper()
	out := map[int32]int64{}
	if err := s.ElementLiveTokens(defKey, func(elementId int32, count int64) error {
		if count != 0 {
			out[elementId] = count
		}
		return nil
	}); err != nil {
		t.Fatalf("ElementLiveTokens: %v", err)
	}
	return out
}
