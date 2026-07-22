package state_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

func TestOpenMkdirError(t *testing.T) {
	// Opening under a path whose parent is a regular file makes MkdirAll fail,
	// exercising Open's early error return.
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := state.Open(filepath.Join(f, "sub")); err == nil {
		t.Errorf("Open under a file: err = nil, want error")
	}
}

func TestStoreGetJob(t *testing.T) {
	s := openStore(t)
	key := model.NewKey(1, 7)
	jv := &model.JobValue{
		ProcessInstanceKey: model.NewKey(1, 1),
		ElementInstanceKey: model.NewKey(1, 2),
		JobType:            5,
		Retries:            3,
		Deadline:           99,
	}

	// Absent job.
	if got, ok, err := s.GetJob(key); err != nil || ok || got != nil {
		t.Fatalf("absent GetJob = %v, %v, %v, want nil,false,nil", got, ok, err)
	}

	commit(t, s, func(tx *state.Tx) error { return tx.PutJob(key, jv) })

	got, ok, err := s.GetJob(key)
	if err != nil || !ok {
		t.Fatalf("GetJob = %v, ok=%v, err=%v", got, ok, err)
	}
	if !reflect.DeepEqual(got, jv) {
		t.Errorf("GetJob = %+v, want %+v", got, jv)
	}
}

func TestStoreGetElementInstance(t *testing.T) {
	s := openStore(t)
	key := model.NewKey(1, 7)
	ei := &model.ElementInstanceValue{
		ProcessInstanceKey: model.NewKey(1, 1),
		ProcessDefKey:      model.NewKey(1, 2),
		ElementId:          4,
		FlowScopeKey:       model.NewKey(1, 1),
		BpmnElementType:    2,
	}

	if got, ok, err := s.GetElementInstance(key); err != nil || ok || got != nil {
		t.Fatalf("absent = %v, %v, %v, want nil,false,nil", got, ok, err)
	}

	commit(t, s, func(tx *state.Tx) error { return tx.PutElementInstance(key, ei) })

	got, ok, err := s.GetElementInstance(key)
	if err != nil || !ok {
		t.Fatalf("GetElementInstance ok=%v err=%v", ok, err)
	}
	if !reflect.DeepEqual(got, ei) {
		t.Errorf("GetElementInstance = %+v, want %+v", got, ei)
	}
}

func TestProcessInstancePutGetDelete(t *testing.T) {
	s := openStore(t)
	key := model.NewKey(1, 3)
	pi := &model.ProcessInstanceValue{ProcessDefKey: model.NewKey(1, 9)}

	// Absent.
	tx0 := s.NewTransaction()
	if got, err := tx0.GetProcessInstance(key); err != nil || got != nil {
		t.Fatalf("absent GetProcessInstance = %v, %v", got, err)
	}
	tx0.Close()

	commit(t, s, func(tx *state.Tx) error { return tx.PutProcessInstance(key, pi) })

	tx := s.NewTransaction()
	got, err := tx.GetProcessInstance(key)
	if err != nil {
		t.Fatalf("GetProcessInstance: %v", err)
	}
	if !reflect.DeepEqual(got, pi) {
		t.Errorf("GetProcessInstance = %+v, want %+v", got, pi)
	}
	// GetProcessInstanceInto path.
	var into model.ProcessInstanceValue
	ok, err := tx.GetProcessInstanceInto(key, &into)
	if err != nil || !ok {
		t.Fatalf("GetProcessInstanceInto ok=%v err=%v", ok, err)
	}
	if into != *pi {
		t.Errorf("into = %+v, want %+v", into, *pi)
	}
	tx.Close()

	commit(t, s, func(tx *state.Tx) error { return tx.DeleteProcessInstance(key) })

	tx2 := s.NewTransaction()
	defer tx2.Close()
	if got, err := tx2.GetProcessInstance(key); err != nil || got != nil {
		t.Errorf("after delete = %v, %v, want nil,nil", got, err)
	}
}

func TestJobPutGetDelete(t *testing.T) {
	s := openStore(t)
	key := model.NewKey(1, 3)
	jv := &model.JobValue{ProcessInstanceKey: model.NewKey(1, 1), JobType: 8, Retries: 1, Deadline: 5}

	tx0 := s.NewTransaction()
	if got, err := tx0.GetJob(key); err != nil || got != nil {
		t.Fatalf("absent GetJob = %v, %v", got, err)
	}
	tx0.Close()

	commit(t, s, func(tx *state.Tx) error { return tx.PutJob(key, jv) })

	tx := s.NewTransaction()
	got, err := tx.GetJob(key)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if !reflect.DeepEqual(got, jv) {
		t.Errorf("GetJob = %+v, want %+v", got, jv)
	}
	var into model.JobValue
	ok, err := tx.GetJobInto(key, &into)
	if err != nil || !ok {
		t.Fatalf("GetJobInto ok=%v err=%v", ok, err)
	}
	if into != *jv {
		t.Errorf("GetJobInto = %+v, want %+v", into, *jv)
	}
	tx.Close()
}

func TestVariablePutGetAndScope(t *testing.T) {
	s := openStore(t)
	scope := model.NewKey(1, 1)
	other := model.NewKey(1, 2)

	vars := []*model.VariableValue{
		{ScopeKey: scope, Name: "a", Kind: model.VarNumber, Text: "1"},
		{ScopeKey: scope, Name: "b", Kind: model.VarString, Text: "hi"},
		{ScopeKey: scope, Name: "c", Kind: model.VarBool, Bool: true},
	}

	// Absent variable read.
	tx0 := s.NewTransaction()
	if got, err := tx0.GetVariable(scope, "missing"); err != nil || got != nil {
		t.Fatalf("absent GetVariable = %v, %v", got, err)
	}
	tx0.Close()

	commit(t, s, func(tx *state.Tx) error {
		for _, v := range vars {
			if err := tx.PutVariable(v); err != nil {
				return err
			}
		}
		// A variable in another scope must not appear in scope's scan.
		return tx.PutVariable(&model.VariableValue{ScopeKey: other, Name: "z", Kind: model.VarString, Text: "no"})
	})

	tx := s.NewTransaction()
	got, err := tx.GetVariable(scope, "b")
	if err != nil {
		t.Fatalf("GetVariable: %v", err)
	}
	want := &model.VariableValue{ScopeKey: scope, Name: "b", Kind: model.VarString, Text: "hi"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GetVariable = %+v, want %+v", got, want)
	}
	tx.Close()

	names := map[string]bool{}
	if err := s.VariablesOfScope(scope, func(v *model.VariableValue) error {
		names[v.Name] = true
		return nil
	}); err != nil {
		t.Fatalf("VariablesOfScope: %v", err)
	}
	if !reflect.DeepEqual(names, map[string]bool{"a": true, "b": true, "c": true}) {
		t.Errorf("VariablesOfScope names = %v, want a,b,c", names)
	}
}

func TestActiveInstanceQueries(t *testing.T) {
	s := openStore(t)

	// Empty store.
	if n, err := s.ActiveProcessInstanceCount(); err != nil || n != 0 {
		t.Fatalf("empty ActiveProcessInstanceCount = %d, %v", n, err)
	}
	if n, err := s.ActiveElementInstanceCount(); err != nil || n != 0 {
		t.Fatalf("empty ActiveElementInstanceCount = %d, %v", n, err)
	}

	procs := map[uint64]*model.ProcessInstanceValue{
		model.NewKey(1, 10): {ProcessDefKey: model.NewKey(1, 1)},
		model.NewKey(1, 11): {ProcessDefKey: model.NewKey(1, 1)},
	}
	els := map[uint64]*model.ElementInstanceValue{
		model.NewKey(1, 20): {ProcessInstanceKey: model.NewKey(1, 10), ElementId: 1},
		model.NewKey(1, 21): {ProcessInstanceKey: model.NewKey(1, 10), ElementId: 2},
		model.NewKey(1, 22): {ProcessInstanceKey: model.NewKey(1, 11), ElementId: 3},
	}
	commit(t, s, func(tx *state.Tx) error {
		for k, v := range procs {
			if err := tx.PutProcessInstance(k, v); err != nil {
				return err
			}
		}
		for k, v := range els {
			if err := tx.PutElementInstance(k, v); err != nil {
				return err
			}
		}
		return nil
	})

	if n, err := s.ActiveProcessInstanceCount(); err != nil || n != len(procs) {
		t.Errorf("ActiveProcessInstanceCount = %d, %v, want %d", n, err, len(procs))
	}
	if n, err := s.ActiveElementInstanceCount(); err != nil || n != len(els) {
		t.Errorf("ActiveElementInstanceCount = %d, %v, want %d", n, err, len(els))
	}

	gotProcs := map[uint64]*model.ProcessInstanceValue{}
	if err := s.ActiveProcessInstances(func(k uint64, v *model.ProcessInstanceValue) error {
		cp := *v
		gotProcs[k] = &cp
		return nil
	}); err != nil {
		t.Fatalf("ActiveProcessInstances: %v", err)
	}
	if !reflect.DeepEqual(gotProcs, procs) {
		t.Errorf("ActiveProcessInstances = %+v, want %+v", gotProcs, procs)
	}

	gotEls := map[uint64]*model.ElementInstanceValue{}
	if err := s.ActiveElementInstances(func(k uint64, v *model.ElementInstanceValue) error {
		cp := *v
		gotEls[k] = &cp
		return nil
	}); err != nil {
		t.Fatalf("ActiveElementInstances: %v", err)
	}
	if !reflect.DeepEqual(gotEls, els) {
		t.Errorf("ActiveElementInstances = %+v, want %+v", gotEls, els)
	}
}

// TestScanCallbackErrorPropagates covers the fn-returns-error branch of the
// scan machinery (scanRange) across the store's scanning queries.
func TestScanCallbackErrorPropagates(t *testing.T) {
	s := openStore(t)
	sentinel := errors.New("stop")
	commit(t, s, func(tx *state.Tx) error {
		if err := tx.PutProcessInstance(model.NewKey(1, 1), &model.ProcessInstanceValue{}); err != nil {
			return err
		}
		if err := tx.PutElementInstance(model.NewKey(1, 2), &model.ElementInstanceValue{ProcessInstanceKey: model.NewKey(1, 1)}); err != nil {
			return err
		}
		if err := tx.PutJob(model.NewKey(1, 3), &model.JobValue{JobType: 1}); err != nil {
			return err
		}
		if err := tx.PutTimer(model.NewKey(1, 4), &model.TimerValue{DueDate: 1}); err != nil {
			return err
		}
		return tx.PutVariable(&model.VariableValue{ScopeKey: model.NewKey(1, 1), Name: "v"})
	})

	fail := func(name string, run func(func() error) error) {
		if err := run(func() error { return sentinel }); !errors.Is(err, sentinel) {
			t.Errorf("%s error = %v, want sentinel", name, err)
		}
	}

	fail("ActiveProcessInstances", func(stop func() error) error {
		return s.ActiveProcessInstances(func(uint64, *model.ProcessInstanceValue) error { return stop() })
	})
	fail("ActiveElementInstances", func(stop func() error) error {
		return s.ActiveElementInstances(func(uint64, *model.ElementInstanceValue) error { return stop() })
	})
	fail("ElementInstancesOfProcess", func(stop func() error) error {
		return s.ElementInstancesOfProcess(model.NewKey(1, 1), func(uint64) error { return stop() })
	})
	fail("ActivatableJobs", func(stop func() error) error {
		return s.ActivatableJobs(1, func(uint64) error { return stop() })
	})
	fail("DueTimers", func(stop func() error) error {
		return s.DueTimers(1000, func(uint64, *model.TimerValue) error { return stop() })
	})
	fail("VariablesOfScope", func(stop func() error) error {
		return s.VariablesOfScope(model.NewKey(1, 1), func(*model.VariableValue) error { return stop() })
	})
}
