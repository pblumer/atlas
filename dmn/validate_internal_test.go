package dmn

import (
	"strings"
	"testing"

	tdmn "github.com/pblumer/temis/dmn"
)

// TestFormatDiagnostics covers the one-line rendering the Modeler shows when a
// decision model fails to compile. Warnings are not what stopped the deploy, so
// they are left out; and a compile that reports errors without a usable message
// still has to say something, because "invalid, reason blank" is worse for the
// author than a generic line.
func TestFormatDiagnostics(t *testing.T) {
	tests := []struct {
		name  string
		diags tdmn.Diagnostics
		want  string
	}{
		{
			"one error without a decision id",
			tdmn.Diagnostics{{Severity: tdmn.SevError, Message: "unexpected token"}},
			"unexpected token",
		},
		{
			"a decision id prefixes its error",
			tdmn.Diagnostics{{Severity: tdmn.SevError, DecisionID: "rating", Message: "unknown input"}},
			"rating: unknown input",
		},
		{
			"several errors join on one line",
			tdmn.Diagnostics{
				{Severity: tdmn.SevError, DecisionID: "a", Message: "first"},
				{Severity: tdmn.SevError, DecisionID: "b", Message: "second"},
			},
			"a: first; b: second",
		},
		{
			"warnings are not what failed the model",
			tdmn.Diagnostics{
				{Severity: tdmn.SevWarning, Message: "unused input"},
				{Severity: tdmn.SevError, Message: "real problem"},
				{Severity: tdmn.SevWarning, Message: "also unused"},
			},
			"real problem",
		},
		{
			"no error-severity diagnostic still says something",
			tdmn.Diagnostics{{Severity: tdmn.SevWarning, Message: "unused input"}},
			"model has errors",
		},
		{"nothing at all still says something", nil, "model has errors"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatDiagnostics(tc.diags); got != tc.want {
				t.Errorf("formatDiagnostics = %q, want %q", got, tc.want)
			}
			if strings.Contains(formatDiagnostics(tc.diags), "unused input") {
				t.Error("a warning leaked into the failure message")
			}
		})
	}
}
