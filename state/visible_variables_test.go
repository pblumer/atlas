package state_test

import (
	"sort"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// TestVisibleVariablesOfScope covers the scope-chain variable read that backs form
// prefill (ADR-0068): a leaf scope sees its own variables plus those inherited from
// each enclosing scope up to the process root, with the nearest scope shadowing a
// repeated name. A root read is unaffected (it has no enclosing scope).
func TestVisibleVariablesOfScope(t *testing.T) {
	s := openStore(t)

	root := model.NewKey(1, 1)  // process instance root scope
	mid := model.NewKey(1, 10)  // a subprocess body scope
	leaf := model.NewKey(1, 20) // a task's element-instance scope

	// The element-instance chain: leaf -> mid -> root. The root itself has no
	// element-instance record (it is the process instance), so the walk stops there.
	commit(t, s, func(tx *state.Tx) error {
		if err := tx.PutElementInstance(leaf, &model.ElementInstanceValue{
			ProcessInstanceKey: root, FlowScopeKey: mid,
		}); err != nil {
			return err
		}
		return tx.PutElementInstance(mid, &model.ElementInstanceValue{
			ProcessInstanceKey: root, FlowScopeKey: root,
		})
	})

	// Root carries the process variables (the intake case: vorname/benutzername
	// live at the root). mid shadows one name; leaf shadows another and adds a local.
	commit(t, s, func(tx *state.Tx) error {
		put := func(scope uint64, name, text string) error {
			return tx.PutVariable(&model.VariableValue{ScopeKey: scope, Name: name, Kind: model.VarString, Text: text})
		}
		if err := put(root, "vorname", "Markus"); err != nil {
			return err
		}
		if err := put(root, "shared", "root"); err != nil {
			return err
		}
		if err := put(mid, "shared", "mid"); err != nil { // shadows root's "shared"
			return err
		}
		if err := put(leaf, "shared", "leaf"); err != nil { // shadows mid's "shared"
			return err
		}
		return put(leaf, "local", "L")
	})

	collect := func(scope uint64) map[string]string {
		out := map[string]string{}
		if err := s.VisibleVariablesOfScope(scope, func(v *model.VariableValue) error {
			out[v.Name] = v.Text
			return nil
		}); err != nil {
			t.Fatalf("VisibleVariablesOfScope(%d): %v", scope, err)
		}
		return out
	}

	// From the leaf: inherits vorname from root, sees its own local, and the nearest
	// "shared" (leaf) wins over mid and root.
	got := collect(leaf)
	want := map[string]string{"vorname": "Markus", "shared": "leaf", "local": "L"}
	if len(got) != len(want) {
		keys := make([]string, 0, len(got))
		for k := range got {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Fatalf("leaf visible vars = %v (keys %v), want %v", got, keys, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("leaf[%q] = %q, want %q", k, got[k], v)
		}
	}

	// From mid: root's vorname, mid's "shared" over root's; no leaf-local.
	if m := collect(mid); m["shared"] != "mid" || m["vorname"] != "Markus" || m["local"] != "" {
		t.Errorf("mid visible vars = %v, want shared=mid vorname=Markus no local", m)
	}

	// A root read is exactly the root's own variables — unchanged for existing callers.
	if r := collect(root); r["shared"] != "root" || r["vorname"] != "Markus" || len(r) != 2 {
		t.Errorf("root visible vars = %v, want {vorname:Markus, shared:root}", r)
	}
}
