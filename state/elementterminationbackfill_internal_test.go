package state

import (
	"testing"

	"github.com/cockroachdb/pebble"

	"github.com/pblumer/atlas/model"
)

// The termination counter (ADR-0249) was added after the fact, and on a store that has
// been running it starts at zero while `visits` goes back to the beginning. That is not
// merely "no data yet": the overlay's gray badge is visits − live − terminated, so every
// cancellation the store never counted is still sitting in gray, claiming to be a token
// that completed. On an event-based gateway that is most of the number — both branches
// read as near-equal winners, which is the very misreading the counter exists to remove.
//
// The lifecycle trail (ADR-0136) has recorded ReplayTerminated all along, so the history
// is not lost, only uncounted. These tests pin the one-time reconstruction from it.

// TestBackfillElementTerminations: a store whose cancellations predate the counter seeds
// them from the retained trail — for live and for finished instances, only for
// terminations, attributed to the right definition — and re-running changes nothing.
func TestBackfillElementTerminations(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// Pre-migration state, written directly: the trail records what happened, and
	// nothing maintains the termination counters (only applyToState does, and it did
	// not exist when these facts were written).
	tx := s.NewTransaction()
	mustNil(t, tx.PutProcessInstance(1, &model.ProcessInstanceValue{ProcessDefKey: 100}))
	mustNil(t, tx.PutProcessInstanceHistory(2, &model.ProcessInstanceValue{ProcessDefKey: 100}))
	mustNil(t, tx.PutProcessInstance(3, &model.ProcessInstanceValue{ProcessDefKey: 200}))
	// Instance 1 lost the race on element 5 twice (a loop around the gateway), won on
	// element 6, and is sitting on element 7.
	mustNil(t, tx.RecordElementReplay(1, 10, 1, 5, 51, 1, 0, -1, ReplayTerminated))
	mustNil(t, tx.RecordElementReplay(1, 11, 2, 5, 52, 1, 0, -1, ReplayTerminated))
	mustNil(t, tx.RecordElementReplay(1, 12, 3, 6, 53, 1, 0, -1, ReplayCompleted))
	mustNil(t, tx.RecordElementReplay(1, 13, 4, 7, 54, 1, 0, -1, ReplayActivated))
	// A finished instance's trail counts the same as a live one's.
	mustNil(t, tx.RecordElementReplay(2, 14, 5, 5, 55, 1, 0, -1, ReplayTerminated))
	// A different definition keeps its own count.
	mustNil(t, tx.RecordElementReplay(3, 15, 6, 9, 56, 1, 0, -1, ReplayTerminated))
	// An instance whose record is gone but whose trail lingers cannot be attributed to
	// a definition, so it is skipped rather than guessed at.
	mustNil(t, tx.RecordElementReplay(4, 16, 7, 5, 57, 1, 0, -1, ReplayTerminated))
	// One of instance 1's two cancellations was already counted — the store ran the new
	// binary for a while before the backfill landed. It must be topped up, not doubled.
	mustNil(t, tx.RecordElementTermination(100, 1, 5))
	mustNil(t, tx.IncElementTerminationAgg(100, 5))
	mustNil(t, tx.Commit())
	mustNil(t, tx.Close())

	// Clear the marker Open set on the empty store and re-run, as a store predating the
	// counter would on its first open after the upgrade.
	mustNil(t, s.db.Delete(keyMeta(metaElementTerminationsV1), pebble.Sync))
	mustNil(t, s.backfillElementTerminationsIfNeeded())

	if got := terminationMap(t, s, 100); got[5] != 3 || got[6] != 0 || got[7] != 0 {
		t.Errorf("def 100 terminations = %v, want {5:3} — two of instance 1's plus instance 2's, and nothing for a completed or a live element", got)
	}
	if got := terminationMap(t, s, 200); got[9] != 1 {
		t.Errorf("def 200 terminations = %v, want {9:1}", got)
	}
	// Per instance, so the single-instance overlay reads the same split.
	if got := instanceTerminations(t, s, 100, 1); got[5] != 2 {
		t.Errorf("instance 1 terminations = %v, want {5:2} (topped up from 1, not doubled to 3)", got)
	}
	if got := instanceTerminations(t, s, 100, 2); got[5] != 1 {
		t.Errorf("instance 2 terminations = %v, want {5:1}", got)
	}

	// Idempotent twice over: the marker stops a second run, and the delta it computes
	// would be zero even without it.
	mustNil(t, s.backfillElementTerminationsIfNeeded())
	mustNil(t, s.db.Delete(keyMeta(metaElementTerminationsV1), pebble.Sync))
	mustNil(t, s.backfillElementTerminationsIfNeeded())
	if got := terminationMap(t, s, 100); got[5] != 3 {
		t.Errorf("after re-running def 100 terminations = %v, want {5:3} (no double count)", got)
	}
}

// TestBackfillElementTerminationsSkipsAFreshStore: nothing to reconstruct, and the
// marker is still set, so the scan is paid for once and never again.
func TestBackfillElementTerminationsSkipsAFreshStore(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if _, ok, err := getCopy(s.db, keyMeta(metaElementTerminationsV1)); err != nil || !ok {
		t.Fatalf("marker after Open: ok=%v err=%v, want it set", ok, err)
	}
	if got := terminationMap(t, s, 100); len(got) != 0 {
		t.Errorf("fresh store terminations = %v, want none", got)
	}
}

func terminationMap(t *testing.T, s *Store, def uint64) map[int32]int64 {
	t.Helper()
	m := map[int32]int64{}
	mustNil(t, s.ElementTerminationTotals(def, func(el int32, c int64) error { m[el] = c; return nil }))
	return m
}

func instanceTerminations(t *testing.T, s *Store, def, pi uint64) map[int32]int64 {
	t.Helper()
	m := map[int32]int64{}
	mustNil(t, s.ElementTerminationHistory(def, pi, func(el int32, c int64) error { m[el] += c; return nil }))
	return m
}
