package state

import (
	"testing"

	"github.com/pblumer/atlas/model"
)

// The history-expiry index (ADR-0146) is the retention sweep's candidate set: entries
// keyed by purge due date, scanned as a range up to now. These pin the three properties
// the sweep rests on — only what is due comes back, in due order, and the window is
// bounded — plus the delete that keeps the index from outliving what it points at.
func TestDueHistoryExpiries(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	const minute = int64(60e9)
	// Written out of due order on purpose: the index, not the insert order, decides.
	seed := []struct {
		due   int64
		piKey uint64
	}{
		{3 * minute, model.NewKey(1, 30)},
		{1 * minute, model.NewKey(1, 10)},
		{2 * minute, model.NewKey(1, 20)},
	}
	tx := s.NewTransaction()
	for _, e := range seed {
		if err := tx.PutHistoryExpiry(e.due, e.piKey); err != nil {
			t.Fatalf("PutHistoryExpiry: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()

	due := func(now int64, limit int) []uint64 {
		t.Helper()
		var got []uint64
		if _, err := s.DueHistoryExpiries(now, limit, func(_ int64, piKey uint64) error {
			got = append(got, piKey)
			return nil
		}); err != nil {
			t.Fatalf("DueHistoryExpiries: %v", err)
		}
		return got
	}

	// Nothing is due before the earliest entry — the idle-server case, which must cost
	// one empty range scan rather than a walk of the history.
	if got := due(minute-1, 10); len(got) != 0 {
		t.Errorf("nothing should be due yet, got %v", got)
	}
	// Due-date order, not key order, and inclusive at the due date itself.
	if got := due(2*minute, 10); len(got) != 2 || got[0] != seed[1].piKey || got[1] != seed[2].piKey {
		t.Errorf("due(2m) = %v, want the 1m and 2m entries in that order", got)
	}
	// The limit bounds the window: one tick never drains more than its batch.
	if got := due(3*minute, 2); len(got) != 2 {
		t.Errorf("due(3m, limit 2) returned %d entries, want 2", len(got))
	}
	// The callback sees the due date too, so a purge can delete the entry it came from.
	var seenDue int64
	if _, err := s.DueHistoryExpiries(minute, 10, func(d int64, _ uint64) error {
		seenDue = d
		return nil
	}); err != nil {
		t.Fatalf("DueHistoryExpiries: %v", err)
	}
	if seenDue != minute {
		t.Errorf("callback saw due = %d, want %d", seenDue, minute)
	}

	// Purging an instance drops its index entry, so the sweep cannot return it twice.
	tx = s.NewTransaction()
	if err := tx.PurgeInstanceHistory(seed[1].piKey, 9, seed[1].due); err != nil {
		t.Fatalf("PurgeInstanceHistory: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()
	if got := due(3*minute, 10); len(got) != 2 || got[0] != seed[2].piKey {
		t.Errorf("after purge due(3m) = %v, want the 2m and 3m entries", got)
	}

	// A zero due date means "no history TTL": there is nothing to delete, and the purge
	// must not touch the family (a zero-keyed entry would sort before every real one).
	tx = s.NewTransaction()
	if err := tx.PurgeInstanceHistory(seed[2].piKey, 9, 0); err != nil {
		t.Fatalf("PurgeInstanceHistory(no ttl): %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()
	if got := due(3*minute, 10); len(got) != 2 {
		t.Errorf("a zero due date must delete no index entry, got %v", got)
	}
}
