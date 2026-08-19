package state

import (
	"testing"

	"github.com/pblumer/atlas/model"
)

// TestPurgeInstanceHistory writes a finished instance's terminal record plus several
// per-instance families, purges it, and confirms every family is gone — while another
// instance's history is untouched (ADR-0115).
func TestPurgeInstanceHistory(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	const defKey = uint64(9)
	piKey := model.NewKey(1, 1)
	other := model.NewKey(1, 2)

	tx := s.NewTransaction()
	// The target instance, with a spread of per-instance families populated.
	must(t, tx.PutProcessInstanceHistory(piKey, &model.ProcessInstanceValue{ProcessDefKey: defKey, State: model.PITerminated, CompletedPosition: 5}))
	must(t, tx.RecordElementStep(piKey, 1, 1, 7))
	must(t, tx.RecordElementVisit(defKey, piKey, 7))
	must(t, tx.RecordVariableSnapshot(1, 1, &model.VariableValue{ScopeKey: piKey, Name: "x", Kind: model.VarString, Text: "v"}))
	must(t, tx.PutVariable(&model.VariableValue{ScopeKey: piKey, Name: "x", Kind: model.VarString, Text: "v"}))
	// A second, unrelated finished instance that must survive the purge.
	must(t, tx.PutProcessInstanceHistory(other, &model.ProcessInstanceValue{ProcessDefKey: defKey, State: model.PICompleted, CompletedPosition: 6}))
	must(t, tx.RecordElementStep(other, 1, 2, 8))
	commit(t, tx)

	// Sanity: both present before the purge.
	if n := len(completedKeys(t, s)); n != 2 {
		t.Fatalf("history has %d instances before purge, want 2", n)
	}
	if len(stepsOf(t, s, piKey)) == 0 {
		t.Fatalf("target has no element steps before purge")
	}

	// Purge the target.
	tx = s.NewTransaction()
	must(t, tx.PurgeInstanceHistory(piKey, defKey, 0))
	commit(t, tx)

	// The target and all its families are gone; the other instance is intact.
	if got := completedKeys(t, s); len(got) != 1 || !got[other] {
		t.Fatalf("history after purge = %v, want only the other instance", got)
	}
	if n := len(stepsOf(t, s, piKey)); n != 0 {
		t.Errorf("element steps after purge = %d, want 0", n)
	}
	if n := len(varsOf(t, s, piKey)); n != 0 {
		t.Errorf("variables after purge = %d, want 0", n)
	}
	visits := 0
	if err := s.ElementVisitHistory(defKey, piKey, func(int32, int64) error { visits++; return nil }); err != nil {
		t.Fatalf("ElementVisitHistory: %v", err)
	}
	if visits != 0 {
		t.Errorf("element visits after purge = %d, want 0", visits)
	}
	// The other instance still has its step.
	if n := len(stepsOf(t, s, other)); n != 1 {
		t.Errorf("other instance's steps = %d, want 1 (untouched)", n)
	}
}

// TestPurgeInstanceHistoryIdempotent purges an instance with nothing stored: every
// delete is a no-op, no error.
func TestPurgeInstanceHistoryIdempotent(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	tx := s.NewTransaction()
	if err := tx.PurgeInstanceHistory(model.NewKey(1, 99), 1, 0); err != nil {
		t.Fatalf("PurgeInstanceHistory on empty: %v", err)
	}
	commit(t, tx)
}

// TestCompletedProcessInstancesFrom exercises the bounded, resumable window the
// retention sweep drives: a capped window reports more and a resume key; resuming
// reaches the end; a fn error propagates.
func TestCompletedProcessInstancesFrom(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	keys := []uint64{model.NewKey(1, 1), model.NewKey(1, 2), model.NewKey(1, 3)}
	tx := s.NewTransaction()
	for _, k := range keys {
		must(t, tx.PutProcessInstanceHistory(k, &model.ProcessInstanceValue{ProcessDefKey: 9, State: model.PICompleted}))
	}
	commit(t, tx)

	// A window of 2 over 3 records: two seen, more=true, resume at the third key.
	var seen []uint64
	next, more, err := s.CompletedProcessInstancesFrom(0, 2, func(k uint64, _ *model.ProcessInstanceValue) error {
		seen = append(seen, k)
		return nil
	})
	if err != nil {
		t.Fatalf("CompletedProcessInstancesFrom: %v", err)
	}
	if len(seen) != 2 || !more {
		t.Fatalf("first window: seen=%d more=%v, want 2 and true", len(seen), more)
	}
	if next != keys[1]+1 {
		t.Fatalf("resume key = %d, want %d", next, keys[1]+1)
	}

	// Resume: the rest fit, more=false (caller then restarts from genesis).
	seen = nil
	_, more, err = s.CompletedProcessInstancesFrom(next, 10, func(k uint64, _ *model.ProcessInstanceValue) error {
		seen = append(seen, k)
		return nil
	})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(seen) != 1 || more {
		t.Fatalf("resume window: seen=%d more=%v, want 1 and false", len(seen), more)
	}

	// A fn error is surfaced.
	sentinel := errSentinel{}
	if _, _, err := s.CompletedProcessInstancesFrom(0, 10, func(uint64, *model.ProcessInstanceValue) error {
		return sentinel
	}); err != sentinel {
		t.Fatalf("fn error = %v, want the sentinel", err)
	}
}

type errSentinel struct{}

func (errSentinel) Error() string { return "sentinel" }

// TestCompletedProcessInstancesFromDecodeError: a history record that cannot decode
// surfaces the error rather than silently skipping it.
func TestCompletedProcessInstancesFromDecodeError(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	// Write a too-short value under a history key so DecodeValue fails.
	if err := s.db.Set(keyProcessInstanceHistory(model.NewKey(1, 1)), []byte{0x01}, nil); err != nil {
		t.Fatalf("seed corrupt: %v", err)
	}
	if _, _, err := s.CompletedProcessInstancesFrom(0, 10, func(uint64, *model.ProcessInstanceValue) error {
		return nil
	}); err == nil {
		t.Fatalf("want a decode error for a corrupt history record")
	}
}

// --- small helpers ---

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("tx op: %v", err)
	}
}

func commit(t *testing.T, tx *Tx) {
	t.Helper()
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := tx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func completedKeys(t *testing.T, s *Store) map[uint64]bool {
	t.Helper()
	out := map[uint64]bool{}
	if err := s.CompletedProcessInstances(func(k uint64, _ *model.ProcessInstanceValue) error {
		out[k] = true
		return nil
	}); err != nil {
		t.Fatalf("CompletedProcessInstances: %v", err)
	}
	return out
}

func stepsOf(t *testing.T, s *Store, piKey uint64) []int32 {
	t.Helper()
	var out []int32
	if err := s.ElementStepHistory(piKey, func(_ int64, _ uint64, elementId int32) error {
		out = append(out, elementId)
		return nil
	}); err != nil {
		t.Fatalf("ElementStepHistory: %v", err)
	}
	return out
}

func varsOf(t *testing.T, s *Store, scope uint64) []string {
	t.Helper()
	var out []string
	if err := s.VariablesOfScope(scope, func(v *model.VariableValue) error {
		out = append(out, v.Name)
		return nil
	}); err != nil {
		t.Fatalf("VariablesOfScope: %v", err)
	}
	return out
}
