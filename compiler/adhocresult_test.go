package compiler

import "testing"

// SetAdHocResultCollection names where an agent-driven ad-hoc's tool results
// accumulate. It takes the collection by *name* because the name is an interned
// index inside the detail, which only a builder can mint — and it had no test.
//
// The two guards are the point. Both are silent no-ops rather than panics, so
// nothing tells a caller it addressed the wrong node; only a test does.
func TestSetAdHocResultCollectionInternsTheName(t *testing.T) {
	b := NewBuilder(1, "p", 1)
	node := b.AddAdHocSubProcess(AdHocDetail{AgentWorker: -1, AgentModel: -1, ResultCollection: -1})
	b.SetAdHocResultCollection(node, "findings", nil)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	d := cp.AdHoc(cp.Node(node).Detail)
	if got := cp.Intern(d.ResultCollection); got != "findings" {
		t.Errorf("result collection = %q, want the interned name back", got)
	}
}

// An empty collection clears it, which is how an author removes the accumulation
// rather than leaving the previous name in place.
func TestSetAdHocResultCollectionClearsOnAnEmptyName(t *testing.T) {
	b := NewBuilder(1, "p", 1)
	node := b.AddAdHocSubProcess(AdHocDetail{AgentWorker: -1, AgentModel: -1, ResultCollection: -1})
	b.SetAdHocResultCollection(node, "findings", nil)
	b.SetAdHocResultCollection(node, "", nil)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	d := cp.AdHoc(cp.Node(node).Detail)
	if got := cp.Intern(d.ResultCollection); got != "" {
		t.Errorf("result collection = %q after being cleared, want empty", got)
	}
}

// A node that is not an ad-hoc subprocess, and an id that is no node at all, are
// both ignored. The detail table is indexed by the node's own Detail, so writing
// through the wrong node would corrupt an unrelated element's detail — which is why
// the guard is there and why its absence would not show up as a crash.
func TestSetAdHocResultCollectionIgnoresAWrongNode(t *testing.T) {
	b := NewBuilder(1, "p", 1)
	plain := b.AddSubProcess()
	b.SetAdHocResultCollection(plain, "findings", nil)
	b.SetAdHocResultCollection(-1, "findings", nil)
	b.SetAdHocResultCollection(9999, "findings", nil)
	if _, err := b.Build(); err != nil {
		t.Fatalf("Build after the ignored writes: %v", err)
	}
}
