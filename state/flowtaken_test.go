package state_test

import (
	"reflect"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// The element counters (ADR-0022, ADR-0080) say how often a token reached a shape; a
// heatmap also has to say which arrow it came over, or a gateway's branches can only be
// guessed at from where they lead. These tests pin the per-definition sequence-flow
// counter and the per-instance record of a task being taken up and handed back — the
// two facts the Operations heatmap needs that the engine did not keep
// (ADR-draft-operations-heatmap).

// TestFlowTakenTotals: traversals accumulate per (definition, flow), are read in
// O(flows), and never leak between definitions — two versions of a process number
// their flows independently, so one version's flow 3 is not the other's.
func TestFlowTakenTotals(t *testing.T) {
	s := openStore(t)
	def := model.NewKey(1, 2)
	otherDef := model.NewKey(1, 9)

	commit(t, s, func(tx *state.Tx) error {
		for _, f := range []int32{0, 1, 1, 3} {
			if err := tx.IncFlowTakenAgg(def, f); err != nil {
				return err
			}
		}
		return tx.IncFlowTakenAgg(otherDef, 1)
	})

	got := flowTotals(t, s, def)
	if want := map[int32]int64{0: 1, 1: 2, 3: 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("flow totals = %v, want %v", got, want)
	}
	if got := flowTotals(t, s, otherDef); !reflect.DeepEqual(got, map[int32]int64{1: 1}) {
		t.Errorf("other definition's flow totals = %v, want {1:1}", got)
	}
}

// TestFlowTakenTotalsSurvivePurge: like the visit aggregate, the flow aggregate is the
// retained heatmap. Purging an instance's history (ADR-0146, ADR-0486) takes the
// instance's own record and leaves the definition's picture of its past alone.
func TestFlowTakenTotalsSurvivePurge(t *testing.T) {
	s := openStore(t)
	def := model.NewKey(1, 2)
	pi := model.NewKey(1, 10)

	commit(t, s, func(tx *state.Tx) error { return tx.IncFlowTakenAgg(def, 4) })
	commit(t, s, func(tx *state.Tx) error { return tx.PurgeInstanceHistory(pi, def, 0) })

	if got := flowTotals(t, s, def); !reflect.DeepEqual(got, map[int32]int64{4: 1}) {
		t.Errorf("flow totals after purge = %v, want {4:1}", got)
	}
}

// TestJobTakeHistory: every time a task is taken up — claimed by a person, leased by a
// Worker — or handed back, the instance keeps the fact in the order it happened, with
// the element instance it belongs to. Waiting and working time are read off it; nothing
// else records when a claim happened, because the job keeps only who holds it now.
func TestJobTakeHistory(t *testing.T) {
	s := openStore(t)
	pi := model.NewKey(1, 10)
	other := model.NewKey(1, 11)

	commit(t, s, func(tx *state.Tx) error {
		// Written out of time order on purpose: the history is ordered by the event's
		// own timestamp and position, not by when the write happened to be issued.
		if err := tx.RecordJobTake(pi, 300, 3, 77, state.JobTaken); err != nil {
			return err
		}
		if err := tx.RecordJobTake(pi, 100, 1, 77, state.JobTaken); err != nil {
			return err
		}
		if err := tx.RecordJobTake(pi, 200, 2, 77, state.JobReleased); err != nil {
			return err
		}
		return tx.RecordJobTake(other, 150, 4, 88, state.JobTaken)
	})

	type row struct {
		ts   int64
		key  uint64
		kind byte
	}
	var got []row
	if err := s.JobTakeHistory(pi, func(ts int64, _ uint64, v state.JobTakeValue) error {
		got = append(got, row{ts, v.ElementInstanceKey, v.Kind})
		return nil
	}); err != nil {
		t.Fatalf("JobTakeHistory: %v", err)
	}
	want := []row{{100, 77, state.JobTaken}, {200, 77, state.JobReleased}, {300, 77, state.JobTaken}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("take history = %v, want %v (time order, this instance only)", got, want)
	}
}

// TestPurgeDropsJobTakeHistory: who-took-it-when is history about one instance and is
// retained exactly as long as the rest of it (ADR-0146) — a purge takes it, and only
// for the instance purged.
func TestPurgeDropsJobTakeHistory(t *testing.T) {
	s := openStore(t)
	def := model.NewKey(1, 2)
	pi := model.NewKey(1, 10)
	kept := model.NewKey(1, 11)

	commit(t, s, func(tx *state.Tx) error {
		if err := tx.RecordJobTake(pi, 100, 1, 77, state.JobTaken); err != nil {
			return err
		}
		return tx.RecordJobTake(kept, 100, 2, 88, state.JobTaken)
	})
	commit(t, s, func(tx *state.Tx) error { return tx.PurgeInstanceHistory(pi, def, 0) })

	if n := jobTakeRows(t, s, pi); n != 0 {
		t.Errorf("purged instance still has %d take rows, want 0", n)
	}
	if n := jobTakeRows(t, s, kept); n != 1 {
		t.Errorf("unpurged instance has %d take rows, want 1", n)
	}
}

func flowTotals(t *testing.T, s *state.Store, def uint64) map[int32]int64 {
	t.Helper()
	m := map[int32]int64{}
	if err := s.FlowTakenTotals(def, func(f int32, c int64) error { m[f] += c; return nil }); err != nil {
		t.Fatalf("FlowTakenTotals: %v", err)
	}
	return m
}

func jobTakeRows(t *testing.T, s *state.Store, pi uint64) int {
	t.Helper()
	n := 0
	if err := s.JobTakeHistory(pi, func(int64, uint64, state.JobTakeValue) error { n++; return nil }); err != nil {
		t.Fatalf("JobTakeHistory: %v", err)
	}
	return n
}
