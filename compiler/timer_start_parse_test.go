package compiler

import (
	"strings"
	"testing"
	"time"
)

// timerStartBPMN builds a one-line process whose only start event is a timer start
// carrying the given <timerEventDefinition> child element.
func timerStartBPMN(timerChild string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="scheduled">
    <startEvent id="s">
      <timerEventDefinition>` + timerChild + `</timerEventDefinition>
    </startEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`
}

// TestParseTimerStartEvent proves a <startEvent> with a <timerEventDefinition>
// compiles to a TypeTimerStartEvent carrying the right compiled schedule, for each
// of the four timer kinds (ADR-0051).
func TestParseTimerStartEvent(t *testing.T) {
	tests := []struct {
		name     string
		child    string
		wantKind TimerScheduleKind
		wantReps int32
		wantBase int64 // 0 = don't check
	}{
		{"duration", `<timeDuration>PT1H</timeDuration>`, TimerDuration, 0, int64(time.Hour)},
		{"date", `<timeDate>2026-08-01T09:00:00Z</timeDate>`, TimerDate, 0, mustDate(t, "2026-08-01T09:00:00Z")},
		{"interval cycle", `<timeCycle>R3/PT1H</timeCycle>`, TimerCycleInterval, 2, int64(time.Hour)},
		{"cron cycle", `<timeCycle>0 * * * *</timeCycle>`, TimerCycleCron, -1, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cp, err := Parse(1, 1, strings.NewReader(timerStartBPMN(tc.child)))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			starts := cp.TimerStartEvents()
			if len(starts) != 1 {
				t.Fatalf("timer start events = %d, want 1", len(starts))
			}
			s := starts[0].Schedule
			if s.Kind != tc.wantKind {
				t.Errorf("Kind = %d, want %d", s.Kind, tc.wantKind)
			}
			if s.Repetitions != tc.wantReps {
				t.Errorf("Repetitions = %d, want %d", s.Repetitions, tc.wantReps)
			}
			if tc.wantBase != 0 && s.BaseNanos != tc.wantBase {
				t.Errorf("BaseNanos = %d, want %d", s.BaseNanos, tc.wantBase)
			}
			// The node is a start event and a process entry point.
			if got := cp.Node(starts[0].ElementId).Type; got != TypeTimerStartEvent {
				t.Errorf("node type = %v, want TimerStartEvent", got)
			}
			if len(cp.StartEvents()) != 1 {
				t.Errorf("StartEvents = %d, want 1 (a timer start is an entry point)", len(cp.StartEvents()))
			}
		})
	}
}

// TestParseTimerStartEventInvalid proves a malformed timer start schedule is a
// compile error naming the element, not a silently dropped timer (ADR-0051).
func TestParseTimerStartEventInvalid(t *testing.T) {
	_, err := Parse(1, 1, strings.NewReader(timerStartBPMN(`<timeCycle>not a cron</timeCycle>`)))
	if err == nil {
		t.Fatal("expected a compile error for an invalid timer start schedule")
	}
	if !strings.Contains(err.Error(), "s") {
		t.Errorf("error %q should name the offending start event", err)
	}
}

func mustDate(t *testing.T, s string) int64 {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("bad date %q: %v", s, err)
	}
	return tm.UnixNano()
}
