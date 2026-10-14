package api

import (
	"reflect"
	"testing"
)

// Marking a product (ADR-0348).

// TestAStarPressedTwiceChangesNothingTheSecondTime.
//
// Marking what is already marked is the state the caller asked for, not an
// error — the same reading the inventory's revocation takes. Reporting no change
// is what lets the handler skip the write, so a star pressed twice does not churn
// a stored file.
func TestAStarPressedTwiceChangesNothingTheSecondTime(t *testing.T) {
	first, changed := withFavourite(nil, "vpn", true)
	if !changed || !reflect.DeepEqual(first, []string{"vpn"}) {
		t.Fatalf("marking = %v changed=%v, want it added", first, changed)
	}
	again, changed := withFavourite(first, "vpn", true)
	if changed {
		t.Error("marking what is already marked reports a change, so the store is " +
			"rewritten every time somebody presses a star that is already on")
	}
	if !reflect.DeepEqual(again, first) {
		t.Errorf("= %v, want the list unchanged", again)
	}
}

// TestClearingWhatIsNotMarkedIsNotAnError.
func TestClearingWhatIsNotMarkedIsNotAnError(t *testing.T) {
	out, changed := withFavourite([]string{"vpn"}, "laptop", false)
	if changed {
		t.Error("clearing something that was never marked reports a change")
	}
	if !reflect.DeepEqual(out, []string{"vpn"}) {
		t.Errorf("= %v, want the list untouched", out)
	}
}

// TestTheStoredListIsAFunctionOfTheSetAndNotOfTheOrder.
//
// Sorted on write, so two accounts that marked the same products in different
// orders store the same bytes. A file whose contents changed without its meaning
// changing makes a backup diff unreadable.
func TestTheStoredListIsAFunctionOfTheSetAndNotOfTheOrder(t *testing.T) {
	a := []string{}
	for _, id := range []string{"vpn", "laptop", "copilot"} {
		a, _ = withFavourite(a, id, true)
	}
	b := []string{}
	for _, id := range []string{"copilot", "vpn", "laptop"} {
		b, _ = withFavourite(b, id, true)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("the same set stored differently: %v vs %v", a, b)
	}
	if !reflect.DeepEqual(a, []string{"copilot", "laptop", "vpn"}) {
		t.Errorf("= %v, want it sorted", a)
	}
}

// TestUnmarkingLeavesTheRest.
func TestUnmarkingLeavesTheRest(t *testing.T) {
	have := []string{"copilot", "laptop", "vpn"}
	out, changed := withFavourite(have, "laptop", false)
	if !changed {
		t.Error("unmarking a marked product reports no change")
	}
	if !reflect.DeepEqual(out, []string{"copilot", "vpn"}) {
		t.Errorf("= %v, want only the laptop gone", out)
	}
	// And the caller's slice is not rewritten under them: the handler holds the
	// record it read while it decides whether to save.
	if !reflect.DeepEqual(have, []string{"copilot", "laptop", "vpn"}) {
		t.Errorf("the input was modified in place: %v", have)
	}
}
