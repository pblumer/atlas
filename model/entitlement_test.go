package model

import (
	"encoding/binary"
	"testing"
)

// TestEntitlementRoundTrips is the ordinary case: everything written comes back.
func TestEntitlementRoundTrips(t *testing.T) {
	want := EntitlementValue{
		Principal: "usr_ada", ItemID: "laptop", VariantID: "14zoll",
		OrderID: "ord_1", Since: 1_700_000_000, Origin: OriginAdopted,
	}
	var got EntitlementValue
	if err := DecodeValueInto(&got, AppendValue(nil, &want)); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != want {
		t.Errorf("= %+v, want %+v", got, want)
	}
}

// TestEntitlementIsAppendCompatible pins the property the whole family depends on.
//
// An entitlement outlives the order that granted it by years, and reconciliation
// will want fields nothing yet defines well enough to write — which target system
// a right lives in, what state it is in there. In an append-only column family a
// decoder that errors on a short record turns adding one of those into a
// migration of every record ever written. So a record that ends early leaves the
// optional fields empty, exactly as the other values in this package do.
//
// The two that make the record mean anything do not get that treatment: an
// entitlement decoding to "nobody holds nothing" would be counted by every reader
// as a right somebody has.
func TestEntitlementIsAppendCompatible(t *testing.T) {
	full := AppendValue(nil, &EntitlementValue{
		Principal: "usr_ada", ItemID: "laptop", VariantID: "14zoll", OrderID: "ord_1",
		Since: 1_700_000_000, Origin: OriginLegacy,
	})

	// Cut where a writer that knew only the two required strings would have
	// stopped: the fixed header plus both length-prefixed strings.
	end := 8 + 1
	for range 2 {
		n := int(binary.LittleEndian.Uint32(full[end:]))
		end += 4 + n
	}
	old := full[:end]

	var got EntitlementValue
	if err := DecodeValueInto(&got, old); err != nil {
		t.Fatalf("a record written before the later fields did not decode: %v", err)
	}
	want := EntitlementValue{
		Principal: "usr_ada", ItemID: "laptop", Since: 1_700_000_000, Origin: OriginLegacy,
	}
	if got != want {
		t.Errorf("= %+v, want %+v", got, want)
	}
	if !got.Valid() {
		t.Error("a record with both required halves was called invalid")
	}
}

// A record too short to name who holds what is an error and not an empty
// entitlement. Silently decoding one would put a right belonging to nobody into
// every count and every report of the estate.
func TestATruncatedEntitlementIsRefused(t *testing.T) {
	full := AppendValue(nil, &EntitlementValue{
		Principal: "usr_ada", ItemID: "laptop", Since: 1, Origin: OriginOrdered,
	})
	for _, n := range []int{0, 4, 8 + 1, 8 + 1 + 4} {
		var got EntitlementValue
		if err := DecodeValueInto(&got, full[:n]); err == nil {
			t.Errorf("%d bytes decoded to %+v, want an error", n, got)
		}
	}
}

// TestEveryOriginHasAWord: the origin travels to a screen and to a report as a
// word, and an unnamed one would show up as a number somebody has to look up.
//
// The default arm is deliberately "ordered" rather than "unknown": zero is the
// ordinary case, and a byte that decoded as unknown would make every record
// written by a writer that predates a future origin look suspect.
func TestEveryOriginHasAWord(t *testing.T) {
	for origin, want := range map[EntitlementOrigin]string{
		OriginOrdered: "ordered",
		OriginAdopted: "adopted",
		OriginLegacy:  "legacy",
		99:            "ordered",
	} {
		if got := origin.String(); got != want {
			t.Errorf("EntitlementOrigin(%d) = %q, want %q", origin, got, want)
		}
	}
	if vt := (&EntitlementValue{}).ValueType(); vt != VTEntitlement {
		t.Errorf("ValueType = %v, want VTEntitlement", vt)
	}
}
