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
	txn.Close()
}
