package state_test

import (
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

func putVar(t *testing.T, s *state.Store, scope uint64, name, value string) {
	t.Helper()
	tx := s.NewTransaction()
	if err := tx.PutVariable(&model.VariableValue{
		ScopeKey: scope, Name: name, Text: value, Kind: model.VarString,
	}); err != nil {
		t.Fatalf("PutVariable: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

// A read view is a *consistent* view, which is the whole reason it exists: a job
// handler running off the run loop reads several things — its element instance,
// then its variables — and must not see a write that landed between them. On the
// loop it could not; off the loop, only a consistent read view gives it the same guarantee.
func TestReadViewDoesNotSeeLaterWrites(t *testing.T) {
	s := openStore(t)
	putVar(t, s, 7, "to", "before@example.com")

	snap := s.ReadView()
	defer func() { _ = snap.Close() }()

	putVar(t, s, 7, "to", "after@example.com")
	putVar(t, s, 7, "added", "later")

	got := map[string]string{}
	if err := snap.VariablesOfScope(7, func(v *model.VariableValue) error {
		got[v.Name] = v.Text
		return nil
	}); err != nil {
		t.Fatalf("VariablesOfScope: %v", err)
	}
	if got["to"] != "before@example.com" {
		t.Errorf("to = %q, want the value as of the read view", got["to"])
	}
	if _, ok := got["added"]; ok {
		t.Error("the read view saw a variable written after it was taken")
	}
	// The live store, meanwhile, has moved on.
	live := map[string]string{}
	if err := s.VariablesOfScope(7, func(v *model.VariableValue) error {
		live[v.Name] = v.Text
		return nil
	}); err != nil {
		t.Fatalf("VariablesOfScope: %v", err)
	}
	if live["to"] != "after@example.com" || live["added"] != "later" {
		t.Errorf("live store = %v, want the later writes", live)
	}
}

// Element instances read the same way, since a handler starts from one.
func TestReadViewReadsElementInstances(t *testing.T) {
	s := openStore(t)
	tx := s.NewTransaction()
	if err := tx.PutElementInstance(99, &model.ElementInstanceValue{
		ProcessInstanceKey: 7, ProcessDefKey: 3, ElementId: 4,
	}); err != nil {
		t.Fatalf("PutElementInstance: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	snap := s.ReadView()
	defer func() { _ = snap.Close() }()
	ei, ok, err := snap.GetElementInstance(99)
	if err != nil || !ok {
		t.Fatalf("GetElementInstance: %v ok=%v", err, ok)
	}
	if ei.ProcessInstanceKey != 7 || ei.ProcessDefKey != 3 {
		t.Errorf("element instance = %+v, want instance 7 of definition 3", ei)
	}
	if _, ok, err := snap.GetElementInstance(1234); err != nil || ok {
		t.Errorf("GetElementInstance of a missing key = ok %v err %v, want not found", ok, err)
	}
}

// Both the live store and a read view satisfy the read surface a handler is given,
// so a handler cannot tell — and does not need to know — which one it holds.
func TestBothStoreAndReadViewAreReaders(t *testing.T) {
	s := openStore(t)
	snap := s.ReadView()
	defer func() { _ = snap.Close() }()
	var _ state.Reader = s
	var _ state.Reader = snap
}
