package api

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
)

// TestStopReasonNamesOnlyWhatTheRecordKnows pins the one derivation in the loop
// explanation: which of the loop's bounds ended it. A stated maximum reached on that very
// round is the cap; a loop that states a condition and stopped short of its cap is the
// condition; a loop with no maximum stopping on a multiple of the engine's ceiling is the
// backstop. A multi-instance ends when its collection runs out or its completion
// condition holds — the log records neither as a fact of its own, so nothing is claimed.
func TestStopReasonNamesOnlyWhatTheRecordKnows(t *testing.T) {
	cond, err := expr.CompileAuto("again")
	if err != nil {
		t.Fatalf("CompileAuto: %v", err)
	}
	for name, tc := range map[string]struct {
		detail compiler.MultiInstanceDetail
		round  int
		want   string
	}{
		"cap reached":            {compiler.MultiInstanceDetail{Standard: true, LoopMaximum: 9}, 9, "maximum"},
		"cap reached with cond":  {compiler.MultiInstanceDetail{Standard: true, LoopMaximum: 9, LoopCondition: cond}, 9, "maximum"},
		"condition stopped it":   {compiler.MultiInstanceDetail{Standard: true, LoopMaximum: 9, LoopCondition: cond}, 4, "condition"},
		"condition, no cap":      {compiler.MultiInstanceDetail{Standard: true, LoopCondition: cond}, 3, "condition"},
		"safety ceiling":         {compiler.MultiInstanceDetail{Standard: true}, compiler.SafeLoopCeiling, "ceiling"},
		"below the ceiling":      {compiler.MultiInstanceDetail{Standard: true}, 7, ""},
		"multi-instance is mute": {compiler.MultiInstanceDetail{CompletionCondition: cond}, 3, ""},
	} {
		t.Run(name, func(t *testing.T) {
			d := tc.detail
			if got := stopReason(&d, tc.round); got != tc.want {
				t.Errorf("stopReason = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestLookupVarPrefersTheNearestScope: a loop body holds what its rounds wrote under the
// looping element's own id, and that shadows a process variable of the same name for
// everything inside the loop — so an explanation that quoted the process value would be
// quoting the one the condition did not read (ADR-0074/0133).
func TestLookupVarPrefersTheNearestScope(t *testing.T) {
	list := []variableView{
		{Name: "result", Value: "0"},                      // the process scope
		{Name: "result", Value: "100", Scope: "rechne"},   // the loop body's
		{Name: "other", Value: "7", Scope: "woandershin"}, // a different element's
	}
	if v, ok := lookupVar(list, "result", "rechne"); !ok || v.Value != "100" {
		t.Errorf("lookupVar(result) = %+v (ok=%v), want the loop body's 100", v, ok)
	}
	// With nothing in the preferred scope, the first match still answers — a condition
	// reading a plain process variable is the ordinary case.
	if v, ok := lookupVar(list, "other", "rechne"); !ok || v.Value != "7" {
		t.Errorf("lookupVar(other) = %+v (ok=%v), want 7", v, ok)
	}
	if _, ok := lookupVar(list, "nichtDa", "rechne"); ok {
		t.Error("lookupVar found a name that is not in the list")
	}
}
