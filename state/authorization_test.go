package state_test

import (
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// Evidence is read per instance and per job, in order, isolated from other instances,
// and it goes with the instance's history when that is purged
// (ADR-0475 §12).
func TestAuthorizationEvidenceIsPerInstanceAndPurgedWithIt(t *testing.T) {
	s := openStore(t)
	i1, i2 := model.NewKey(1, 10), model.NewKey(1, 11)
	j1, j2 := model.NewKey(1, 30), model.NewKey(1, 31)
	commit(t, s, func(tx *state.Tx) error {
		for _, d := range []struct {
			pos uint64
			v   model.AuthorizationValue
		}{
			{20, model.AuthorizationValue{ProcessInstanceKey: i1, JobKey: j2, ReasonCode: "B"}},
			{10, model.AuthorizationValue{ProcessInstanceKey: i1, JobKey: j1, ReasonCode: "A1"}},
			{15, model.AuthorizationValue{ProcessInstanceKey: i1, JobKey: j1, ReasonCode: "A2"}},
			{12, model.AuthorizationValue{ProcessInstanceKey: i2, JobKey: j1, ReasonCode: "OTHER"}},
		} {
			v := d.v
			if err := tx.RecordAuthorization(d.pos, &v); err != nil {
				return err
			}
		}
		for _, f := range []model.ActionApprovalValue{
			{ProcessInstanceKey: i1, ApprovalKey: 5, Kind: model.ApprovalRequested, ActionDigest: "d"},
			{ProcessInstanceKey: i2, ApprovalKey: 6, Kind: model.ApprovalRequested, ActionDigest: "e"},
		} {
			f := f
			if err := tx.RecordApprovalFact(1, &f); err != nil {
				return err
			}
		}
		// The command path sees its own batch.
		n := 0
		if err := tx.ApprovalFactsInBatch(i1, func(*model.ActionApprovalValue) error { n++; return nil }); err != nil {
			return err
		}
		if n != 1 {
			t.Errorf("in-batch facts = %d", n)
		}
		return nil
	})
	var reasons []string
	if err := s.AuthorizationHistory(i1, func(_ uint64, v *model.AuthorizationValue) error {
		reasons = append(reasons, v.ReasonCode)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(reasons) != 3 || reasons[0] != "A1" || reasons[1] != "A2" || reasons[2] != "B" {
		t.Fatalf("history = %v, want job then log order", reasons)
	}
	var positions []uint64
	if err := s.AuthorizationsOfJob(i1, j1, func(pos uint64, _ *model.AuthorizationValue) error {
		positions = append(positions, pos)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(positions) != 2 || positions[0] != 10 || positions[1] != 15 {
		t.Fatalf("one job's decisions = %v", positions)
	}

	commit(t, s, func(tx *state.Tx) error { return tx.PurgeInstanceHistory(i1, 1, 0) })
	gone := 0
	_ = s.AuthorizationHistory(i1, func(uint64, *model.AuthorizationValue) error { gone++; return nil })
	_ = s.ApprovalFacts(i1, func(*model.ActionApprovalValue) error { gone++; return nil })
	if gone != 0 {
		t.Fatalf("%d records outlived their instance's purge", gone)
	}
	kept := 0
	_ = s.AuthorizationHistory(i2, func(uint64, *model.AuthorizationValue) error { kept++; return nil })
	_ = s.ApprovalFacts(i2, func(*model.ActionApprovalValue) error { kept++; return nil })
	if kept != 2 {
		t.Fatalf("a purge reached another instance: %d left", kept)
	}
}
