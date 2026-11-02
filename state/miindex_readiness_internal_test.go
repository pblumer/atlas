package state

import (
	"errors"
	"testing"

	"github.com/cockroachdb/pebble"
	"github.com/pblumer/atlas/model"
)

func TestMIHistoryReadinessFreshStoreSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		s, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		ready, err := s.MIHistoryIndexReady()
		if err != nil || !ready {
			t.Fatalf("open %d: MI index ready=%v, err=%v", i, ready, err)
		}
		if _, ok, err := getCopy(s.db, keyMeta(metaMIHistoryIndexV1)); err != nil || !ok {
			t.Fatalf("open %d: missing durable schema marker: ok=%v, err=%v", i, ok, err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMIHistoryReadinessLegacyIndexRejectsPartialHistory(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	body := model.ElementInstanceValue{ProcessInstanceKey: 40, ProcessDefKey: 9, MultiInstance: 1}
	round := body
	round.MultiInstance = 2
	round.FlowScopeKey = 100
	tx := s.NewTransaction()
	for _, f := range []func() error{
		func() error { return tx.RecordMIActivation(100, &body, 10) },
		func() error { return tx.RecordMIActivation(101, &round, 11) },
		func() error { return tx.RecordElementReplay(40, 1, 10, 3, 100, 0, 0, -1, ReplayActivated) },
		func() error { return tx.SetLastAppliedPosition(99) },
	} {
		if err := f(); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Close(); err != nil {
		t.Fatal(err)
	}
	// Simulate upgrading an already populated state store from an older build.
	if err := s.db.Delete(keyMeta(metaMIHistoryIndexV1), pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ready, err := s.MIHistoryIndexReady()
	if err != nil || ready {
		t.Fatalf("legacy index incorrectly ready=%v, err=%v", ready, err)
	}
	if _, _, err := s.MIBody(40, 100); !errors.Is(err, ErrMIHistoryIndexUnavailable) {
		t.Fatalf("point body lookup exposed incomplete history: %v", err)
	}
	if _, _, err := s.MIIteration(40, 101); !errors.Is(err, ErrMIHistoryIndexUnavailable) {
		t.Fatalf("exact round lookup exposed incomplete history: %v", err)
	}
	if err := s.MIBodies(40, 100, func(MIHistoryEntry) error {
		t.Fatal("visited incomplete body history")
		return nil
	}); !errors.Is(err, ErrMIHistoryIndexUnavailable) {
		t.Fatalf("body scan exposed incomplete history: %v", err)
	}
	if err := s.MIIterations(40, 100, 10, 100, func(MIHistoryEntry) error {
		t.Fatal("visited incomplete round history")
		return nil
	}); !errors.Is(err, ErrMIHistoryIndexUnavailable) {
		t.Fatalf("round scan exposed incomplete history: %v", err)
	}
	v := s.ReadView()
	defer v.Close()
	if ready, err := v.MIHistoryIndexReady(); err != nil || ready {
		t.Fatalf("snapshot readiness: %v, %v", ready, err)
	}
	if _, _, err := v.MIBody(40, 100); !errors.Is(err, ErrMIHistoryIndexUnavailable) {
		t.Fatalf("snapshot exposed partial index: %v", err)
	}

	// New events from a running upgraded process do not complete old history.
	tx = s.NewTransaction()
	if err := tx.RecordMIActivation(200, &body, 100); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Close(); err != nil {
		t.Fatal(err)
	}
	if ready, err := s.MIHistoryIndexReady(); err != nil || ready {
		t.Fatalf("new events made a legacy store ready: ready=%v err=%v", ready, err)
	}
}

func TestMIHistoryReadinessDetectsOldDataWithoutAppliedPosition(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tx := s.NewTransaction()
	if err := tx.PutProcessInstanceHistory(123, &model.ProcessInstanceValue{ProcessDefKey: 456}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Delete(keyMeta(metaMIHistoryIndexV1), pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if ready, err := s.MIHistoryIndexReady(); err != nil || ready {
		t.Fatalf("old history without applied position was declared complete: %v, %v", ready, err)
	}
}

func TestMIHistoryReadinessRejectsCorruptMarker(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.Set(keyMeta(metaMIHistoryIndexV1), []byte{255}, pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MIHistoryIndexReady(); err == nil {
		t.Fatal("corrupt schema marker was accepted")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("reopen accepted a corrupt schema marker")
	}
}

func TestMIHistoryReadinessEmptyLegacyStoreCanInitialize(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.Delete(keyMeta(metaMIHistoryIndexV1), pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if ready, err := s.MIHistoryIndexReady(); err != nil || !ready {
		t.Fatalf("empty old store not ready: %v, %v", ready, err)
	}
}
