package scenarios

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// mustPanic runs fn and fails unless it panics with a message containing want.
func mustPanic(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected a panic mentioning %q, got none", want)
		}
		if msg, ok := r.(string); !ok || !strings.Contains(msg, want) {
			t.Fatalf("panic = %v, want one mentioning %q", r, want)
		}
	}()
	fn()
}

// TestMustExprPanicsOnBadExpression covers the guard that turns an unparseable
// FEEL expression — a programming error in the catalogue — into a clear panic.
func TestMustExprPanicsOnBadExpression(t *testing.T) {
	mustPanic(t, "compile", func() { mustExpr("this is not ) valid feel (") })
}

// TestMustBuildPanicsOnInvalidProcess covers the guard for a builder that cannot
// finalize — here, a sequence flow to a node that does not exist.
func TestMustBuildPanicsOnInvalidProcess(t *testing.T) {
	b := compiler.NewBuilder(1, "invalid", 1)
	s := b.AddStartEvent()
	b.Connect(s, 999) // dangling flow → Build fails validation
	mustPanic(t, "build", func() { mustBuild(b) })
}
