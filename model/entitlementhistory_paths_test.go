package model

import (
	"errors"
	"testing"
)

// TestADamagedHistoryRowIsRefused: a closed hold that cannot name who held what, or
// that ends in the middle of a field, is damage. Decoding it as an empty row would
// put "nobody held nothing" into every report of who had access when.
func TestADamagedHistoryRowIsRefused(t *testing.T) {
	const fixed = 8 + 8 + 8 + 1 + 1
	full := AppendValue(nil, &EntitlementHistoryValue{
		Principal: "usr_ada", ItemID: "laptop", VariantID: "14zoll", OrderID: "ord_1",
		Since: 1, EndedAt: 2, EndedBy: "usr_boss",
	})
	ends := offsetsAfterStrings(t, full, fixed, 3)
	for name, n := range map[string]int{
		"shorter than the fixed part": fixed - 1,
		"inside the principal":        fixed + 4 + 2,
		"inside the variant":          ends[1] + 4 + 2,
	} {
		var got EntitlementHistoryValue
		if err := DecodeValueInto(&got, full[:n]); !errors.Is(err, ErrShortBuffer) {
			t.Errorf("cut %s: err = %v, want ErrShortBuffer", name, err)
		}
	}

	// Ending cleanly after the two required strings is an older row, not damage.
	var old EntitlementHistoryValue
	if err := DecodeValueInto(&old, full[:ends[1]]); err != nil {
		t.Fatalf("a row written before the optional fields: %v", err)
	}
	if old.Principal != "usr_ada" || old.ItemID != "laptop" || old.VariantID != "" || old.EndedBy != "" {
		t.Fatalf("old row = %+v, want the required halves and nothing else", old)
	}
}
