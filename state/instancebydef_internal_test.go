package state

import (
	"testing"

	"github.com/cockroachdb/pebble"

	"github.com/pblumer/atlas/model"
)

// TestBackfillInstanceDefIndex proves the one-time migration seeds the
// by-definition instance indexes from instances and history that were written
// before those indexes existed, and that it is idempotent. Without it, a store
// upgraded into this version would answer "this version's instances" with nothing
// at all — a silently empty list is worse than a slow one.
func TestBackfillInstanceDefIndex(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	tx := s.NewTransaction()
	_ = tx.PutProcessInstance(100, &model.ProcessInstanceValue{ProcessDefKey: 7, CreatedAt: 100})
	_ = tx.PutProcessInstance(101, &model.ProcessInstanceValue{ProcessDefKey: 7, CreatedAt: 110})
	_ = tx.PutProcessInstance(102, &model.ProcessInstanceValue{ProcessDefKey: 8, CreatedAt: 120})
	_ = tx.PutProcessInstanceHistory(200, &model.ProcessInstanceValue{ProcessDefKey: 7, State: model.PICompleted, CreatedAt: 10, CompletedAt: 300})
	_ = tx.PutProcessInstanceHistory(201, &model.ProcessInstanceValue{ProcessDefKey: 7, State: model.PITerminated, CreatedAt: 20, CompletedAt: 400})
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()

	// Drop both index families, leaving exactly the state a store written before
	// they existed would hold.
	for _, cf := range []columnFamily{cfInstanceByDef, cfInstanceDoneByDef} {
		lo := []byte{byte(cf)}
		if err := s.db.DeleteRange(lo, prefixEnd(lo), pebble.Sync); err != nil {
			t.Fatalf("wipe index %#x: %v", byte(cf), err)
		}
	}
	if n := countActiveOfDef(t, s, 7); n != 0 {
		t.Fatalf("index survived the wipe: %d entries", n)
	}

	// The migration ran on the empty store at Open; clear its marker so it re-runs
	// over the state we just wrote.
	if err := s.db.Delete(keyMeta(metaInstanceDefIndexV1), pebble.Sync); err != nil {
		t.Fatalf("clear marker: %v", err)
	}
	if err := s.backfillInstanceDefIndexIfNeeded(); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	if n := countActiveOfDef(t, s, 7); n != 2 {
		t.Errorf("def 7 active after backfill = %d, want 2", n)
	}
	if n := countActiveOfDef(t, s, 8); n != 1 {
		t.Errorf("def 8 active after backfill = %d, want 1", n)
	}
	var done []uint64
	if err := s.FinishedInstancesOfDefDesc(7, 0, 0, func(key uint64, _ *model.ProcessInstanceValue) error {
		done = append(done, key)
		return nil
	}); err != nil {
		t.Fatalf("FinishedInstancesOfDefDesc: %v", err)
	}
	// Newest completion first: 201 finished at 400, 200 at 300.
	if len(done) != 2 || done[0] != 201 || done[1] != 200 {
		t.Errorf("def 7 finished after backfill = %v, want [201 200]", done)
	}

	// Idempotent: the marker is set now, so a re-run neither doubles nor drops.
	if err := s.backfillInstanceDefIndexIfNeeded(); err != nil {
		t.Fatalf("backfill re-run: %v", err)
	}
	if n := countActiveOfDef(t, s, 7); n != 2 {
		t.Errorf("def 7 active after re-run = %d, want 2 (idempotent)", n)
	}
}

func countActiveOfDef(t *testing.T, s *Store, defKey uint64) int {
	t.Helper()
	n := 0
	if err := s.ActiveInstancesOfDefDesc(defKey, 0, func(uint64, *model.ProcessInstanceValue) error {
		n++
		return nil
	}); err != nil {
		t.Fatalf("ActiveInstancesOfDefDesc: %v", err)
	}
	return n
}

// TestBackfillInstanceDefIndexDecodeErrors covers the migration's decode-error
// paths: an undecodable record in either family fails the backfill rather than
// half-seeding an index that would then under-report a version's instances.
func TestBackfillInstanceDefIndexDecodeErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		corrupt func(s *Store) error
	}{
		{"active", func(s *Store) error {
			return s.db.Set(keyProcessInstance(1), []byte{0xff}, pebble.Sync)
		}},
		{"history", func(s *Store) error {
			return s.db.Set(keyProcessInstanceHistory(1), []byte{0xff}, pebble.Sync)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Open(t.TempDir())
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer func() { _ = s.Close() }()
			if err := tc.corrupt(s); err != nil {
				t.Fatalf("corrupt: %v", err)
			}
			if err := s.db.Delete(keyMeta(metaInstanceDefIndexV1), pebble.Sync); err != nil {
				t.Fatalf("clear marker: %v", err)
			}
			if err := s.backfillInstanceDefIndexIfNeeded(); err == nil {
				t.Fatal("backfill over an undecodable record: want error")
			}
		})
	}
}

// TestInstanceDefIndexSkipsAnOrphanEntry covers the defensive branch in both
// index scans: an index entry whose record is gone is skipped rather than
// reported as an instance. The pair is written in one batch, so this is only
// reachable on a store that predates the index and has not been backfilled —
// but a listing that invents rows from a stale index is exactly the failure a
// derived index has to be unable to cause.
func TestInstanceDefIndexSkipsAnOrphanEntry(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	tx := s.NewTransaction()
	_ = tx.PutProcessInstance(100, &model.ProcessInstanceValue{ProcessDefKey: 7, CreatedAt: 100})
	_ = tx.PutProcessInstanceHistory(200, &model.ProcessInstanceValue{
		ProcessDefKey: 7, State: model.PICompleted, CreatedAt: 10, CompletedAt: 300,
	})
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()

	// Index entries pointing at instances that were never written.
	if err := s.db.Set(keyInstanceByDef(7, 101), nil, pebble.Sync); err != nil {
		t.Fatalf("orphan active entry: %v", err)
	}
	if err := s.db.Set(keyInstanceDoneByDef(7, 400, 201), nil, pebble.Sync); err != nil {
		t.Fatalf("orphan finished entry: %v", err)
	}

	var active []uint64
	if err := s.ActiveInstancesOfDefDesc(7, 0, func(key uint64, _ *model.ProcessInstanceValue) error {
		active = append(active, key)
		return nil
	}); err != nil {
		t.Fatalf("ActiveInstancesOfDefDesc: %v", err)
	}
	if len(active) != 1 || active[0] != 100 {
		t.Errorf("active = %v, want only the instance that exists ([100])", active)
	}

	var done []uint64
	if err := s.FinishedInstancesOfDefDesc(7, 0, 0, func(key uint64, _ *model.ProcessInstanceValue) error {
		done = append(done, key)
		return nil
	}); err != nil {
		t.Fatalf("FinishedInstancesOfDefDesc: %v", err)
	}
	if len(done) != 1 || done[0] != 200 {
		t.Errorf("finished = %v, want only the instance that exists ([200])", done)
	}
}

// TestInstanceDefIndexScanDecodeErrors: an undecodable record behind an index
// entry surfaces as an error rather than a silently short list.
func TestInstanceDefIndexScanDecodeErrors(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	tx := s.NewTransaction()
	_ = tx.PutProcessInstance(100, &model.ProcessInstanceValue{ProcessDefKey: 7})
	_ = tx.PutProcessInstanceHistory(200, &model.ProcessInstanceValue{ProcessDefKey: 7, State: model.PICompleted, CompletedAt: 300})
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()
	if err := s.db.Set(keyProcessInstance(100), []byte{0xff}, pebble.Sync); err != nil {
		t.Fatalf("corrupt active: %v", err)
	}
	if err := s.db.Set(keyProcessInstanceHistory(200), []byte{0xff}, pebble.Sync); err != nil {
		t.Fatalf("corrupt history: %v", err)
	}

	if err := s.ActiveInstancesOfDefDesc(7, 0, func(uint64, *model.ProcessInstanceValue) error {
		return nil
	}); err == nil {
		t.Error("ActiveInstancesOfDefDesc over an undecodable record: want error")
	}
	if err := s.FinishedInstancesOfDefDesc(7, 0, 0, func(uint64, *model.ProcessInstanceValue) error {
		return nil
	}); err == nil {
		t.Error("FinishedInstancesOfDefDesc over an undecodable record: want error")
	}
}
