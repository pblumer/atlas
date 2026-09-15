package dmn

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// bindProcess builds a one-task process with the given binding, so the seam that
// picks which deployment a business rule task evaluates against can be exercised
// without a running engine.
func bindProcess(t *testing.T, binding compiler.DecisionBinding) (*compiler.CompiledProcess, *compiler.BusinessRuleTaskDetail) {
	t.Helper()
	b := compiler.NewBuilder(77, "orders", 1)
	start := b.AddStartEvent()
	rule, err := b.AddBusinessRuleTaskMapped("eligibility", "e", nil, nil, 3, binding)
	if err != nil {
		t.Fatalf("AddBusinessRuleTaskMapped: %v", err)
	}
	end := b.AddEndEvent()
	b.Connect(start, rule)
	b.Connect(rule, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.BusinessRuleTask(cp.Node(rule).Detail)
}

// TestDecisionModelKey is the whole of the runtime's version choice, which after
// ADR-0319 is meant to be no choice at
// all: a deployment-bound task reads its own snapshot, a pinned latest-bound task
// reads the decision deployment its deploy resolved, and only a definition
// deployed before pinning falls through to the ADR-0063 runtime lookup.
func TestDecisionModelKey(t *testing.T) {
	t.Run("deployment binding uses the process's own snapshot", func(t *testing.T) {
		cp, detail := bindProcess(t, compiler.BindingDeployment)
		// Even a pinned definition keeps deployment binding on its own key: the two
		// bindings follow different lineages, and pinning does not merge them.
		cp.PinDecisions(map[string]uint64{"eligibility": 1001})
		if key, ok := decisionModelKey(cp, detail, "eligibility"); !ok || key != cp.Key {
			t.Fatalf("decisionModelKey = %d, %v; want %d, true", key, ok, cp.Key)
		}
	})

	t.Run("pinned latest binding uses the resolved decision deployment", func(t *testing.T) {
		cp, detail := bindProcess(t, compiler.BindingLatest)
		cp.PinDecisions(map[string]uint64{"eligibility": 1001})
		if key, ok := decisionModelKey(cp, detail, "eligibility"); !ok || key != 1001 {
			t.Fatalf("decisionModelKey = %d, %v; want 1001, true", key, ok)
		}
	})

	t.Run("a definition deployed before pinning still resolves at runtime", func(t *testing.T) {
		cp, detail := bindProcess(t, compiler.BindingLatest)
		if key, ok := decisionModelKey(cp, detail, "eligibility"); ok {
			t.Fatalf("decisionModelKey = %d, true; want ok=false (legacy runtime latest)", key)
		}
	})
}
