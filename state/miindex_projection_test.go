package state

import (
	"reflect"
	"testing"

	"github.com/pblumer/atlas/model"
)

func TestMIHistoryProjectionIsolatesBodyActivationsAndRounds(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tx := s.NewTransaction()

	body := model.ElementInstanceValue{
		ProcessInstanceKey: 40, ProcessDefKey: 9, ElementId: 5,
		TokenID: 70, ParentTokenID: 60, MultiInstance: 1,
	}
	round := body
	round.MultiInstance = 2
	for _, run := range []struct {
		bodyKey, activationPos, roundKey, roundPos uint64
	}{
		{100, 10, 101, 11}, {200, 20, 201, 21},
	} {
		if err := tx.RecordMIActivation(run.bodyKey, &body, run.activationPos); err != nil {
			t.Fatal(err)
		}
		round.FlowScopeKey = run.bodyKey
		if err := tx.RecordMIActivation(run.roundKey, &round, run.roundPos); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_ = tx.Close()

	v := s.ReadView()
	defer v.Close()
	first, ok, err := v.MIBody(40, 100)
	if err != nil || !ok || first.ActivationPosition != 10 || first.State != MIHistoryActive {
		t.Fatalf("first body: value=%+v ok=%v err=%v", first, ok, err)
	}
	second, ok, err := v.MIBody(40, 200)
	if err != nil || !ok || second.ActivationPosition != 20 {
		t.Fatalf("second body: value=%+v ok=%v err=%v", second, ok, err)
	}
	r1, ok, err := v.MIIteration(40, 101)
	if err != nil || !ok || r1.BodyElementKey != 100 || r1.BodyActivationPosition != 10 || r1.ActivationPosition != 11 {
		t.Fatalf("first iteration: value=%+v ok=%v err=%v", r1, ok, err)
	}
	r2, ok, err := v.MIIteration(40, 201)
	if err != nil || !ok || r2.BodyElementKey != 200 || r2.BodyActivationPosition != 20 {
		t.Fatalf("second iteration: value=%+v ok=%v err=%v", r2, ok, err)
	}
	if _, ok, err := v.MIIteration(41, 101); err != nil || ok {
		t.Fatalf("cross-instance lookup: found=%v err=%v", ok, err)
	}
}

func TestMIHistoryProjectionLifecyclePurgeAndLegacyTail(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	body := model.ElementInstanceValue{ProcessInstanceKey: 50, ProcessDefKey: 7, ElementId: 3, MultiInstance: 1}
	round := body
	round.MultiInstance = 2
	round.FlowScopeKey = 500
	tx := s.NewTransaction()
	// Upgrades may replay the end of an old instance that has no MI index yet.
	if err := tx.RecordMIEnd(900, &round, 70, false); err != nil {
		t.Fatalf("legacy iteration ending: %v", err)
	}
	if err := tx.RecordMIActivation(500, &body, 11); err != nil {
		t.Fatal(err)
	}
	if err := tx.RecordMIActivation(501, &round, 12); err != nil {
		t.Fatal(err)
	}
	if err := tx.RecordMIEnd(501, &round, 30, true); err != nil {
		t.Fatal(err)
	}
	if err := tx.RecordMIEnd(500, &body, 31, false); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_ = tx.Close()

	b, ok, err := s.MIBody(50, 500)
	if err != nil || !ok || b.State != MIHistoryCompleted || b.EndPosition != 31 {
		t.Fatalf("completed body: %+v %v %v", b, ok, err)
	}
	r, ok, err := s.MIIteration(50, 501)
	if err != nil || !ok || r.State != MIHistoryTerminated || r.EndPosition != 30 {
		t.Fatalf("terminated iteration: %+v %v %v", r, ok, err)
	}

	tx = s.NewTransaction()
	if err := tx.PurgeInstanceHistory(50, 7, 0); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_ = tx.Close()
	if _, ok, err := s.MIBody(50, 500); err != nil || ok {
		t.Fatalf("body survived purge: %v %v", ok, err)
	}
	if _, ok, err := s.MIIteration(50, 501); err != nil || ok {
		t.Fatalf("iteration survived purge: %v %v", ok, err)
	}
}

func TestMIHistoryProjectionReadViewIsStable(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	before := s.ReadView()
	defer before.Close()
	body := model.ElementInstanceValue{ProcessInstanceKey: 60, ProcessDefKey: 2, MultiInstance: 1}
	tx := s.NewTransaction()
	if err := tx.RecordMIActivation(100, &body, 3); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_ = tx.Close()

	if _, ok, err := before.MIBody(60, 100); err != nil || ok {
		t.Fatalf("read view saw a future write: ok=%v err=%v", ok, err)
	}
	after, ok, err := s.MIBody(60, 100)
	if err != nil || !ok || !reflect.DeepEqual(after, &MIHistoryEntry{
		ElementInstanceKey: 100, BodyElementKey: 100, BodyActivationPosition: 3,
		ActivationPosition: 3, ProcessDefKey: 2, State: MIHistoryActive,
	}) {
		t.Fatalf("committed value: %+v ok=%v err=%v", after, ok, err)
	}
}
