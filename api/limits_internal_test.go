package api

import (
	"testing"

	"github.com/pblumer/atlas/limits"
)

// TestAServerLiteralStillHasBudgets pins the property that makes the zero value
// safe. Seventy-odd tests build a Server as a struct literal, and the zero Limits is
// sixteen ceilings of zero — which admits nothing. Without the default, a handler
// would refuse every body as unreadable and a test asserting "too large" would pass
// without the ceiling ever being the reason.
//
// It was not hypothetical: TestAReportIsRefusedWhenTheServerKeepsNoView started
// answering 400 instead of 503, because the report never got far enough to find that
// there was no view to report into.
func TestAServerLiteralStillHasBudgets(t *testing.T) {
	if got := (&Server{}).budgets(); got != limits.Default() {
		t.Errorf("a Server literal has %+v, want the defaults", got)
	}
}

// TestConfiguredBudgetsWin: the whole point of the option is that an installation's
// numbers reach the handlers, so the default must not shadow them.
func TestConfiguredBudgetsWin(t *testing.T) {
	want := limits.Default()
	want.ModelUpload = 123
	s := &Server{}
	WithLimits(want)(s)
	if got := s.budgets(); got != want {
		t.Errorf("budgets = %+v, want the configured %+v", got, want)
	}
}
