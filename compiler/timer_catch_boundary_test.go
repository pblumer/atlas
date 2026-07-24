package compiler

import (
	"strings"
	"testing"
	"time"
)

// TestParseCatchTimerDate proves an intermediate timer catch event accepts a
// <timeDate> (ADR-0054).
func TestParseCatchTimerDate(t *testing.T) {
	const bpmn = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p">
    <startEvent id="s"/>
    <intermediateCatchEvent id="wait">
      <timerEventDefinition><timeDate>2026-08-01T09:00:00Z</timeDate></timerEventDefinition>
    </intermediateCatchEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="wait"/>
    <sequenceFlow id="f2" sourceRef="wait" targetRef="e"/>
  </process>
</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	d := cp.TimerCatch(cp.Node(node).Detail)
	want, _ := time.Parse(time.RFC3339, "2026-08-01T09:00:00Z")
	if d.Schedule.Kind != TimerDate || d.Schedule.BaseNanos != want.UnixNano() {
		t.Errorf("schedule = %+v, want date %d", d.Schedule, want.UnixNano())
	}
}

// TestParseCatchTimerRejectsCycle proves a <timeCycle> on an intermediate catch is
// a compile error — a catch fires once (ADR-0054).
func TestParseCatchTimerRejectsCycle(t *testing.T) {
	const bpmn = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p">
    <startEvent id="s"/>
    <intermediateCatchEvent id="wait">
      <timerEventDefinition><timeCycle>R3/PT1H</timeCycle></timerEventDefinition>
    </intermediateCatchEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="wait"/>
    <sequenceFlow id="f2" sourceRef="wait" targetRef="e"/>
  </process>
</definitions>`
	_, err := Parse(1, 1, strings.NewReader(bpmn))
	if err == nil || !strings.Contains(err.Error(), "timeCycle") {
		t.Fatalf("err = %v, want a timeCycle-not-supported error", err)
	}
}

// boundaryTimerBPMN builds a host user task with a boundary timer carrying the
// given cancelActivity attr and <timerEventDefinition> child.
func boundaryTimerCycleBPMN(cancelActivity, timerChild string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p">
    <startEvent id="s"/>
    <userTask id="host"/>
    <boundaryEvent id="b" attachedToRef="host" cancelActivity="` + cancelActivity + `">
      <timerEventDefinition>` + timerChild + `</timerEventDefinition>
    </boundaryEvent>
    <endEvent id="e"/>
    <endEvent id="be"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="host"/>
    <sequenceFlow id="f2" sourceRef="host" targetRef="e"/>
    <sequenceFlow id="f3" sourceRef="b" targetRef="be"/>
  </process>
</definitions>`
}

// TestParseBoundaryTimerCycle proves a cycle is accepted on a non-interrupting
// boundary (a repeating reminder) and rejected on an interrupting one (ADR-0054).
func TestParseBoundaryTimerCycle(t *testing.T) {
	// Non-interrupting: cycle compiles to a recurring schedule.
	cp, err := Parse(1, 1, strings.NewReader(boundaryTimerCycleBPMN("false", `<timeCycle>R/PT1H</timeCycle>`)))
	if err != nil {
		t.Fatalf("non-interrupting cycle: %v", err)
	}
	var d *BoundaryEventDetail
	for id := 0; id < len(cp.nodes); id++ {
		if cp.Node(int32(id)).Type == TypeBoundaryEvent {
			d = cp.BoundaryEvent(cp.Node(int32(id)).Detail)
		}
	}
	if d == nil || !d.Schedule.Repeats() || d.Interrupting {
		t.Fatalf("boundary detail = %+v, want a non-interrupting recurring schedule", d)
	}

	// Interrupting: cycle is a compile error.
	_, err = Parse(1, 1, strings.NewReader(boundaryTimerCycleBPMN("true", `<timeCycle>R/PT1H</timeCycle>`)))
	if err == nil || !strings.Contains(err.Error(), "timeCycle") {
		t.Fatalf("interrupting cycle err = %v, want a timeCycle-not-supported error", err)
	}
}

// TestParseBoundaryTimerDate proves a <timeDate> is accepted on a boundary event.
func TestParseBoundaryTimerDate(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(boundaryTimerCycleBPMN("true", `<timeDate>2026-08-01T09:00:00Z</timeDate>`)))
	if err != nil {
		t.Fatalf("boundary date: %v", err)
	}
	var d *BoundaryEventDetail
	for id := 0; id < len(cp.nodes); id++ {
		if cp.Node(int32(id)).Type == TypeBoundaryEvent {
			d = cp.BoundaryEvent(cp.Node(int32(id)).Detail)
		}
	}
	if d == nil || d.Schedule.Kind != TimerDate {
		t.Fatalf("boundary detail = %+v, want a date schedule", d)
	}
}
