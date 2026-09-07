package engine

import "testing"

// TestExecutionBudgetDefaultsWhenUnset pins the "no way to turn it off" rule: an
// unset, zero or negative budget all mean DefaultExecutionBudget, not "unbounded".
// Unbounded is the behaviour the budget exists to remove, so it is not on offer.
func TestExecutionBudgetDefaultsWhenUnset(t *testing.T) {
	var p Processor
	for _, set := range []struct {
		name string
		call func()
	}{
		{"never set", func() {}},
		{"zero", func() { p.SetExecutionBudget(0) }},
		{"negative", func() { p.SetExecutionBudget(-1) }},
	} {
		set.call()
		if got := p.budget(); got != DefaultExecutionBudget {
			t.Errorf("%s: budget = %d, want the default %d", set.name, got, DefaultExecutionBudget)
		}
	}
	p.SetExecutionBudget(7)
	if got := p.budget(); got != 7 {
		t.Errorf("budget = %d, want 7", got)
	}
}

// TestChargeTokenIgnoresTheTokenlessActivation: the activation that starts an
// instance carries no token yet, and there is exactly one of them per instance.
// Charging it would put every instance's first step under one shared key.
func TestChargeTokenIgnoresTheTokenlessActivation(t *testing.T) {
	var p Processor
	p.SetExecutionBudget(1)
	for i := 0; i < 5; i++ {
		if p.chargeToken(0) {
			t.Fatal("a tokenless activation was charged against the budget")
		}
	}
	if p.tokenSteps != nil {
		t.Errorf("tokenSteps = %v, want no entry for token 0", p.tokenSteps)
	}
	if p.chargeToken(9) {
		t.Error("the first step of a token is already over a budget of 1")
	}
	if !p.chargeToken(9) {
		t.Error("the second step of a token is not over a budget of 1")
	}
}

// TestInheritTokenStepsOnlyCarriesWhatWasCounted: a token minted from a parent that
// never took a step records nothing, which keeps the map to the tokens that are
// actually running rather than one entry per token ever created.
func TestInheritTokenStepsOnlyCarriesWhatWasCounted(t *testing.T) {
	var p Processor
	p.inheritTokenSteps(2, 1) // nothing counted yet: no map, no entry
	if p.tokenSteps != nil {
		t.Fatalf("tokenSteps = %v, want nil", p.tokenSteps)
	}
	p.chargeToken(1)
	p.chargeToken(1)
	p.inheritTokenSteps(2, 1)
	if got := p.tokenSteps[2]; got != 2 {
		t.Errorf("inherited steps = %d, want 2", got)
	}
	p.inheritTokenSteps(3, 0) // no parent to inherit from
	if _, ok := p.tokenSteps[3]; ok {
		t.Error("a token with no parent inherited a count")
	}
}
