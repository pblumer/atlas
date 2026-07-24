package compiler

import (
	"fmt"
	"strconv"
	"strings"
	"time"
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
)

// TimerSchedule is a compiled timer definition: enough to compute every due date
// deterministically at runtime without re-parsing the XML (invariant I5). Timer
// start events use the full range (ADR-0051); catch and boundary timers use only
// a duration today and do not carry a schedule.
type TimerSchedule struct {
	Kind        TimerScheduleKind
	BaseNanos   int64    // Duration/CycleInterval: the interval in ns; Date: the absolute instant (unix ns)
	Repetitions int32    // remaining fires after the first; -1 = infinite; 0 = fire once
	Cron        cronSpec // populated only for TimerCycleCron
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
	return s.Kind == TimerCycleInterval || s.Kind == TimerCycleCron
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
// must be present; each is trimmed and tolerant of a leading FEEL '=' the modeler
// may emit.
func parseTimerSchedule(def *xmlTimerEventDefinition) (TimerSchedule, error) {
	dur := trimTimerExpr(def.TimeDuration)
	date := trimTimerExpr(def.TimeDate)
	cycle := trimTimerExpr(def.TimeCycle)
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
		nanos, err := parseISO8601Duration(dur)
		if err != nil {
			return TimerSchedule{}, err
		}
		return TimerSchedule{Kind: TimerDuration, BaseNanos: nanos}, nil
	case date != "":
		return parseTimeDate(date)
	default:
		return parseTimeCycle(cycle)
	}
}

// trimTimerExpr trims whitespace and a single leading FEEL '=' prefix.
func trimTimerExpr(s string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "="))
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
