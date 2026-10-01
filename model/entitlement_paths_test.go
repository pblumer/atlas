package model

import (
	"encoding/binary"
	"errors"
	"testing"
)

// offsetsAfterStrings returns where each length-prefixed string after the fixed
// header ends, so a test can cut a record exactly where an older writer stopped.
func offsetsAfterStrings(t *testing.T, full []byte, fixed, count int) []int {
	t.Helper()
	ends := make([]int, 0, count)
	at := fixed
	for range count {
		at += 4 + int(binary.LittleEndian.Uint32(full[at:]))
		ends = append(ends, at)
	}
	return ends
}

// TestARecordWrittenBeforeCeilingsHasNoEnd: a record that stops after its strings
// predates Until, and decodes as a right with no end — which is what it meant.
func TestARecordWrittenBeforeCeilingsHasNoEnd(t *testing.T) {
	full := AppendValue(nil, &EntitlementValue{
		Principal: "usr_ada", ItemID: "laptop", VariantID: "14zoll", OrderID: "ord_1",
		Since: 10, Origin: OriginOrdered, Until: 99, ApprovedBy: "usr_boss",
	})
	ends := offsetsAfterStrings(t, full, 8+1, 4)

	var got EntitlementValue
	// Three bytes of a torn Until are not an Until either.
	if err := DecodeValueInto(&got, full[:ends[3]+3]); err != nil {
		t.Fatalf("decode before ceilings: %v", err)
	}
	want := EntitlementValue{Principal: "usr_ada", ItemID: "laptop", VariantID: "14zoll", OrderID: "ord_1",
		Since: 10, Origin: OriginOrdered}
	if got != want {
		t.Fatalf("= %+v, want %+v", got, want)
	}
}

// TestATornOptionalFieldIsRefused: ending early is the old-record case; ending in the
// middle of a field is damage, and decoding it as empty would hide that.
func TestATornOptionalFieldIsRefused(t *testing.T) {
	full := AppendValue(nil, &EntitlementValue{
		Principal: "usr_ada", ItemID: "laptop", VariantID: "14zoll", OrderID: "ord_1",
		Since: 10, Until: 99, ApprovedBy: "usr_boss",
	})
	ends := offsetsAfterStrings(t, full, 8+1, 4)
	for name, n := range map[string]int{
		"inside the variant":  ends[1] + 4 + 2,
		"inside the approver": ends[3] + 8 + 4 + 3,
	} {
		var got EntitlementValue
		if err := DecodeValueInto(&got, full[:n]); !errors.Is(err, ErrShortBuffer) {
			t.Errorf("cut %s: err = %v, want ErrShortBuffer", name, err)
		}
	}
}

// TestAnEntitlementDecodesByItsTag: the generic decoder dispatches on the value type,
// so the inventory's records come back as entitlements and not as nothing.
func TestAnEntitlementDecodesByItsTag(t *testing.T) {
	in := &EntitlementValue{Principal: "usr_ada", ItemID: "vpn", Since: 5, ApprovedBy: "usr_boss"}
	v, err := DecodeValue(VTEntitlement, AppendValue(nil, in))
	if err != nil {
		t.Fatalf("DecodeValue: %v", err)
	}
	got, ok := v.(*EntitlementValue)
	if !ok || *got != *in {
		t.Fatalf("DecodeValue = %#v, want %+v", v, in)
	}
}
