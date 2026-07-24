package state_test

import (
	"reflect"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// TestVariableSnapshotHistory records a variable-change trail for two instances
// and checks that VariableSnapshotHistory returns one instance's changes in
// (timestamp, position) order, isolated from the other's — the per-step variable
// timeline the single-process replay folds (ADR-0047). Repeated writes to the same
// name are retained as distinct changes (the whole point: values evolve over time).
func TestVariableSnapshotHistory(t *testing.T) {
	s := openStore(t)
	i1 := model.NewKey(1, 10)
	i2 := model.NewKey(1, 11)

	// Written out of order to prove the scan sorts by (ts, pos).
	changes := []struct {
		ts  int64
		pos uint64
		v   model.VariableValue
	}{
		{ts: 300, pos: 30, v: model.VariableValue{ScopeKey: i1, Name: "x", Kind: model.VarNumber, Text: "2"}},
		{ts: 100, pos: 10, v: model.VariableValue{ScopeKey: i1, Name: "x", Kind: model.VarNumber, Text: "1"}},
		{ts: 200, pos: 20, v: model.VariableValue{ScopeKey: i1, Name: "y", Kind: model.VarString, Text: "hi"}},
		{ts: 150, pos: 15, v: model.VariableValue{ScopeKey: i2, Name: "z", Kind: model.VarBool, Bool: true}},
	}
	commit(t, s, func(tx *state.Tx) error {
		for i := range changes {
			c := changes[i]
			if err := tx.RecordVariableSnapshot(c.ts, c.pos, &c.v); err != nil {
				return err
			}
		}
		return nil
	})

	type row struct {
		ts   int64
		pos  uint64
		name string
		text string
	}
	fold := func(scope uint64) []row {
		var out []row
		if err := s.VariableSnapshotHistory(scope, func(ts int64, pos uint64, v *model.VariableValue) error {
			out = append(out, row{ts, pos, v.Name, v.Text})
			return nil
		}); err != nil {
			t.Fatalf("VariableSnapshotHistory(%d): %v", scope, err)
		}
		return out
	}

	// Instance 1's changes, oldest first, isolated from instance 2 — including both
	// writes to "x" in order (1 then 2).
	if got, want := fold(i1), []row{{100, 10, "x", "1"}, {200, 20, "y", "hi"}, {300, 30, "x", "2"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("instance-1 changes = %v, want %v", got, want)
	}
	if got, want := fold(i2), []row{{150, 15, "z", ""}}; !reflect.DeepEqual(got, want) {
		t.Errorf("instance-2 changes = %v, want %v", got, want)
	}
	if got := fold(model.NewKey(1, 99)); len(got) != 0 {
		t.Errorf("unknown scope changes = %v, want none", got)
	}
}
