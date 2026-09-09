package expr_test

import (
	"testing"
	"time"

	"github.com/pblumer/atlas/expr"
)

// TestDateTimeBinding covers the temporal binding the task-folder filters need:
// an instant bound as a FEEL date-and-time compares against another instant and
// against one shifted by a duration, which a number binding cannot do.
func TestDateTimeBinding(t *testing.T) {
	// The cases involving FEEL's built-in now() need inputs relative to the real
	// clock. A fixed date eventually crosses the one-hour boundary and reverses the
	// expected answer even though the implementation has not changed.
	now := time.Now().UTC()
	vars := map[string]expr.Value{
		"due":  expr.DateTime(now.Add(-2 * time.Hour)),
		"cut":  expr.DateTime(now),
		"soon": expr.DateTime(now.Add(48 * time.Hour)),
	}
	cases := []struct {
		src  string
		want bool
	}{
		{"due < cut", true},
		{"due > cut", false},
		{`due < cut + duration("P3D")`, true},
		{`due > cut - duration("PT1H")`, false},
		{`due > cut - duration("P1D")`, true},
		// The builtin clock is what a generated rule actually compares against.
		{"due < now()", true},
		{`soon < now() + duration("P3D")`, true},
		{`soon < now() + duration("PT1H")`, false},
	}
	for _, tc := range cases {
		c, err := expr.CompileAuto(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		v, err := c.Eval(vars)
		if err != nil {
			t.Fatalf("eval %q: %v", tc.src, err)
		}
		kind, b, text := expr.Classify(v)
		if kind != expr.KindBool {
			t.Fatalf("%q: kind %v (%q), want boolean", tc.src, kind, text)
		}
		if b != tc.want {
			t.Errorf("%q = %v, want %v", tc.src, b, tc.want)
		}
	}
}
