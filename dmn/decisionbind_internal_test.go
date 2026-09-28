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

// TestDecisionModelKey is the whole of the runtime's version choice. A
// deployment-bound task reads its own snapshot; a fixed version reads exactly the
// decision deployment its deploy resolved; latest reads the newest decision
// deployment when the job is worked on a definition deployed under the runtime
// policy, the key frozen at deploy on one deployed under ADR-0319, and falls
// through to the ADR-0063 lookup on one deployed before either.
func TestDecisionModelKey(t *testing.T) {
	reg := NewRegistry()

	t.Run("deployment binding uses the process's own snapshot", func(t *testing.T) {
		cp, detail := bindProcess(t, compiler.BindingDeployment)
		// Even a pinned definition keeps deployment binding on its own key: the two
		// bindings follow different lineages, and pinning does not merge them.
		cp.PinDecisions(map[string]uint64{"eligibility": 1001})
		if key, ok, err := decisionModelKey(reg, cp, detail, "eligibility"); err != nil || !ok || key != cp.Key {
			t.Fatalf("decisionModelKey = %d, %v, %v; want %d, true", key, ok, err, cp.Key)
		}
	})

	t.Run("pinned latest binding uses the resolved decision deployment", func(t *testing.T) {
		cp, detail := bindProcess(t, compiler.BindingLatest)
		cp.PinDecisions(map[string]uint64{"eligibility": 1001})
		if key, ok, err := decisionModelKey(reg, cp, detail, "eligibility"); err != nil || !ok || key != 1001 {
			t.Fatalf("decisionModelKey = %d, %v, %v; want 1001, true", key, ok, err)
		}
	})

	t.Run("a definition deployed before pinning still resolves at runtime", func(t *testing.T) {
		cp, detail := bindProcess(t, compiler.BindingLatest)
		if key, ok, err := decisionModelKey(reg, cp, detail, "eligibility"); err != nil || ok {
			t.Fatalf("decisionModelKey = %d, %v, %v; want ok=false (legacy runtime latest)", key, ok, err)
		}
	})

	t.Run("runtime latest follows the newest decision deployment as it moves", func(t *testing.T) {
		live := NewRegistry()
		cp, detail := bindProcess(t, compiler.BindingLatest)
		cp.ResolveLatestAtRuntime(nil)
		// Nothing deployed on its own yet: the model bundled with the process answers.
		if key, ok, err := decisionModelKey(live, cp, detail, "eligibility"); err != nil || !ok || key != cp.Key {
			t.Fatalf("before any decision deployment: %d, %v, %v; want the bundle %d", key, ok, err, cp.Key)
		}
		if err := live.DeployDecision(2001, []byte(eligibilityModel)); err != nil {
			t.Fatal(err)
		}
		if key, _, _ := decisionModelKey(live, cp, detail, "eligibility"); key != 2001 {
			t.Fatalf("after v1: key %d, want 2001", key)
		}
		if err := live.DeployDecision(2002, []byte(eligibilityModel)); err != nil {
			t.Fatal(err)
		}
		if key, _, _ := decisionModelKey(live, cp, detail, "eligibility"); key != 2002 {
			t.Fatalf("after v2: key %d, want 2002 — the definition did not follow the new version", key)
		}
	})

	t.Run("a fixed version reads exactly its deployment, and never another", func(t *testing.T) {
		cp, detail := bindProcess(t, compiler.BindingVersion)
		detail.Version = 2
		cp.ResolveLatestAtRuntime(map[compiler.DecisionVersionRef]uint64{{DecisionID: "eligibility", Version: 2}: 3002})
		if key, ok, err := decisionModelKey(reg, cp, detail, "eligibility"); err != nil || !ok || key != 3002 {
			t.Fatalf("decisionModelKey = %d, %v, %v; want 3002, true", key, ok, err)
		}
		detail.Version = 3
		if _, _, err := decisionModelKey(reg, cp, detail, "eligibility"); err == nil {
			t.Fatal("a version the deploy never resolved evaluated something instead of failing")
		}
	})
}

// eligibilityModel provides the decision the binding tests name.
const eligibilityModel = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="e" name="E" namespace="http://atlas/test/bind">
  <decision id="dec_e" name="eligibility"><variable name="eligibility" typeRef="string"/>
    <literalExpression><text>"ok"</text></literalExpression></decision>
</definitions>`
