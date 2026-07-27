package engine

import (
	"testing"

	"github.com/pblumer/atlas/model"
)

// TestResolveInChain exercises the pure scope-chain walk: a hit at the start scope,
// inheritance from a parent, nearest-scope-wins shadowing, a miss that walks to the
// root, and the cycle guard.
func TestResolveInChain(t *testing.T) {
	// Scopes: 1 (root) → child 2 → grandchild 3.
	parents := map[uint64]uint64{3: 2, 2: 1} // 1 has no parent (root)
	parentOf := func(s uint64) (uint64, bool) { p, ok := parents[s]; return p, ok }
	vars := map[uint64]map[string]*model.VariableValue{
		1: {"root": {ScopeKey: 1, Name: "root", Kind: model.VarString, Text: "r"}, "shadowed": {ScopeKey: 1, Name: "shadowed", Text: "from-root"}},
		2: {"mid": {ScopeKey: 2, Name: "mid", Text: "m"}},
		3: {"shadowed": {ScopeKey: 3, Name: "shadowed", Text: "from-3"}},
	}
	getVar := func(s uint64, name string) *model.VariableValue {
		if m := vars[s]; m != nil {
			return m[name]
		}
		return nil
	}

	// Hit at the start scope.
	if v := resolveInChain(3, "shadowed", getVar, parentOf); v == nil || v.Text != "from-3" {
		t.Errorf("shadowed from 3 = %+v, want from-3 (nearest wins)", v)
	}
	// Inherited from a parent scope.
	if v := resolveInChain(3, "mid", getVar, parentOf); v == nil || v.Text != "m" {
		t.Errorf("mid from 3 = %+v, want inherited m", v)
	}
	// Inherited from the root.
	if v := resolveInChain(3, "root", getVar, parentOf); v == nil || v.Text != "r" {
		t.Errorf("root from 3 = %+v, want inherited r", v)
	}
	// Missing everywhere → nil after walking to the root.
	if v := resolveInChain(3, "nope", getVar, parentOf); v != nil {
		t.Errorf("nope = %+v, want nil", v)
	}
	// A hit at the start scope short-circuits (no parent needed).
	if v := resolveInChain(1, "root", getVar, parentOf); v == nil || v.Text != "r" {
		t.Errorf("root from 1 = %+v, want r", v)
	}

	// Cycle guard: a chain that never terminates must not hang and resolves to nil.
	cyclic := func(s uint64) (uint64, bool) { return s + 1, true } // 1→2→3→… forever
	empty := func(uint64, string) *model.VariableValue { return nil }
	if v := resolveInChain(1, "x", empty, cyclic); v != nil {
		t.Errorf("cyclic chain = %+v, want nil (guarded)", v)
	}
}

// TestBindInputsChainBuiltin checks bindInputsChain resolves the processInstanceKey
// built-in to the owning instance's key from any start scope: an activity-local
// scope resolves via its element instance's ProcessInstanceKey, and the process-
// instance root scope (no element instance) resolves to itself (ADR-0068).
func TestBindInputsChainBuiltin(t *testing.T) {
	s := openStore(t)
	p := &Processor{store: s, clock: &wbClock{}}
	tx := s.NewTransaction()

	const root uint64 = 1000
	const local uint64 = 2000
	if err := tx.PutElementInstance(local, &model.ElementInstanceValue{ProcessInstanceKey: root, FlowScopeKey: root}); err != nil {
		t.Fatalf("PutElementInstance: %v", err)
	}
	c := &ProcessingContext{p: p, tx: tx}

	// From the local scope, the built-in is the owning process-instance key.
	got := bindInputsChain(c, []string{builtinProcessInstanceKey}, local)
	if v, ok := got[builtinProcessInstanceKey]; !ok || v.String() != "1000" {
		t.Errorf("builtin from local = %q (ok=%v), want 1000", v.String(), ok)
	}
	// From the root scope (no element instance), the built-in is that scope itself.
	got = bindInputsChain(c, []string{builtinProcessInstanceKey}, root)
	if v, ok := got[builtinProcessInstanceKey]; !ok || v.String() != "1000" {
		t.Errorf("builtin from root = %q (ok=%v), want 1000", v.String(), ok)
	}
	// No inputs returns a nil map (the allocation-free fast path).
	if got := bindInputsChain(c, nil, local); got != nil {
		t.Errorf("bindInputsChain(nil) = %v, want nil", got)
	}
}

// TestResolveVariableChain wires the walk to live element/variable state through a
// transaction: a local scope inherits from the root, and a local variable shadows
// an inherited one of the same name.
func TestResolveVariableChain(t *testing.T) {
	s := openStore(t)
	p := &Processor{store: s, clock: &wbClock{}}
	tx := s.NewTransaction()

	const root uint64 = 1000
	const local uint64 = 2000
	// The local scope is an element instance whose flow scope is the root.
	if err := tx.PutElementInstance(local, &model.ElementInstanceValue{ProcessInstanceKey: root, FlowScopeKey: root}); err != nil {
		t.Fatalf("PutElementInstance: %v", err)
	}
	put := func(scope uint64, name, text string) {
		if err := tx.PutVariable(&model.VariableValue{ScopeKey: scope, Name: name, Kind: model.VarString, Text: text}); err != nil {
			t.Fatalf("PutVariable: %v", err)
		}
	}
	put(root, "inherited", "from-root")
	put(root, "name", "root-value")
	put(local, "name", "local-value") // shadows the root's "name"
	put(local, "onlyLocal", "local-only")

	c := &ProcessingContext{p: p, tx: tx}

	cases := []struct {
		scope    uint64
		name     string
		wantText string // "" means expect nil
	}{
		{local, "onlyLocal", "local-only"},
		{local, "inherited", "from-root"}, // walked up to root
		{local, "name", "local-value"},    // nearest wins
		{local, "missing", ""},            // not found anywhere
		{root, "name", "root-value"},      // reading the root directly
	}
	for _, tt := range cases {
		got := c.ResolveVariable(tt.scope, tt.name)
		if tt.wantText == "" {
			if got != nil {
				t.Errorf("ResolveVariable(%d,%q) = %+v, want nil", tt.scope, tt.name, got)
			}
			continue
		}
		if got == nil || got.Text != tt.wantText {
			t.Errorf("ResolveVariable(%d,%q) = %+v, want text %q", tt.scope, tt.name, got, tt.wantText)
		}
	}
}
