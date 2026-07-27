package state_test

import (
	"reflect"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

func TestElementReplayHistory(t *testing.T) {
	s := openStore(t)
	pi := model.NewKey(1, 10)
	other := model.NewKey(1, 11)

	commit(t, s, func(tx *state.Tx) error {
		rows := []struct {
			pi                                 uint64
			ts                                 int64
			pos                                uint64
			elementID, sourceFlowID            int32
			elementKey, tokenID, parentTokenID uint64
			action                             byte
		}{
			{pi, 300, 30, 3, 8, 103, 13, 11, 2},
			{other, 150, 15, 9, 1, 201, 21, 0, 1},
			{pi, 100, 10, 1, -1, 101, 11, 0, 1},
			{pi, 200, 20, 2, 7, 102, 12, 11, 1},
		}
		for _, row := range rows {
			if err := tx.RecordElementReplay(row.pi, row.ts, row.pos, row.elementID, row.elementKey, row.tokenID, row.parentTokenID, row.sourceFlowID, row.action); err != nil {
				return err
			}
		}
		return nil
	})

	type row struct {
		ts  int64
		pos uint64
		v   state.ElementReplayValue
	}
	var got []row
	if err := s.ElementReplayHistory(pi, func(ts int64, pos uint64, v state.ElementReplayValue) error {
		got = append(got, row{ts: ts, pos: pos, v: v})
		return nil
	}); err != nil {
		t.Fatalf("ElementReplayHistory: %v", err)
	}
	want := []row{
		{100, 10, state.ElementReplayValue{ElementID: 1, SourceFlowID: -1, ElementInstanceKey: 101, TokenID: 11, Action: 1}},
		{200, 20, state.ElementReplayValue{ElementID: 2, SourceFlowID: 7, ElementInstanceKey: 102, TokenID: 12, ParentTokenID: 11, Action: 1}},
		{300, 30, state.ElementReplayValue{ElementID: 3, SourceFlowID: 8, ElementInstanceKey: 103, TokenID: 13, ParentTokenID: 11, Action: 2}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("replay history = %#v, want %#v", got, want)
	}
}
