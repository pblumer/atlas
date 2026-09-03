package state_test

import (
	"reflect"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// putActive writes a live instance of a definition through the ordinary
// transaction path, so the by-definition index is written the way the engine
// writes it.
func putActive(t *testing.T, s *state.Store, key, defKey uint64, createdAt int64) {
	t.Helper()
	commit(t, s, func(tx *state.Tx) error {
		return tx.PutProcessInstance(key, &model.ProcessInstanceValue{ProcessDefKey: defKey, CreatedAt: createdAt})
	})
}

// finish moves a live instance into the history the way applyToState does: write
// the terminal record, drop the active one.
func finish(t *testing.T, s *state.Store, key, defKey uint64, createdAt, completedAt int64) {
	t.Helper()
	commit(t, s, func(tx *state.Tx) error {
		if err := tx.PutProcessInstanceHistory(key, &model.ProcessInstanceValue{
			ProcessDefKey: defKey, State: model.PICompleted, CreatedAt: createdAt, CompletedAt: completedAt,
		}); err != nil {
			return err
		}
		return tx.DeleteProcessInstance(key, defKey)
	})
}

// collectActive drains a definition's live instances into the keys it yielded.
func collectActive(t *testing.T, s *state.Store, defKey, before uint64) []uint64 {
	t.Helper()
	var got []uint64
	if err := s.ActiveInstancesOfDefDesc(defKey, before, func(key uint64, v *model.ProcessInstanceValue) error {
		if v.ProcessDefKey != defKey {
			t.Errorf("instance %d has def %d, want %d", key, v.ProcessDefKey, defKey)
		}
		got = append(got, key)
		return nil
	}); err != nil {
		t.Fatalf("ActiveInstancesOfDefDesc: %v", err)
	}
	return got
}

// TestActiveInstancesOfDef is the point of the index: one definition's live
// instances are reachable without walking the others, newest first, and an
// instance that finishes leaves the live index.
func TestActiveInstancesOfDef(t *testing.T) {
	s := openStore(t)
	putActive(t, s, 10, 7, 100)
	putActive(t, s, 11, 8, 110) // a different definition — must never show up under 7
	putActive(t, s, 12, 7, 120)
	putActive(t, s, 13, 7, 130)

	if got, want := collectActive(t, s, 7, 0), []uint64{13, 12, 10}; !reflect.DeepEqual(got, want) {
		t.Errorf("def 7 active = %v, want %v (newest first)", got, want)
	}
	if got, want := collectActive(t, s, 8, 0), []uint64{11}; !reflect.DeepEqual(got, want) {
		t.Errorf("def 8 active = %v, want %v", got, want)
	}
	if got := collectActive(t, s, 99, 0); got != nil {
		t.Errorf("unknown definition = %v, want nothing", got)
	}

	// `before` is the newest-first paging cursor: strictly below the given key.
	if got, want := collectActive(t, s, 7, 13), []uint64{12, 10}; !reflect.DeepEqual(got, want) {
		t.Errorf("def 7 active before 13 = %v, want %v", got, want)
	}
	if got := collectActive(t, s, 7, 10); got != nil {
		t.Errorf("def 7 active before the oldest = %v, want nothing", got)
	}

	// A finished instance leaves the live index — otherwise the operations list
	// would keep reporting it as running.
	finish(t, s, 12, 7, 120, 500)
	if got, want := collectActive(t, s, 7, 0), []uint64{13, 10}; !reflect.DeepEqual(got, want) {
		t.Errorf("def 7 active after 12 finished = %v, want %v", got, want)
	}
}

// TestFinishedInstancesOfDef covers the history half: a definition's finished
// instances come back most-recently-completed first — the order the operations
// list wants — without sorting the whole history in memory to get it.
func TestFinishedInstancesOfDef(t *testing.T) {
	s := openStore(t)
	// Completion order deliberately differs from key order, which is the whole
	// reason this index is keyed by completion time and not by key.
	putActive(t, s, 20, 7, 10)
	putActive(t, s, 21, 7, 20)
	putActive(t, s, 22, 7, 30)
	putActive(t, s, 23, 8, 40)
	finish(t, s, 21, 7, 20, 900)
	finish(t, s, 20, 7, 10, 700)
	finish(t, s, 22, 7, 30, 800)
	finish(t, s, 23, 8, 40, 950)

	type row struct {
		key         uint64
		completedAt int64
	}
	collect := func(defKey uint64, beforeAt int64, beforeKey uint64) []row {
		t.Helper()
		var got []row
		if err := s.FinishedInstancesOfDefDesc(defKey, beforeAt, beforeKey, func(key uint64, v *model.ProcessInstanceValue) error {
			got = append(got, row{key, v.CompletedAt})
			return nil
		}); err != nil {
			t.Fatalf("FinishedInstancesOfDefDesc: %v", err)
		}
		return got
	}
	want := []row{{21, 900}, {22, 800}, {20, 700}}
	if got := collect(7, 0, 0); !reflect.DeepEqual(got, want) {
		t.Errorf("def 7 finished = %v, want %v (newest completion first)", got, want)
	}
	if got, want := collect(8, 0, 0), []row{{23, 950}}; !reflect.DeepEqual(got, want) {
		t.Errorf("def 8 finished = %v, want %v", got, want)
	}
	// The cursor is the (completedAt, key) pair of the last row on the page.
	if got, want := collect(7, 900, 21), []row{{22, 800}, {20, 700}}; !reflect.DeepEqual(got, want) {
		t.Errorf("def 7 finished after the first row = %v, want %v", got, want)
	}
	if got := collect(7, 700, 20); got != nil {
		t.Errorf("def 7 finished after the last row = %v, want nothing", got)
	}
}

// TestInstanceDefIndexPurged: history retention hard-deletes a finished instance,
// and the by-definition index entry must go with it — an index pointing at a
// record that no longer exists is how a list grows phantom rows.
func TestInstanceDefIndexPurged(t *testing.T) {
	s := openStore(t)
	putActive(t, s, 30, 7, 10)
	putActive(t, s, 31, 7, 20)
	finish(t, s, 30, 7, 10, 700)
	finish(t, s, 31, 7, 20, 800)

	commit(t, s, func(tx *state.Tx) error { return tx.PurgeInstanceHistory(31, 7, 0) })

	var got []uint64
	if err := s.FinishedInstancesOfDefDesc(7, 0, 0, func(key uint64, _ *model.ProcessInstanceValue) error {
		got = append(got, key)
		return nil
	}); err != nil {
		t.Fatalf("FinishedInstancesOfDefDesc: %v", err)
	}
	if !reflect.DeepEqual(got, []uint64{30}) {
		t.Errorf("finished after purging 31 = %v, want [30]", got)
	}
	// Purging an instance that is already gone is a no-op, as every purge must be:
	// the sweep can re-apply one on replay.
	commit(t, s, func(tx *state.Tx) error { return tx.PurgeInstanceHistory(31, 7, 0) })
}

// TestInstanceDefIndexFollowsMigration: an instance migrated to another version
// moves between the two definitions' live indexes, exactly as its per-definition
// counter does (ADR-0080) — otherwise the source version keeps listing an instance
// it no longer owns.
func TestInstanceDefIndexFollowsMigration(t *testing.T) {
	s := openStore(t)
	putActive(t, s, 40, 7, 10)
	putActive(t, s, 41, 7, 20)

	commit(t, s, func(tx *state.Tx) error {
		return tx.MigrateInstance(&model.ProcessMigrationValue{
			ProcessInstanceKey: 41,
			FromProcessDefKey:  7,
			ToProcessDefKey:    8,
		})
	})

	if got, want := collectActive(t, s, 7, 0), []uint64{40}; !reflect.DeepEqual(got, want) {
		t.Errorf("source definition = %v, want %v (41 migrated away)", got, want)
	}
	if got, want := collectActive(t, s, 8, 0), []uint64{41}; !reflect.DeepEqual(got, want) {
		t.Errorf("target definition = %v, want %v", got, want)
	}
}
