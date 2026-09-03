package state_test

import (
	"reflect"
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

// The instance search runs off the run loop against a read view (ADR-0157), so
// the view must reach the two process-instance families the search walks — the
// live one and the terminal history — and must reach them as of the moment it was
// taken, not as of the moment it is read.
func TestReadViewReadsProcessInstances(t *testing.T) {
	s := openStore(t)
	putPI := func(key uint64, hist bool, v *model.ProcessInstanceValue) {
		t.Helper()
		commit(t, s, func(tx *state.Tx) error {
			if hist {
				return tx.PutProcessInstanceHistory(key, v)
			}
			return tx.PutProcessInstance(key, v)
		})
	}
	putPI(10, false, &model.ProcessInstanceValue{ProcessDefKey: 3, CreatedAt: 100})
	putPI(11, true, &model.ProcessInstanceValue{ProcessDefKey: 3, State: model.PICompleted, CreatedAt: 90, CompletedAt: 110})

	snap := s.ReadView()
	defer func() { _ = snap.Close() }()

	// Written after the view was taken: invisible to it, visible to the store.
	putPI(12, false, &model.ProcessInstanceValue{ProcessDefKey: 3, CreatedAt: 120})

	var active []uint64
	if err := snap.ActiveProcessInstances(func(key uint64, _ *model.ProcessInstanceValue) error {
		active = append(active, key)
		return nil
	}); err != nil {
		t.Fatalf("ActiveProcessInstances: %v", err)
	}
	if !reflect.DeepEqual(active, []uint64{10}) {
		t.Errorf("active = %v, want [10] (12 was written after the view)", active)
	}

	var done []uint64
	if err := snap.CompletedProcessInstances(func(key uint64, v *model.ProcessInstanceValue) error {
		if v.CompletedAt != 110 {
			t.Errorf("completedAt = %d, want 110", v.CompletedAt)
		}
		done = append(done, key)
		return nil
	}); err != nil {
		t.Fatalf("CompletedProcessInstances: %v", err)
	}
	if !reflect.DeepEqual(done, []uint64{11}) {
		t.Errorf("completed = %v, want [11]", done)
	}

	// The point lookup spans both families, so a key resolves whether the instance
	// is still running or already finished — the exact-key search.
	for _, tc := range []struct {
		key  uint64
		want bool
	}{{10, true}, {11, true}, {12, false}, {99, false}} {
		pi, ok, err := snap.ProcessInstance(tc.key)
		if err != nil {
			t.Fatalf("ProcessInstance(%d): %v", tc.key, err)
		}
		if ok != tc.want {
			t.Errorf("ProcessInstance(%d) found = %v, want %v", tc.key, ok, tc.want)
		}
		if ok && pi.ProcessDefKey != 3 {
			t.Errorf("ProcessInstance(%d) def = %d, want 3", tc.key, pi.ProcessDefKey)
		}
	}
	if _, ok, err := s.ProcessInstance(12); err != nil || !ok {
		t.Errorf("the live store lost instance 12: ok=%v err=%v", ok, err)
	}
}

// The instances list pages a definition's own indexes off a read view, so the
// view has to page them exactly as the live store does — and, being a view, has
// to keep showing what it was taken over while the store moves on.
func TestReadViewPagesInstancesByDefinition(t *testing.T) {
	s := openStore(t)
	commit(t, s, func(tx *state.Tx) error {
		if err := tx.PutProcessInstance(10, &model.ProcessInstanceValue{ProcessDefKey: 3, CreatedAt: 100}); err != nil {
			return err
		}
		if err := tx.PutProcessInstance(11, &model.ProcessInstanceValue{ProcessDefKey: 3, CreatedAt: 110}); err != nil {
			return err
		}
		// One live element instance under 11, so the listing's token count has
		// something to walk.
		if err := tx.PutElementInstance(500, &model.ElementInstanceValue{
			ProcessInstanceKey: 11, ProcessDefKey: 3, ElementId: 2,
		}); err != nil {
			return err
		}
		return tx.PutProcessInstanceHistory(12, &model.ProcessInstanceValue{
			ProcessDefKey: 3, State: model.PICompleted, CreatedAt: 90, CompletedAt: 700,
		})
	})

	snap := s.ReadView()
	defer func() { _ = snap.Close() }()

	// Written after the view: invisible to it, in both indexes.
	commit(t, s, func(tx *state.Tx) error {
		if err := tx.PutProcessInstance(13, &model.ProcessInstanceValue{ProcessDefKey: 3, CreatedAt: 120}); err != nil {
			return err
		}
		return tx.PutProcessInstanceHistory(14, &model.ProcessInstanceValue{
			ProcessDefKey: 3, State: model.PICompleted, CreatedAt: 95, CompletedAt: 800,
		})
	})

	var active []uint64
	if err := snap.ActiveInstancesOfDefDesc(3, 0, func(key uint64, _ *model.ProcessInstanceValue) error {
		active = append(active, key)
		return nil
	}); err != nil {
		t.Fatalf("ActiveInstancesOfDefDesc: %v", err)
	}
	if !reflect.DeepEqual(active, []uint64{11, 10}) {
		t.Errorf("view active = %v, want [11 10] (13 was written after it)", active)
	}
	// The cursor pages the same way it does on the store.
	active = nil
	if err := snap.ActiveInstancesOfDefDesc(3, 11, func(key uint64, _ *model.ProcessInstanceValue) error {
		active = append(active, key)
		return nil
	}); err != nil {
		t.Fatalf("ActiveInstancesOfDefDesc(before): %v", err)
	}
	if !reflect.DeepEqual(active, []uint64{10}) {
		t.Errorf("view active before 11 = %v, want [10]", active)
	}

	var done []uint64
	if err := snap.FinishedInstancesOfDefDesc(3, 0, 0, func(key uint64, _ *model.ProcessInstanceValue) error {
		done = append(done, key)
		return nil
	}); err != nil {
		t.Fatalf("FinishedInstancesOfDefDesc: %v", err)
	}
	if !reflect.DeepEqual(done, []uint64{12}) {
		t.Errorf("view finished = %v, want [12] (14 was written after it)", done)
	}

	n := 0
	if err := snap.ElementInstancesOfProcess(11, func(uint64) error {
		n++
		return nil
	}); err != nil {
		t.Fatalf("ElementInstancesOfProcess: %v", err)
	}
	if n != 1 {
		t.Errorf("view element instances of 11 = %d, want 1", n)
	}

	// The live store, meanwhile, has moved on.
	live := 0
	if err := s.ActiveInstancesOfDefDesc(3, 0, func(uint64, *model.ProcessInstanceValue) error {
		live++
		return nil
	}); err != nil {
		t.Fatalf("store ActiveInstancesOfDefDesc: %v", err)
	}
	if live != 3 {
		t.Errorf("store active = %d, want 3", live)
	}
}
