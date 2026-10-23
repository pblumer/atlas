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

// Open approval requests are found across instances without scanning every approval
// fact ever recorded: a request is indexed by when it expires, and a read from now
// sees only the ones still open, soonest-expiring first. The index goes with the
// instance's history.
func TestOpenApprovalRequestsAreReadByExpiry(t *testing.T) {
	s := openStore(t)
	i1, i2 := model.NewKey(1, 10), model.NewKey(1, 11)
	commit(t, s, func(tx *state.Tx) error {
		for i, f := range []model.ActionApprovalValue{
			{ProcessInstanceKey: i1, ApprovalKey: 5, Kind: model.ApprovalRequested, ExpiresAt: 3000},
			{ProcessInstanceKey: i2, ApprovalKey: 6, Kind: model.ApprovalRequested, ExpiresAt: 2000},
			{ProcessInstanceKey: i1, ApprovalKey: 7, Kind: model.ApprovalRequested, ExpiresAt: 900},
			// An answer is not a request, whatever it carries.
			{ProcessInstanceKey: i1, ApprovalKey: 5, Kind: model.ApprovalDecided, Principal: "usr_bob", ExpiresAt: 9999},
		} {
			f := f
			// Each fact at its own log position, as the engine records them.
			if err := tx.RecordApprovalFact(uint64(i+1), &f); err != nil {
				return err
			}
		}
		return nil
	})
	type open struct{ pi, approval uint64 }
	read := func(now int64) []open {
		t.Helper()
		var got []open
		if err := s.OpenApprovalRequests(now, func(piKey, approvalKey uint64) error {
			got = append(got, open{piKey, approvalKey})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := read(1000); len(got) != 2 || got[0] != (open{i2, 6}) || got[1] != (open{i1, 5}) {
		t.Fatalf("open at 1000 = %v, want 6 then 5", got)
	}
	// Expiry is exclusive: at its expiry instant a request is closed.
	if got := read(3000); len(got) != 0 {
		t.Fatalf("open at 3000 = %v", got)
	}
	commit(t, s, func(tx *state.Tx) error { return tx.PurgeInstanceHistory(i1, 1, 0) })
	if got := read(0); len(got) != 1 || got[0] != (open{i2, 6}) {
		t.Fatalf("after purging instance 1: %v", got)
	}
}
