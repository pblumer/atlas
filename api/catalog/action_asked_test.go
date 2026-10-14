package catalog

import "testing"

// TestAnActionSaysWhoAsksItAndWhetherItIsAskedOfAHeldRight: the act and the portal
// read three things off an action — whether it is asked of a held right at all (a
// change or a service; the provision and the return are the order's), whether a
// trigger is one of its own, and whether a trigger a caller names exists.
func TestAnActionSaysWhoAsksItAndWhetherItIsAskedOfAHeldRight(t *testing.T) {
	for effect, want := range map[string]bool{
		EffectProvision: false, EffectDeprovision: false, EffectChange: true, EffectService: true,
	} {
		if got := (Action{Effect: effect}).AskedOfHeld(); got != want {
			t.Errorf("AskedOfHeld(%s) = %v, want %v", effect, got, want)
		}
	}
	a := Action{Triggers: []string{TriggerCustomer, TriggerSystem}}
	if !a.TriggeredBy(TriggerCustomer) || !a.TriggeredBy(TriggerSystem) || a.TriggeredBy(TriggerOperator) {
		t.Errorf("TriggeredBy over %v answered wrongly", a.Triggers)
	}
	if (Action{}).TriggeredBy(TriggerCustomer) {
		t.Error("an action with no triggers is triggered by the customer")
	}
	for _, tr := range []string{TriggerCustomer, TriggerOperator, TriggerSystem} {
		if !KnownTrigger(tr) {
			t.Errorf("KnownTrigger(%q) = false", tr)
		}
	}
	for _, tr := range []string{"", "robot", "Customer"} {
		if KnownTrigger(tr) {
			t.Errorf("KnownTrigger(%q) = true", tr)
		}
	}
}
