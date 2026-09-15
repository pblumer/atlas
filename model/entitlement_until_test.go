package model

import (
	"encoding/binary"
	"testing"
)

// A record written before Until existed must still decode, and must decode to a
// right with no end — not to a right that ended at the epoch, which every reader
// would count as overdue.
func TestARecordWrittenBeforeEndsExistedHasNoEnd(t *testing.T) {
	old := EntitlementValue{Principal: "usr_ada", ItemID: "vpn", Since: 1_000, Origin: OriginLegacy}
	// The encoding as it was: everything up to OrderID and then nothing.
	var buf []byte
	buf = binary.LittleEndian.AppendUint64(buf, uint64(old.Since))
	buf = append(buf, byte(old.Origin))
	buf = appendString(buf, old.Principal)
	buf = appendString(buf, old.ItemID)

	var got EntitlementValue
	if err := got.decode(buf); err != nil {
		t.Fatalf("decode a record written before the field: %v", err)
	}
	if got.Until != 0 {
		t.Errorf("Until = %d, want no end. A pre-existing right that decoded to an end at "+
			"the epoch would be reported overdue by every reader", got.Until)
	}
	if got.Expired(1) {
		t.Error("a right with no end reads as expired")
	}
}

func TestAnEndSurvivesARoundTrip(t *testing.T) {
	want := EntitlementValue{
		Principal: "usr_ada", ItemID: "vpn", VariantID: "large", OrderID: "ord_7",
		Since: 1_000, Origin: OriginOrdered, Until: 90_000,
	}
	var got EntitlementValue
	if err := got.decode(want.encode(nil)); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != want {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
	if !got.Expired(90_000) || got.Expired(89_999) {
		t.Error("Expired does not treat the end as the first expired moment")
	}
}
