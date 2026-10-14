package model

import (
	"encoding/binary"
	"testing"
)

// TestIncidentWithoutAReasonDecodesAsUnclassified pins the append-compatibility of
// the reason byte, which is what lets it be added to a value type that is already on
// every existing log. A record written before the field existed ends at the message,
// and reading one must not fail and must not invent a classification: it decodes as
// IncidentUnclassified, which is what the engine's node-type dispatch already
// assumes for every incident it did not label.
func TestIncidentWithoutAReasonDecodesAsUnclassified(t *testing.T) {
	// Exactly the layout the encoder produced before Reason existed: the fixed
	// header, then the message, and nothing after it.
	var legacy []byte
	legacy = binary.LittleEndian.AppendUint64(legacy, NewKey(1, 5))
	legacy = binary.LittleEndian.AppendUint64(legacy, NewKey(1, 6))
	legacy = binary.LittleEndian.AppendUint64(legacy, NewKey(1, 7))
	legacy = binary.LittleEndian.AppendUint32(legacy, uint32(9))
	legacy = binary.LittleEndian.AppendUint64(legacy, uint64(1_700_000_999))
	legacy = appendString(legacy, "worker: connection refused")

	var v IncidentValue
	if err := v.decode(legacy); err != nil {
		t.Fatalf("decode of a pre-Reason record: %v", err)
	}
	if v.Reason != IncidentUnclassified {
		t.Errorf("Reason = %d, want IncidentUnclassified (%d)", v.Reason, IncidentUnclassified)
	}
	if v.Message != "worker: connection refused" || v.ElementId != 9 {
		t.Errorf("the rest of the record did not survive: %+v", v)
	}

	// And a record this build writes carries the byte, so the two are distinguishable.
	current := (&IncidentValue{Message: "worker: connection refused", ElementId: 9, Reason: IncidentOverBudget}).encode(nil)
	if len(current) != len(legacy)+1 {
		t.Fatalf("encoded length = %d, want one byte more than the legacy %d", len(current), len(legacy))
	}
	var back IncidentValue
	if err := back.decode(current); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if back.Reason != IncidentOverBudget {
		t.Errorf("Reason = %d, want IncidentOverBudget (%d)", back.Reason, IncidentOverBudget)
	}
}
