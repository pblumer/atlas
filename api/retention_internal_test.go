package api

import (
	"testing"
	"time"

	"github.com/pblumer/atlas/model"
)

// TestSweepRetentionSelection drives the sweep directly over seeded history to cover
// its selection rules (ADR-0115): an old, exported instance is purged, while a legacy
// record (no terminal position), a too-new one, and an unexported one (terminal
// position beyond the safe point) are all left alone.
func TestSweepRetentionSelection(t *testing.T) {
	srv, _ := newSystemServer(t)
	srv.retentionMaxAge = time.Hour
	srv.retentionBatch = 100

	const safePos = uint64(100)
	now := int64(10 * time.Hour)
	old := now - int64(2*time.Hour)   // older than maxAge
	fresh := now - int64(time.Minute) // within maxAge

	seed := func(counter, completedPos uint64, completedAt int64) uint64 {
		k := model.NewKey(1, counter)
		srv.do(func() {
			tx := srv.store.NewTransaction()
			if err := tx.SetLastAppliedPosition(safePos); err != nil {
				t.Errorf("SetLastAppliedPosition: %v", err)
			}
			if err := tx.PutProcessInstanceHistory(k, &model.ProcessInstanceValue{
				ProcessDefKey: 9, State: model.PICompleted, CompletedAt: completedAt, CompletedPosition: completedPos,
			}); err != nil {
				t.Errorf("PutProcessInstanceHistory: %v", err)
			}
			if err := tx.Commit(); err != nil {
				t.Errorf("Commit: %v", err)
			}
			_ = tx.Close()
		})
		return k
	}
	eligible := seed(1, 50, old)    // old + exported (50 <= 100) → purged
	legacy := seed(2, 0, old)       // old but no terminal position → skipped
	tooNew := seed(3, 50, fresh)    // exported but within maxAge → skipped
	unexported := seed(4, 200, old) // old but position beyond the safe point → skipped

	srv.do(func() { srv.sweepRetention(now) })

	remaining := map[uint64]bool{}
	srv.do(func() {
		if err := srv.store.CompletedProcessInstances(func(k uint64, _ *model.ProcessInstanceValue) error {
			remaining[k] = true
			return nil
		}); err != nil {
			t.Errorf("CompletedProcessInstances: %v", err)
		}
	})

	if remaining[eligible] {
		t.Errorf("eligible instance %d was not purged", eligible)
	}
	for name, k := range map[string]uint64{"legacy": legacy, "tooNew": tooNew, "unexported": unexported} {
		if !remaining[k] {
			t.Errorf("%s instance %d was purged but should have been skipped", name, k)
		}
	}
}
