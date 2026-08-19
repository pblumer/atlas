package state

import (
	"testing"

	"github.com/cockroachdb/pebble"

	"github.com/pblumer/atlas/model"
)

// ADR-0142 slice 4: engine-wide counters for the things that are currently *open* —
// jobs waiting for a worker, timers waiting to fire, subscriptions waiting to
// correlate. Each is a write-only signed merge paired with the events that create and
// remove the entity, so it costs no read on the hot path and replay rebuilds it.
//
// Two failure modes are worth more than the happy path, and both have tests: a counter
// that only climbs (a leak of increments) reads as a permanently growing backlog, and a
// counter seeded at zero on a store that already holds work drifts *negative* as that
// work finishes — which looks plausible right up until it does not.

func TestRuntimeTotalsFollowOpenAndClose(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	tx := s.NewTransaction()
	for i := 0; i < 3; i++ {
		if err := tx.IncOpenJobs(); err != nil {
			t.Fatalf("IncOpenJobs: %v", err)
		}
	}
	if err := tx.IncPendingTimers(); err != nil {
		t.Fatalf("IncPendingTimers: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := tx.IncMessageSubscriptions(); err != nil {
			t.Fatalf("IncMessageSubscriptions: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()

	assertTotals(t, s, 3, 1, 2)

	tx = s.NewTransaction()
	if err := tx.DecOpenJobs(); err != nil {
		t.Fatalf("DecOpenJobs: %v", err)
	}
	if err := tx.DecPendingTimers(); err != nil {
		t.Fatalf("DecPendingTimers: %v", err)
	}
	if err := tx.DecMessageSubscriptions(); err != nil {
		t.Fatalf("DecMessageSubscriptions: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()

	assertTotals(t, s, 2, 0, 1)
}

// TestRuntimeTotalsAreZeroOnAFreshStore: an unset counter reads as zero rather than
// failing — what every scrape of a just-started server sees.
func TestRuntimeTotalsAreZeroOnAFreshStore(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()
	assertTotals(t, s, 0, 0, 0)
}

// TestRuntimeTotalsBackfillSeedsExistingWork is the migration that matters. A store
// written before these counters existed already holds open jobs, timers and
// subscriptions; without a backfill it would report zero for all of them and then go
// *negative* as that work completed. The seed happens once, at Open.
func TestRuntimeTotalsBackfillSeedsExistingWork(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Write the entities directly and then remove the marker, which is exactly the
	// shape of a store from before the counters existed: entities present, counters
	// absent.
	tx := s.NewTransaction()
	for i := 0; i < 4; i++ {
		if err := tx.PutJob(uint64(100+i), &model.JobValue{JobType: 7, Retries: 3}); err != nil {
			t.Fatalf("PutJob: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := tx.PutTimer(uint64(200+i), &model.TimerValue{DueDate: int64(1000 + i)}); err != nil {
			t.Fatalf("PutTimer: %v", err)
		}
	}
	if err := tx.PutMessageSubscription(&model.MessageSubscriptionValue{
		MessageName: "order", CorrelationKey: "c1", ElementInstanceKey: 300,
	}); err != nil {
		t.Fatalf("PutMessageSubscription: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()

	// Drop the counters and the marker: a pre-ADR-0142 store.
	b := s.db.NewBatch()
	for _, k := range [][]byte{
		keyRuntimeTotal(rtOpenJobs),
		keyRuntimeTotal(rtPendingTimers),
		keyRuntimeTotal(rtMessageSubscriptions),
		keyMeta(metaRuntimeTotalsV1),
	} {
		if err := b.Delete(k, nil); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	}
	if err := b.Commit(pebble.Sync); err != nil {
		t.Fatalf("Commit batch: %v", err)
	}
	_ = b.Close()
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Re-opening runs the backfill.
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	defer func() { _ = s2.Close() }()
	assertTotals(t, s2, 4, 2, 1)

	// And it is a one-time migration: closing a job now leaves 3, not -1 or 7.
	tx = s2.NewTransaction()
	if err := tx.DecOpenJobs(); err != nil {
		t.Fatalf("DecOpenJobs: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()
	assertTotals(t, s2, 3, 2, 1)
}

// TestRuntimeTotalsBackfillRunsOnlyOnce: re-opening a store that has already been
// seeded must not seed it again, or every restart would double the counts.
func TestRuntimeTotalsBackfillRunsOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	tx := s.NewTransaction()
	for i := 0; i < 3; i++ {
		if err := tx.PutJob(uint64(100+i), &model.JobValue{JobType: 7, Retries: 3}); err != nil {
			t.Fatalf("PutJob: %v", err)
		}
		if err := tx.IncOpenJobs(); err != nil {
			t.Fatalf("IncOpenJobs: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	for i := 0; i < 3; i++ { // several restarts
		s2, err := Open(dir)
		if err != nil {
			t.Fatalf("re-Open %d: %v", i, err)
		}
		if got, _ := s2.OpenJobs(); got != 3 {
			t.Fatalf("after restart %d: open jobs = %d, want 3 — the backfill ran again", i, got)
		}
		if err := s2.Close(); err != nil {
			t.Fatalf("Close %d: %v", i, err)
		}
	}
}

func assertTotals(t *testing.T, s *Store, jobs, timers, subs int64) {
	t.Helper()
	if got, err := s.OpenJobs(); err != nil || got != jobs {
		t.Errorf("OpenJobs = %d, %v; want %d, nil", got, err, jobs)
	}
	if got, err := s.PendingTimers(); err != nil || got != timers {
		t.Errorf("PendingTimers = %d, %v; want %d, nil", got, err, timers)
	}
	if got, err := s.MessageSubscriptions(); err != nil || got != subs {
		t.Errorf("MessageSubscriptions = %d, %v; want %d, nil", got, err, subs)
	}
}
