package compiler

import (
	"strings"
	"testing"
	"time"
)

// TestResolveFeelDue covers turning a FEEL expression's evaluated text into a due
// date for each FEEL field, including the unparseable and non-FEEL cases.
func TestResolveFeelDue(t *testing.T) {
	dur := TimerSchedule{Kind: TimerFeelDuration}
	if got, ok := dur.ResolveFeelDue("PT1H", 1_000); !ok || got != 1_000+int64(time.Hour) {
		t.Errorf("duration resolve = %d, %v; want %d, true", got, ok, 1_000+int64(time.Hour))
	}
	if _, ok := dur.ResolveFeelDue("not-a-duration", 1_000); ok {
		t.Error("invalid duration text should not resolve")
	}
	date := TimerSchedule{Kind: TimerFeelDate}
	inst, _ := time.Parse(time.RFC3339, "2026-08-01T09:00:00Z")
	if got, ok := date.ResolveFeelDue("2026-08-01T09:00:00Z", 1_000); !ok || got != inst.UnixNano() {
		t.Errorf("date resolve = %d, %v; want %d, true", got, ok, inst.UnixNano())
	}
	if _, ok := date.ResolveFeelDue("not-a-date", 1_000); ok {
		t.Error("invalid date text should not resolve")
	}
	// A non-FEEL schedule never resolves through this path.
	if _, ok := (TimerSchedule{Kind: TimerDuration}).ResolveFeelDue("PT1H", 0); ok {
		t.Error("a non-FEEL schedule should not resolve via ResolveFeelDue")
	}
}

func catchTimerBPMN(child string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p">
    <startEvent id="s"/>
    <intermediateCatchEvent id="wait">
      <timerEventDefinition>` + child + `</timerEventDefinition>
    </intermediateCatchEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="wait"/>
    <sequenceFlow id="f2" sourceRef="wait" targetRef="e"/>
  </process>
</definitions>`
}

func catchSchedule(t *testing.T, cp *CompiledProcess) TimerSchedule {
	t.Helper()
	node := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	if cp.Node(node).Type != TypeTimerCatchEvent {
		t.Fatalf("node type = %v, want TimerCatchEvent", cp.Node(node).Type)
	}
	return cp.TimerCatch(cp.Node(node).Detail).Schedule
}

// TestParseTimerFeelExpression proves a timer field marked with '=' whose body is
// not a literal compiles to a FEEL schedule carrying the compiled expression, for
// both the duration and date fields (ADR-0055).
func TestParseTimerFeelExpression(t *testing.T) {
	// FEEL duration.
	cp, err := Parse(1, 1, strings.NewReader(catchTimerBPMN(`<timeDuration>=orderTimeout</timeDuration>`)))
	if err != nil {
		t.Fatalf("Parse (feel duration): %v", err)
	}
	s := catchSchedule(t, cp)
	if s.Kind != TimerFeelDuration || !s.IsFeel() || s.Expr == nil {
		t.Fatalf("schedule = %+v, want a FEEL duration with a compiled expression", s)
	}
	if got := s.Expr.Inputs(); len(got) != 1 || got[0] != "orderTimeout" {
		t.Errorf("expression inputs = %v, want [orderTimeout]", got)
	}

	// FEEL date.
	cp, err = Parse(1, 1, strings.NewReader(catchTimerBPMN(`<timeDate>=slaDeadline</timeDate>`)))
	if err != nil {
		t.Fatalf("Parse (feel date): %v", err)
	}
	if s := catchSchedule(t, cp); s.Kind != TimerFeelDate || !s.IsFeel() {
		t.Fatalf("schedule = %+v, want a FEEL date", s)
	}
}

// TestParseTimerLiteralWithEqualsStaysLiteral proves a literal a modeler wrote
// with a leading '=' is still a fixed schedule, not FEEL — backward compatibility
// (ADR-0055).
func TestParseTimerLiteralWithEqualsStaysLiteral(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(catchTimerBPMN(`<timeDuration>=PT1H</timeDuration>`)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s := catchSchedule(t, cp); s.Kind != TimerDuration || s.IsFeel() {
		t.Fatalf("schedule = %+v, want a fixed duration (=PT1H is a literal)", s)
	}
}

// TestParseTimerFeelRejections proves the unsupported FEEL cases are compile
// errors, not silent misparses: a FEEL cycle, FEEL on a start event, and a
// syntactically invalid FEEL expression (ADR-0055).
func TestParseTimerFeelRejections(t *testing.T) {
	tests := []struct {
		name string
		xml  string
		want string
	}{
		{"feel cycle", catchTimerBPMN(`<timeCycle>=reminderInterval</timeCycle>`), "timeCycle"},
		{"feel on start event", timerStartBPMN(`<timeDuration>=orderTimeout</timeDuration>`), "start event"},
		{"invalid feel duration syntax", catchTimerBPMN(`<timeDuration>=1 +</timeDuration>`), "FEEL"},
		{"invalid feel date syntax", catchTimerBPMN(`<timeDate>=1 +</timeDate>`), "FEEL"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(1, 1, strings.NewReader(tc.xml))
			if err == nil {
				t.Fatalf("expected a compile error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should mention %q", err, tc.want)
			}
		})
	}
}
