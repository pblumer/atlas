package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// boundaryReplayBPMN mirrors the shape that surfaced the ghost-token bug: a user
// task with an interrupting boundary event, whose escape route rejoins the normal
// route at a gateway and ends. The boundary is a message (not a timer) so the
// interrupt fires deterministically, without waiting on a clock; the engine path
// is the same — interruptHost terminates the host, and the host's own outgoing
// flow is never taken.
const boundaryReplayBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <message id="Msg_abort" name="abort">
    <extensionElements><zeebe:subscription correlationKey="= caseId"/></extensionElements>
  </message>
  <process id="boundary-replay" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="ack"/>
    <boundaryEvent id="deadline" attachedToRef="ack">
      <messageEventDefinition messageRef="Msg_abort"/>
    </boundaryEvent>
    <exclusiveGateway id="join"/>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="ack"/>
    <sequenceFlow id="f2" sourceRef="ack" targetRef="join"/>
    <sequenceFlow id="f3" sourceRef="deadline" targetRef="join"/>
    <sequenceFlow id="f4" sourceRef="join" targetRef="end"/>
  </process>
</definitions>`

// fetchInterruptedTimeline deploys boundaryReplayBPMN, parks an instance on the
// user task, fires the interrupting boundary with a matching message, and returns
// the finished instance's replay timeline.
func fetchInterruptedTimeline(t *testing.T) instanceTimeline {
	t.Helper()
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", boundaryReplayBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key),
		`{"variables":{"caseId":"case-1"}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create instance status=%d body=%s", code, body)
	}
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/messages",
		`{"name":"abort","correlationKey":"case-1"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("publish status=%d body=%s", code, body)
	}
	key := onlyInstanceKey(t, ts)
	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/timeline", key), "", "")
	if code != http.StatusOK {
		t.Fatalf("timeline status=%d body=%s", code, body)
	}
	var tl instanceTimeline
	if err := json.Unmarshal(body, &tl); err != nil {
		t.Fatalf("decode timeline: %v (%s)", err, body)
	}
	return tl
}

func dumpFrames(tl instanceTimeline) string {
	s := ""
	for i, f := range tl.Frames {
		s += fmt.Sprintf("\n  frame %d pos=%d:", i, f.Position)
		for _, tok := range f.Tokens {
			s += fmt.Sprintf(" [tok=%d el=%s st=%s]", tok.TokenID, tok.ElementID, tok.State)
		}
	}
	return s
}

// TestInterruptedHostLeavesNoGhostToken pins the invariant a finished instance
// must satisfy in the replay: an interrupting boundary event terminates its host,
// so the host's token is consumed by the interrupt — it must not linger waiting
// for a successor activation that the terminated host will never produce.
func TestInterruptedHostLeavesNoGhostToken(t *testing.T) {
	tl := fetchInterruptedTimeline(t)
	if tl.State != "completed" {
		t.Fatalf("state = %q, want completed", tl.State)
	}
	if len(tl.Frames) == 0 {
		t.Fatal("no frames")
	}
	last := tl.Frames[len(tl.Frames)-1]
	if len(last.Tokens) != 0 {
		t.Errorf("completed instance ends with %d live token(s), want none:%s", len(last.Tokens), dumpFrames(tl))
	}
	// The host's token must be gone from the moment the escape route is walked: no
	// frame that already shows the boundary's continuation may still show the host.
	sawJoin := false
	for _, f := range tl.Frames {
		for _, tok := range f.Tokens {
			if tok.ElementID == "join" || tok.ElementID == "end" {
				sawJoin = true
			}
		}
		if !sawJoin {
			continue
		}
		for _, tok := range f.Tokens {
			if tok.ElementID == "ack" {
				t.Fatalf("terminated host still holds a token after the interrupt continued:%s", dumpFrames(tl))
			}
		}
	}
	if !sawJoin {
		t.Fatalf("the boundary's escape route never appears in the frames:%s", dumpFrames(tl))
	}
}

// TestArmedBoundaryHasNoSourceFlow pins that an element activated without taking a
// sequence flow records no source flow. An armed boundary event is attached to its
// host, not reached over an edge, so the replay must not claim it came from one —
// the frontend would animate the token along an edge that does not exist, and the
// frame fold would treat the arming as the successor of whatever completed on that
// edge's source, retiring a token that is still live.
func TestArmedBoundaryHasNoSourceFlow(t *testing.T) {
	tl := fetchInterruptedTimeline(t)
	seen := false
	for _, s := range tl.Steps {
		if s.ElementID != "deadline" {
			continue
		}
		seen = true
		if s.SourceElementID != "" {
			t.Errorf("armed boundary reports sourceElementId = %q, want none (it is attached, not entered over a flow)", s.SourceElementID)
		}
	}
	if !seen {
		t.Fatal("no step for the boundary event")
	}
}

// deadEndGatewayBPMN routes into an exclusive gateway whose only condition is
// false and which has no default flow: the gateway completes, takes nothing, and
// the instance drains to completed with the branch simply ending there.
const deadEndGatewayBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <process id="dead-end" isExecutable="true">
    <startEvent id="start"/>
    <exclusiveGateway id="choose"/>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="choose"/>
    <sequenceFlow id="f2" sourceRef="choose" targetRef="end">
      <conditionExpression xsi:type="tFormalExpression">= false</conditionExpression>
    </sequenceFlow>
  </process>
</definitions>`

// TestCancelledInstanceEndsWithNoToken pins the invariant for a token that never
// reached a successor at all: the gateway matches no condition and has no default,
// so it completes and takes nothing, stranding the instance with a token deferred
// on it forever. Once an operator cancels that instance it is no longer running,
// and its replay must not still show the token — a finished instance holds no
// element instance, so it can hold no token (ADR-0136).
func TestCancelledInstanceEndsWithNoToken(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", deadEndGatewayBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	if code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance status=%d body=%s", code, body)
	}
	key := onlyInstanceKey(t, ts)
	if code, body = doReq(t, ts, http.MethodDelete, fmt.Sprintf("/api/v1/instances/%d", key), "", ""); code != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", code, body)
	}
	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/timeline", key), "", "")
	if code != http.StatusOK {
		t.Fatalf("timeline status=%d body=%s", code, body)
	}
	var tl instanceTimeline
	if err := json.Unmarshal(body, &tl); err != nil {
		t.Fatalf("decode timeline: %v (%s)", err, body)
	}
	if tl.State == "active" {
		t.Fatalf("instance still active after cancel:%s", dumpFrames(tl))
	}
	if last := tl.Frames[len(tl.Frames)-1]; len(last.Tokens) != 0 {
		t.Errorf("cancelled instance ends with %d live token(s), want none:%s", len(last.Tokens), dumpFrames(tl))
	}
}
