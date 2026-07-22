package expr

import (
	"errors"
	"testing"
)

// TestUnknownVariableParsing exercises unknownVariable directly, including the
// malformed shapes the CompileAuto retry loop must reject rather than loop on:
// an error without the marker, and a marker with no closing quote.
func TestUnknownVariableParsing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		wantName string
		wantOK   bool
	}{
		{"well-formed", errors.New(`compile: unknown variable "Amount" at 1:1`), "Amount", true},
		{"no-marker", errors.New("some unrelated syntax error"), "", false},
		{"unterminated", errors.New(`unknown variable "Amount`), "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name, ok := unknownVariable(tc.err)
			if name != tc.wantName || ok != tc.wantOK {
				t.Errorf("unknownVariable(%v) = (%q,%v), want (%q,%v)",
					tc.err, name, ok, tc.wantName, tc.wantOK)
			}
		})
	}
}
