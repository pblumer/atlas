package state_test

import (
	"reflect"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// A multi-instance body fills its output collection one element per round, and the
// log records only the element (ADR-0296). The element is retained too, the size of
// one result, so the replay can show the collection as each round left it
// (ADR-draft-collection-by-round). It is history about one instance: read back per
// body scope in change order, and purged with the instance.
func TestVariableElementHistory(t *testing.T) {
	s := openStore(t)
	def := model.NewKey(1, 2)
	pi := model.NewKey(1, 10)
	other := model.NewKey(1, 11)
	body := model.NewKey(1, 20)
	sibling := model.NewKey(1, 21)

	el := func(scope uint64, idx int32, text string) model.VariableValue {
		return model.VariableValue{ScopeKey: scope, Name: "results", Kind: model.VarNumber, Text: text, Index: idx}
	}
	commit(t, s, func(tx *state.Tx) error {
		// Written out of order to prove the scan sorts by (ts, pos).
		rows := []struct {
			pi, ts int64
			pos    uint64
			v      model.VariableValue
		}{
			{int64(pi), 300, 30, el(body, 2, "30")},
			{int64(pi), 100, 10, el(body, 0, "10")},
			{int64(pi), 200, 20, el(body, 1, "20")},
			{int64(pi), 150, 15, el(sibling, 0, "99")},
			{int64(other), 120, 12, el(body, 0, "77")},
		}
		for i := range rows {
			r := rows[i]
			if err := tx.RecordVariableElementSnapshot(uint64(r.pi), r.ts, r.pos, &r.v); err != nil {
				return err
			}
		}
		return nil
	})

	type row struct {
		pos  uint64
		idx  int32
		text string
	}
	fold := func(pi, scope uint64) []row {
		var out []row
		if err := s.VariableElementHistory(pi, scope, func(_ int64, pos uint64, v *model.VariableValue) error {
			out = append(out, row{pos, v.Index, v.Text})
			return nil
		}); err != nil {
			t.Fatalf("VariableElementHistory: %v", err)
		}
		return out
	}

	if got, want := fold(pi, body), []row{{10, 0, "10"}, {20, 1, "20"}, {30, 2, "30"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("body elements = %v, want %v", got, want)
	}
	if got, want := fold(pi, sibling), []row{{15, 0, "99"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("sibling body elements = %v, want %v", got, want)
	}
	if got, want := fold(other, body), []row{{12, 0, "77"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("other instance's elements = %v, want %v", got, want)
	}

	commit(t, s, func(tx *state.Tx) error { return tx.PurgeInstanceHistory(pi, def, 0) })
	if got := fold(pi, body); len(got) != 0 {
		t.Errorf("purged instance still has elements %v", got)
	}
	if got := fold(other, body); len(got) != 1 {
		t.Errorf("unpurged instance has %v, want its one element", got)
	}
}
