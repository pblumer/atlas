package state

import (
	"reflect"
	"testing"

	"github.com/cockroachdb/pebble"

	"github.com/pblumer/atlas/model"
)

// The flow counter (ADR-0496) arrives on stores that already hold
// years of element counts. Left at zero it would not read as "no data": the heatmap
// would draw every arrow of a busy process cold — dashed, "never reached" — between
// shapes glowing with thousands of visits. The lifecycle trail (ADR-0136) recorded the
// flow every activation came over all along, so the counts are reconstructed once from
// it.

// TestBackfillFlowTaken: an activation over a flow counts for that flow, attributed to
// the instance's definition, live and finished instances alike — except a branch of an
// event-based gateway's race, which counts only for the branch that won.
func TestBackfillFlowTaken(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	tx := s.NewTransaction()
	mustNil(t, tx.PutProcessInstance(1, &model.ProcessInstanceValue{ProcessDefKey: 100}))
	mustNil(t, tx.PutProcessInstanceHistory(2, &model.ProcessInstanceValue{ProcessDefKey: 100}))
	mustNil(t, tx.PutProcessInstance(3, &model.ProcessInstanceValue{ProcessDefKey: 200}))

	// Instance 1, a plain path: a task reached over flow 0 that completed, one reached
	// over flow 1 that is still running, and one reached over flow 2 that a boundary
	// event interrupted — the token did take that arrow, so it counts.
	mustNil(t, tx.RecordElementReplay(1, 10, 1, 5, 51, 1, 0, 0, ReplayActivated))
	mustNil(t, tx.RecordElementReplay(1, 11, 2, 5, 51, 1, 0, 0, ReplayCompleted))
	mustNil(t, tx.RecordElementReplay(1, 12, 3, 6, 52, 1, 0, 1, ReplayActivated))
	mustNil(t, tx.PutElementInstance(52, &model.ElementInstanceValue{ProcessInstanceKey: 1, ProcessDefKey: 100, ElementId: 6, SourceFlowId: 1}))
	mustNil(t, tx.RecordElementReplay(1, 13, 4, 7, 53, 1, 0, 2, ReplayActivated))
	mustNil(t, tx.RecordElementReplay(1, 14, 5, 7, 53, 1, 0, 2, ReplayTerminated))
	// A start event reached over no flow at all.
	mustNil(t, tx.RecordElementReplay(1, 9, 6, 4, 50, 1, 0, -1, ReplayActivated))
	// A race that has not been decided yet: both catches are armed and still waiting.
	// Neither arrow has been taken in the sense the diagram means.
	mustNil(t, tx.RecordElementReplay(1, 15, 7, 10, 71, 71, 1, 5, ReplayActivated))
	mustNil(t, tx.RecordElementReplay(1, 15, 8, 11, 72, 72, 1, 6, ReplayActivated))
	mustNil(t, tx.PutElementInstance(71, &model.ElementInstanceValue{ProcessInstanceKey: 1, ProcessDefKey: 100, ElementId: 10, SourceFlowId: 5, EventGatewayKey: 70}))
	mustNil(t, tx.PutElementInstance(72, &model.ElementInstanceValue{ProcessInstanceKey: 1, ProcessDefKey: 100, ElementId: 11, SourceFlowId: 6, EventGatewayKey: 70}))

	// Instance 2, finished: a decided race. Both catches were armed by the same token;
	// the message won over flow 3, the timer over flow 4 was cancelled.
	mustNil(t, tx.RecordElementReplay(2, 20, 9, 8, 61, 61, 9, 3, ReplayActivated))
	mustNil(t, tx.RecordElementReplay(2, 20, 10, 9, 62, 62, 9, 4, ReplayActivated))
	mustNil(t, tx.RecordElementReplay(2, 21, 11, 8, 61, 61, 9, 3, ReplayCompleted))
	mustNil(t, tx.RecordElementReplay(2, 21, 12, 9, 62, 62, 9, 4, ReplayTerminated))

	// A different definition keeps its own count.
	mustNil(t, tx.RecordElementReplay(3, 30, 13, 2, 81, 1, 0, 0, ReplayActivated))
	// A trail whose instance record is gone cannot be attributed, and is skipped.
	mustNil(t, tx.RecordElementReplay(4, 40, 14, 2, 91, 1, 0, 0, ReplayActivated))
	mustNil(t, tx.Commit())
	mustNil(t, tx.Close())

	// Clear the marker Open set on the empty store and re-run, as a store predating the
	// counter would on its first open after the upgrade.
	mustNil(t, s.db.Delete(keyMeta(metaFlowTakenV1), pebble.Sync))
	mustNil(t, s.backfillFlowTakenIfNeeded())

	if got, want := flowMap(t, s, 100), map[int32]int64{0: 1, 1: 1, 2: 1, 3: 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("def 100 flows = %v, want %v — the plain path, and only the race's winner", got, want)
	}
	if got, want := flowMap(t, s, 200), map[int32]int64{0: 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("def 200 flows = %v, want %v", got, want)
	}

	// The marker makes it a one-time migration: a second open counts nothing twice.
	mustNil(t, s.backfillFlowTakenIfNeeded())
	if got := flowMap(t, s, 100); got[0] != 1 {
		t.Errorf("after a second open def 100 flows = %v, want flow 0 still 1", got)
	}
}

// TestBackfillFlowTakenSkipsAFreshStore: nothing to reconstruct, and the marker is still
// set, so the scan of the trail is paid for once and never again.
func TestBackfillFlowTakenSkipsAFreshStore(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if _, ok, err := getCopy(s.db, keyMeta(metaFlowTakenV1)); err != nil || !ok {
		t.Fatalf("marker after Open: ok=%v err=%v, want it set", ok, err)
	}
	if got := flowMap(t, s, 100); len(got) != 0 {
		t.Errorf("fresh store flows = %v, want none", got)
	}
}

func flowMap(t *testing.T, s *Store, def uint64) map[int32]int64 {
	t.Helper()
	m := map[int32]int64{}
	mustNil(t, s.FlowTakenTotals(def, func(f int32, c int64) error { m[f] = c; return nil }))
	return m
}
