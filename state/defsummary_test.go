package state

import (
	"testing"

	"github.com/cockroachdb/pebble"

	"github.com/pblumer/atlas/model"
)

// TestDefSummaryCounters covers the per-definition finished-count and last-activity
// counters behind the O(1) instances summary (ADR-0083): increments accumulate, the
// last-activity is a monotonic overwrite, and unset definitions read zero.
func TestDefSummaryCounters(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	tx := s.NewTransaction()
	for i := 0; i < 3; i++ {
		if err := tx.IncDefCompletedCount(7); err != nil {
			t.Fatalf("IncDefCompletedCount: %v", err)
		}
	}
	if err := tx.SetDefLastActivity(7, 1000); err != nil {
		t.Fatalf("SetDefLastActivity: %v", err)
	}
	if err := tx.SetDefLastActivity(7, 2000); err != nil { // overwrite with a later time
		t.Fatalf("SetDefLastActivity: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()

	if n, _ := s.DefCompletedCount(7); n != 3 {
		t.Fatalf("completed = %d, want 3", n)
	}
	if ts, _ := s.DefLastActivity(7); ts != 2000 {
		t.Fatalf("last activity = %d, want 2000", ts)
	}
	if n, _ := s.DefCompletedCount(9); n != 0 {
		t.Fatalf("unset completed = %d, want 0", n)
	}
	if ts, _ := s.DefLastActivity(9); ts != 0 {
		t.Fatalf("unset last activity = %d, want 0", ts)
	}
}

// TestBackfillSummaryCounters proves the one-time migration seeds the finished-count
// and last-activity counters from pre-existing active instances and history that were
// written before the counters existed (ADR-0083), and that it is idempotent.
func TestBackfillSummaryCounters(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Write instances + history WITHOUT touching the summary counters, mimicking state
	// that predates them.
	tx := s.NewTransaction()
	_ = tx.PutProcessInstance(100, &model.ProcessInstanceValue{ProcessDefKey: 7, CreatedAt: 100})
	// This finished record's start time (500) is later than any completion time, so it
	// exercises the "last activity is a start, not a finish" branch of the backfill.
	_ = tx.PutProcessInstanceHistory(200, &model.ProcessInstanceValue{ProcessDefKey: 7, State: model.PICompleted, CreatedAt: 500, CompletedAt: 300})
	_ = tx.PutProcessInstanceHistory(201, &model.ProcessInstanceValue{ProcessDefKey: 7, State: model.PITerminated, CreatedAt: 60, CompletedAt: 250})
	_ = tx.PutProcessInstance(101, &model.ProcessInstanceValue{ProcessDefKey: 8, CreatedAt: 400})
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()

	// The migration ran on the empty store at Open; clear its marker so it re-runs over
	// the state we just wrote.
	if err := s.db.Delete(keyMeta(metaSummaryCountersV1), pebble.Sync); err != nil {
		t.Fatalf("clear marker: %v", err)
	}
	if err := s.backfillSummaryCountersIfNeeded(); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	if n, _ := s.DefCompletedCount(7); n != 2 {
		t.Fatalf("def 7 completed = %d, want 2", n)
	}
	if ts, _ := s.DefLastActivity(7); ts != 500 { // the newest event is record 200's start (500)
		t.Fatalf("def 7 last activity = %d, want 500", ts)
	}
	if n, _ := s.DefCompletedCount(8); n != 0 {
		t.Fatalf("def 8 completed = %d, want 0", n)
	}
	if ts, _ := s.DefLastActivity(8); ts != 400 { // active-only def: last activity is its start
		t.Fatalf("def 8 last activity = %d, want 400", ts)
	}

	// Idempotent: the marker is set now, so a re-run does not double-count.
	if err := s.backfillSummaryCountersIfNeeded(); err != nil {
		t.Fatalf("backfill re-run: %v", err)
	}
	if n, _ := s.DefCompletedCount(7); n != 2 {
		t.Fatalf("def 7 completed after re-run = %d, want 2 (idempotent)", n)
	}
}

// TestBackfillOpenErrorOnCorruptRecord proves a corrupt record that a startup backfill
// scans makes Open fail cleanly (returning the error, closing the db) rather than
// opening a store with half-seeded counters. It covers both migrations' Open-time
// error branches by re-arming each with its own marker.
func TestBackfillOpenErrorOnCorruptRecord(t *testing.T) {
	t.Run("summary-migration/history", func(t *testing.T) {
		dir := t.TempDir()
		s, err := Open(dir)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		_ = s.db.Set(keyProcessInstanceHistory(1), []byte{0x01}, pebble.Sync)
		_ = s.db.Delete(keyMeta(metaSummaryCountersV1), pebble.Sync) // re-arm the summary migration only
		_ = s.Close()
		if _, err := Open(dir); err == nil {
			t.Fatal("Open with a corrupt history record: want error")
		}
	})
	t.Run("runtime-migration/active", func(t *testing.T) {
		dir := t.TempDir()
		s, err := Open(dir)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		_ = s.db.Set(keyProcessInstance(1), []byte{0x01}, pebble.Sync)
		_ = s.db.Delete(keyMeta(metaRuntimeCountersV1), pebble.Sync) // re-arm the runtime migration
		_ = s.Close()
		if _, err := Open(dir); err == nil {
			t.Fatal("Open with a corrupt instance record: want error")
		}
	})
}

// TestBackfillSummaryCountersDecodeErrors covers the migration's decode-error paths: an
// undecodable record in the active-instance family, or in the history family, aborts
// the scan with an error rather than seeding garbage.
func TestBackfillSummaryCountersDecodeErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  []byte
	}{
		{"active", keyProcessInstance(1)},
		{"history", keyProcessInstanceHistory(1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Open(t.TempDir())
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer func() { _ = s.Close() }()
			if err := s.db.Set(tc.key, []byte{0x01}, pebble.Sync); err != nil {
				t.Fatalf("plant corrupt record: %v", err)
			}
			if err := s.db.Delete(keyMeta(metaSummaryCountersV1), pebble.Sync); err != nil {
				t.Fatalf("clear marker: %v", err)
			}
			if err := s.backfillSummaryCountersIfNeeded(); err == nil {
				t.Fatal("backfill over an undecodable record: want error")
			}
		})
	}
}

// TestChildIndexBackfillSeedsExistingChildren covers the upgrade path for the
// reverse call-activity index: a store written before the index existed holds
// children that record their parent but have no index entry, and a cancel of their
// caller would then leave them running. Opening the store must seed the index once
// from those records.
//
// The marker is deleted to re-arm the migration, the same way the counter backfills
// above are exercised.
func TestChildIndexBackfillSeedsExistingChildren(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	const parentEl, childPi = uint64(4242), uint64(99)
	tx := s.NewTransaction()
	if err := tx.PutProcessInstance(childPi, &model.ProcessInstanceValue{
		ProcessDefKey:            1,
		ParentElementInstanceKey: parentEl,
	}); err != nil {
		t.Fatalf("PutProcessInstance: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	// Make the store look like one written before the index existed: no index entry,
	// and the migration marker cleared so the next open runs it.
	if err := s.db.Delete(keyChildByParent(parentEl, childPi), pebble.Sync); err != nil {
		t.Fatalf("clear index entry: %v", err)
	}
	if err := s.db.Delete(keyMeta(metaChildIndexV1), pebble.Sync); err != nil {
		t.Fatalf("clear marker: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	var got []uint64
	if err := s2.ChildInstancesOfParent(parentEl, func(k uint64) error {
		got = append(got, k)
		return nil
	}); err != nil {
		t.Fatalf("ChildInstancesOfParent: %v", err)
	}
	if len(got) != 1 || got[0] != childPi {
		t.Errorf("index after backfill = %v, want [%d]", got, childPi)
	}
}
