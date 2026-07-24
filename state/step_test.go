package state_test

import (
	"reflect"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// TestElementStepHistory records an ordered step trail for two instances and
// checks that ElementStepHistory returns one instance's steps in (timestamp,
// position) order, isolated from the other instance's. Steps written out of time
// order must still scan back oldest-first — the single-process replay timeline
// (ADR-0046).
func TestElementStepHistory(t *testing.T) {
	s := openStore(t)
	i1 := model.NewKey(1, 10)
	i2 := model.NewKey(1, 11)

	// Written out of order to prove the scan sorts by (ts, pos). Instance 1 walks
	// start(0) → task(1) → end(2); instance 2 walks start(0) → task(1).
	steps := []struct {
		pi        uint64
		ts        int64
		pos       uint64
		elementId int32
	}{
		{i1, 300, 30, 2}, // end, recorded first but latest in time
		{i1, 100, 10, 0}, // start
		{i1, 200, 20, 1}, // task
		{i2, 150, 15, 0}, // other instance start
		{i2, 250, 25, 1}, // other instance task
	}
	commit(t, s, func(tx *state.Tx) error {
		for _, st := range steps {
			if err := tx.RecordElementStep(st.pi, st.ts, st.pos, st.elementId); err != nil {
				return err
			}
		}
		return nil
	})

	type row struct {
		ts  int64
		pos uint64
		el  int32
	}
	fold := func(pi uint64) []row {
		var out []row
		if err := s.ElementStepHistory(pi, func(ts int64, pos uint64, elementId int32) error {
			out = append(out, row{ts, pos, elementId})
			return nil
		}); err != nil {
			t.Fatalf("ElementStepHistory(%d): %v", pi, err)
		}
		return out
	}

	// Instance 1's own trail, oldest first, isolated from instance 2.
	if got, want := fold(i1), []row{{100, 10, 0}, {200, 20, 1}, {300, 30, 2}}; !reflect.DeepEqual(got, want) {
		t.Errorf("instance-1 steps = %v, want %v", got, want)
	}
	if got, want := fold(i2), []row{{150, 15, 0}, {250, 25, 1}}; !reflect.DeepEqual(got, want) {
		t.Errorf("instance-2 steps = %v, want %v", got, want)
	}
	// An instance with no recorded steps scans empty.
	if got := fold(model.NewKey(1, 99)); len(got) != 0 {
		t.Errorf("unknown instance steps = %v, want none", got)
	}
}

// TestProcessInstanceResolvesActiveAndHistory checks the keyed lookup the replay
// endpoint uses to find an instance's definition: it resolves a live instance
// from the active family and a finished one from the history family, and reports
// absence for an unknown key.
func TestProcessInstanceResolvesActiveAndHistory(t *testing.T) {
	s := openStore(t)
	active := model.NewKey(1, 1)
	done := model.NewKey(1, 2)

	commit(t, s, func(tx *state.Tx) error {
		if err := tx.PutProcessInstance(active, &model.ProcessInstanceValue{ProcessDefKey: 7}); err != nil {
			return err
		}
		return tx.PutProcessInstanceHistory(done, &model.ProcessInstanceValue{
			ProcessDefKey: 9, State: model.PICompleted, CompletedAt: 123,
		})
	})

	if v, ok, err := s.ProcessInstance(active); err != nil || !ok || v.ProcessDefKey != 7 {
		t.Errorf("active lookup = %+v, ok=%v, err=%v, want defKey 7", v, ok, err)
	}
	if v, ok, err := s.ProcessInstance(done); err != nil || !ok || v.ProcessDefKey != 9 || v.State != model.PICompleted {
		t.Errorf("history lookup = %+v, ok=%v, err=%v, want defKey 9 completed", v, ok, err)
	}
	if v, ok, err := s.ProcessInstance(model.NewKey(1, 99)); err != nil || ok || v != nil {
		t.Errorf("unknown lookup = %+v, ok=%v, err=%v, want nil,false,nil", v, ok, err)
	}
}
