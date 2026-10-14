package model

import (
	"errors"
	"testing"
)

func anOutcome() ActionOutcomeValue {
	return ActionOutcomeValue{
		InstanceKey: NewKey(1, 42), At: 1_790_000_000_000_000_000,
		OrderID: "ord_1", Position: "mailbox", CommandID: "cmd-7", Source: "atlas:shop",
		Action: "storage-extend", Effect: "change", Outcome: "completed",
		EventType: "mailbox.storage.extended", Principal: "usr_ada",
		ItemID: "mailbox", VariantID: "50gb", Result: `{"quotaGB":100}`,
	}
}

// TestAnActionOutcomeSurvivesTheLog: every field comes back as it was written, the
// type and the intents name themselves, and the record decodes through the same
// entry point every reader of the log uses.
func TestAnActionOutcomeSurvivesTheLog(t *testing.T) {
	want := anOutcome()
	got, err := DecodeValue(VTActionOutcome, want.encode(nil))
	if err != nil {
		t.Fatalf("DecodeValue: %v", err)
	}
	if *got.(*ActionOutcomeValue) != want {
		t.Fatalf("round trip = %+v, want %+v", *got.(*ActionOutcomeValue), want)
	}
	if want.ValueType() != VTActionOutcome || VTActionOutcome.String() != "ActionOutcome" ||
		IntentActionReporting.String() != "ActionReporting" || IntentActionCompleted.String() != "ActionCompleted" {
		t.Fatal("the outcome's type or intents do not name themselves")
	}
}

// TestAnActionOutcomeEndingEarlyKeepsWhatItHas: a record written before a trailing
// field existed decodes with that field empty, and one cut inside the fields that
// make it mean anything is refused rather than read as some command ending somehow.
func TestAnActionOutcomeEndingEarlyKeepsWhatItHas(t *testing.T) {
	full := anOutcome()
	short := full
	short.EventType, short.Principal, short.ItemID, short.VariantID, short.Result = "", "", "", "", ""
	raw := short.encode(nil)
	// Drop the five empty trailing strings: an older record simply ends there.
	raw = raw[:len(raw)-5*len(appendString(nil, ""))]
	var got ActionOutcomeValue
	if err := got.decode(raw); err != nil || got != short {
		t.Fatalf("an older record = %+v (%v), want %+v", got, err, short)
	}

	whole := full.encode(nil)
	for _, cut := range []int{0, 15, 16, 20} {
		var v ActionOutcomeValue
		if err := v.decode(whole[:cut]); err == nil {
			t.Errorf("cut at %d decoded without error: %+v", cut, v)
		} else if cut < 16 && !errors.Is(err, ErrShortBuffer) {
			t.Errorf("cut at %d: %v, want ErrShortBuffer", cut, err)
		}
	}
}

// TestAnActionOutcomeNamesItsCommandAndItsEnding: an outcome without an order, a
// position, a command, an action or an ending is a fact about nothing.
func TestAnActionOutcomeNamesItsCommandAndItsEnding(t *testing.T) {
	ok := anOutcome()
	if !ok.Valid() {
		t.Fatal("a complete outcome is not valid")
	}
	for _, blank := range []func(*ActionOutcomeValue){
		func(v *ActionOutcomeValue) { v.OrderID = "" },
		func(v *ActionOutcomeValue) { v.Position = " " },
		func(v *ActionOutcomeValue) { v.CommandID = "" },
		func(v *ActionOutcomeValue) { v.Action = "" },
		func(v *ActionOutcomeValue) { v.Outcome = "" },
	} {
		v := anOutcome()
		blank(&v)
		if v.Valid() {
			t.Errorf("%+v is valid", v)
		}
	}
}
