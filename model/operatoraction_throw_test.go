package model

import (
	"errors"
	"testing"
)

// An operator throwing a BPMN error from a job by hand is its own kind of
// intervention, and its record names the code it threw (ADR-0089, ADR-0159): the
// reason says why, the code says what.
func TestAThrownErrorRecordCarriesItsCode(t *testing.T) {
	want := OperatorActionValue{
		ProcessInstanceKey: NewKey(1, 2), ElementInstanceKey: NewKey(1, 3), JobKey: NewKey(1, 4),
		Kind: OperatorActionThrowJobError, Actor: "ops", Reason: "the customer withdrew by phone",
		ErrorCode: "WITHDRAWN",
	}
	v, err := DecodeValue(VTOperatorAction, AppendValue(nil, &want))
	if err != nil {
		t.Fatalf("DecodeValue: %v", err)
	}
	if got := v.(*OperatorActionValue); *got != want {
		t.Fatalf("round trip = %+v, want %+v", *got, want)
	}
	if got := OperatorActionThrowJobError.String(); got != "throwJobError" {
		t.Errorf("OperatorActionThrowJobError.String() = %q, want throwJobError", got)
	}
}

// A record with no code is written exactly as before the field existed, so every
// other kind keeps its bytes, and an older reader stops where it always stopped.
func TestARecordWithoutACodeKeepsItsLayout(t *testing.T) {
	v := OperatorActionValue{
		ProcessInstanceKey: NewKey(1, 2), ElementInstanceKey: NewKey(1, 3), JobKey: NewKey(1, 4),
		Kind: OperatorActionCompleteJob, Actor: "admin", Reason: "done by hand", FromProcessDefKey: 9,
	}
	enc := AppendValue(nil, &v)
	const fixed = 8 + 8 + 8 + 1 + 4 + 4 + 8
	if want := fixed + len(v.Actor) + len(v.Reason); len(enc) != want {
		t.Errorf("encoded length = %d, want %d (no trailing code)", len(enc), want)
	}
}

// A code cut short is a short buffer, not a half-read audit record.
func TestATruncatedCodeIsAShortBuffer(t *testing.T) {
	full := AppendValue(nil, &OperatorActionValue{
		Kind: OperatorActionThrowJobError, Actor: "a", Reason: "r", ErrorCode: "CODE",
	})
	var v OperatorActionValue
	if err := v.decode(full[:len(full)-2]); !errors.Is(err, ErrShortBuffer) {
		t.Errorf("decode(truncated code) err = %v, want ErrShortBuffer", err)
	}
}
