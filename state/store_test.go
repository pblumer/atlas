package state_test

import (
	"reflect"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

func openStore(t *testing.T) *state.Store {
	t.Helper()
	s, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// commit runs fn against a fresh transaction and commits it.
func commit(t *testing.T, s *state.Store, fn func(tx *state.Tx) error) {
	t.Helper()
	tx := s.NewTransaction()
	defer tx.Close()
	if err := fn(tx); err != nil {
		t.Fatalf("tx body: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

func TestElementInstancePutGetDelete(t *testing.T) {
	s := openStore(t)
	key := model.NewKey(1, 100)
	ei := &model.ElementInstanceValue{
		ProcessInstanceKey: model.NewKey(1, 1),
		ProcessDefKey:      model.NewKey(1, 2),
		ElementId:          7,
		FlowScopeKey:       model.NewKey(1, 1),
		BpmnElementType:    3,
	}

	commit(t, s, func(tx *state.Tx) error { return tx.PutElementInstance(key, ei) })

	tx := s.NewTransaction()
	defer tx.Close()
	got, err := tx.GetElementInstance(key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got, ei) {
		t.Errorf("got %+v, want %+v", got, ei)
	}

	commit(t, s, func(tx *state.Tx) error { return tx.DeleteElementInstance(key, ei) })

	tx2 := s.NewTransaction()
	defer tx2.Close()
	if got, err := tx2.GetElementInstance(key); err != nil || got != nil {
		t.Errorf("after delete: got %v, err %v, want nil,nil", got, err)
	}
}

func TestTransactionSeesOwnWrites(t *testing.T) {
	s := openStore(t)
	key := model.NewKey(1, 5)
	ei := &model.ElementInstanceValue{ProcessInstanceKey: model.NewKey(1, 1), ElementId: 1}

	tx := s.NewTransaction()
	defer tx.Close()
	if err := tx.PutElementInstance(key, ei); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Read-through before commit must see the pending write.
	got, err := tx.GetElementInstance(key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("uncommitted write not visible within its transaction")
	}
}

func TestElementInstancesOfProcess(t *testing.T) {
	s := openStore(t)
	proc := model.NewKey(1, 1)
	other := model.NewKey(1, 2)
	want := map[uint64]bool{
		model.NewKey(1, 10): true,
		model.NewKey(1, 11): true,
		model.NewKey(1, 12): true,
	}

	commit(t, s, func(tx *state.Tx) error {
		for k := range want {
			if err := tx.PutElementInstance(k, &model.ElementInstanceValue{ProcessInstanceKey: proc}); err != nil {
				return err
			}
		}
		// An element of another instance must not show up.
		return tx.PutElementInstance(model.NewKey(1, 99), &model.ElementInstanceValue{ProcessInstanceKey: other})
	})

	got := map[uint64]bool{}
	if err := s.ElementInstancesOfProcess(proc, func(elKey uint64) error {
		got[elKey] = true
		return nil
	}); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestActivatableJobsIndex(t *testing.T) {
	s := openStore(t)
	const jobType int32 = 42
	jobA := model.NewKey(1, 1)
	jobB := model.NewKey(1, 2)

	commit(t, s, func(tx *state.Tx) error {
		if err := tx.PutJob(jobA, &model.JobValue{JobType: jobType}); err != nil {
			return err
		}
		if err := tx.PutJob(jobB, &model.JobValue{JobType: jobType}); err != nil {
			return err
		}
		// Different type — must not appear in the jobType==42 scan.
		return tx.PutJob(model.NewKey(1, 3), &model.JobValue{JobType: 7})
	})

	collect := func() map[uint64]bool {
		got := map[uint64]bool{}
		if err := s.ActivatableJobs(jobType, func(k uint64) error { got[k] = true; return nil }); err != nil {
			t.Fatalf("scan: %v", err)
		}
		return got
	}
	if got := collect(); !reflect.DeepEqual(got, map[uint64]bool{jobA: true, jobB: true}) {
		t.Errorf("activatable jobs = %v, want {%d,%d}", got, jobA, jobB)
	}

	// Completing a job removes it from the activatable index.
	commit(t, s, func(tx *state.Tx) error { return tx.DeleteJob(jobA, &model.JobValue{JobType: jobType}) })
	if got := collect(); !reflect.DeepEqual(got, map[uint64]bool{jobB: true}) {
		t.Errorf("after delete = %v, want {%d}", got, jobB)
	}
}

func TestDueTimersRangeScan(t *testing.T) {
	s := openStore(t)
	// Insert out of order; scan must return due timers in due-date order.
	timers := []struct {
		key uint64
		due int64
	}{
		{model.NewKey(1, 1), 300},
		{model.NewKey(1, 2), 100},
		{model.NewKey(1, 3), 200},
		{model.NewKey(1, 4), 500}, // not yet due
	}
	commit(t, s, func(tx *state.Tx) error {
		for _, tm := range timers {
			if err := tx.PutTimer(tm.key, &model.TimerValue{DueDate: tm.due, Repetitions: -1}); err != nil {
				return err
			}
		}
		return nil
	})

	var gotDue []int64
	var gotKeys []uint64
	if err := s.DueTimers(300, func(timerKey uint64, v *model.TimerValue) error {
		gotDue = append(gotDue, v.DueDate)
		gotKeys = append(gotKeys, timerKey)
		return nil
	}); err != nil {
		t.Fatalf("DueTimers: %v", err)
	}

	wantDue := []int64{100, 200, 300}
	if !reflect.DeepEqual(gotDue, wantDue) {
		t.Errorf("due dates = %v, want %v (sorted, excluding 500)", gotDue, wantDue)
	}
	wantKeys := []uint64{model.NewKey(1, 2), model.NewKey(1, 3), model.NewKey(1, 1)}
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Errorf("timer keys = %v, want %v", gotKeys, wantKeys)
	}
}

func TestDeleteTimerRemovesFromIndex(t *testing.T) {
	s := openStore(t)
	key := model.NewKey(1, 1)
	tv := &model.TimerValue{DueDate: 100, Repetitions: 0}
	commit(t, s, func(tx *state.Tx) error { return tx.PutTimer(key, tv) })
	commit(t, s, func(tx *state.Tx) error { return tx.DeleteTimer(key, tv) })

	count := 0
	if err := s.DueTimers(1000, func(uint64, *model.TimerValue) error { count++; return nil }); err != nil {
		t.Fatalf("DueTimers: %v", err)
	}
	if count != 0 {
		t.Errorf("expected no timers after delete, got %d", count)
	}
}

func TestActiveChildrenCounter(t *testing.T) {
	s := openStore(t)
	scope := model.NewKey(1, 1)

	read := func() int32 {
		tx := s.NewTransaction()
		defer tx.Close()
		n, err := tx.ActiveChildren(scope)
		if err != nil {
			t.Fatalf("ActiveChildren: %v", err)
		}
		return n
	}

	// Absent scope reads as zero.
	if got := read(); got != 0 {
		t.Fatalf("initial count = %d, want 0", got)
	}

	// Read-your-writes within a transaction: a pending merge is visible.
	tx := s.NewTransaction()
	if err := tx.IncrementActiveChildren(scope); err != nil {
		t.Fatalf("Increment: %v", err)
	}
	if err := tx.IncrementActiveChildren(scope); err != nil {
		t.Fatalf("Increment: %v", err)
	}
	if n, err := tx.ActiveChildren(scope); err != nil || n != 2 {
		t.Fatalf("in-tx count = %d, %v, want 2", n, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	tx.Close()

	if got := read(); got != 2 {
		t.Fatalf("after +2 = %d, want 2", got)
	}

	// Decrement back to zero across separate transactions.
	commit(t, s, func(tx *state.Tx) error {
		if err := tx.DecrementActiveChildren(scope); err != nil {
			return err
		}
		return tx.DecrementActiveChildren(scope)
	})
	if got := read(); got != 0 {
		t.Fatalf("after -2 = %d, want 0", got)
	}
}

func TestActiveChildrenSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	scope := model.NewKey(1, 1)

	s1, err := state.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	tx := s1.NewTransaction()
	for range 3 {
		if err := tx.IncrementActiveChildren(scope); err != nil {
			t.Fatalf("Increment: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	tx.Close()
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The merged counter must fold correctly after reopening.
	s2, err := state.Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	tx2 := s2.NewTransaction()
	defer tx2.Close()
	if n, err := tx2.ActiveChildren(scope); err != nil || n != 3 {
		t.Errorf("after reopen = %d, %v, want 3", n, err)
	}
}

func TestLastAppliedPosition(t *testing.T) {
	s := openStore(t)
	if pos, err := s.LastAppliedPosition(); err != nil || pos != 0 {
		t.Fatalf("initial position = %d, %v, want 0, nil", pos, err)
	}
	commit(t, s, func(tx *state.Tx) error { return tx.SetLastAppliedPosition(4096) })
	if pos, err := s.LastAppliedPosition(); err != nil || pos != 4096 {
		t.Errorf("position = %d, %v, want 4096, nil", pos, err)
	}
}

// TestPersistsAcrossReopen is the durability check: state and the applied
// position committed before a clean close must be present after reopening,
// which is the foundation recovery builds on.
func TestPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	key := model.NewKey(2, 50)
	ei := &model.ElementInstanceValue{ProcessInstanceKey: model.NewKey(2, 1), ElementId: 9}

	s1, err := state.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	tx := s1.NewTransaction()
	if err := tx.PutElementInstance(key, ei); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := tx.SetLastAppliedPosition(777); err != nil {
		t.Fatalf("SetPosition: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	tx.Close()
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := state.Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	pos, err := s2.LastAppliedPosition()
	if err != nil || pos != 777 {
		t.Errorf("position after reopen = %d, %v, want 777", pos, err)
	}
	tx2 := s2.NewTransaction()
	defer tx2.Close()
	got, err := tx2.GetElementInstance(key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got, ei) {
		t.Errorf("element after reopen = %+v, want %+v", got, ei)
	}
}
