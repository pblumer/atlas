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

	// Structured, and still not a list: a JSON object of the same name. It reaches
	// further into the fold than the scalar above — the kind check lets it through, and
	// what turns it away is that it has no elements to move.
	if err := tx.PutVariable(&model.VariableValue{
		ScopeKey: scope, Name: "object", Kind: model.VarJSON, Text: `{"a":1}`, Index: -1,
	}); err != nil {
		t.Fatalf("PutVariable: %v", err)
	}
	if err := set("object", 0); err != nil {
		t.Errorf("setting an element of a JSON object: %v", err)
	}
	if v, err := tx.GetVariable(scope, "object"); err != nil || v == nil || v.Text != `{"a":1}` {
		t.Errorf("object = %v, want it untouched", v)
	}

	// And the case that does apply.
	if err := set("list", 1); err != nil {
		t.Fatalf("setting element 1: %v", err)
	}
	if v, err := tx.GetVariable(scope, "list"); err != nil || v == nil || v.Text != `[null,"v"]` {
		t.Errorf("list = %v, want the second element set", v)
	}
}

// TestAListThatAlreadyHasElementsKeepsThemWhenItBecomesACollection covers the move a
// loop never asks for. Seeding writes nulls, so in a run the conversion has nothing to
// carry across — but the fold is written against a list, not against a loop's list, and
// a list that arrived some other way must not lose what it holds when one element of it
// is set.
func TestAListThatAlreadyHasElementsKeepsThemWhenItBecomesACollection(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer func() { _ = store.Close() }()
	tx := store.NewTransaction()
	defer func() { _ = tx.Close() }()

	const scope = 42
	if err := tx.PutVariable(&model.VariableValue{
		ScopeKey: scope, Name: "list", Kind: model.VarJSON, Text: `[1,"two",null,true]`, Index: -1,
	}); err != nil {
		t.Fatalf("PutVariable: %v", err)
	}
	if err := setVariableElement(tx, &model.VariableValue{
		ScopeKey: scope, Name: "list", Index: 2, Kind: model.VarString, Text: "filled",
	}); err != nil {
		t.Fatalf("setVariableElement: %v", err)
	}
	v, err := tx.GetVariable(scope, "list")
	if err != nil || v == nil {
		t.Fatalf("GetVariable: %v (%v)", err, v)
	}
	if want := `[1,"two","filled",true]`; v.Text != want {
		t.Errorf("list = %s, want %s — the elements it already held did not survive the move", v.Text, want)
	}
}
