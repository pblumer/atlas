package state_test

import (
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// seedCollection writes the stub of a collection under construction plus whichever
// elements the caller names, the way the fold does: the record says how long the list
// is, and the elements sit beside it under their own keys.
func seedCollection(t *testing.T, s *state.Store, scope uint64, name string, parts int32, elems map[int32]string) {
	t.Helper()
	commit(t, s, func(tx *state.Tx) error {
		if err := tx.PutVariable(&model.VariableValue{
			ScopeKey: scope, Name: name, Kind: model.VarJSON, Parts: parts,
		}); err != nil {
			return err
		}
		for i, e := range elems {
			if err := tx.PutVariableElement(scope, name, i, e); err != nil {
				return err
			}
		}
		return nil
	})
}

// scopeVariables reads a scope's variables off the committed store — the path an
// operator's variable view, a connector's payload and a FEEL scope all take.
func scopeVariables(t *testing.T, s *state.Store, scope uint64) map[string]model.VariableValue {
	t.Helper()
	out := map[string]model.VariableValue{}
	if err := s.VariablesOfScope(scope, func(v *model.VariableValue) error {
		out[v.Name] = *v
		return nil
	}); err != nil {
		t.Fatalf("VariablesOfScope: %v", err)
	}
	return out
}

// getVariable reads one variable through a transaction — the path the engine takes.
func getVariable(t *testing.T, s *state.Store, scope uint64, name string) *model.VariableValue {
	t.Helper()
	tx := s.NewTransaction()
	defer tx.Close()
	v, err := tx.GetVariable(scope, name)
	if err != nil {
		t.Fatalf("GetVariable: %v", err)
	}
	return v
}

// The contract of the whole form: a collection held one key per element reads back as
// the list it stands for, and reads back the same on both paths. A caller never learns
// that the value was stored in pieces — which is what lets a loop change how it writes
// without any reader changing how it reads.
//
// A slot no iteration has written yet is null, the same null the collection was seeded
// with.
func TestACollectionReadsBackAsTheListItStandsFor(t *testing.T) {
	s := openStore(t)
	const scope, name = 10, "results"
	seedCollection(t, s, scope, name, 4, map[int32]string{0: `"a"`, 2: `{"n":1}`})

	want := `["a",null,{"n":1},null]`
	if got := getVariable(t, s, scope, name); got == nil || got.Text != want {
		t.Errorf("GetVariable text = %q, want %q", textOf(got), want)
	}
	if got := scopeVariables(t, s, scope)[name]; got.Text != want {
		t.Errorf("VariablesOfScope text = %q, want %q", got.Text, want)
	}
}

// A reader must never meet the stub. Parts is how the storage layer tells the two
// forms apart, so a record that still carried it would be one a caller could act on —
// an empty text that looks like an empty value.
func TestAnAssembledCollectionNoLongerCallsItselfOne(t *testing.T) {
	s := openStore(t)
	const scope, name = 10, "results"
	seedCollection(t, s, scope, name, 2, map[int32]string{1: "7"})

	got := getVariable(t, s, scope, name)
	if got == nil {
		t.Fatal("GetVariable returned nothing")
	}
	if got.Parts != 0 {
		t.Errorf("Parts = %d, want 0 — an assembled collection is an ordinary record", got.Parts)
	}
	if got.Kind != model.VarJSON {
		t.Errorf("Kind = %v, want VarJSON", got.Kind)
	}
}

// The elements of "a" and of "ab" must not answer each other's scans. The element key
// carries the index *after* the name, so without a separator one name's keys would be
// a prefix of the other's — and the shorter collection would read the longer one's
// elements as its own.
func TestOneCollectionDoesNotReadAnothersElements(t *testing.T) {
	s := openStore(t)
	const scope = 10
	seedCollection(t, s, scope, "a", 2, map[int32]string{0: `"from-a"`, 1: `"from-a"`})
	seedCollection(t, s, scope, "ab", 2, map[int32]string{0: `"from-ab"`, 1: `"from-ab"`})

	vars := scopeVariables(t, s, scope)
	if got, want := vars["a"].Text, `["from-a","from-a"]`; got != want {
		t.Errorf(`variable "a" = %s, want %s`, got, want)
	}
	if got, want := vars["ab"].Text, `["from-ab","from-ab"]`; got != want {
		t.Errorf(`variable "ab" = %s, want %s`, got, want)
	}
}

// Dropping the record has to drop the elements with it. A scope tearing down deletes
// its variables one by one, and an element left behind would outlive everything that
// could ever name it — and would reappear the moment a later run made a collection of
// that name again.
func TestDroppingACollectionDropsItsElements(t *testing.T) {
	s := openStore(t)
	const scope, name = 10, "results"
	seedCollection(t, s, scope, name, 3, map[int32]string{0: "1", 1: "2", 2: "3"})

	commit(t, s, func(tx *state.Tx) error { return tx.DeleteVariable(scope, name) })
	if got := getVariable(t, s, scope, name); got != nil {
		t.Fatalf("the variable is still there: %+v", got)
	}

	// The elements are gone, not merely unreachable: a collection seeded again under
	// the same name reads back as the nulls it was seeded with.
	seedCollection(t, s, scope, name, 3, nil)
	if got, want := getVariable(t, s, scope, name).Text, "[null,null,null]"; got != want {
		t.Errorf("a re-seeded collection = %s, want %s — the run before left its elements behind", got, want)
	}
}

// A variable that is not a collection is untouched by any of this: the read path costs
// it the Parts check and nothing more, and its value is where it always was.
func TestAnOrdinaryVariableIsUnaffected(t *testing.T) {
	s := openStore(t)
	const scope = 10
	commit(t, s, func(tx *state.Tx) error {
		return tx.PutVariable(&model.VariableValue{
			ScopeKey: scope, Name: "ticket", Kind: model.VarString, Text: "PAT-9",
		})
	})
	got := getVariable(t, s, scope, "ticket")
	if got == nil || got.Text != "PAT-9" || got.Kind != model.VarString {
		t.Errorf("value = %+v, want the string it was written as", got)
	}
}

func textOf(v *model.VariableValue) string {
	if v == nil {
		return "<absent>"
	}
	return v.Text
}
