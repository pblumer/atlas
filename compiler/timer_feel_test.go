package compiler

import (
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/expr"
)

// evalFeel compiles and evaluates a constant FEEL expression for a test.
func evalFeel(t *testing.T, src string) expr.Value {
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

// TestResolveFeelValue proves ResolveFeelValue reads a first-class FEEL temporal
// exactly and falls back to the string form otherwise (ADR-0057).
func TestResolveFeelValue(t *testing.T) {
	dur := TimerSchedule{Kind: TimerFeelDuration}
	// First-class duration value.
	if got, ok := dur.ResolveFeelValue(evalFeel(t, `duration("PT1H")`)); !ok || got.Kind != TimerDuration || got.BaseNanos != int64(time.Hour) {
		t.Errorf("duration value resolve = %+v, %v; want TimerDuration of 1h", got, ok)
	}
	// String fallback (a variable holding an ISO string).
	if got, ok := dur.ResolveFeelValue(expr.String("PT30S")); !ok || got.Kind != TimerDuration || got.BaseNanos != int64(30*time.Second) {
		t.Errorf("duration string resolve = %+v, %v; want TimerDuration of 30s", got, ok)
	}
	// First-class date-time value.
	date := TimerSchedule{Kind: TimerFeelDate}
	inst, _ := time.Parse(time.RFC3339, "2026-08-01T09:00:00Z")
	if got, ok := date.ResolveFeelValue(evalFeel(t, `date and time("2026-08-01T09:00:00Z")`)); !ok || got.Kind != TimerDate || got.BaseNanos != inst.UnixNano() {
		t.Errorf("date value resolve = %+v, %v; want TimerDate at the instant", got, ok)
	}
	// A cycle has no first-class temporal — it goes through the string form.
	cyc := TimerSchedule{Kind: TimerFeelCycle}
	if got, ok := cyc.ResolveFeelValue(expr.String("R3/PT1H")); !ok || got.Kind != TimerCycleInterval {
		t.Errorf("cycle string resolve = %+v, %v; want TimerCycleInterval", got, ok)
	}
	// Unresolvable: a number is neither a usable temporal nor a parseable string.
	if _, ok := dur.ResolveFeelValue(expr.Number(5)); ok {
		t.Error("a bare number should not resolve to a duration")
	}
}

// TestResolveFeel covers turning a FEEL expression's evaluated text into the
// concrete schedule for each FEEL field, including the unparseable and non-FEEL
// cases (ADR-0056).
func TestResolveFeel(t *testing.T) {
	dur := TimerSchedule{Kind: TimerFeelDuration}
	if got, ok := dur.ResolveFeel("PT1H"); !ok || got.Kind != TimerDuration || got.BaseNanos != int64(time.Hour) {
		t.Errorf("duration resolve = %+v, %v; want a TimerDuration of 1h", got, ok)
	}
	if _, ok := dur.ResolveFeel("not-a-duration"); ok {
		t.Error("invalid duration text should not resolve")
	}
	date := TimerSchedule{Kind: TimerFeelDate}
	inst, _ := time.Parse(time.RFC3339, "2026-08-01T09:00:00Z")
	if got, ok := date.ResolveFeel("2026-08-01T09:00:00Z"); !ok || got.Kind != TimerDate || got.BaseNanos != inst.UnixNano() {
		t.Errorf("date resolve = %+v, %v; want a TimerDate at the instant", got, ok)
	}
	if _, ok := date.ResolveFeel("not-a-date"); ok {
		t.Error("invalid date text should not resolve")
	}
	cyc := TimerSchedule{Kind: TimerFeelCycle}
	if got, ok := cyc.ResolveFeel("R3/PT1H"); !ok || got.Kind != TimerCycleInterval || got.Repetitions != 2 {
		t.Errorf("interval cycle resolve = %+v, %v; want TimerCycleInterval with 2 reps-after-first", got, ok)
	}
	if got, ok := cyc.ResolveFeel("0 * * * *"); !ok || got.Kind != TimerCycleCron {
		t.Errorf("cron cycle resolve = %+v, %v; want TimerCycleCron", got, ok)
	}
	if _, ok := cyc.ResolveFeel("not-a-cycle"); ok {
		t.Error("invalid cycle text should not resolve")
	}
	// A non-FEEL schedule never resolves through this path.
	if _, ok := (TimerSchedule{Kind: TimerDuration}).ResolveFeel("PT1H"); ok {
		t.Error("a non-FEEL schedule should not resolve via ResolveFeel")
	}
}

// TestParseTimerFeelCycleOnBoundary proves a FEEL <timeCycle> compiles to a
// TimerFeelCycle on a non-interrupting boundary (ADR-0056).
func TestParseTimerFeelCycleOnBoundary(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(boundaryTimerCycleBPMN("false", `<timeCycle>=reminderCadence</timeCycle>`)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var d *BoundaryEventDetail
	for id := 0; id < len(cp.nodes); id++ {
		if cp.Node(int32(id)).Type == TypeBoundaryEvent {
			d = cp.BoundaryEvent(cp.Node(int32(id)).Detail)
		}
	}
	if d == nil || d.Schedule.Kind != TimerFeelCycle || !d.Schedule.Repeats() {
		t.Fatalf("boundary detail = %+v, want a FEEL cycle that repeats", d)
	}
	// An interrupting boundary still rejects a cycle (FEEL or literal).
	if _, err := Parse(1, 1, strings.NewReader(boundaryTimerCycleBPMN("true", `<timeCycle>=reminderCadence</timeCycle>`))); err == nil {
		t.Fatal("interrupting boundary FEEL cycle should be rejected")
	}
}

// TestParseTimerFeelConstantOnStart proves a start-event timer accepts a *constant*
// FEEL schedule (no variable references) and rejects one that reads a variable
// (ADR-0056).
func TestParseTimerFeelConstantOnStart(t *testing.T) {
	// Constant: a string literal expression compiles.
	cp, err := Parse(1, 1, strings.NewReader(timerStartBPMN(`<timeDuration>="PT1H"</timeDuration>`)))
	if err != nil {
		t.Fatalf("Parse (constant FEEL start): %v", err)
	}
	if s := cp.TimerStartEvents()[0].Schedule; s.Kind != TimerFeelDuration || len(s.Expr.Inputs()) != 0 {
		t.Fatalf("start schedule = %+v, want a constant FEEL duration", s)
	}
	// Variable-referencing FEEL on a start event is a compile error.
	_, err = Parse(1, 1, strings.NewReader(timerStartBPMN(`<timeDuration>=orderTimeout</timeDuration>`)))
	if err == nil || !strings.Contains(err.Error(), "constant") {
		t.Fatalf("err = %v, want a 'must be constant' error", err)
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
