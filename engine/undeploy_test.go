package engine

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// TestRemoveStartRef covers the message-start index helper directly: it drops the
// first entry for a definition and returns the slice unchanged when it is absent.
func TestRemoveStartRef(t *testing.T) {
	refs := []messageStartRef{{defKey: 1, elementId: 0}, {defKey: 2, elementId: 1}, {defKey: 3, elementId: 2}}
	if got := removeStartRef(refs, 2); len(got) != 2 || got[0].defKey != 1 || got[1].defKey != 3 {
		t.Errorf("removeStartRef(refs, 2) = %v, want defKeys [1 3]", got)
	}
	if got := removeStartRef(refs, 9); len(got) != 3 {
		t.Errorf("removeStartRef(refs, 9) = %v, want the slice unchanged", got)
	}
	if got := removeStartRef(nil, 1); got != nil {
		t.Errorf("removeStartRef(nil, 1) = %v, want nil", got)
	}
}

// TestUndeploy removes a deployed definition so it can no longer be resolved by
// key. After Undeploy the processor cannot resolve the definition (a later
// CreateInstance for that key has nothing to run — the caller's responsibility
// per the method contract), which we assert directly: the definition is gone
// from the resolver map, while an unrelated deployment is untouched.
func TestUndeploy(t *testing.T) {
	b := compiler.NewBuilder(42, "gone", 1)
	b.AddStartEvent()
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	other := compiler.NewBuilder(43, "kept", 1)
	other.AddStartEvent()
	keep, err := other.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	p := New(1, nil, nil, nil)
	p.Deploy(cp)
	p.Deploy(keep)
	if p.processes[cp.Key] == nil {
		t.Fatal("Deploy did not register the definition")
	}

	p.Undeploy(cp.Key)
	if _, ok := p.processes[cp.Key]; ok {
		t.Errorf("after Undeploy, key %d still resolves, want removed", cp.Key)
	}
	if p.processes[keep.Key] == nil {
		t.Errorf("Undeploy removed unrelated definition %d", keep.Key)
	}
}
