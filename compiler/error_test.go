package compiler

import (
	"strings"
	"testing"
)

// TestParseErrorEndAndBoundary checks that an error end event inside a subprocess compiles
// to TypeErrorEndEvent with its code, and an error boundary on that subprocess compiles to
// a BoundaryError boundary with the code and is *always interrupting* — even though the XML
// declares cancelActivity="false" (ADR-0089).
func TestParseErrorEndAndBoundary(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <error id="Err_boom" errorCode="BOOM" name="Boom"/>
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <subProcess id="sub">
	      <startEvent id="ss"/>
	      <endEvent id="ee"><errorEventDefinition errorRef="Err_boom"/></endEvent>
	      <sequenceFlow id="if" sourceRef="ss" targetRef="ee"/>
	    </subProcess>
	    <boundaryEvent id="b" attachedToRef="sub" cancelActivity="false"><errorEventDefinition errorRef="Err_boom"/></boundaryEvent>
	    <endEvent id="done"/>
	    <endEvent id="recovered"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="sub"/>
	    <sequenceFlow id="f2" sourceRef="sub" targetRef="done"/>
	    <sequenceFlow id="f3" sourceRef="b" targetRef="recovered"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ee := nodeByBpmnId(t, cp, "ee")
	if ee.Type != TypeErrorEndEvent || ee.Type.String() != "ErrorEndEvent" {
		t.Fatalf("error end type = %v (%q), want ErrorEndEvent", ee.Type, ee.Type.String())
	}
	if got := cp.ErrorEnd(ee.Detail).ErrorCode; got != "BOOM" {
		t.Errorf("error end code = %q, want BOOM", got)
	}
	b := nodeByBpmnId(t, cp, "b")
	if b.Type != TypeBoundaryEvent {
		t.Fatalf("boundary type = %v, want BoundaryEvent", b.Type)
	}
	d := cp.BoundaryEvent(b.Detail)
	if d.Kind != BoundaryError {
		t.Errorf("boundary kind = %v, want BoundaryError", d.Kind)
	}
	if d.ErrorCode != "BOOM" {
		t.Errorf("boundary error code = %q, want BOOM", d.ErrorCode)
	}
	if !d.Interrupting {
		t.Errorf("error boundary interrupting = false, want true (an error boundary is always interrupting, even with cancelActivity=false)")
	}
}

// TestParseErrorCatchAll checks that an error boundary with no errorRef compiles as a
// code-less catch-all (ErrorCode ""), and an error end with a code-less error throws ""
// (ADR-0089).
func TestParseErrorCatchAll(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <error id="Err_bare" name="Bare"/>
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <subProcess id="sub">
	      <startEvent id="ss"/>
	      <endEvent id="ee"><errorEventDefinition errorRef="Err_bare"/></endEvent>
	      <sequenceFlow id="if" sourceRef="ss" targetRef="ee"/>
	    </subProcess>
	    <boundaryEvent id="b" attachedToRef="sub"><errorEventDefinition/></boundaryEvent>
	    <endEvent id="done"/>
	    <endEvent id="recovered"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="sub"/>
	    <sequenceFlow id="f2" sourceRef="sub" targetRef="done"/>
	    <sequenceFlow id="f3" sourceRef="b" targetRef="recovered"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := cp.ErrorEnd(nodeByBpmnId(t, cp, "ee").Detail).ErrorCode; got != "" {
		t.Errorf("code-less error end code = %q, want empty", got)
	}
	d := cp.BoundaryEvent(nodeByBpmnId(t, cp, "b").Detail)
	if d.Kind != BoundaryError || d.ErrorCode != "" {
		t.Errorf("catch-all boundary = {kind:%v code:%q}, want {BoundaryError \"\"}", d.Kind, d.ErrorCode)
	}
}

// TestParseErrorEventSubprocess checks that an event subprocess triggered by an error start
// compiles with a BoundaryError event-subprocess detail carrying the code, and is forced
// interrupting even though the XML says isInterrupting="false" (ADR-0089, reusing ADR-0082).
func TestParseErrorEventSubprocess(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <error id="Err" errorCode="E1" name="E1"/>
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="e"/>
	    <subProcess id="es" triggeredByEvent="true">
	      <startEvent id="es_start" isInterrupting="false"><errorEventDefinition errorRef="Err"/></startEvent>
	      <endEvent id="es_end"/>
	      <sequenceFlow id="ef" sourceRef="es_start" targetRef="es_end"/>
	    </subProcess>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	es := nodeByBpmnId(t, cp, "es")
	if es.EventSub < 0 {
		t.Fatalf("event subprocess has no EventSub detail (%d)", es.EventSub)
	}
	d := cp.EventSubProcess(es.EventSub)
	if d.Kind != BoundaryError || d.ErrorCode != "E1" {
		t.Errorf("event-sub trigger = {kind:%v code:%q}, want {BoundaryError E1}", d.Kind, d.ErrorCode)
	}
	if !d.Interrupting {
		t.Errorf("error event subprocess interrupting = false, want true (always interrupting, even with isInterrupting=false)")
	}
}

// TestParseErrorUnknownRef fails deploy when an error event references an undeclared error,
// in every error position — end, boundary, and event-subprocess start (ADR-0089).
func TestParseErrorUnknownRef(t *testing.T) {
	wrap := func(body string) string {
		return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
		  <process id="p" isExecutable="true">` + body + `</process></definitions>`
	}
	cases := map[string]string{
		"end": `<startEvent id="s"/><endEvent id="e"><errorEventDefinition errorRef="Nope"/></endEvent>
		        <sequenceFlow id="f" sourceRef="s" targetRef="e"/>`,
		"boundary": `<startEvent id="s"/>
		        <subProcess id="sub"><startEvent id="ss"/><endEvent id="se"/><sequenceFlow id="isf" sourceRef="ss" targetRef="se"/></subProcess>
		        <boundaryEvent id="b" attachedToRef="sub"><errorEventDefinition errorRef="Nope"/></boundaryEvent>
		        <endEvent id="e"/><endEvent id="r"/>
		        <sequenceFlow id="f1" sourceRef="s" targetRef="sub"/><sequenceFlow id="f2" sourceRef="sub" targetRef="e"/><sequenceFlow id="f3" sourceRef="b" targetRef="r"/>`,
		"eventsub": `<startEvent id="s"/><endEvent id="e"/><sequenceFlow id="f" sourceRef="s" targetRef="e"/>
		        <subProcess id="es" triggeredByEvent="true"><startEvent id="es_s"><errorEventDefinition errorRef="Nope"/></startEvent><endEvent id="es_e"/><sequenceFlow id="ef" sourceRef="es_s" targetRef="es_e"/></subProcess>`,
	}
	for name, body := range cases {
		if _, err := Parse(1, 1, strings.NewReader(wrap(body))); err == nil {
			t.Errorf("%s: Parse succeeded, want an error for an unknown errorRef", name)
		}
	}
}

// TestErrorUncaughtWarning proves checkErrorHandling raises a warning (never a deploy error)
// for an error end with no statically matching enclosing handler, and none when a handler —
// matching-code or catch-all — is in scope (ADR-0089).
func TestErrorUncaughtWarning(t *testing.T) {
	// hasUnhandled reports whether validating the model raises a RuleErrorUnhandled warning.
	hasUnhandled := func(t *testing.T, xml string) bool {
		t.Helper()
		ps, err := ValidateModel(strings.NewReader(xml))
		if err != nil {
			t.Fatalf("ValidateModel: %v", err)
		}
		found := false
		for _, p := range ps {
			if p.Rule == RuleErrorUnhandled {
				if p.Severity != SeverityWarning {
					t.Errorf("RuleErrorUnhandled severity = %q, want warning (never blocks a deploy)", p.Severity)
				}
				found = true
			}
		}
		return found
	}

	// An error end at the process root with no handler: warned, but still deployable.
	rootThrow := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <error id="Err" errorCode="BOOM"/>
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <endEvent id="e"><errorEventDefinition errorRef="Err"/></endEvent>
	    <sequenceFlow id="f" sourceRef="s" targetRef="e"/>
	  </process>
	</definitions>`
	if !hasUnhandled(t, rootThrow) {
		t.Error("root error end with no handler: want an unhandled-error warning, got none")
	}
	if _, err := Parse(1, 1, strings.NewReader(rootThrow)); err != nil {
		t.Errorf("an unhandled error is a warning, not a deploy error, but Parse failed: %v", err)
	}

	// A subprocess error end caught by a matching-code boundary on the subprocess: no warning.
	caught := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <error id="Err" errorCode="BOOM"/>
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <subProcess id="sub"><startEvent id="ss"/><endEvent id="ee"><errorEventDefinition errorRef="Err"/></endEvent><sequenceFlow id="isf" sourceRef="ss" targetRef="ee"/></subProcess>
	    <boundaryEvent id="b" attachedToRef="sub"><errorEventDefinition errorRef="Err"/></boundaryEvent>
	    <endEvent id="done"/><endEvent id="rec"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="sub"/><sequenceFlow id="f2" sourceRef="sub" targetRef="done"/><sequenceFlow id="f3" sourceRef="b" targetRef="rec"/>
	  </process>
	</definitions>`
	if hasUnhandled(t, caught) {
		t.Error("error caught by a matching boundary: want no warning, got one")
	}

	// The same subprocess error end, but the only boundary catches a different code: warned
	// (the codes don't match).
	mismatched := strings.Replace(caught, `<boundaryEvent id="b" attachedToRef="sub"><errorEventDefinition errorRef="Err"/>`,
		`<boundaryEvent id="b" attachedToRef="sub"><errorEventDefinition errorRef="Other"/>`, 1)
	mismatched = strings.Replace(mismatched, `<error id="Err" errorCode="BOOM"/>`,
		`<error id="Err" errorCode="BOOM"/><error id="Other" errorCode="NOPE"/>`, 1)
	if !hasUnhandled(t, mismatched) {
		t.Error("error end whose only boundary catches a different code: want a warning, got none")
	}

	// A code-less catch-all boundary catches the coded error: no warning.
	catchAll := strings.Replace(caught, `<boundaryEvent id="b" attachedToRef="sub"><errorEventDefinition errorRef="Err"/>`,
		`<boundaryEvent id="b" attachedToRef="sub"><errorEventDefinition/>`, 1)
	if hasUnhandled(t, catchAll) {
		t.Error("error caught by a code-less catch-all boundary: want no warning, got one")
	}

	// A root error end caught by a root error event subprocess of the same code: no warning
	// (the handler is found by the scope's event-subprocess walk, not a boundary).
	eventSubCatch := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <error id="Err" errorCode="BOOM"/>
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <endEvent id="e"><errorEventDefinition errorRef="Err"/></endEvent>
	    <sequenceFlow id="f" sourceRef="s" targetRef="e"/>
	    <subProcess id="es" triggeredByEvent="true">
	      <startEvent id="es_s"><errorEventDefinition errorRef="Err"/></startEvent>
	      <endEvent id="es_e"/>
	      <sequenceFlow id="ef" sourceRef="es_s" targetRef="es_e"/>
	    </subProcess>
	  </process>
	</definitions>`
	if hasUnhandled(t, eventSubCatch) {
		t.Error("error caught by a root error event subprocess: want no warning, got one")
	}
}
