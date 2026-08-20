package state_test

import (
	"reflect"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// TestOperatorActionHistory records operator interventions for two instances and
// checks that OperatorActionHistory returns one instance's actions in (timestamp,
// position) order, isolated from the other's — the "who forced this step, and why"
// trail (ADR-0159). Written out of order to prove the scan sorts by (ts, pos).
func TestOperatorActionHistory(t *testing.T) {
	s := openStore(t)
	i1 := model.NewKey(1, 10)
	i2 := model.NewKey(1, 11)

	actions := []struct {
		ts  int64
		pos uint64
		v   model.OperatorActionValue
	}{
		{ts: 300, pos: 30, v: model.OperatorActionValue{
			ProcessInstanceKey: i1, ElementInstanceKey: model.NewKey(1, 21), JobKey: model.NewKey(1, 31),
			Kind: model.OperatorActionCompleteJob, Actor: "patrick", Reason: "second",
		}},
		{ts: 100, pos: 10, v: model.OperatorActionValue{
			ProcessInstanceKey: i1, ElementInstanceKey: model.NewKey(1, 20), JobKey: model.NewKey(1, 30),
			Kind: model.OperatorActionCompleteJob, Actor: "sam", Reason: "first",
		}},
		// Auth off: no actor, but the reason and the act are still recorded.
		{ts: 150, pos: 15, v: model.OperatorActionValue{
			ProcessInstanceKey: i2, ElementInstanceKey: model.NewKey(1, 22), JobKey: model.NewKey(1, 32),
			Kind: model.OperatorActionCompleteJob, Reason: "other instance",
		}},
	}
	commit(t, s, func(tx *state.Tx) error {
		for i := range actions {
			a := actions[i]
			if err := tx.RecordOperatorAction(a.ts, a.pos, &a.v); err != nil {
				return err
			}
		}
		return nil
	})

	type row struct {
		ts     int64
		pos    uint64
		actor  string
		reason string
		job    uint64
	}
	var got []row
	if err := s.OperatorActionHistory(i1, func(ts int64, pos uint64, v *model.OperatorActionValue) error {
		got = append(got, row{ts: ts, pos: pos, actor: v.Actor, reason: v.Reason, job: v.JobKey})
		return nil
	}); err != nil {
		t.Fatalf("OperatorActionHistory: %v", err)
	}
	want := []row{
		{ts: 100, pos: 10, actor: "sam", reason: "first", job: model.NewKey(1, 30)},
		{ts: 300, pos: 30, actor: "patrick", reason: "second", job: model.NewKey(1, 31)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("history = %+v, want %+v", got, want)
	}

	// The other instance's trail is its own, and an instance with no interventions
	// yields nothing at all — the common case, since most steps are engine-driven.
	var other int
	if err := s.OperatorActionHistory(i2, func(int64, uint64, *model.OperatorActionValue) error {
		other++
		return nil
	}); err != nil {
		t.Fatalf("OperatorActionHistory(i2): %v", err)
	}
	if other != 1 {
		t.Fatalf("second instance has %d actions, want 1", other)
	}
	var none int
	if err := s.OperatorActionHistory(model.NewKey(1, 99), func(int64, uint64, *model.OperatorActionValue) error {
		none++
		return nil
	}); err != nil {
		t.Fatalf("OperatorActionHistory(unknown): %v", err)
	}
	if none != 0 {
		t.Fatalf("an instance with no interventions yielded %d rows, want 0", none)
	}
}
