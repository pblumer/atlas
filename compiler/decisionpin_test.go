package compiler_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// buildPinProcess assembles a process with one latest-bound task, one
// deployment-bound task on a second decision, and one central (worker) decision —
// the three shapes deploy-time pinning has to tell apart.
func buildPinProcess(t *testing.T) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(500, "orders", 1)
	start := b.AddStartEvent()
	tracking, err := b.AddBusinessRuleTaskMapped("eligibility", "e", nil, nil, 3, compiler.BindingLatest)
	if err != nil {
		t.Fatalf("latest task: %v", err)
	}
	pinned, err := b.AddBusinessRuleTaskMapped("discount", "d", nil, nil, 3, compiler.BindingDeployment)
	if err != nil {
		t.Fatalf("deployment task: %v", err)
	}
	central, err := b.AddTemisDecisionTask("central", "risk", "r", nil, nil, 3)
	if err != nil {
		t.Fatalf("central task: %v", err)
	}
	end := b.AddEndEvent()
	b.Connect(start, tracking)
	b.Connect(tracking, pinned)
	b.Connect(pinned, central)
	b.Connect(central, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// TestLatestBoundDecisions is what a deploy resolves: exactly the local,
// latest-bound decisions. A deployment-bound task already names its model (this
// process's own snapshot) and a central decision resolves through its worker, so
// neither is pinned (ADR-0319).
func TestLatestBoundDecisions(t *testing.T) {
	cp := buildPinProcess(t)
	got := cp.LatestBoundDecisions()
	if len(got) != 1 || got[0] != "eligibility" {
		t.Fatalf("LatestBoundDecisions = %v, want [eligibility]", got)
	}
}

// TestPinnedDecisionKey covers the binding a deploy writes onto the definition and
// the worker reads back: a pinned definition answers with the exact decision
// deployment, and a definition that was never pinned — one deployed before
// deploy-time pinning existed — says so, which is what keeps its ADR-0063
// runtime-latest behavior reachable.
func TestPinnedDecisionKey(t *testing.T) {
	cp := buildPinProcess(t)
	if key, ok := cp.PinnedDecisionKey("eligibility"); ok {
		t.Fatalf("unpinned definition answered with key %d; want ok=false", key)
	}

	cp.PinDecisions(map[string]uint64{"eligibility": 1001})
	if key, ok := cp.PinnedDecisionKey("eligibility"); !ok || key != 1001 {
		t.Fatalf("PinnedDecisionKey = %d, %v; want 1001, true", key, ok)
	}
	// A decision the pin map does not carry falls back to the runtime lookup rather
	// than inventing a key.
	if key, ok := cp.PinnedDecisionKey("discount"); ok {
		t.Fatalf("PinnedDecisionKey(discount) = %d, true; want ok=false", key)
	}
	if !cp.DecisionsPinned() {
		t.Fatal("DecisionsPinned = false after pinning")
	}
}

// TestPinDecisionsEmptyStillMarksPinned proves the marker is the policy, not the
// map: a definition with no latest-bound task at all is still a pinned deployment,
// so nothing about it is left to a runtime lookup.
func TestPinDecisionsEmptyStillMarksPinned(t *testing.T) {
	b := compiler.NewBuilder(501, "plain", 1)
	start := b.AddStartEvent()
	end := b.AddEndEvent()
	b.Connect(start, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if cp.DecisionsPinned() {
		t.Fatal("a freshly compiled process reports itself pinned")
	}
	if got := cp.LatestBoundDecisions(); len(got) != 0 {
		t.Fatalf("LatestBoundDecisions = %v, want none", got)
	}
	cp.PinDecisions(nil)
	if !cp.DecisionsPinned() {
		t.Fatal("DecisionsPinned = false after PinDecisions(nil)")
	}
}
