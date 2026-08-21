package state_test

import (
	"errors"
	"sort"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// errRead stands for any store read failure the walk must propagate.
var errRead = errors.New("store read failed")

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

// failingReader is a [state.Reader] whose two reads fail on demand, and which can
// serve a cyclic FlowScopeKey chain — the malformed inputs the shared walk guards
// against but a real store never produces.
type failingReader struct {
	varsErr   error
	elemErr   error
	flowScope map[uint64]uint64 // scope → its parent, for a hand-built chain
	vars      map[uint64][]model.VariableValue
}

func (r failingReader) VariablesOfScope(scope uint64, fn func(v *model.VariableValue) error) error {
	if r.varsErr != nil {
		return r.varsErr
	}
	for i := range r.vars[scope] {
		v := r.vars[scope][i]
		if err := fn(&v); err != nil {
			return err
		}
	}
	return nil
}

func (r failingReader) GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error) {
	if r.elemErr != nil {
		return nil, false, r.elemErr
	}
	parent, ok := r.flowScope[key]
	if !ok {
		return nil, false, nil // the process-instance root: no element instance
	}
	return &model.ElementInstanceValue{FlowScopeKey: parent}, true, nil
}

// TestVisibleVariablesMapAndLocals covers the two map-shaped reads every connector
// worker uses: the scope-chain one a worker binds FEEL against (nearest scope wins),
// and the single-scope one an outbound body uses when its task maps its inputs
// (ADR-draft-connector-payloads-are-the-input-mapping) — which must inherit nothing.
func TestVisibleVariablesMapAndLocals(t *testing.T) {
	root := model.NewKey(1, 1)
	leaf := model.NewKey(1, 20)
	r := failingReader{
		flowScope: map[uint64]uint64{leaf: root},
		vars: map[uint64][]model.VariableValue{
			root: {
				{Name: "inherited", Kind: model.VarString, Text: "R"},
				{Name: "shared", Kind: model.VarString, Text: "root"},
			},
			leaf: {{Name: "shared", Kind: model.VarString, Text: "leaf"}},
		},
	}

	chain, err := state.VisibleVariablesMap(r, leaf)
	if err != nil {
		t.Fatalf("VisibleVariablesMap: %v", err)
	}
	if len(chain) != 2 || chain["inherited"].Text != "R" || chain["shared"].Text != "leaf" {
		t.Errorf("chain read = %v, want the inherited value plus the nearer \"shared\"", chain)
	}

	locals, err := state.LocalVariablesMap(r, leaf)
	if err != nil {
		t.Fatalf("LocalVariablesMap: %v", err)
	}
	if len(locals) != 1 || locals["shared"].Text != "leaf" {
		t.Errorf("local read = %v, want only the leaf scope's own variable", locals)
	}
}

// TestVisibleVariablesErrorsAndCycle covers the walk's failure paths: either store
// read failing propagates rather than yielding a half-read scope, and a cyclic
// FlowScopeKey chain terminates on the depth guard instead of spinning.
func TestVisibleVariablesErrorsAndCycle(t *testing.T) {
	key := model.NewKey(1, 20)
	noop := func(*model.VariableValue) error { return nil }

	if _, err := state.VisibleVariablesMap(failingReader{varsErr: errRead}, key); err == nil {
		t.Error("a failing variable read: want an error")
	}
	if _, err := state.LocalVariablesMap(failingReader{varsErr: errRead}, key); err == nil {
		t.Error("a failing local variable read: want an error")
	}
	if err := state.VisibleVariables(failingReader{elemErr: errRead}, key, noop); err == nil {
		t.Error("a failing element-instance read: want an error")
	}

	// A chain that points at itself through a second scope: the walk must stop.
	other := model.NewKey(1, 21)
	cyclic := failingReader{flowScope: map[uint64]uint64{key: other, other: key}}
	if err := state.VisibleVariables(cyclic, key, noop); err != nil {
		t.Errorf("a cyclic chain: want it bounded by the depth guard, got %v", err)
	}
}
