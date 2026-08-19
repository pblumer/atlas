package layout

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"testing"
)

// Layout quality, expressed as executable predicates over generated DI.
//
// The rest of the layout tests assert coordinates ("this label's right edge is at
// the event's center-x"). That pins the implementation, not the goal: when the
// generator improves, such a test fails even though the picture got better — and
// it stays silent about every model it doesn't name. The invariants here say what
// a *good* layout is instead — nothing overlaps, edges don't cut through boxes,
// the flow reads left to right — and are checked across a corpus of models. A
// change is then judged by whether the picture is sound, not by whether a pixel
// moved.
//
// Adding a model to layoutCorpus is the cheapest way to protect a shape class:
// every invariant applies to it automatically.

// layoutCase is one corpus entry: a name for failure messages and the semantic
// BPMN to lay out (deliberately DI-free, so generateDI has to synthesize).
type layoutCase struct {
	name string
	src  string
}

// layoutCorpus spans the structures the generator has to handle. Each entry is a
// shape class, not a specific past bug — a regression in any of them shows up as
// an invariant violation rather than as a coordinate mismatch.
var layoutCorpus = []layoutCase{
	{"linear", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="Start"/><serviceTask id="A"/><serviceTask id="B"/><endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="A"/>
    <sequenceFlow id="f2" sourceRef="A" targetRef="B"/>
    <sequenceFlow id="f3" sourceRef="B" targetRef="End"/>
  </process></definitions>`},

	// A rework loop: the reviewer sends the case back. The back edge must not drag
	// its target off to the right of the nodes that follow it.
	{"rework-loop", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="Start"/><serviceTask id="Review"/><exclusiveGateway id="Ok"/>
    <serviceTask id="Rework"/><endEvent id="Done"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Review"/>
    <sequenceFlow id="f2" sourceRef="Review" targetRef="Ok"/>
    <sequenceFlow id="f3" sourceRef="Ok" targetRef="Done"/>
    <sequenceFlow id="f4" sourceRef="Ok" targetRef="Rework"/>
    <sequenceFlow id="f5" sourceRef="Rework" targetRef="Review"/>
  </process></definitions>`},

	// A bypass: a short-circuit edge runs parallel to the real main sequence. The
	// long run is the happy path; the shortcut must not seize it.
	{"bypass", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="Start"/><exclusiveGateway id="Split"/>
    <serviceTask id="A"/><serviceTask id="B"/><serviceTask id="C"/><endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Split"/>
    <sequenceFlow id="f2" sourceRef="Split" targetRef="A"/>
    <sequenceFlow id="f3" sourceRef="A" targetRef="B"/>
    <sequenceFlow id="f4" sourceRef="B" targetRef="C"/>
    <sequenceFlow id="f5" sourceRef="C" targetRef="End"/>
    <sequenceFlow id="f6" sourceRef="Split" targetRef="End"/>
  </process></definitions>`},

	// The Invoke-DummyAction shape: a linear process with one boundary error handler.
	{"boundary-error", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="Start"/><serviceTask id="Init"/><serviceTask id="Execute"/>
    <serviceTask id="Log"/><endEvent id="EndSuccess"/>
    <boundaryEvent id="Err" name="Error" attachedToRef="Execute"/>
    <serviceTask id="Handle"/><endEvent id="EndError"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Init"/>
    <sequenceFlow id="f2" sourceRef="Init" targetRef="Execute"/>
    <sequenceFlow id="f3" sourceRef="Execute" targetRef="Log"/>
    <sequenceFlow id="f4" sourceRef="Log" targetRef="EndSuccess"/>
    <sequenceFlow id="e1" sourceRef="Err" targetRef="Handle"/>
    <sequenceFlow id="e2" sourceRef="Handle" targetRef="EndError"/>
  </process></definitions>`},

	// Two boundary events on one host, so their labels compete for the same corner.
	{"two-boundaries", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="Start"/><serviceTask id="Call"/><endEvent id="Done"/>
    <boundaryEvent id="Timeout" name="Timeout" attachedToRef="Call"/>
    <boundaryEvent id="Fault" name="Fault" attachedToRef="Call"/>
    <serviceTask id="Retry"/><serviceTask id="Abort"/>
    <endEvent id="E1"/><endEvent id="E2"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Call"/>
    <sequenceFlow id="f2" sourceRef="Call" targetRef="Done"/>
    <sequenceFlow id="t1" sourceRef="Timeout" targetRef="Retry"/>
    <sequenceFlow id="t2" sourceRef="Retry" targetRef="E1"/>
    <sequenceFlow id="u1" sourceRef="Fault" targetRef="Abort"/>
    <sequenceFlow id="u2" sourceRef="Abort" targetRef="E2"/>
  </process></definitions>`},

	// A parallel split/join: three branches share the columns between the gateways.
	{"gateway-fan", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="Start"/><parallelGateway id="Fork"/>
    <serviceTask id="A"/><serviceTask id="B"/><serviceTask id="C"/>
    <parallelGateway id="Join"/><endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Fork"/>
    <sequenceFlow id="a1" sourceRef="Fork" targetRef="A"/>
    <sequenceFlow id="b1" sourceRef="Fork" targetRef="B"/>
    <sequenceFlow id="c1" sourceRef="Fork" targetRef="C"/>
    <sequenceFlow id="a2" sourceRef="A" targetRef="Join"/>
    <sequenceFlow id="b2" sourceRef="B" targetRef="Join"/>
    <sequenceFlow id="c2" sourceRef="C" targetRef="Join"/>
    <sequenceFlow id="f2" sourceRef="Join" targetRef="End"/>
  </process></definitions>`},

	{"lanes", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <laneSet>
      <lane id="L1"><flowNodeRef>Start</flowNodeRef><flowNodeRef>Ask</flowNodeRef></lane>
      <lane id="L2"><flowNodeRef>Approve</flowNodeRef><flowNodeRef>Done</flowNodeRef></lane>
    </laneSet>
    <startEvent id="Start"/><userTask id="Ask"/><userTask id="Approve"/><endEvent id="Done"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Ask"/>
    <sequenceFlow id="f2" sourceRef="Ask" targetRef="Approve"/>
    <sequenceFlow id="f3" sourceRef="Approve" targetRef="Done"/>
  </process></definitions>`},

	{"shared-error-handler", sharedErrorHandlerSrc},

	// Two independent shared handlers whose corridors compete for the same columns.
	// Only one can have the row nearest the trunk, so the other's exception flows
	// have to reach a higher row without cutting through the handler on the row in
	// between — the case that forces channel-routed edges to use gutters at both
	// ends rather than the columns their endpoints stand in.
	{"two-corridors", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="S"/>
    <scriptTask id="T1"/><scriptTask id="T2"/><scriptTask id="T3"/><scriptTask id="T4"/>
    <scriptTask id="T5"/><scriptTask id="T6"/><endEvent id="E"/>
    <boundaryEvent id="a1" name="A" attachedToRef="T1"/>
    <boundaryEvent id="a2" name="A" attachedToRef="T2"/>
    <boundaryEvent id="b1" name="B" attachedToRef="T3"/>
    <boundaryEvent id="b2" name="B" attachedToRef="T4"/>
    <scriptTask id="HA"/><endEvent id="EA"/>
    <scriptTask id="HB"/><endEvent id="EB"/>
    <sequenceFlow id="f1" sourceRef="S" targetRef="T1"/>
    <sequenceFlow id="f2" sourceRef="T1" targetRef="T2"/>
    <sequenceFlow id="f3" sourceRef="T2" targetRef="T3"/>
    <sequenceFlow id="f4" sourceRef="T3" targetRef="T4"/>
    <sequenceFlow id="f5" sourceRef="T4" targetRef="T5"/>
    <sequenceFlow id="f6" sourceRef="T5" targetRef="T6"/>
    <sequenceFlow id="f7" sourceRef="T6" targetRef="E"/>
    <sequenceFlow id="x1" sourceRef="a1" targetRef="HA"/>
    <sequenceFlow id="x2" sourceRef="a2" targetRef="HA"/>
    <sequenceFlow id="x3" sourceRef="HA" targetRef="EA"/>
    <sequenceFlow id="y1" sourceRef="b1" targetRef="HB"/>
    <sequenceFlow id="y2" sourceRef="b2" targetRef="HB"/>
    <sequenceFlow id="y3" sourceRef="HB" targetRef="EB"/>
  </process></definitions>`},

	// Branch wiring that crosses: each branch of a fan feeds the *opposite* branch of
	// the next stage. Placing each node on the lowest free row would order the second
	// stage against the first and draw two risers on top of each other in one column
	// gap; the row nearest the predecessors keeps every branch straight.
	{"crossed-wiring", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="S"/><exclusiveGateway id="Split"/>
    <serviceTask id="A"/><serviceTask id="B"/><serviceTask id="C"/>
    <serviceTask id="X"/><serviceTask id="Y"/><serviceTask id="Z"/>
    <exclusiveGateway id="Join"/><endEvent id="E"/>
    <sequenceFlow id="f1" sourceRef="S" targetRef="Split"/>
    <sequenceFlow id="s1" sourceRef="Split" targetRef="A"/>
    <sequenceFlow id="s2" sourceRef="Split" targetRef="B"/>
    <sequenceFlow id="s3" sourceRef="Split" targetRef="C"/>
    <sequenceFlow id="w1" sourceRef="A" targetRef="Z"/>
    <sequenceFlow id="w2" sourceRef="B" targetRef="Y"/>
    <sequenceFlow id="w3" sourceRef="C" targetRef="X"/>
    <sequenceFlow id="j1" sourceRef="X" targetRef="Join"/>
    <sequenceFlow id="j2" sourceRef="Y" targetRef="Join"/>
    <sequenceFlow id="j3" sourceRef="Z" targetRef="Join"/>
    <sequenceFlow id="f9" sourceRef="Join" targetRef="E"/>
  </process></definitions>`},

	{"three-way-fan", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="S"/><inclusiveGateway id="G"/>
    <serviceTask id="A1"/><serviceTask id="A2"/><serviceTask id="B1"/><serviceTask id="B2"/>
    <serviceTask id="C1"/><serviceTask id="C2"/><inclusiveGateway id="J"/><endEvent id="E"/>
    <sequenceFlow id="f1" sourceRef="S" targetRef="G"/>
    <sequenceFlow id="a1" sourceRef="G" targetRef="A1"/>
    <sequenceFlow id="a2" sourceRef="A1" targetRef="A2"/>
    <sequenceFlow id="a3" sourceRef="A2" targetRef="J"/>
    <sequenceFlow id="b1" sourceRef="G" targetRef="B1"/>
    <sequenceFlow id="b2" sourceRef="B1" targetRef="B2"/>
    <sequenceFlow id="b3" sourceRef="B2" targetRef="J"/>
    <sequenceFlow id="c1" sourceRef="G" targetRef="C1"/>
    <sequenceFlow id="c2" sourceRef="C1" targetRef="C2"/>
    <sequenceFlow id="c3" sourceRef="C2" targetRef="J"/>
    <sequenceFlow id="f9" sourceRef="J" targetRef="E"/>
  </process></definitions>`},

	{"nested-subprocess", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="Start"/>
    <subProcess id="Sub">
      <startEvent id="SubStart"/><serviceTask id="SubTask"/><endEvent id="SubEnd"/>
      <sequenceFlow id="s1" sourceRef="SubStart" targetRef="SubTask"/>
      <sequenceFlow id="s2" sourceRef="SubTask" targetRef="SubEnd"/>
    </subProcess>
    <endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Sub"/>
    <sequenceFlow id="f2" sourceRef="Sub" targetRef="End"/>
  </process></definitions>`},

	// Gateway branches carry their condition as a flow name, and the reader has to
	// be able to tell which caption belongs to which arrow — including when one
	// branch steps up into another row.
	{"labelled-branches", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="Start"/><exclusiveGateway id="Gw" default="fLow"/>
    <serviceTask id="High"/><serviceTask id="Low"/><endEvent id="End"/>
    <sequenceFlow id="f0" sourceRef="Start" targetRef="Gw"/>
    <sequenceFlow id="fHigh" name="over 1000" sourceRef="Gw" targetRef="High"/>
    <sequenceFlow id="fLow" name="otherwise" sourceRef="Gw" targetRef="Low"/>
    <sequenceFlow id="f1" sourceRef="High" targetRef="End"/>
    <sequenceFlow id="f2" sourceRef="Low" targetRef="End"/>
  </process></definitions>`},

	// Four named branches out of one gateway with long captions: the straight run
	// from the fork to the first branch is one column gap wide, far narrower than the
	// captions, so every one of them has to be reflowed or moved to fit.
	{"crowded-labels", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="S"/><exclusiveGateway id="Gw"/><exclusiveGateway id="Jn"/><endEvent id="E"/>
    <serviceTask id="A"/><serviceTask id="B"/><serviceTask id="C"/><serviceTask id="D"/>
    <sequenceFlow id="f0" sourceRef="S" targetRef="Gw"/>
    <sequenceFlow id="a1" name="amount over 100000" sourceRef="Gw" targetRef="A"/>
    <sequenceFlow id="b1" name="amount over 10000" sourceRef="Gw" targetRef="B"/>
    <sequenceFlow id="c1" name="amount over 1000" sourceRef="Gw" targetRef="C"/>
    <sequenceFlow id="d1" name="otherwise reject" sourceRef="Gw" targetRef="D"/>
    <sequenceFlow id="a2" sourceRef="A" targetRef="Jn"/>
    <sequenceFlow id="b2" sourceRef="B" targetRef="Jn"/>
    <sequenceFlow id="c2" sourceRef="C" targetRef="Jn"/>
    <sequenceFlow id="d2" sourceRef="D" targetRef="Jn"/>
    <sequenceFlow id="f9" sourceRef="Jn" targetRef="E"/>
  </process></definitions>`},

	// Captions on the two routes a caption is hardest to place on: a loop's returning
	// edge and a multi-column bypass, both drawn as a U through a channel.
	{"labelled-detours", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="S"/><exclusiveGateway id="Split"/>
    <serviceTask id="A"/><serviceTask id="B"/><exclusiveGateway id="Ok"/>
    <serviceTask id="Rework"/><endEvent id="E"/>
    <sequenceFlow id="f1" sourceRef="S" targetRef="Split"/>
    <sequenceFlow id="f2" name="full check" sourceRef="Split" targetRef="A"/>
    <sequenceFlow id="f3" sourceRef="A" targetRef="B"/>
    <sequenceFlow id="f4" sourceRef="B" targetRef="Ok"/>
    <sequenceFlow id="f5" name="approved" sourceRef="Ok" targetRef="E"/>
    <sequenceFlow id="f6" name="needs rework" sourceRef="Ok" targetRef="Rework"/>
    <sequenceFlow id="f7" name="resubmit for review" sourceRef="Rework" targetRef="A"/>
    <sequenceFlow id="f8" name="skip all checks entirely" sourceRef="Split" targetRef="E"/>
  </process></definitions>`},

	// Long boundary captions on consecutive tasks, all fanning into one handler: each
	// caption competes with its neighbour's exception riser and with the next task.
	{"dense-boundary-labels", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="S"/><serviceTask id="T1"/><serviceTask id="T2"/><serviceTask id="T3"/><endEvent id="E"/>
    <boundaryEvent id="e1" name="Connection timed out" attachedToRef="T1"/>
    <boundaryEvent id="e2" name="Authentication failed" attachedToRef="T2"/>
    <boundaryEvent id="e3" name="Quota exceeded" attachedToRef="T3"/>
    <serviceTask id="H"/><endEvent id="EE"/>
    <sequenceFlow id="f1" sourceRef="S" targetRef="T1"/>
    <sequenceFlow id="f2" sourceRef="T1" targetRef="T2"/>
    <sequenceFlow id="f3" sourceRef="T2" targetRef="T3"/>
    <sequenceFlow id="f4" sourceRef="T3" targetRef="E"/>
    <sequenceFlow id="x1" sourceRef="e1" targetRef="H"/>
    <sequenceFlow id="x2" sourceRef="e2" targetRef="H"/>
    <sequenceFlow id="x3" sourceRef="e3" targetRef="H"/>
    <sequenceFlow id="x4" sourceRef="H" targetRef="EE"/>
  </process></definitions>`},

	// Activity shapes beyond the plain task list: a call activity delegating to
	// another process, and the two message-carrying tasks. All are drawn as tasks.
	{"call-and-message-tasks", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="Start"/><callActivity id="Call"/><receiveTask id="Await"/>
    <sendTask id="Notify"/><endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Call"/>
    <sequenceFlow id="f2" sourceRef="Call" targetRef="Await"/>
    <sequenceFlow id="f3" sourceRef="Await" targetRef="Notify"/>
    <sequenceFlow id="f4" sourceRef="Notify" targetRef="End"/>
  </process></definitions>`},

	// An event-based gateway racing a message against a timer: the deferred choice
	// fans out like any other gateway.
	{"event-based-gateway", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="Start"/><eventBasedGateway id="Race"/>
    <intermediateCatchEvent id="Reply"/><intermediateCatchEvent id="Timeout"/>
    <endEvent id="EndReply"/><endEvent id="EndTimeout"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Race"/>
    <sequenceFlow id="f2" sourceRef="Race" targetRef="Reply"/>
    <sequenceFlow id="f3" sourceRef="Race" targetRef="Timeout"/>
    <sequenceFlow id="f4" sourceRef="Reply" targetRef="EndReply"/>
    <sequenceFlow id="f5" sourceRef="Timeout" targetRef="EndTimeout"/>
  </process></definitions>`},

	// A transaction: a container that holds its own flow, like a subprocess, and
	// carries a compensation handler off to the side.
	{"transaction", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="P">
    <startEvent id="Start"/>
    <transaction id="Book">
      <startEvent id="TStart"/><serviceTask id="Reserve"/><endEvent id="TEnd"/>
      <sequenceFlow id="t1" sourceRef="TStart" targetRef="Reserve"/>
      <sequenceFlow id="t2" sourceRef="Reserve" targetRef="TEnd"/>
    </transaction>
    <boundaryEvent id="Cancelled" name="Cancelled" attachedToRef="Book"/>
    <serviceTask id="Undo"/><endEvent id="EndUndo"/><endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Book"/>
    <sequenceFlow id="f2" sourceRef="Book" targetRef="End"/>
    <sequenceFlow id="c1" sourceRef="Cancelled" targetRef="Undo"/>
    <sequenceFlow id="c2" sourceRef="Undo" targetRef="EndUndo"/>
  </process></definitions>`},
}

// sharedErrorHandlerSrc is a real automation process — the mailbox-copy job —
// reduced to what layout depends on: its exact flow nodes, boundary attachments and
// sequence flows, with the scripts and the hand-drawn DI dropped. A long linear run
// whose steps each carry an error boundary, all eight fanning into one shared
// handler near the end, plus an early step with its own separate handler.
//
// It is in the corpus because this is the shape a hand-drawn diagram solves with a
// single horizontal corridor — every exception flow rising to one shared line — and
// the generator has to arrive at the same picture. See
// TestLayoutSharedErrorCorridor for the structural assertions.
const sharedErrorHandlerSrc = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="Process_Start-MailboxCopyContentJob">
    <startEvent id="startEvent_1" name="Start"/>
    <scriptTask id="scriptTask_2" name="Initialize Variables"/>
    <scriptTask id="scriptTask_4" name="Resolve Exchange Target System"/>
    <scriptTask id="scriptTask_6" name="Establish Exchange Session"/>
    <boundaryEvent id="boundaryEvent_6" name="Session Error" attachedToRef="scriptTask_6"/>
    <scriptTask id="scriptTask_8" name="Prepare Status File"/>
    <boundaryEvent id="boundaryEvent_8" name="Error" attachedToRef="scriptTask_8"/>
    <scriptTask id="scriptTask_10" name="Get Source Mailbox Info"/>
    <boundaryEvent id="boundaryEvent_10" name="Error" attachedToRef="scriptTask_10"/>
    <scriptTask id="scriptTask_12" name="Get Target Mailbox Info"/>
    <boundaryEvent id="boundaryEvent_12" name="Error" attachedToRef="scriptTask_12"/>
    <scriptTask id="scriptTask_14" name="Validate Mailbox GUIDs"/>
    <boundaryEvent id="boundaryEvent_14" name="Error" attachedToRef="scriptTask_14"/>
    <scriptTask id="scriptTask_16" name="Disable Source Mailbox"/>
    <boundaryEvent id="boundaryEvent_16" name="Error" attachedToRef="scriptTask_16"/>
    <scriptTask id="scriptTask_18" name="Update Store Mailbox State"/>
    <boundaryEvent id="boundaryEvent_18" name="Error" attachedToRef="scriptTask_18"/>
    <scriptTask id="scriptTask_20" name="Start Mailbox Restore Request"/>
    <boundaryEvent id="boundaryEvent_20" name="Error" attachedToRef="scriptTask_20"/>
    <scriptTask id="scriptTask_22" name="Set LegacyExchangeDN"/>
    <boundaryEvent id="boundaryEvent_22" name="Error" attachedToRef="scriptTask_22"/>
    <scriptTask id="scriptTask_24" name="Log Success and Commit"/>
    <scriptTask id="scriptTask_26" name="Cleanup Remove PSSession"/>
    <endEvent id="endEvent_28" name="End Success"/>
    <scriptTask id="scriptTask_30" name="Handle Session Error"/>
    <endEvent id="endEvent_32" name="End Error Session"/>
    <scriptTask id="scriptTask_40" name="Cleanup on Error"/>
    <scriptTask id="scriptTask_42" name="Handle Error"/>
    <endEvent id="endEvent_44" name="End Error"/>
    <sequenceFlow id="Flow_01" sourceRef="startEvent_1" targetRef="scriptTask_2"/>
    <sequenceFlow id="Flow_02" sourceRef="scriptTask_2" targetRef="scriptTask_4"/>
    <sequenceFlow id="Flow_03" sourceRef="scriptTask_4" targetRef="scriptTask_6"/>
    <sequenceFlow id="Flow_04" sourceRef="scriptTask_6" targetRef="scriptTask_8"/>
    <sequenceFlow id="Flow_05" sourceRef="scriptTask_8" targetRef="scriptTask_10"/>
    <sequenceFlow id="Flow_06" sourceRef="scriptTask_10" targetRef="scriptTask_12"/>
    <sequenceFlow id="Flow_07" sourceRef="scriptTask_12" targetRef="scriptTask_14"/>
    <sequenceFlow id="Flow_08" sourceRef="scriptTask_14" targetRef="scriptTask_16"/>
    <sequenceFlow id="Flow_09" sourceRef="scriptTask_16" targetRef="scriptTask_18"/>
    <sequenceFlow id="Flow_10" sourceRef="scriptTask_18" targetRef="scriptTask_20"/>
    <sequenceFlow id="Flow_11" sourceRef="scriptTask_20" targetRef="scriptTask_22"/>
    <sequenceFlow id="Flow_12" sourceRef="scriptTask_22" targetRef="scriptTask_24"/>
    <sequenceFlow id="Flow_13" sourceRef="scriptTask_24" targetRef="scriptTask_26"/>
    <sequenceFlow id="Flow_14" sourceRef="scriptTask_26" targetRef="endEvent_28"/>
    <sequenceFlow id="Flow_20" sourceRef="boundaryEvent_6" targetRef="scriptTask_30"/>
    <sequenceFlow id="Flow_21" sourceRef="scriptTask_30" targetRef="endEvent_32"/>
    <sequenceFlow id="Flow_30" sourceRef="boundaryEvent_8" targetRef="scriptTask_40"/>
    <sequenceFlow id="Flow_31" sourceRef="boundaryEvent_10" targetRef="scriptTask_40"/>
    <sequenceFlow id="Flow_32" sourceRef="boundaryEvent_12" targetRef="scriptTask_40"/>
    <sequenceFlow id="Flow_33" sourceRef="boundaryEvent_14" targetRef="scriptTask_40"/>
    <sequenceFlow id="Flow_34" sourceRef="boundaryEvent_16" targetRef="scriptTask_40"/>
    <sequenceFlow id="Flow_35" sourceRef="boundaryEvent_18" targetRef="scriptTask_40"/>
    <sequenceFlow id="Flow_36" sourceRef="boundaryEvent_20" targetRef="scriptTask_40"/>
    <sequenceFlow id="Flow_37" sourceRef="boundaryEvent_22" targetRef="scriptTask_40"/>
    <sequenceFlow id="Flow_38" sourceRef="scriptTask_40" targetRef="scriptTask_42"/>
    <sequenceFlow id="Flow_39" sourceRef="scriptTask_42" targetRef="endEvent_44"/>
  </process>
</definitions>`

// TestLayoutSharedErrorCorridor pins the structure a hand-drawn diagram of this
// process uses, which the invariants alone do not capture: they forbid collisions
// but permit an ugly-yet-legal picture, and this shape has a clearly right answer.
//
// Every exception flow must reach the shared handler along **one** horizontal
// corridor, and the branch that has its own handler must sit on a *different*,
// higher row so the corridor passes cleanly beneath it. Getting this wrong is not
// cosmetic: with both branches on one row, an unrelated end event lands directly
// above a boundary event and blocks the flow leaving it.
func TestLayoutSharedErrorCorridor(t *testing.T) {
	di, ok := generateDI([]byte(sharedErrorHandlerSrc))
	if !ok {
		t.Fatal("generateDI: want ok")
	}
	shapes := parseShapes(t, di)
	edges := parseEdges(t, di)

	handler, ok := shapes["scriptTask_40"]
	if !ok {
		t.Fatal("shared error handler has no shape")
	}
	exceptions := []string{"Flow_30", "Flow_31", "Flow_32", "Flow_33", "Flow_34", "Flow_35", "Flow_36", "Flow_37"}

	// One corridor: every exception flow ends with a horizontal run at the same y,
	// arriving at the handler's left edge.
	corridor := -1
	for _, f := range exceptions {
		pts := edges[f]
		if len(pts) < 2 {
			t.Fatalf("exception flow %q has %d waypoint(s)", f, len(pts))
		}
		last, prev := pts[len(pts)-1], pts[len(pts)-2]
		if last.y != prev.y {
			t.Errorf("exception flow %q does not arrive horizontally: %v", f, pts)
			continue
		}
		if last.x != handler.x {
			t.Errorf("exception flow %q arrives at x=%d, not the handler's left edge x=%d", f, last.x, handler.x)
		}
		if corridor == -1 {
			corridor = last.y
		} else if last.y != corridor {
			t.Errorf("exception flow %q runs at y=%d, but the corridor is at y=%d — "+
				"all exception flows must share one line", f, last.y, corridor)
		}
	}

	// The separately-handled branch sits on its own row above the corridor, so the
	// corridor runs beneath it rather than around it.
	sess := shapes["scriptTask_30"]
	sessEnd := shapes["endEvent_32"]
	if sess.y+sess.h/2 == handler.y+handler.h/2 {
		t.Errorf("the session-error branch shares the shared handler's row (cy=%d); "+
			"it must sit on its own row so the corridor stays clear", handler.y+handler.h/2)
	}
	if sessEnd.bottom() >= corridor {
		t.Errorf("session branch end event (y..%d) reaches the corridor at y=%d", sessEnd.bottom(), corridor)
	}

	// And the main line stays one straight run underneath it all.
	trunk := []string{"startEvent_1", "scriptTask_2", "scriptTask_4", "scriptTask_6", "scriptTask_8",
		"scriptTask_10", "scriptTask_12", "scriptTask_14", "scriptTask_16", "scriptTask_18",
		"scriptTask_20", "scriptTask_22", "scriptTask_24", "scriptTask_26", "endEvent_28"}
	want := shapes[trunk[0]]
	wantCY := want.y + want.h/2
	for _, id := range trunk {
		s := shapes[id]
		if cy := s.y + s.h/2; cy != wantCY {
			t.Errorf("main-line node %q center y=%d, want %d", id, cy, wantCY)
		}
	}
	if corridor >= wantCY {
		t.Errorf("corridor y=%d is not above the main line at y=%d", corridor, wantCY)
	}
}

// TestLayoutInvariants runs every invariant over every corpus model. A failure
// names the model and the specific rule, so the offending picture is identifiable
// without reading coordinates.
func TestLayoutInvariants(t *testing.T) {
	for _, tc := range layoutCorpus {
		t.Run(tc.name, func(t *testing.T) {
			di, ok := generateDI([]byte(tc.src))
			if !ok {
				t.Fatalf("generateDI returned not-ok for corpus model %q", tc.name)
			}
			m := newLayoutModel(t, tc.src, di)
			m.checkCompleteness(t)
			m.checkNoShapeOverlap(t)
			m.checkEdgesOrthogonal(t)
			m.checkNoEdgeThroughShape(t)
			m.checkLabelsClear(t)
			m.checkNamedFlowsLabelled(t)
			m.checkForwardEdgesReadLeftToRight(t)
			m.checkTrunkCarriesLongestChain(t)
			m.checkGatewayBranchesLeaveSeparately(t)
			m.checkForwardEdgesStayInBand(t)
		})
	}
}

// crossingBudget caps how many edge crossings a corpus model may be drawn with.
// Absent means zero. A budget is not a target: it is an admission that a shape has
// no crossing-free drawing under this layout model, and the number is the price.
//
// two-corridors: two independent shared handlers whose corridors compete for the
// same columns. Only one gets the row nearest the trunk; the other's flows have to
// reach a higher row from below, and any route from below the main axis to a
// handler above it crosses that axis once. Ordering cannot remove it.
var crossingBudget = map[string]int{"two-corridors": 1, "labelled-detours": 1}

// overlapBudget is the same admission for edges drawn along one another. It should
// stay almost empty: two edges sharing a line read as one, which is worse than a
// crossing, so a model needs a real reason to be here.
//
// labelled-detours: a loop's returning edge and a column-skipping bypass in one
// small process. Both are channel-routed, and both approach their target along the
// trunk row, so a stretch of that row carries two edges. Giving each channel-routed
// edge its own offset within the column gutters was tried and reverted: it removed
// this overlap but added a crossing, because the horizontal channel legs then cut
// across each other's gutters instead. Separating them properly needs per-edge
// routing lanes end to end, not an offset.
var overlapBudget = map[string]int{"labelled-detours": 1}

// TestLayoutScore scores every corpus model on edge crossings and on edges drawn
// on top of one another, and fails a model that exceeds its budget. The invariants
// say a layout is *sound* — nothing collides. These say it is *legible*: two edges
// sharing a line read as one, and a crossing costs the reader a moment. Scoring the
// whole corpus is what makes a layout change judgeable as an improvement rather
// than a change, which coordinate assertions on one diagram never showed.
func TestLayoutScore(t *testing.T) {
	for _, tc := range layoutCorpus {
		t.Run(tc.name, func(t *testing.T) {
			di, ok := generateDI([]byte(tc.src))
			if !ok {
				t.Fatalf("generateDI returned not-ok for %q", tc.name)
			}
			m := newLayoutModel(t, tc.src, di)
			cross, overlap := m.score()
			if want := crossingBudget[tc.name]; cross > want {
				t.Errorf("score[crossings]: %d, budget %d", cross, want)
			}
			if want := overlapBudget[tc.name]; overlap > want {
				t.Errorf("score[overlaps]: %d edge pairs are drawn on top of one another "+
					"(they read as a single line), budget %d", overlap, want)
			}
		})
	}
}

// score counts crossings and collinear overlaps between edges that share no
// endpoint. Edges that do share one are excluded: a fan-out leaving a gateway and a
// fan-in arriving at a shared handler run together by design — that is a bus, not a
// defect — and a boundary event counts as its host for this purpose.
func (m *layoutModel) score() (cross, overlap int) {
	ends := map[string][2]string{}
	for _, f := range m.flows {
		ends[f.Id] = [2]string{f.SourceRef, f.TargetRef}
	}
	norm := func(id string) string {
		if h := m.host[id]; h != "" {
			return h
		}
		return id
	}
	shares := func(a, b string) bool {
		x, y := ends[a], ends[b]
		for _, p := range []string{norm(x[0]), norm(x[1])} {
			for _, q := range []string{norm(y[0]), norm(y[1])} {
				if p == q {
					return true
				}
			}
		}
		return false
	}
	type seg struct {
		a, b point
		id   string
	}
	var segs []seg
	for _, id := range sortedKeys(m.edges) {
		pts := m.edges[id]
		for k := 1; k < len(pts); k++ {
			segs = append(segs, seg{pts[k-1], pts[k], id})
		}
	}
	horiz := func(s seg) bool { return s.a.y == s.b.y }
	for i := 0; i < len(segs); i++ {
		for j := i + 1; j < len(segs); j++ {
			s, u := segs[i], segs[j]
			if s.id == u.id || shares(s.id, u.id) {
				continue
			}
			if horiz(s) != horiz(u) {
				h, v := s, u
				if !horiz(s) {
					h, v = u, s
				}
				if mini(h.a.x, h.b.x) < v.a.x && v.a.x < maxi(h.a.x, h.b.x) &&
					mini(v.a.y, v.b.y) < h.a.y && h.a.y < maxi(v.a.y, v.b.y) {
					cross++
				}
				continue
			}
			if horiz(s) && s.a.y == u.a.y &&
				mini(s.a.x, s.b.x) < maxi(u.a.x, u.b.x) && mini(u.a.x, u.b.x) < maxi(s.a.x, s.b.x) {
				overlap++
			}
			if !horiz(s) && s.a.x == u.a.x &&
				mini(s.a.y, s.b.y) < maxi(u.a.y, u.b.y) && mini(u.a.y, u.b.y) < maxi(s.a.y, s.b.y) {
				overlap++
			}
		}
	}
	return cross, overlap
}

// layoutModel pairs the generated geometry with the semantic roles that decide
// which overlaps are legitimate: a pool, lane, or expanded subprocess is *meant*
// to contain other shapes, and a boundary event is *meant* to straddle its host's
// border. Roles come from the same parser the generator uses, so the checker and
// the generator can't disagree about what an element is.
type layoutModel struct {
	name   string
	di     string
	shapes map[string]shapeBox
	labels map[string]shapeBox
	edges  map[string][]point

	host     map[string]string // boundary event id -> the activity it rides
	flows    []layoutFlow      // every sequence flow, all nesting levels
	nodeIDs  []string          // every flow node that should have a shape
	backEdge map[string]bool   // flow ids that close a cycle
	gateways map[string]bool   // ids drawn as a diamond, which has vertices to leave from
	hasLanes bool              // swimlanes: the flow is meant to step between bands
}

func newLayoutModel(t *testing.T, src, di string) *layoutModel {
	t.Helper()
	var defs layoutDefs
	if err := xml.Unmarshal([]byte(src), &defs); err != nil {
		t.Fatalf("corpus model does not parse: %v", err)
	}
	m := &layoutModel{
		di:       di,
		shapes:   parseShapes(t, di),
		labels:   parseLabels(t, di),
		edges:    parseEdges(t, di),
		host:     map[string]string{},
		gateways: map[string]bool{},
	}
	for _, p := range defs.Processes {
		m.collect(p)
	}
	m.backEdge = findBackEdges(m.nodeIDs, m.flows)
	return m
}

// collect walks a container and everything nested in it, recording the flow nodes
// that must be drawn, the boundary attachments, and the sequence flows.
func (m *layoutModel) collect(c layoutContainer) {
	for _, group := range [][]layoutElem{
		c.StartEvents, c.EndEvents, c.Tasks, c.ServiceTasks, c.ScriptTasks,
		c.UserTasks, c.ManualTasks, c.BizRuleTasks, c.ReceiveTasks, c.SendTasks,
		c.CallActivities, c.ExclusiveGws, c.ParallelGws, c.InclusiveGws,
		c.EventBasedGws, c.IntermediateCatchEvents, c.IntermediateThrowEvents,
	} {
		for _, e := range group {
			if e.Id != "" {
				m.nodeIDs = append(m.nodeIDs, e.Id)
			}
		}
	}
	for _, group := range [][]layoutElem{c.ExclusiveGws, c.ParallelGws, c.InclusiveGws, c.EventBasedGws} {
		for _, e := range group {
			if e.Id != "" {
				m.gateways[e.Id] = true
			}
		}
	}
	for _, be := range c.BoundaryEvents {
		if be.Id != "" {
			m.nodeIDs = append(m.nodeIDs, be.Id)
			m.host[be.Id] = be.AttachedToRef
		}
	}
	m.flows = append(m.flows, c.Flows...)
	if len(c.LaneSets) > 0 {
		m.hasLanes = true
	}
	for _, sp := range c.subContainers() {
		if sp.Id != "" {
			m.nodeIDs = append(m.nodeIDs, sp.Id)
		}
		m.collect(sp)
	}
}

// isContainer reports whether a shape is one that legitimately holds others: a
// pool or lane band (isHorizontal) or an expanded subprocess (isExpanded).
func (m *layoutModel) isContainer(id string) bool {
	s := m.shapes[id]
	return s.expanded || s.horizontal
}

// --- invariants ---

// checkCompleteness: every flow node gets a shape and every sequence flow an edge.
// A layout that silently drops an element is worse than an ugly one — the picture
// no longer shows the model.
func (m *layoutModel) checkCompleteness(t *testing.T) {
	t.Helper()
	for _, id := range m.nodeIDs {
		if _, ok := m.shapes[id]; !ok {
			t.Errorf("invariant[completeness]: flow node %q has no shape", id)
		}
	}
	for _, f := range m.flows {
		if f.Id == "" {
			continue
		}
		if _, ok := m.edges[f.Id]; !ok {
			t.Errorf("invariant[completeness]: sequence flow %q has no edge", f.Id)
		}
	}
}

// checkNoShapeOverlap: no two shapes share interior area, except where the BPMN
// says they should — a container holding its children, or a boundary event
// straddling the border of the activity it is attached to.
func (m *layoutModel) checkNoShapeOverlap(t *testing.T) {
	t.Helper()
	ids := sortedKeys(m.shapes)
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			a, b := ids[i], ids[j]
			if m.isContainer(a) || m.isContainer(b) {
				continue // containers are meant to enclose other shapes
			}
			if m.host[a] == b || m.host[b] == a {
				continue // a boundary event rides its host's border by design
			}
			if overlapArea(m.shapes[a], m.shapes[b]) {
				t.Errorf("invariant[no-shape-overlap]: %q %v overlaps %q %v",
					a, box(m.shapes[a]), b, box(m.shapes[b]))
			}
		}
	}
}

// checkEdgesOrthogonal: every edge runs in axis-aligned segments. Diagonals read
// as sloppy and cut corners across whatever they pass.
func (m *layoutModel) checkEdgesOrthogonal(t *testing.T) {
	t.Helper()
	for _, id := range sortedKeys(m.edges) {
		pts := m.edges[id]
		if len(pts) < 2 {
			t.Errorf("invariant[orthogonal]: edge %q has %d waypoint(s)", id, len(pts))
			continue
		}
		if !isOrthogonal(pts) {
			t.Errorf("invariant[orthogonal]: edge %q has a diagonal segment: %v", id, pts)
		}
	}
}

// checkNoEdgeThroughShape: no edge crosses the interior of a shape that is not one
// of its own endpoints. This is the rule AGENTS.md states for hand-authored models
// ("never through the main-axis boxes") and the generator owes the same. Containers
// are exempt: an edge inside a subprocess or across a lane band legitimately runs
// within that box.
func (m *layoutModel) checkNoEdgeThroughShape(t *testing.T) {
	t.Helper()
	endpoints := map[string][2]string{}
	for _, f := range m.flows {
		endpoints[f.Id] = [2]string{f.SourceRef, f.TargetRef}
	}
	for _, fid := range sortedKeys(m.edges) {
		pts := m.edges[fid]
		ends := endpoints[fid]
		for _, sid := range sortedKeys(m.shapes) {
			if sid == ends[0] || sid == ends[1] || m.isContainer(sid) {
				continue
			}
			// A boundary event on this edge's source is part of the same attachment,
			// and the exception flow leaves right at it.
			if m.host[ends[0]] == sid || m.host[sid] == ends[0] {
				continue
			}
			for k := 1; k < len(pts); k++ {
				if segmentCutsBox(pts[k-1], pts[k], m.shapes[sid]) {
					t.Errorf("invariant[no-edge-through-shape]: edge %q segment %v->%v cuts through %q %v",
						fid, pts[k-1], pts[k], sid, box(m.shapes[sid]))
					break
				}
			}
		}
	}
}

// checkLabelsClear: an explicit label box overlaps no shape and no edge. This is
// the rule the boundary-event caption kept breaking — stated once, it holds for
// every label the generator ever emits, whichever corner it picks.
func (m *layoutModel) checkLabelsClear(t *testing.T) {
	t.Helper()
	for _, owner := range sortedKeys(m.labels) {
		lbl := m.labels[owner]
		for _, sid := range sortedKeys(m.shapes) {
			if sid == owner || m.isContainer(sid) {
				continue
			}
			if overlapArea(lbl, m.shapes[sid]) {
				t.Errorf("invariant[label-clear]: label of %q %v overlaps shape %q %v",
					owner, box(lbl), sid, box(m.shapes[sid]))
			}
		}
		for _, fid := range sortedKeys(m.edges) {
			pts := m.edges[fid]
			for k := 1; k < len(pts); k++ {
				if segmentCutsBox(pts[k-1], pts[k], lbl) {
					t.Errorf("invariant[label-clear]: label of %q %v is crossed by edge %q segment %v->%v",
						owner, box(lbl), fid, pts[k-1], pts[k])
					break
				}
			}
		}
		// Two captions on one spot read as one unreadable smudge, and neither owner is
		// identifiable. Checked once per pair, in sorted order.
		for _, other := range sortedKeys(m.labels) {
			if other <= owner {
				continue
			}
			if overlapArea(lbl, m.labels[other]) {
				t.Errorf("invariant[label-clear]: label of %q %v overlaps label of %q %v",
					owner, box(lbl), other, box(m.labels[other]))
			}
		}
	}
}

// checkNamedFlowsLabelled: a sequence flow that carries a name gets a label
// position of its own. Without one the renderer has to guess, and its guess is the
// polyline's midpoint — which for a branch that steps into another row lands on the
// riser, stranded between two arrows and readable as belonging to either. Where the
// caption then sits is covered by checkLabelsClear, which sees edge labels too.
func (m *layoutModel) checkNamedFlowsLabelled(t *testing.T) {
	t.Helper()
	for _, f := range m.flows {
		if f.Id == "" || f.Name == "" {
			continue
		}
		if _, ok := m.labels[f.Id]; !ok {
			t.Errorf("invariant[flow-label]: named flow %q (%q) has no label position", f.Id, f.Name)
		}
	}
}

// checkForwardEdgesReadLeftToRight: every sequence flow that does not close a cycle
// advances to the right (or stays in the same column). A process reads left to
// right, so a forward step that moves backwards means the layering lost the plot —
// which is exactly what an unhandled loop does to longest-path layering. Back edges
// are exempt: a rework loop is *supposed* to return to an earlier column.
func (m *layoutModel) checkForwardEdgesReadLeftToRight(t *testing.T) {
	t.Helper()
	for _, f := range m.flows {
		if f.Id == "" || m.backEdge[f.Id] {
			continue
		}
		src, sok := m.shapes[f.SourceRef]
		tgt, tok := m.shapes[f.TargetRef]
		if !sok || !tok {
			continue // reported by checkCompleteness
		}
		if tgt.x < src.x {
			t.Errorf("invariant[left-to-right]: forward flow %q runs backwards: %q at x=%d -> %q at x=%d",
				f.Id, f.SourceRef, src.x, f.TargetRef, tgt.x)
		}
	}
}

// collinearRun returns how far two axis-aligned segments run along the same line.
// Zero means they meet at a point or not at all, which is what leaving a diamond by
// two different vertices looks like.
func collinearRun(a1, a2, b1, b2 point) int {
	if a1.y == a2.y && b1.y == b2.y && a1.y == b1.y { // both horizontal, same line
		lo := maxInt(minInt(a1.x, a2.x), minInt(b1.x, b2.x))
		hi := minInt(maxInt(a1.x, a2.x), maxInt(b1.x, b2.x))
		return maxInt(hi-lo, 0)
	}
	if a1.x == a2.x && b1.x == b2.x && a1.x == b1.x { // both vertical, same line
		lo := maxInt(minInt(a1.y, a2.y), minInt(b1.y, b2.y))
		hi := minInt(maxInt(a1.y, a2.y), maxInt(b1.y, b2.y))
		return maxInt(hi-lo, 0)
	}
	return 0
}

// isGateway reports whether an id is drawn as a diamond. Read from the same parser
// the generator uses, so the two cannot disagree about what an element is.
func (m *layoutModel) isGateway(id string) bool { return m.gateways[id] }

// checkGatewayBranchesLeaveSeparately: two flows out of the same gateway must not
// begin with a collinear run. A diamond has four vertices and BPMN uses them: the
// branch that carries on leaves the side, the one that steps to another row leaves
// the top or the bottom. Sharing the side exit draws both on one line for the width
// of the gap — the reader sees a single edge that splits somewhere out in the open,
// and the caption of one branch sits over the stub of the other.
//
// This is narrower than the score's overlap count, which excludes edges sharing an
// endpoint as "a bus, not a defect". A fan-in to a shared handler genuinely is a
// bus: those edges have nowhere else to go. A gateway has three other vertices.
func (m *layoutModel) checkGatewayBranchesLeaveSeparately(t *testing.T) {
	t.Helper()
	bySource := map[string][]string{}
	dir := map[string]int{} // -1 up, 0 same row, +1 down, relative to the gateway
	for _, f := range m.flows {
		if f.Id == "" || m.backEdge[f.Id] || !m.isGateway(f.SourceRef) {
			continue
		}
		s, sok := m.shapes[f.SourceRef]
		t, tok := m.shapes[f.TargetRef]
		if !sok || !tok {
			continue
		}
		switch sc, tc := s.y+s.h/2, t.y+t.h/2; {
		case tc < sc:
			dir[f.Id] = -1
		case tc > sc:
			dir[f.Id] = 1
		}
		bySource[f.SourceRef] = append(bySource[f.SourceRef], f.Id)
	}
	for _, src := range sortedKeys(bySource) {
		ids := bySource[src]
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				if dir[ids[i]] == dir[ids[j]] {
					continue // both go the same way: a fan, and it may share its stub
				}
				a, b := m.edges[ids[i]], m.edges[ids[j]]
				if len(a) < 2 || len(b) < 2 {
					continue
				}
				// The stubs need not be identical to read as one line: it is enough
				// that they run along each other, which is what happens when a branch
				// shares the side exit and only turns off partway across the gap.
				if n := collinearRun(a[0], a[1], b[0], b[1]); n > 0 {
					t.Errorf("invariant[gateway-exits]: flows %q and %q leave gateway %q along the same line for %dpx (%v->%v and %v->%v)",
						ids[i], ids[j], src, n, a[0], a[1], b[0], b[1])
				}
			}
		}
	}
}

// TestLayoutIsDeterministic runs the generator repeatedly over the whole corpus and
// requires byte-identical DI every time. ADR-0127 names this as phase 2's standing
// risk: the ordering sweeps and the dummy chains are keyed by flow, and a single
// range over a map would make a diagram's rows depend on Go's randomized map order
// — reordering a picture between two fetches of the same model.
func TestLayoutIsDeterministic(t *testing.T) {
	for _, tc := range layoutCorpus {
		t.Run(tc.name, func(t *testing.T) {
			first, ok := generateDI([]byte(tc.src))
			if !ok {
				t.Fatalf("generateDI returned not-ok for %q", tc.name)
			}
			for i := 0; i < 20; i++ {
				again, ok := generateDI([]byte(tc.src))
				if !ok {
					t.Fatalf("run %d: generateDI returned not-ok", i)
				}
				if again != first {
					t.Fatalf("run %d differs from the first layout of the same model", i)
				}
			}
		})
	}
}

// checkForwardEdgesStayInBand: a forward flow is drawn among the nodes, not around
// them. An edge skipping several columns used to be sent down to a channel beneath
// the whole diagram — a symptom-level mitigation for having no reserved space in
// the layers it passes over (ADR-0127, phase 2). With dummy nodes holding that
// space the detour is no longer needed, and a forward edge that still dips below
// the lowest shape means the corridor was not reserved. Back edges are exempt: a
// loop returning under the diagram is a legitimate drawing.
func (m *layoutModel) checkForwardEdgesStayInBand(t *testing.T) {
	t.Helper()
	bottom := 0
	for _, id := range sortedKeys(m.shapes) {
		if b := m.shapes[id].y + m.shapes[id].h; b > bottom {
			bottom = b
		}
	}
	for _, f := range m.flows {
		if f.Id == "" || m.backEdge[f.Id] {
			continue
		}
		// An exception flow is not a bypass: it leaves a host's border rather than a
		// grid cell, and reaching a shared handler several rows away legitimately
		// runs through the channel (see the two-corridors model). Corridors are built
		// for node-to-node flows, and this states only what they promise.
		if m.host[f.SourceRef] != "" || m.host[f.TargetRef] != "" {
			continue
		}
		for _, p := range m.edges[f.Id] {
			if p.y > bottom {
				t.Errorf("invariant[edge-in-band]: forward flow %q detours to y=%d, below the lowest shape (y=%d)",
					f.Id, p.y, bottom)
				break
			}
		}
	}
}

// checkTrunkCarriesLongestChain: the main axis — the center line shared by the most
// nodes — must carry at least as many nodes as the longest chain of sequence flows.
// In other words the happy path is the *longest* run through the model and it is
// drawn straight. Without this, a short bypass edge can be mistaken for the main
// line and the real sequence gets exiled to a branch row: structurally sound, but
// the picture tells the wrong story. Lane models are exempt — there the flow is
// meant to step between bands.
func (m *layoutModel) checkTrunkCarriesLongestChain(t *testing.T) {
	t.Helper()
	if m.hasLanes {
		return
	}
	byAxis := map[int]int{}
	for _, id := range m.nodeIDs {
		if m.host[id] != "" {
			continue // boundary events ride their host, not the grid
		}
		s, ok := m.shapes[id]
		if !ok {
			continue
		}
		byAxis[s.y+s.h/2]++
	}
	onAxis := 0
	for _, n := range byAxis {
		if n > onAxis {
			onAxis = n
		}
	}
	if chain := m.longestChain(); onAxis < chain {
		t.Errorf("invariant[trunk-longest]: main axis carries %d node(s) but the longest chain is %d — "+
			"the happy path is not drawn straight (a shortcut may have taken the main line)", onAxis, chain)
	}
}

// longestChain returns the number of nodes on the longest path of forward sequence
// flows. Back edges are excluded so a loop doesn't make the chain unbounded.
func (m *layoutModel) longestChain() int {
	adj := map[string][]string{}
	for _, f := range m.flows {
		if f.Id == "" || m.backEdge[f.Id] || m.host[f.SourceRef] != "" {
			continue // skip loops and exception flows leaving a boundary event
		}
		adj[f.SourceRef] = append(adj[f.SourceRef], f.TargetRef)
	}
	memo := map[string]int{}
	var walk func(string) int
	walk = func(n string) int {
		if v, ok := memo[n]; ok {
			return v
		}
		memo[n] = 1 // guards against any residual cycle
		best := 1
		for _, t := range adj[n] {
			if v := 1 + walk(t); v > best {
				best = v
			}
		}
		memo[n] = best
		return best
	}
	best := 0
	for _, id := range m.nodeIDs {
		if v := walk(id); v > best {
			best = v
		}
	}
	return best
}

// --- geometry helpers ---

// overlapArea reports whether two boxes share interior area. Touching borders is
// fine — adjacent is not overlapping.
func overlapArea(a, b shapeBox) bool {
	return a.x < b.right() && b.x < a.right() && a.y < b.bottom() && b.y < a.bottom()
}

// segmentCutsBox reports whether the axis-aligned segment p->q passes through the
// interior of box. Running along a border, or merely touching it (as an edge does
// where it meets the node it enters), does not count.
func segmentCutsBox(p, q point, box shapeBox) bool {
	if p.y == q.y { // horizontal run: must be strictly inside the vertical span
		if p.y <= box.y || p.y >= box.bottom() {
			return false
		}
		return maxi(mini(p.x, q.x), box.x) < mini(maxi(p.x, q.x), box.right())
	}
	if p.x == q.x { // vertical run: must be strictly inside the horizontal span
		if p.x <= box.x || p.x >= box.right() {
			return false
		}
		return maxi(mini(p.y, q.y), box.y) < mini(maxi(p.y, q.y), box.bottom())
	}
	return false // diagonal: reported by the orthogonality invariant instead
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func mini(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// findBackEdges marks the sequence flows that close a cycle, via a DFS colouring:
// an edge to a node currently on the recursion stack is a back edge. Nodes are
// visited in declaration order so the result is deterministic.
func findBackEdges(nodeIDs []string, flows []layoutFlow) map[string]bool {
	out := map[string]bool{}
	adj := map[string][]layoutFlow{}
	for _, f := range flows {
		adj[f.SourceRef] = append(adj[f.SourceRef], f)
	}
	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := map[string]int{}
	var visit func(string)
	visit = func(n string) {
		color[n] = grey
		for _, f := range adj[n] {
			switch color[f.TargetRef] {
			case grey:
				out[f.Id] = true // target is on the stack: this edge closes a loop
			case white:
				visit(f.TargetRef)
			}
		}
		color[n] = black
	}
	for _, n := range nodeIDs {
		if color[n] == white {
			visit(n)
		}
	}
	return out
}

// parseLabels extracts the explicit BPMNLabel bounds per element, for the shapes
// that carry one. Shapes without a label are absent from the result.
func parseLabels(t *testing.T, di string) map[string]shapeBox {
	t.Helper()
	out := map[string]shapeBox{}
	matches := labelShapeRe.FindAllStringSubmatch(di, -1)
	matches = append(matches, labelEdgeRe.FindAllStringSubmatch(di, -1)...)
	for _, m := range matches {
		atoi := func(s string) int {
			n, err := strconv.Atoi(s)
			if err != nil {
				t.Fatalf("bad number %q in DI: %v", s, err)
			}
			return n
		}
		out[m[1]] = shapeBox{x: atoi(m[2]), y: atoi(m[3]), w: atoi(m[4]), h: atoi(m[5])}
	}
	return out
}

var labelShapeRe = regexp.MustCompile(
	`(?s)<bpmndi:BPMNShape id="[^"]*" bpmnElement="([^"]*)"[^>]*>\s*` +
		`<omgdc:Bounds[^/]*/>\s*<bpmndi:BPMNLabel>\s*` +
		`<omgdc:Bounds x="(-?\d+)" y="(-?\d+)" width="(\d+)" height="(\d+)"/>`)

// An edge's caption is a BPMNLabel after its waypoints. Parsed into the same map as
// shape labels, so every label invariant applies to flow captions as well.
// The waypoint run between the two anchors keeps the match inside one edge: a
// wildcard would happily skip a label-less edge and pin the next edge's caption on
// it. RE2 has no lookahead, so the structure does the fencing.
var labelEdgeRe = regexp.MustCompile(
	`(?s)<bpmndi:BPMNEdge id="[^"]*" bpmnElement="([^"]*)"[^>]*>` +
		`(?:\s*<omgdi:waypoint[^>]*/>)+\s*<bpmndi:BPMNLabel>\s*` +
		`<omgdc:Bounds x="(-?\d+)" y="(-?\d+)" width="(\d+)" height="(\d+)"/>`)

// sortedKeys returns a map's keys in a stable order, so failures are reported
// deterministically rather than in Go's randomized map order.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// box renders bounds compactly for failure messages.
func box(s shapeBox) string {
	return fmt.Sprintf("(%d,%d %dx%d)", s.x, s.y, s.w, s.h)
}
