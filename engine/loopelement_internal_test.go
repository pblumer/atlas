package engine

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// TestSettingAnElementToleratesACollectionThatIsNotThere pins the fold's tolerance,
// and the reason for it. Setting one element of a list reads the list first, so it has
// to answer for a collection that is absent, is not a list, or is shorter than the
// index. Each is a no-op rather than an error, because the loop that seeds the
// collection and the loop that fills it are the same loop: a mismatch here is a bug to
// find in a test, not a reason to stop a partition — a returned error would abort the
// batch and take every unrelated instance in it down.
func TestSettingAnElementToleratesACollectionThatIsNotThere(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer func() { _ = store.Close() }()
	tx := store.NewTransaction()
	defer func() { _ = tx.Close() }()

	const scope = 42
	set := func(name string, idx int32) error {
		return setVariableElement(tx, &model.VariableValue{
			ScopeKey: scope, Name: name, Index: idx, Kind: model.VarString, Text: "v",
		})
	}

	if err := set("absent", 0); err != nil {
		t.Errorf("setting an element of a collection that does not exist: %v", err)
	}
	// A collection that is not a list: a scalar of the same name.
	if err := tx.PutVariable(&model.VariableValue{
		ScopeKey: scope, Name: "scalar", Kind: model.VarString, Text: "not a list", Index: -1,
	}); err != nil {
		t.Fatalf("PutVariable: %v", err)
	}
	if err := set("scalar", 0); err != nil {
		t.Errorf("setting an element of a scalar: %v", err)
	}
	if v, err := tx.GetVariable(scope, "scalar"); err != nil || v == nil || v.Text != "not a list" {
		t.Errorf("scalar = %v, want it untouched", v)
	}

	// A real list, and an index past its end.
	if err := tx.PutVariable(&model.VariableValue{
		ScopeKey: scope, Name: "list", Kind: model.VarJSON, Text: `[null,null]`, Index: -1,
	}); err != nil {
		t.Fatalf("PutVariable: %v", err)
	}
	for _, idx := range []int32{-1, 2, 99} {
		if err := set("list", idx); err != nil {
			t.Errorf("setting element %d of a two-element list: %v", idx, err)
		}
	}
	if v, err := tx.GetVariable(scope, "list"); err != nil || v == nil || v.Text != `[null,null]` {
		t.Errorf("list = %v, want it untouched by an out-of-range index", v)
	}

	// And the case that does apply.
	if err := set("list", 1); err != nil {
		t.Fatalf("setting element 1: %v", err)
	}
	if v, err := tx.GetVariable(scope, "list"); err != nil || v == nil || v.Text != `[null,"v"]` {
		t.Errorf("list = %v, want the second element set", v)
	}
}
