package order

import (
	"strings"
	"testing"
)

// TestACancelledLineNamesWhoAndWhen: a cancellation needs no reason — the person it
// is explained to made it — but a stored line claiming to be cancelled by nobody, or
// at no moment, is a record that cannot be audited and is refused at the boundary.
func TestACancelledLineNamesWhoAndWhen(t *testing.T) {
	for _, tc := range []struct {
		name string
		line Line
		want string // "" means valid
	}{
		{"cancelled with an author and a moment",
			Line{ItemID: "a", Status: StatusCancelled, DecidedBy: "usr_7", DecidedAt: 1700}, ""},
		{"cancelled by nobody",
			Line{ItemID: "a", Status: StatusCancelled, DecidedAt: 1700}, "without naming who withdrew it"},
		{"cancelled at no moment",
			Line{ItemID: "a", Status: StatusCancelled, DecidedBy: "usr_7"}, "without a moment"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.line.Valid()
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("Valid() = %v, want nil", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("Valid() = %v, want an error saying %q", err, tc.want)
			}
		})
	}
}
