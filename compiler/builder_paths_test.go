package compiler_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// TestAssignReadsLiteralsAndRefusesBrokenFEEL: an assignment is a literal unless it
// starts with "=", and a "=" that says nothing or does not compile is refused at
// deploy, naming the task and which assignment it was — not left to address a task
// to nobody at runtime.
func TestAssignReadsLiteralsAndRefusesBrokenFEEL(t *testing.T) {
	lit, err := compiler.Assign("approve", "assignee", "usr_ada")
	if err != nil || lit.Literal != "usr_ada" || lit.Expr != nil {
		t.Fatalf("Assign(literal) = %+v, %v; want the literal", lit, err)
	}
	feel, err := compiler.Assign("approve", "assignee", " = requester ")
	if err != nil || feel.Expr == nil || feel.Literal != "" {
		t.Fatalf("Assign(=requester) = %+v, %v; want a compiled expression", feel, err)
	}

	for _, tc := range []struct{ raw, want string }{
		{"=  ", `user task "approve" has an empty FEEL expression for candidateGroups`},
		{"= (", `user task "approve": candidateGroups:`},
	} {
		if _, err := compiler.Assign("approve", "candidateGroups", tc.raw); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Assign(%q) = %v, want an error containing %q", tc.raw, err, tc.want)
		}
	}
}
