package engine

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// TestRemoveKey covers the message-start index helper directly: it drops the
// first matching key and returns the slice unchanged when the key is absent.
func TestRemoveKey(t *testing.T) {
	if got := removeKey([]uint64{1, 2, 3}, 2); len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Errorf("removeKey([1 2 3], 2) = %v, want [1 3]", got)
	}
	if got := removeKey([]uint64{1, 2, 3}, 9); len(got) != 3 {
		t.Errorf("removeKey([1 2 3], 9) = %v, want the slice unchanged", got)
	}
	if got := removeKey(nil, 1); got != nil {
		t.Errorf("removeKey(nil, 1) = %v, want nil", got)
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
