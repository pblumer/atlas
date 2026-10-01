package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// A multi-instance activity's rounds in an instance's timeline (ADR-0077) while
// they are still open, and after the instance was cancelled under them — the two
// outcomes a finished run never shows — and the count a loop by cardinality was
// given.

// timelineLoopPathsBPMN parks on a parallel multi-instance user task whose round
// count is read from a variable.
const timelineLoopPathsBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="mi-open" isExecutable="true">
    <startEvent id="s"/>
    <userTask id="sign" name="Sign">
      <multiInstanceLoopCharacteristics isSequential="false">
        <loopCardinality>=signers</loopCardinality>
      </multiInstanceLoopCharacteristics>
    </userTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="sign"/>
    <sequenceFlow id="f2" sourceRef="sign" targetRef="e"/>
  </process>
</definitions>`

type timelineLoopPathsStep struct {
	ElementID string `json:"elementId"`
	Loop      *struct {
		Kind       string `json:"kind"`
		Collection string `json:"collection"`
		Rounds     int    `json:"rounds"`
		Outcome    string `json:"outcome"`
		Reads      []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"reads"`
	} `json:"loop"`
}

// timelineLoopPathsStart deploys the model, starts it with two signers and
// returns the instance key.
func timelineLoopPathsStart(t *testing.T, s *Server) uint64 {
	t.Helper()
	h := s.Handler()
	if code, body := recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/deployments",
		strings.NewReader(timelineLoopPathsBPMN)); code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, body)
	}
	code, body := recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/instances",
		strings.NewReader(`{"processId":"mi-open","variables":{"signers":2}}`))
	if code != http.StatusOK {
		t.Fatalf("start: %d %s", code, body)
	}
	var resp createInstanceResp
	if err := json.Unmarshal([]byte(body), &resp); err != nil || resp.InstanceKey == 0 {
		t.Fatalf("decode start: %v (%s)", err, body)
	}
	return resp.InstanceKey
}

// timelineLoopPathsSteps reads the instance's timeline and returns its looping steps.
func timelineLoopPathsSteps(t *testing.T, s *Server, key uint64) (body timelineLoopPathsStep, rounds []timelineLoopPathsStep) {
	t.Helper()
	code, raw := recertifyHTTPPathsCall(t, s.Handler(), http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/timeline", key), nil)
	if code != http.StatusOK {
		t.Fatalf("timeline: %d %s", code, raw)
	}
	var tl struct {
		Steps []timelineLoopPathsStep `json:"steps"`
	}
	if err := json.Unmarshal([]byte(raw), &tl); err != nil {
		t.Fatalf("decode timeline: %v", err)
	}
	for _, st := range tl.Steps {
		if st.ElementID != "sign" || st.Loop == nil {
			continue
		}
		if st.Loop.Outcome == "" {
			body = st
		} else {
			rounds = append(rounds, st)
		}
	}
	return body, rounds
}

// TestTimelineLoopOpenRoundsAreRunningAndTheCountSaysWhatItRead. The body names
// the cardinality expression and the value it read, so a loop that ran two rounds
// can be traced to the variable that said two; each round still open is running.
func TestTimelineLoopOpenRoundsAreRunningAndTheCountSaysWhatItRead(t *testing.T) {
	srv := newServerForErrors(t)
	key := timelineLoopPathsStart(t, srv)

	body, rounds := timelineLoopPathsSteps(t, srv, key)
	if body.Loop == nil || body.Loop.Kind != "multi-instance" || body.Loop.Collection != "signers" {
		t.Fatalf("body = %+v, want a multi-instance counted by signers", body.Loop)
	}
	if len(body.Loop.Reads) != 1 || body.Loop.Reads[0].Name != "signers" || body.Loop.Reads[0].Value != "2" {
		t.Errorf("reads = %+v, want signers = 2", body.Loop.Reads)
	}
	if len(rounds) != 2 {
		t.Fatalf("%d round(s), want 2", len(rounds))
	}
	for _, r := range rounds {
		if r.Loop.Outcome != "running" {
			t.Errorf("round outcome = %q, want running", r.Loop.Outcome)
		}
	}
}

// TestTimelineLoopRoundsCancelledUnderneathAreTerminated, not "stopped": nothing in
// the model decided to stop them.
func TestTimelineLoopRoundsCancelledUnderneathAreTerminated(t *testing.T) {
	srv := newServerForErrors(t)
	key := timelineLoopPathsStart(t, srv)
	if code, body := recertifyHTTPPathsCall(t, srv.Handler(), http.MethodDelete,
		fmt.Sprintf("/api/v1/instances/%d", key), nil); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("cancel: %d %s", code, body)
	}

	_, rounds := timelineLoopPathsSteps(t, srv, key)
	if len(rounds) != 2 {
		t.Fatalf("%d round(s), want 2", len(rounds))
	}
	for _, r := range rounds {
		if r.Loop.Outcome != "terminated" {
			t.Errorf("round outcome = %q, want terminated", r.Loop.Outcome)
		}
	}
}
