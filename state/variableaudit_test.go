package state_test

import (
	"reflect"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// TestVariableAuditHistory records an external-override audit trail for two
// instances and checks that VariableAuditHistory returns one instance's overrides in
// (timestamp, position) order, isolated from the other's — the "who changed it"
// trail (ADR-0097). Written out of order to prove the scan sorts by (ts, pos).
func TestVariableAuditHistory(t *testing.T) {
	s := openStore(t)
	i1 := model.NewKey(1, 10)
	i2 := model.NewKey(1, 11)

	overrides := []struct {
		ts  int64
		pos uint64
		v   model.VariableAuditValue
	}{
		{ts: 300, pos: 30, v: model.VariableAuditValue{ProcessInstanceKey: i1, ScopeKey: i1, Actor: "patrick", Name: "amount", Kind: model.VarNumber, Text: "2"}},
		{ts: 100, pos: 10, v: model.VariableAuditValue{ProcessInstanceKey: i1, ScopeKey: i1, Actor: "sam", Name: "amount", Kind: model.VarNumber, Text: "1"}},
		{ts: 200, pos: 20, v: model.VariableAuditValue{ProcessInstanceKey: i1, ScopeKey: i1, Actor: "patrick", Name: "flag", Kind: model.VarBool, Bool: true}},
		{ts: 150, pos: 15, v: model.VariableAuditValue{ProcessInstanceKey: i2, ScopeKey: i2, Actor: "", Name: "z", Kind: model.VarString, Text: "hi"}},
	}
	commit(t, s, func(tx *state.Tx) error {
		for i := range overrides {
			o := overrides[i]
			if err := tx.RecordVariableAudit(o.ts, o.pos, &o.v); err != nil {
				return err
			}
		}
		return nil
	})

	type row struct {
		ts    int64
		pos   uint64
		actor string
		name  string
		text  string
	}
	fold := func(pi uint64) []row {
		var out []row
		if err := s.VariableAuditHistory(pi, func(ts int64, pos uint64, v *model.VariableAuditValue) error {
			out = append(out, row{ts, pos, v.Actor, v.Name, v.Text})
			return nil
		}); err != nil {
			t.Fatalf("VariableAuditHistory(%d): %v", pi, err)
		}
		return out
	}

	// Instance 1's overrides, oldest first, each attributed to its actor, isolated
	// from instance 2 — including both writes to "amount" in order.
	if got, want := fold(i1), []row{{100, 10, "sam", "amount", "1"}, {200, 20, "patrick", "flag", ""}, {300, 30, "patrick", "amount", "2"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("instance-1 overrides = %v, want %v", got, want)
	}
	if got, want := fold(i2), []row{{150, 15, "", "z", "hi"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("instance-2 overrides = %v, want %v", got, want)
	}
	if got := fold(model.NewKey(1, 99)); len(got) != 0 {
		t.Errorf("unknown instance overrides = %v, want none", got)
	}
}
