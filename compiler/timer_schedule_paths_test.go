package compiler

import (
	"strings"
	"testing"
)

// TestAConstantStartScheduleThatResolvesToNothingIsRefused completes the duration
// case in TestParseTimerFeelConstantOnStart for the other two fields: a start timer
// whose constant FEEL is not a date or a cycle would arm nothing and never fire, and
// the refusal names which field it is (ADR-0111).
func TestAConstantStartScheduleThatResolvesToNothingIsRefused(t *testing.T) {
	for _, tc := range []struct{ child, want string }{
		{`<timeDate>="not-a-date"</timeDate>`, "not a valid date"},
		{`<timeCycle>="not-a-cycle"</timeCycle>`, "not a valid cycle"},
	} {
		_, err := Parse(1, 1, strings.NewReader(timerStartBPMN(tc.child)))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Parse(%s) = %v, want an error containing %q", tc.child, err, tc.want)
		}
	}
}

// TestACycleExpressionThatDoesNotCompileIsRefused: the error names the field, so the
// modeler knows which of the timer's three boxes to look at.
func TestACycleExpressionThatDoesNotCompileIsRefused(t *testing.T) {
	_, err := Parse(1, 1, strings.NewReader(timerStartBPMN(`<timeCycle>=(</timeCycle>`)))
	if err == nil || !strings.Contains(err.Error(), "timeCycle FEEL expression") {
		t.Fatalf("Parse = %v, want the cycle's FEEL refused by name", err)
	}
}

// TestALiteralScheduleResolvesToItself: only a FEEL schedule has anything to resolve.
func TestALiteralScheduleResolvesToItself(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(timerStartBPMN(`<timeDuration>PT1H</timeDuration>`)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	s := cp.TimerStartEvents()[0].Schedule
	got, err := s.ResolveConstant()
	if err != nil || got != s {
		t.Fatalf("ResolveConstant(literal) = %+v, %v; want the schedule unchanged", got, err)
	}
}

// TestALinkEventWithoutANameIsRefused: a link is matched by name alone, so a throw
// or catch without one is a goto to nowhere — refused, naming the event.
func TestALinkEventWithoutANameIsRefused(t *testing.T) {
	for _, tc := range []struct{ events, want string }{
		{`<intermediateCatchEvent id="cat"><linkEventDefinition name=" "/></intermediateCatchEvent>`,
			`link catch event "cat" has no name`},
		{`<intermediateThrowEvent id="thr"><linkEventDefinition/></intermediateThrowEvent>`,
			`link throw event "thr" has no name`},
	} {
		xml := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
		  <process id="p" isExecutable="true">
		    <startEvent id="s"/>` + tc.events + `<endEvent id="e"/>
		    <sequenceFlow id="f1" sourceRef="s" targetRef="e"/>
		  </process>
		</definitions>`
		if _, err := Parse(1, 1, strings.NewReader(xml)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Parse = %v, want an error containing %q", err, tc.want)
		}
	}
}
