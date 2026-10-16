package compiler

import (
	"reflect"
	"testing"
)

// TestCaughtErrorCodes: the codes a process catches are what decides whether a script
// that fails with a code has thrown a BPMN error or merely failed (ADR-draft-script-bpmn-errors).
// An error boundary and an error event subprocess catch their code; a code-less
// catch-all catches every thrown code but names none, so it adds nothing — otherwise
// any failure that happened to carry an identifier would skip its retries. Codes a
// process only throws (an error end) are not catches. The answer is a sorted set.
func TestCaughtErrorCodes(t *testing.T) {
	b := NewBuilder(1, "faengt", 1)
	start := b.AddStartEvent()
	task := b.AddServiceTask("arbeit", 1)
	b.AddBoundaryErrorEvent(task, "UPN_EXISTS")
	b.AddBoundaryErrorEvent(task, "")           // catch-all
	b.AddBoundaryErrorEvent(task, "UPN_EXISTS") // twice, to prove it is a set
	b.AddBoundaryMessageEvent(task, true, "abbruch", nil)
	sub := b.AddSubProcess()
	b.SetEventSubProcess(sub, EventSubProcessDetail{Kind: BoundaryError, ErrorCode: "LICENSE_MISSING"})
	end := b.AddErrorEndEvent("ONLY_THROWN")
	b.Connect(start, task)
	b.Connect(task, end)

	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := []string{"LICENSE_MISSING", "UPN_EXISTS"}
	if got := cp.CaughtErrorCodes(); !reflect.DeepEqual(got, want) {
		t.Errorf("CaughtErrorCodes() = %q, want %q", got, want)
	}
	if !cp.CatchesErrorCode("UPN_EXISTS") || cp.CatchesErrorCode("ONLY_THROWN") || cp.CatchesErrorCode("") {
		t.Error("CatchesErrorCode disagrees with CaughtErrorCodes")
	}
}

// TestCaughtErrorCodesFromXML: the same answer from a deployed model, where the code
// comes from the <bpmn:error> an event definition references.
func TestCaughtErrorCodesFromXML(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <error id="Error_Upn" name="userPrincipalName already exists" errorCode="UPN_EXISTS"/>
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <task id="t"/>
    <boundaryEvent id="b" attachedToRef="t"><errorEventDefinition errorRef="Error_Upn"/></boundaryEvent>
    <endEvent id="e"/>
    <endEvent id="e2"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
    <sequenceFlow id="f3" sourceRef="b" targetRef="e2"/>
  </process>
</definitions>`
	cp := mustParse(t, xml)
	if got := cp.CaughtErrorCodes(); !reflect.DeepEqual(got, []string{"UPN_EXISTS"}) {
		t.Errorf("CaughtErrorCodes() = %q, want [UPN_EXISTS]", got)
	}
}

// TestCaughtErrorCodesEmpty: a process with no error catch answers an empty set, not
// nil and not a panic.
func TestCaughtErrorCodesEmpty(t *testing.T) {
	b := NewBuilder(1, "leer", 1)
	start := b.AddStartEvent()
	end := b.AddEndEvent()
	b.Connect(start, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := cp.CaughtErrorCodes(); len(got) != 0 || cp.CatchesErrorCode("X") {
		t.Errorf("CaughtErrorCodes() = %q, want none", got)
	}
}
