package compiler

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/expr"
)

// TimerScheduleKind discriminates how a timer's due dates are computed.
type TimerScheduleKind uint8

const (
	// TimerDuration fires once, BaseNanos after the timer is armed (ISO-8601
	// <timeDuration>, e.g. PT1H).
	TimerDuration TimerScheduleKind = iota
	// TimerDate fires once, at the absolute instant BaseNanos (ISO-8601
	// <timeDate>, e.g. 2026-08-01T09:00:00Z).
	TimerDate
	// TimerCycleInterval recurs every BaseNanos, Repetitions more times after the
	// first (ISO-8601 repeating interval <timeCycle>, e.g. R3/PT1H or R/PT1H).
	TimerCycleInterval
	// TimerCycleCron recurs on a wall-clock cron schedule (<timeCycle> holding a
	// 5-field cron expression, e.g. "0 * * * *" — every full hour). Always
	// infinite.
	TimerCycleCron
	// TimerFeelDuration fires once, its delay a FEEL expression (Expr) evaluated
	// against the instance's variables when the timer is created; the result's text
	// is parsed as an ISO-8601 duration (ADR-0055).
	TimerFeelDuration
	// TimerFeelDate fires once, its instant a FEEL expression (Expr) evaluated when
	// the timer is created; the result's text is parsed as an RFC3339 instant
	// (ADR-0055).
	TimerFeelDate
	// TimerFeelCycle recurs, its cadence a FEEL expression (Expr) evaluated when the
	// timer is armed and again on each re-arm; the result's text is parsed as a
	// repeating interval or cron (ADR-0056). Boundary (non-interrupting) only.
	TimerFeelCycle
)

// TimerSchedule is a compiled timer definition: enough to compute every due date
// deterministically at runtime without re-parsing the XML (invariant I5). Timer
// start events use the full range (ADR-0051); catch and boundary timers use only
// a duration today and do not carry a schedule.
type TimerSchedule struct {
	Kind        TimerScheduleKind
	BaseNanos   int64          // Duration/CycleInterval: the interval in ns; Date: the absolute instant (unix ns)
	Repetitions int32          // remaining fires after the first; -1 = infinite; 0 = fire once
	Cron        cronSpec       // populated only for TimerCycleCron
	Expr        *expr.Compiled // populated only for the TimerFeel* kinds (ADR-0055/0056)
}

// IsFeel reports whether the schedule comes from a FEEL expression evaluated at
// runtime (ADR-0055/0056), rather than a value fixed at deploy time.
func (s TimerSchedule) IsFeel() bool {
	return s.Kind == TimerFeelDuration || s.Kind == TimerFeelDate || s.Kind == TimerFeelCycle
}

// ResolveFeelValue turns a FEEL expression's evaluated *value* into the concrete
// schedule for the field (ADR-0057). It first reads a first-class FEEL temporal
// exactly — a duration's nanoseconds for a FEEL duration schedule, a date-time's
// instant for a FEEL date schedule — and only falls back to the canonical string
// form (Classify → ResolveFeel) when the value is not a usable temporal (e.g. a
// variable holding an ISO-8601 string, or any cycle). ok is false if neither path
// yields a valid schedule. Only valid on a FEEL schedule.
func (s TimerSchedule) ResolveFeelValue(v expr.Value) (TimerSchedule, bool) {
	switch s.Kind {
	case TimerFeelDuration:
		if nanos, ok := expr.DurationNanos(v); ok {
			return TimerSchedule{Kind: TimerDuration, BaseNanos: nanos}, true
		}
	case TimerFeelDate:
		if inst, ok := expr.InstantNanos(v); ok {
			return TimerSchedule{Kind: TimerDate, BaseNanos: inst}, true
		}
	}
	_, _, text := expr.Classify(v)
	return s.ResolveFeel(text)
}

// ResolveFeel turns the evaluated text of a FEEL timer expression into the concrete
// schedule the literal parser would have produced for the same field — a duration,
// date, or cycle — so downstream FirstDue/NextDue/Repetitions are identical to a
// literal timer's (ADR-0056). ok is false if the text is not valid for the field
// (the caller then treats the timer as unresolvable). Only valid on a FEEL schedule.
func (s TimerSchedule) ResolveFeel(text string) (TimerSchedule, bool) {
	text = strings.TrimSpace(text)
	switch s.Kind {
	case TimerFeelDuration:
		nanos, err := parseISO8601Duration(text)
		if err != nil {
			return TimerSchedule{}, false
		}
		return TimerSchedule{Kind: TimerDuration, BaseNanos: nanos}, true
	case TimerFeelDate:
		sched, err := parseTimeDate(text)
		if err != nil {
			return TimerSchedule{}, false
		}
		return sched, true
	case TimerFeelCycle:
		sched, err := parseTimeCycle(text)
		if err != nil {
			return TimerSchedule{}, false
		}
		return sched, true
	default:
		return TimerSchedule{}, false
	}
}

// FirstDue returns the due date of the first (or only) firing of a timer armed at
// now. The clock is read by the caller and frozen into the arming event, never
// here (invariant I4/I6).
func (s TimerSchedule) FirstDue(now int64) int64 {
	switch s.Kind {
	case TimerDate:
		return s.BaseNanos
	case TimerCycleCron:
		return s.Cron.next(now)
	default: // Duration, CycleInterval
		return now + s.BaseNanos
	}
}

// Repeats reports whether the schedule recurs (a cycle), as opposed to firing
// once (a duration or date). A recurring non-interrupting boundary uses it to
// decide whether to re-arm after each fire (ADR-0054).
func (s TimerSchedule) Repeats() bool {
	return s.Kind == TimerCycleInterval || s.Kind == TimerCycleCron || s.Kind == TimerFeelCycle
}

// NextDue returns the due date of the next firing after a timer fires at now, and
// whether the timer recurs at all. A one-shot (duration/date) returns ok=false.
// A finite cycle whose Repetitions has run out is handled by the caller via the
// Repetitions counter, not here — NextDue only computes when.
func (s TimerSchedule) NextDue(now int64) (int64, bool) {
	switch s.Kind {
	case TimerCycleInterval:
		return now + s.BaseNanos, true
	case TimerCycleCron:
		return s.Cron.next(now), true
	default: // Duration, Date: fire once
		return 0, false
	}
}

// parseTimerSchedule compiles the (at most one) populated field of a timer event
// definition into a TimerSchedule. Exactly one of timeDuration/timeDate/timeCycle
// must be present. A field is a FEEL expression when it begins with '=' and its
// body is not itself a literal; a literal always wins, so '=PT1H' and 'PT1H' are
// the same fixed schedule (ADR-0055).
func parseTimerSchedule(def *xmlTimerEventDefinition) (TimerSchedule, error) {
	dur := strings.TrimSpace(def.TimeDuration)
	date := strings.TrimSpace(def.TimeDate)
	cycle := strings.TrimSpace(def.TimeCycle)
	set := 0
	for _, s := range []string{dur, date, cycle} {
		if s != "" {
			set++
		}
	}
	switch {
	case set == 0:
		return TimerSchedule{}, fmt.Errorf("timer definition has no timeDuration, timeDate, or timeCycle")
	case set > 1:
		return TimerSchedule{}, fmt.Errorf("timer definition sets more than one of timeDuration/timeDate/timeCycle")
	case dur != "":
		return parseDurationField(dur)
	case date != "":
		return parseDateField(date)
	default:
		return parseCycleField(cycle)
	}
}

// splitFeel reports whether s is marked as a FEEL expression (a leading '=') and
// returns the body with the marker and surrounding whitespace removed.
func splitFeel(s string) (body string, isFeel bool) {
	t := strings.TrimSpace(s)
	if rest, ok := strings.CutPrefix(t, "="); ok {
		return strings.TrimSpace(rest), true
	}
	return t, false
}

// parseDurationField parses a <timeDuration>: an ISO-8601 duration literal, or —
// when marked with '=' and not a literal — a FEEL expression (ADR-0055).
func parseDurationField(s string) (TimerSchedule, error) {
	body, isFeel := splitFeel(s)
	if nanos, err := parseISO8601Duration(body); err == nil {
		return TimerSchedule{Kind: TimerDuration, BaseNanos: nanos}, nil
	} else if !isFeel {
		return TimerSchedule{}, err
	}
	e, err := expr.CompileAuto(body)
	if err != nil {
		return TimerSchedule{}, fmt.Errorf("timeDuration FEEL expression: %w", err)
	}
	return TimerSchedule{Kind: TimerFeelDuration, Expr: e}, nil
}

// parseDateField parses a <timeDate>: an RFC3339 literal, or a FEEL expression.
func parseDateField(s string) (TimerSchedule, error) {
	body, isFeel := splitFeel(s)
	if sched, err := parseTimeDate(body); err == nil {
		return sched, nil
	} else if !isFeel {
		return TimerSchedule{}, err
	}
	e, err := expr.CompileAuto(body)
	if err != nil {
		return TimerSchedule{}, fmt.Errorf("timeDate FEEL expression: %w", err)
	}
	return TimerSchedule{Kind: TimerFeelDate, Expr: e}, nil
}

// parseCycleField parses a <timeCycle>: a literal repeating interval or cron, or —
// when marked with '=' and not a literal — a FEEL expression that resolves to one
// at runtime (ADR-0056).
func parseCycleField(s string) (TimerSchedule, error) {
	body, isFeel := splitFeel(s)
	if sched, err := parseTimeCycle(body); err == nil {
		return sched, nil
	} else if !isFeel {
		return TimerSchedule{}, err
	}
	e, err := expr.CompileAuto(body)
	if err != nil {
		return TimerSchedule{}, fmt.Errorf("timeCycle FEEL expression: %w", err)
	}
	return TimerSchedule{Kind: TimerFeelCycle, Expr: e}, nil
}

// parseTimeDate parses an ISO-8601 / RFC3339 instant into an absolute-date timer.
func parseTimeDate(s string) (TimerSchedule, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return TimerSchedule{}, fmt.Errorf("not an RFC3339 date-time (use e.g. 2026-08-01T09:00:00Z): %q", s)
	}
	return TimerSchedule{Kind: TimerDate, BaseNanos: t.UnixNano()}, nil
}

// parseTimeCycle parses a <timeCycle>: either an ISO-8601 repeating interval
// (Rn/<duration> or R/<duration>) or a 5-field cron expression.
func parseTimeCycle(s string) (TimerSchedule, error) {
	if strings.HasPrefix(s, "R") {
		parts := strings.SplitN(s, "/", 2)
		if len(parts) != 2 {
			return TimerSchedule{}, fmt.Errorf("not an ISO-8601 repeating interval (use e.g. R3/PT1H or R/PT1H): %q", s)
		}
		reps, err := parseRepetitions(parts[0])
		if err != nil {
			return TimerSchedule{}, err
		}
		nanos, err := parseISO8601Duration(parts[1])
		if err != nil {
			return TimerSchedule{}, err
		}
		return TimerSchedule{Kind: TimerCycleInterval, BaseNanos: nanos, Repetitions: reps}, nil
	}
	spec, err := parseCron(s)
	if err != nil {
		return TimerSchedule{}, err
	}
	return TimerSchedule{Kind: TimerCycleCron, Repetitions: -1, Cron: spec}, nil
}

// parseRepetitions parses the "Rn" (n total firings) or "R" (infinite) head of an
// ISO-8601 repeating interval into a Repetitions count — the number of firings
// *after* the first. R3 → 2; R (or R0-less bare R) → -1 (infinite).
func parseRepetitions(head string) (int32, error) {
	if head == "R" {
		return -1, nil
	}
	n, err := strconv.Atoi(strings.TrimPrefix(head, "R"))
	if err != nil || n < 1 {
		return 0, fmt.Errorf("repetition count must be R, or Rn with n>=1: %q", head)
	}
	return int32(n - 1), nil
}
