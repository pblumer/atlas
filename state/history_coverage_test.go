package state

import (
	"testing"

	"github.com/cockroachdb/pebble"

	"github.com/pblumer/atlas/model"
)

// TestProcessInstanceHistoryRoundTrip writes a terminal process-instance record
// through a Tx and reads it back via CompletedProcessInstances, pinning that the
// key and the terminal State/CompletedAt round-trip through the history index
// (ADR-0017). It exercises PutProcessInstanceHistory, keyProcessInstanceHistory,
// and CompletedProcessInstances' happy path.
func TestProcessInstanceHistoryRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	key := model.NewKey(1, 42)
	rec := &model.ProcessInstanceValue{
		ProcessDefKey: model.NewKey(1, 9),
		State:         model.PITerminated,
		CompletedAt:   1_700_000_000_000_000_000,
	}
	tx := s.NewTransaction()
	if err := tx.PutProcessInstanceHistory(key, rec); err != nil {
		t.Fatalf("PutProcessInstanceHistory: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := tx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got := map[uint64]*model.ProcessInstanceValue{}
	if err := s.CompletedProcessInstances(func(k uint64, v *model.ProcessInstanceValue) error {
		cp := *v
		got[k] = &cp
		return nil
	}); err != nil {
		t.Fatalf("CompletedProcessInstances: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d completed instances, want 1", len(got))
	}
	if v, ok := got[key]; !ok || *v != *rec {
		t.Errorf("CompletedProcessInstances[%d] = %+v, want %+v", key, v, rec)
	}

	// The record must sit under the history column family, keyed by piKey.
	if raw, ok, err := getCopy(s.db, keyProcessInstanceHistory(key)); err != nil || !ok || raw == nil {
		t.Errorf("history key absent: ok=%v err=%v", ok, err)
	}
}

// TestCompletedProcessInstancesDecodeError covers CompletedProcessInstances'
// decode-error branch by planting a too-short value under the history column
// family straight through the underlying db.
func TestCompletedProcessInstancesDecodeError(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if err := s.db.Set(keyProcessInstanceHistory(model.NewKey(1, 1)), []byte{0x01}, pebble.NoSync); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.CompletedProcessInstances(func(uint64, *model.ProcessInstanceValue) error { return nil }); err == nil {
		t.Errorf("CompletedProcessInstances on corrupt value: err = nil, want error")
	}
}

// TestOpenLockHeldError covers Open's pebble.Open error return: opening a store
// whose directory is already open fails because Pebble holds a directory lock.
func TestOpenLockHeldError(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	defer s.Close()
	if _, err := Open(dir); err == nil {
		t.Errorf("second Open on a locked dir: err = nil, want a lock error")
	}
}
