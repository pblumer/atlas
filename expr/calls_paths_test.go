package expr_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/expr"
)

// TestCallsAreFoundInsideTestsAndRanges extends TestCallsAreFoundWhereverTheySit to
// the node kinds a walker most easily forgets: interval bounds, a negation, the
// operands of between / in / instance of, and the operand of a unary comparison.
func TestCallsAreFoundInsideTestsAndRanges(t *testing.T) {
	for _, src := range []string{
		"x in [strng length(a)..5]",
		"-strng length(a)",
		"x between strng length(a) and 3",
		"strng length(a) in (1, 2)",
		"x in (strng length(a), 2)",
		"x in < strng length(a)",
		"strng length(a) instance of number",
	} {
		if f := only(t, src); f.Name != "strng length" {
			t.Errorf("CheckCalls(%q) found %q, want the nested call", src, f.Name)
		}
	}
}

// TestFaultsAreListedByName: an expression with several unanswerable calls reports
// them in one order, so the same model always produces the same deploy message.
func TestFaultsAreListedByName(t *testing.T) {
	faults := expr.CheckCalls("zeta fn(1) + alpha fn(2) + mid fn(3)")
	var names []string
	for _, f := range faults {
		names = append(names, f.Name)
	}
	if got := strings.Join(names, ","); got != "alpha fn,mid fn,zeta fn" {
		t.Fatalf("faults = %s, want alpha fn,mid fn,zeta fn", got)
	}
}

// TestTheAritySentenceSaysWhatTheSignatureTakes: the refusal states the signature in
// words the author can compare with what they wrote, singular where it is one.
func TestTheAritySentenceSaysWhatTheSignatureTakes(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`upper case("a", "b")`, `"upper case" takes 1 argument and is called with 2 arguments`},
		{`matches("a")`, `"matches" takes 2 to 3 arguments and is called with 1 argument`},
		{`count()`, `"count" takes at least 1 argument and is called with 0 arguments`},
	} {
		if f := only(t, tc.src); !strings.Contains(f.Message, tc.want) {
			t.Errorf("CheckCalls(%q) = %q, want it to say %q", tc.src, f.Message, tc.want)
		}
	}
}
