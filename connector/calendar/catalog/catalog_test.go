package catalog

import (
	"strings"
	"testing"
)

func TestEveryOperationHasItsKeysAndClaim(t *testing.T) {
	names := Names()
	if len(names) != len(Ops) || names[0] != OpCancelBooking {
		t.Fatalf("Names() = %v", names)
	}
	for _, n := range names {
		op, ok := Lookup(n)
		if !ok || op.Label == "" || len(op.Inputs) == 0 || len(op.Required) == 0 {
			t.Errorf("%s: %+v", n, op)
		}
		for _, r := range op.Required {
			if !strings.Contains(","+strings.Join(op.Inputs, ",")+",", ","+r+",") {
				t.Errorf("%s requires %q, which it does not take", n, r)
			}
		}
		if op.TakesResult() != (op.Result != ResultNone) {
			t.Errorf("%s: TakesResult disagrees with Result", n)
		}
	}
	if _, ok := Lookup("book-everything"); ok {
		t.Error("an unknown operation was found")
	}
}

func TestCheckInputKeys(t *testing.T) {
	if err := CheckInputKeys(OpGetBooking, []string{"calendar", "bookingId", "timeZone"}); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		op   string
		keys []string
		want string
	}{
		"unknown operation": {"book-everything", nil, "unknown operation"},
		"unknown key":       {OpGetBooking, []string{"calendar", "bookingId", "subjekt"}, `does not take "subjekt"`},
		"missing key":       {OpGetBooking, []string{"calendar"}, `needs "bookingId"`},
	} {
		if err := CheckInputKeys(tc.op, tc.keys); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}
