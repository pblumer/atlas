package state

import (
	"testing"

	"github.com/cockroachdb/pebble"

	"github.com/pblumer/atlas/model"
)

// countOnElement drains the piByEl walk into the number of instances it named.
func countOnElement(t *testing.T, s *Store, defKey uint64, elementId int32) int {
	t.Helper()
	n := 0
	if err := s.InstancesOnElementDesc(defKey, elementId, 0, func(uint64, *model.ProcessInstanceValue) error {
		n++
		return nil
	}); err != nil {
		t.Fatalf("InstancesOnElementDesc: %v", err)
	}
	return n
}

// TestBackfillElementTokenIndex proves the one-time migration seeds the piByEl
// index from element instances written before it existed, and that it is
// idempotent. Without it, an operator upgrading into this version and clicking a
// task holding thousands of tokens would be told nothing is waiting there — an
// empty answer that reads as a fact about the process rather than about the store.
func TestBackfillElementTokenIndex(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	tx := s.NewTransaction()
	_ = tx.PutProcessInstance(100, &model.ProcessInstanceValue{ProcessDefKey: 7, CreatedAt: 100})
	_ = tx.PutProcessInstance(101, &model.ProcessInstanceValue{ProcessDefKey: 7, CreatedAt: 110})
	_ = tx.PutProcessInstance(102, &model.ProcessInstanceValue{ProcessDefKey: 8, CreatedAt: 120})
	_ = tx.PutElementInstance(200, &model.ElementInstanceValue{ProcessInstanceKey: 100, ProcessDefKey: 7, ElementId: 3})
	_ = tx.PutElementInstance(201, &model.ElementInstanceValue{ProcessInstanceKey: 101, ProcessDefKey: 7, ElementId: 3})
	_ = tx.PutElementInstance(202, &model.ElementInstanceValue{ProcessInstanceKey: 101, ProcessDefKey: 7, ElementId: 4})
	_ = tx.PutElementInstance(203, &model.ElementInstanceValue{ProcessInstanceKey: 102, ProcessDefKey: 8, ElementId: 3})
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()

	// Drop the index, leaving exactly the state a store written before it existed
	// would hold: the element instances, and no reverse direction.
	lo := []byte{byte(cfInstanceByElement)}
	if err := s.db.DeleteRange(lo, prefixEnd(lo), pebble.Sync); err != nil {
		t.Fatalf("wipe index: %v", err)
	}
	if n := countOnElement(t, s, 7, 3); n != 0 {
		t.Fatalf("index survived the wipe: %d instances", n)
	}

	// The migration ran on the empty store at Open; clear its marker so it re-runs
	// over the state written since.
	if err := s.db.Delete(keyMeta(metaElementTokenIndexV1), pebble.Sync); err != nil {
		t.Fatalf("clear marker: %v", err)
	}
	if err := s.backfillElementTokenIndexIfNeeded(); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	if n := countOnElement(t, s, 7, 3); n != 2 {
		t.Errorf("def 7 element 3 after backfill = %d, want 2", n)
	}
	if n := countOnElement(t, s, 7, 4); n != 1 {
		t.Errorf("def 7 element 4 after backfill = %d, want 1", n)
	}
	if n := countOnElement(t, s, 8, 3); n != 1 {
		t.Errorf("def 8 element 3 after backfill = %d, want 1", n)
	}

	// The marker makes it a one-time migration: a second run is a no-op, not a
	// second pass over the store.
	if err := s.backfillElementTokenIndexIfNeeded(); err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if n := countOnElement(t, s, 7, 3); n != 2 {
		t.Errorf("def 7 element 3 after a repeat run = %d, want 2", n)
	}
}

// TestBackfillElementTokenIndexReportsCorruption keeps the migration honest about a
// record it cannot read: it fails the open rather than silently indexing less than
// the store holds.
func TestBackfillElementTokenIndexReportsCorruption(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.InjectCorruptElementInstance(200); err != nil {
		t.Fatalf("inject: %v", err)
	}
	if err := s.db.Delete(keyMeta(metaElementTokenIndexV1), pebble.Sync); err != nil {
		t.Fatalf("clear marker: %v", err)
	}
	if err := s.backfillElementTokenIndexIfNeeded(); err == nil {
		t.Error("backfill over a corrupt element instance reported success")
	}
}

// TestInstancesOnElementSkipsAndReports covers the two things the walk can meet that
// are not an instance: an index entry whose record is gone (reachable only on a store
// awaiting its backfill, and skipped rather than reported), and a record that cannot
// be decoded (reported, because a corrupt store must not read as an empty one).
func TestInstancesOnElementSkipsAndReports(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	tx := s.NewTransaction()
	_ = tx.PutProcessInstance(100, &model.ProcessInstanceValue{ProcessDefKey: 7, CreatedAt: 100})
	_ = tx.PutElementInstance(200, &model.ElementInstanceValue{ProcessInstanceKey: 100, ProcessDefKey: 7, ElementId: 3})
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()

	// An entry naming an instance that is not there at all.
	if err := s.db.Set(keyInstanceByElement(7, 3, 101, 201), nil, pebble.Sync); err != nil {
		t.Fatalf("orphan entry: %v", err)
	}
	if n := countOnElement(t, s, 7, 3); n != 1 {
		t.Errorf("with an orphaned entry the walk yielded %d instances, want 1", n)
	}

	// An entry naming an instance whose record is unreadable.
	if err := s.InjectCorruptProcessInstance(102); err != nil {
		t.Fatalf("inject: %v", err)
	}
	if err := s.db.Set(keyInstanceByElement(7, 3, 102, 202), nil, pebble.Sync); err != nil {
		t.Fatalf("corrupt entry: %v", err)
	}
	if err := s.InstancesOnElementDesc(7, 3, 0, func(uint64, *model.ProcessInstanceValue) error { return nil }); err == nil {
		t.Error("a corrupt instance record read as an empty answer")
	}
}
