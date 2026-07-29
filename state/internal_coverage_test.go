package state

import (
	"bytes"
	"testing"

	"github.com/cockroachdb/pebble"

	"github.com/pblumer/atlas/model"
)

// TestCounterValueMerger drives the merge operator directly so both fold
// directions (MergeNewer and MergeOlder) and Finish are exercised
// deterministically, without relying on Pebble's internal compaction order.
func TestCounterValueMerger(t *testing.T) {
	vm, err := counterMerger.Merge([]byte("k"), encodeCounter(5))
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if err := vm.MergeNewer(encodeCounter(3)); err != nil {
		t.Fatalf("MergeNewer: %v", err)
	}
	if err := vm.MergeOlder(encodeCounter(-2)); err != nil {
		t.Fatalf("MergeOlder: %v", err)
	}
	out, closer, err := vm.Finish(true)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if closer != nil {
		closer.Close()
	}
	if got := decodeCounter(out); got != 6 {
		t.Errorf("folded sum = %d, want 6 (5+3-2)", got)
	}
	if counterMerger.Name != counterMergerName {
		t.Errorf("merger name = %q, want %q", counterMerger.Name, counterMergerName)
	}
}

// TestDecodeCounterShort covers the guard that treats a truncated counter value
// as zero.
func TestDecodeCounterShort(t *testing.T) {
	if got := decodeCounter([]byte{1, 2, 3}); got != 0 {
		t.Errorf("decodeCounter(short) = %d, want 0", got)
	}
	if got := decodeCounter(nil); got != 0 {
		t.Errorf("decodeCounter(nil) = %d, want 0", got)
	}
	if got := decodeCounter(encodeCounter(-42)); got != -42 {
		t.Errorf("roundtrip = %d, want -42", got)
	}
}

// TestKeyBuildersProcessAndVariable pins the byte layout of the process-instance
// and variable key builders (their column-family prefix plus big-endian scope).
func TestKeyBuildersProcessAndVariable(t *testing.T) {
	if got := keyProcessInstance(0x0102030405060708); !bytes.Equal(got, []byte{byte(cfProcessInstance), 1, 2, 3, 4, 5, 6, 7, 8}) {
		t.Errorf("keyProcessInstance = % x", got)
	}
	pre := variablePrefix(0x0102030405060708)
	if !bytes.Equal(pre, []byte{byte(cfVariable), 1, 2, 3, 4, 5, 6, 7, 8}) {
		t.Errorf("variablePrefix = % x", pre)
	}
	full := keyVariable(0x0102030405060708, "xy")
	if !bytes.Equal(full, append(append([]byte(nil), pre...), 'x', 'y')) {
		t.Errorf("keyVariable = % x", full)
	}
}

// TestLastAppliedPositionCorrupt covers the corrupt-length guard by writing a
// wrong-sized meta value straight to the underlying store.
func TestLastAppliedPositionCorrupt(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if err := s.db.Set(keyMeta(metaLastApplied), []byte{1, 2, 3}, pebble.NoSync); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := s.LastAppliedPosition(); err == nil {
		t.Errorf("LastAppliedPosition on corrupt value: err = nil, want error")
	}
}

// TestReadIntoDecodeError covers readInto's decode-error branch: a stored value
// too short for its payload type makes the in-transaction read fail.
func TestReadIntoDecodeError(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	key := model.NewKey(1, 1)
	if err := s.db.Set(keyJob(key), []byte{0x01}, pebble.NoSync); err != nil {
		t.Fatalf("Set: %v", err)
	}
	tx := s.NewTransaction()
	defer tx.Close()
	if ok, err := tx.GetJobInto(key, &model.JobValue{}); err == nil || ok {
		t.Errorf("GetJobInto on corrupt value = ok %v, err %v, want false + error", ok, err)
	}
}

// TestIncidentDecodeError covers the decode-error branch of both incident reads —
// the point GetIncident and the Incidents scan — by planting an undersized value
// under an incident key (ADR-0061).
func TestIncidentDecodeError(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	elKey := model.NewKey(1, 1)
	if err := s.db.Set(keyIncident(elKey), []byte{0x01}, pebble.NoSync); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := s.GetIncident(elKey); err == nil {
		t.Error("GetIncident on corrupt value: err = nil, want error")
	}
	if err := s.Incidents(func(uint64, *model.IncidentValue) error { return nil }); err == nil {
		t.Error("Incidents scan over corrupt value: err = nil, want error")
	}
}

// TestProcessInstanceDecodeError covers ProcessInstance's decode-error branch for
// both the active and the history family, by planting an undersized value under
// each key.
func TestProcessInstanceDecodeError(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if err := s.db.Set(keyProcessInstance(model.NewKey(1, 1)), []byte{0x01}, pebble.NoSync); err != nil {
		t.Fatalf("Set active: %v", err)
	}
	if _, _, err := s.ProcessInstance(model.NewKey(1, 1)); err == nil {
		t.Errorf("ProcessInstance on corrupt active value: err = nil, want error")
	}
	// A key present only in the history family exercises the second loop iteration.
	if err := s.db.Set(keyProcessInstanceHistory(model.NewKey(1, 2)), []byte{0x01}, pebble.NoSync); err != nil {
		t.Fatalf("Set history: %v", err)
	}
	if _, _, err := s.ProcessInstance(model.NewKey(1, 2)); err == nil {
		t.Errorf("ProcessInstance on corrupt history value: err = nil, want error")
	}
}

// TestStoreDecodeErrorPaths covers the decode-error branches in the store's
// scanning/point queries by planting undersized values.
func TestStoreDecodeErrorPaths(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// A too-short timer value makes DueTimers' decode fail.
	if err := s.db.Set(keyTimer(1, model.NewKey(1, 1)), []byte{0x01}, pebble.NoSync); err != nil {
		t.Fatalf("Set timer: %v", err)
	}
	if err := s.DueTimers(1000, func(uint64, *model.TimerValue) error { return nil }); err == nil {
		t.Errorf("DueTimers on corrupt value: err = nil, want error")
	}

	// GetJob (out-of-transaction) decode error.
	if err := s.db.Set(keyJob(model.NewKey(1, 2)), []byte{0x01}, pebble.NoSync); err != nil {
		t.Fatalf("Set job: %v", err)
	}
	if _, _, err := s.GetJob(model.NewKey(1, 2)); err == nil {
		t.Errorf("GetJob on corrupt value: err = nil, want error")
	}

	// GetElementInstance and ActiveElementInstances decode error.
	if err := s.db.Set(keyElementInstance(model.NewKey(1, 3)), []byte{0x01}, pebble.NoSync); err != nil {
		t.Fatalf("Set el: %v", err)
	}
	if _, _, err := s.GetElementInstance(model.NewKey(1, 3)); err == nil {
		t.Errorf("GetElementInstance on corrupt value: err = nil, want error")
	}
	if err := s.ActiveElementInstances(func(uint64, *model.ElementInstanceValue) error { return nil }); err == nil {
		t.Errorf("ActiveElementInstances on corrupt value: err = nil, want error")
	}

	// ActiveProcessInstances decode error.
	if err := s.db.Set(keyProcessInstance(model.NewKey(1, 4)), []byte{0x01}, pebble.NoSync); err != nil {
		t.Fatalf("Set pi: %v", err)
	}
	if err := s.ActiveProcessInstances(func(uint64, *model.ProcessInstanceValue) error { return nil }); err == nil {
		t.Errorf("ActiveProcessInstances on corrupt value: err = nil, want error")
	}

	// VariablesOfScope decode error.
	scope := model.NewKey(1, 5)
	if err := s.db.Set(keyVariable(scope, "bad"), []byte{0x01}, pebble.NoSync); err != nil {
		t.Fatalf("Set var: %v", err)
	}
	if err := s.VariablesOfScope(scope, func(*model.VariableValue) error { return nil }); err == nil {
		t.Errorf("VariablesOfScope on corrupt value: err = nil, want error")
	}
	// Tx.VariablesOfScope surfaces the same decode error through the in-flight batch.
	txn := s.NewTransaction()
	if err := txn.VariablesOfScope(scope, func(*model.VariableValue) error { return nil }); err == nil {
		t.Errorf("Tx.VariablesOfScope on corrupt value: err = nil, want error")
	}

	// VariableSnapshotHistory decode error: a too-short value under a snapshot key.
	if err := s.db.Set(keyVariableSnapshot(model.NewKey(1, 6), 1, 1), []byte{0x01}, pebble.NoSync); err != nil {
		t.Fatalf("Set var snapshot: %v", err)
	}
	if err := s.VariableSnapshotHistory(model.NewKey(1, 6), func(int64, uint64, *model.VariableValue) error { return nil }); err == nil {
		t.Errorf("VariableSnapshotHistory on corrupt value: err = nil, want error")
	}
	txn.Close()
}

// TestBackfillRuntimeCounters proves the one-time migration (ADR-0080): a store
// that gained instances before the counters existed seeds them correctly from a
// scan of the current state, and re-running it is a no-op (no double count).
func TestBackfillRuntimeCounters(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// Write pre-migration state directly. The Put/RecordElementVisit primitives do
	// not maintain the aggregate counters (only applyToState does), so this mirrors
	// instances that existed before the counters were added.
	tx := s.NewTransaction()
	mustNil(t, tx.PutProcessInstance(1, &model.ProcessInstanceValue{ProcessDefKey: 100}))
	mustNil(t, tx.PutProcessInstance(2, &model.ProcessInstanceValue{ProcessDefKey: 100}))
	mustNil(t, tx.PutProcessInstance(3, &model.ProcessInstanceValue{ProcessDefKey: 200}))
	mustNil(t, tx.PutElementInstance(11, &model.ElementInstanceValue{ProcessDefKey: 100, ElementId: 5, ProcessInstanceKey: 1}))
	mustNil(t, tx.PutElementInstance(12, &model.ElementInstanceValue{ProcessDefKey: 100, ElementId: 5, ProcessInstanceKey: 2}))
	mustNil(t, tx.PutElementInstance(13, &model.ElementInstanceValue{ProcessDefKey: 100, ElementId: 7, ProcessInstanceKey: 1}))
	mustNil(t, tx.RecordElementVisit(100, 1, 5))
	mustNil(t, tx.RecordElementVisit(100, 2, 5))
	mustNil(t, tx.RecordElementVisit(100, 1, 7))
	mustNil(t, tx.Commit())
	mustNil(t, tx.Close())

	// Counters are still zero: Open's backfill ran on an empty store, and the direct
	// writes above did not maintain them.
	if n, _ := s.DefInstanceCount(100); n != 0 {
		t.Fatalf("pre-backfill def 100 count = %d, want 0", n)
	}

	// Clear the migration marker and re-run the backfill, as a store predating the
	// counters would on its first open after upgrade.
	mustNil(t, s.db.Delete(keyMeta(metaRuntimeCountersV1), pebble.Sync))
	mustNil(t, s.backfillRuntimeCountersIfNeeded())

	if n, _ := s.DefInstanceCount(100); n != 2 {
		t.Errorf("def 100 count = %d, want 2", n)
	}
	if n, _ := s.DefInstanceCount(200); n != 1 {
		t.Errorf("def 200 count = %d, want 1", n)
	}
	if got := tokenMap(t, s, 100); got[5] != 2 || got[7] != 1 {
		t.Errorf("def 100 live tokens = %v, want {5:2, 7:1}", got)
	}
	if got := visitMap(t, s, 100); got[5] != 2 || got[7] != 1 {
		t.Errorf("def 100 visits = %v, want {5:2, 7:1}", got)
	}

	// Idempotent: with the marker back in place, a second call changes nothing.
	mustNil(t, s.backfillRuntimeCountersIfNeeded())
	if n, _ := s.DefInstanceCount(100); n != 2 {
		t.Errorf("after re-run def 100 count = %d, want 2 (idempotent, no double count)", n)
	}
}

func mustNil(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func tokenMap(t *testing.T, s *Store, def uint64) map[int32]int64 {
	t.Helper()
	m := map[int32]int64{}
	mustNil(t, s.ElementLiveTokens(def, func(el int32, c int64) error { m[el] = c; return nil }))
	return m
}

func visitMap(t *testing.T, s *Store, def uint64) map[int32]int64 {
	t.Helper()
	m := map[int32]int64{}
	mustNil(t, s.ElementVisitTotals(def, func(el int32, c int64) error { m[el] = c; return nil }))
	return m
}

// TestRuntimeCounterMethods exercises the tx-level counter mutators directly and
// reads them back through the store accessors (ADR-0080).
func TestRuntimeCounterMethods(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	tx := s.NewTransaction()
	mustNil(t, tx.IncDefInstanceCount(42))
	mustNil(t, tx.IncDefInstanceCount(42))
	mustNil(t, tx.DecDefInstanceCount(42))
	mustNil(t, tx.IncElementToken(42, 3))
	mustNil(t, tx.IncElementToken(42, 3))
	mustNil(t, tx.DecElementToken(42, 3))
	mustNil(t, tx.IncElementVisitAgg(42, 3))
	mustNil(t, tx.IncElementVisitAgg(42, 3))
	mustNil(t, tx.Commit())
	mustNil(t, tx.Close())

	if n, _ := s.DefInstanceCount(42); n != 1 {
		t.Errorf("def 42 count = %d, want 1 (2 inc, 1 dec)", n)
	}
	if got := tokenMap(t, s, 42); got[3] != 1 {
		t.Errorf("def 42 tokens = %v, want {3:1}", got)
	}
	if got := visitMap(t, s, 42); got[3] != 2 {
		t.Errorf("def 42 visits = %v, want {3:2}", got)
	}
}

// TestBackfillRuntimeCountersScanErrors covers the migration's decode-error
// branches: a corrupt process-instance or element-instance record surfaces as a
// backfill error (and thus a failed Open) rather than a silent miscount.
func TestBackfillRuntimeCountersScanErrors(t *testing.T) {
	corrupt := func(key []byte) error {
		s, err := Open(t.TempDir())
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer s.Close()
		mustNil(t, s.db.Set(key, []byte("not a value"), pebble.Sync))
		mustNil(t, s.db.Delete(keyMeta(metaRuntimeCountersV1), pebble.Sync))
		return s.backfillRuntimeCountersIfNeeded()
	}
	if err := corrupt(keyProcessInstance(1)); err == nil {
		t.Error("corrupt process-instance record: want a backfill error")
	}
	if err := corrupt(keyElementInstance(1)); err == nil {
		t.Error("corrupt element-instance record: want a backfill error")
	}
}
