package state_test

import (
	"reflect"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// A token can leave an element two ways, and they are not the same fact: it either
// completed and handed its token on, or it was terminated — cancelled with the losing
// branch of an event-based gateway, torn down with its scope, interrupted by a boundary
// event. The visit counter (ADR-0022) counts only arrivals, so the overlay's "passed
// through" number merges both, and on an event gateway that is systematically half
// wrong: every decided race leaves one winner and one cancelled loser, so both branches
// show the identical count whatever actually happened. These tests pin the second
// counter that tells them apart, per instance and per definition
// (ADR-0249).

// TestElementTerminationHistory mirrors TestElementVisitHistory: terminations
// accumulate per (definition, instance, element), aggregate across a definition's
// instances, isolate to one instance, and never leak between definitions.
func TestElementTerminationHistory(t *testing.T) {
	s := openStore(t)
	def := model.NewKey(1, 2)
	otherDef := model.NewKey(1, 9)
	i1 := model.NewKey(1, 10)
	i2 := model.NewKey(1, 11)

	commit(t, s, func(tx *state.Tx) error {
		// Instance 1 lost the race on the timer catch (1) twice — a loop around the
		// gateway — and had its subprocess (2) torn down once.
		for _, el := range []int32{1, 2, 1} {
			if err := tx.RecordElementTermination(def, i1, el); err != nil {
				return err
			}
		}
		// Instance 2 lost on the message catch (0) instead.
		if err := tx.RecordElementTermination(def, i2, 0); err != nil {
			return err
		}
		// A termination under a different definition must not leak into def's scan.
		return tx.RecordElementTermination(otherDef, model.NewKey(1, 20), 0)
	})

	fold := func(filter uint64) map[int32]int64 {
		out := map[int32]int64{}
		if err := s.ElementTerminationHistory(def, filter, func(elementId int32, count int64) error {
			out[elementId] += count
			return nil
		}); err != nil {
			t.Fatalf("ElementTerminationHistory(%d): %v", filter, err)
		}
		return out
	}

	if got, want := fold(0), map[int32]int64{0: 1, 1: 2, 2: 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("aggregate terminations = %v, want %v", got, want)
	}
	if got, want := fold(i1), map[int32]int64{1: 2, 2: 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("instance-1 terminations = %v, want %v", got, want)
	}
	if got, want := fold(i2), map[int32]int64{0: 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("instance-2 terminations = %v, want %v", got, want)
	}
}

// TestElementTerminationTotals covers the maintained per-definition aggregate the live
// overlay reads: O(elements), never a walk of the instances behind it (ADR-0080), and
// keyed per definition so a second version's cancellations stay its own.
func TestElementTerminationTotals(t *testing.T) {
	s := openStore(t)
	def := model.NewKey(1, 2)
	otherDef := model.NewKey(1, 9)

	commit(t, s, func(tx *state.Tx) error {
		for _, el := range []int32{1, 1, 4} {
			if err := tx.IncElementTerminationAgg(def, el); err != nil {
				return err
			}
		}
		return tx.IncElementTerminationAgg(otherDef, 1)
	})

	got := map[int32]int64{}
	if err := s.ElementTerminationTotals(def, func(elementId int32, count int64) error {
		got[elementId] += count
		return nil
	}); err != nil {
		t.Fatalf("ElementTerminationTotals: %v", err)
	}
	if want := (map[int32]int64{1: 2, 4: 1}); !reflect.DeepEqual(got, want) {
		t.Errorf("termination totals = %v, want %v", got, want)
	}
}

// TestPurgeDropsTerminationHistory: an instance's cancellations are per-instance
// history and go with the rest of it when its history is purged (ADR-0146). The
// per-definition aggregate is deliberately untouched — like the visit aggregate, it is
// the retained heatmap and outlives the instances it was built from.
func TestPurgeDropsTerminationHistory(t *testing.T) {
	s := openStore(t)
	def := model.NewKey(1, 2)
	pi := model.NewKey(1, 10)

	commit(t, s, func(tx *state.Tx) error {
		if err := tx.RecordElementTermination(def, pi, 3); err != nil {
			return err
		}
		return tx.IncElementTerminationAgg(def, 3)
	})
	commit(t, s, func(tx *state.Tx) error {
		return tx.PurgeInstanceHistory(pi, def, 0)
	})

	rows := 0
	if err := s.ElementTerminationHistory(def, pi, func(int32, int64) error { rows++; return nil }); err != nil {
		t.Fatalf("ElementTerminationHistory: %v", err)
	}
	if rows != 0 {
		t.Errorf("purged instance still has %d termination rows, want 0", rows)
	}
	totals := 0
	if err := s.ElementTerminationTotals(def, func(int32, int64) error { totals++; return nil }); err != nil {
		t.Fatalf("ElementTerminationTotals: %v", err)
	}
	if totals != 1 {
		t.Errorf("definition totals after purge = %d, want 1 (the aggregate is retained)", totals)
	}
}
