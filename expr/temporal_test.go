package expr_test

import (
	"testing"
	"time"

	"github.com/pblumer/atlas/expr"
)

func evalExpr(t *testing.T, src string) expr.Value {
	t.Helper()
	c, err := expr.Compile(src)
	if err != nil {
		t.Fatalf("Compile(%q): %v", src, err)
	}
	v, err := c.Eval(nil)
	if err != nil {
		t.Fatalf("Eval(%q): %v", src, err)
	}
	return v
}

// TestDurationNanos proves a FEEL days-and-time duration is read exactly, and that
// a non-duration and a calendar (years-and-months) duration are not (ADR-0057).
func TestDurationNanos(t *testing.T) {
	if got, ok := expr.DurationNanos(evalExpr(t, `duration("PT1H30M")`)); !ok || got != int64(90*time.Minute) {
		t.Errorf("DurationNanos = %d, %v; want %d, true", got, ok, int64(90*time.Minute))
	}
	// Arithmetic on durations stays a duration.
	if got, ok := expr.DurationNanos(evalExpr(t, `duration("PT1H") + duration("PT30M")`)); !ok || got != int64(90*time.Minute) {
		t.Errorf("duration arithmetic = %d, %v; want 90m", got, ok)
	}
	// A years-and-months duration is calendar-dependent: not convertible.
	if _, ok := expr.DurationNanos(evalExpr(t, `duration("P1Y2M")`)); ok {
		t.Error("a years-and-months duration should not convert to nanoseconds")
	}
	// A plain string is not a duration value.
	if _, ok := expr.DurationNanos(expr.String("PT1H")); ok {
		t.Error("a string is not a FEEL duration")
	}
}

// TestInstantNanos proves a FEEL date-time and a date are read as absolute
// instants, and a non-temporal is not (ADR-0057).
func TestInstantNanos(t *testing.T) {
	want, _ := time.Parse(time.RFC3339, "2026-08-01T09:00:00Z")
	if got, ok := expr.InstantNanos(evalExpr(t, `date and time("2026-08-01T09:00:00Z")`)); !ok || got != want.UnixNano() {
		t.Errorf("InstantNanos(date-time) = %d, %v; want %d, true", got, ok, want.UnixNano())
	}
	// A bare date resolves to midnight.
	midnight, _ := time.Parse(time.RFC3339, "2026-08-01T00:00:00Z")
	if got, ok := expr.InstantNanos(evalExpr(t, `date("2026-08-01")`)); !ok || got != midnight.UnixNano() {
		t.Errorf("InstantNanos(date) = %d, %v; want %d (midnight), true", got, ok, midnight.UnixNano())
	}
	if _, ok := expr.InstantNanos(expr.Number(5)); ok {
		t.Error("a number is not a FEEL instant")
	}
}
