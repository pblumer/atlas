package state_test

import (
	"sort"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// TestTxElementInstancesOfProcess covers the transaction-scoped element scan a
// parallel join uses: it must see element instances of the target process
// instance — including one written earlier in the same uncommitted transaction —
// and none belonging to another instance.
func TestTxElementInstancesOfProcess(t *testing.T) {
	s := openStore(t)
	pi := model.NewKey(1, 1)
	other := model.NewKey(1, 2)

	// Commit two element instances for pi and one for another instance.
	commit(t, s, func(tx *state.Tx) error {
		if err := tx.PutElementInstance(model.NewKey(1, 10), &model.ElementInstanceValue{ProcessInstanceKey: pi, ElementId: 5}); err != nil {
			return err
		}
		if err := tx.PutElementInstance(model.NewKey(1, 11), &model.ElementInstanceValue{ProcessInstanceKey: pi, ElementId: 7}); err != nil {
			return err
		}
		return tx.PutElementInstance(model.NewKey(1, 12), &model.ElementInstanceValue{ProcessInstanceKey: other, ElementId: 5})
	})

	// A fresh transaction that writes a third pi element must see it too (the
	// batch's own uncommitted write), plus the two committed ones — but not the
	// other instance's element.
	tx := s.NewTransaction()
	defer tx.Close()
	if err := tx.PutElementInstance(model.NewKey(1, 13), &model.ElementInstanceValue{ProcessInstanceKey: pi, ElementId: 9}); err != nil {
		t.Fatalf("PutElementInstance: %v", err)
	}

	var gotElems []int32
	if err := tx.ElementInstancesOfProcess(pi, func(_ uint64, v *model.ElementInstanceValue) error {
		gotElems = append(gotElems, v.ElementId)
		return nil
	}); err != nil {
		t.Fatalf("ElementInstancesOfProcess: %v", err)
	}
	sort.Slice(gotElems, func(i, j int) bool { return gotElems[i] < gotElems[j] })
	want := []int32{5, 7, 9}
	if len(gotElems) != len(want) {
		t.Fatalf("elements = %v, want %v (committed + this batch's write, excluding the other instance)", gotElems, want)
	}
	for i := range want {
		if gotElems[i] != want[i] {
			t.Fatalf("elements = %v, want %v", gotElems, want)
		}
	}
}
