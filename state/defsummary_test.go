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
	_ = tx.PutProcessInstanceHistory(200, &model.ProcessInstanceValue{ProcessDefKey: 7, State: model.PICompleted, CreatedAt: 50, CompletedAt: 300})
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
	if ts, _ := s.DefLastActivity(7); ts != 300 { // max of created 100/50/60 and completed 300/250
		t.Fatalf("def 7 last activity = %d, want 300", ts)
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
