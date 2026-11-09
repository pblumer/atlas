package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// A round of a multi-instance keeps what it computes in its own scope: the activity's
// scope chain gives it one, and the body collects the round's output element from it
// when the round finishes (ADR-0077). The timeline read that scope only for the round's
// inputs, so every round reported that it had written nothing — the very round whose
// result then appeared in the collection once the loop was done.
func TestTimelineNamesWhatALoopRoundWroteInItsOwnScope(t *testing.T) {
	steps := scopedTimeline(t, miCollectModel, `{}`)
	rounds := 0
	for _, s := range steps {
		if s.ElementID != "rechne" || s.Iteration == 0 {
			continue
		}
		rounds++
		got, ok := findVar(s.Writes, "doppelt")
		if !ok {
			t.Errorf("round %d: writes = %+v, want the round's own result doppelt", s.Iteration, s.Writes)
			continue
		}
		if want := fmt.Sprint(2 * s.Iteration); got.Value != want {
			t.Errorf("round %d: doppelt = %q, want %q", s.Iteration, got.Value, want)
		}
		if got.Scope != "rechne" || got.ScopeKind != "local" {
			t.Errorf("round %d: doppelt = %+v, want scope rechne of kind local", s.Iteration, got)
		}
		// What the round was handed is its input, not its output.
		for _, name := range []string{"item", "loopCounter"} {
			if _, ok := findVar(s.Writes, name); ok {
				t.Errorf("round %d: %s listed as written; writes = %+v", s.Iteration, name, s.Writes)
			}
		}
	}
	if rounds != 3 {
		t.Fatalf("saw %d rounds, want 3; steps=%+v", rounds, steps)
	}
}

// miUserTaskModel is the case the operator reported: a person assesses each position of
// an order in one round of a multi-instance user task, and the form's answer is the
// round's result, collected into the body's list.
const miUserTaskModel = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="mitask" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="bewerten" name="Position bewerten">
      <multiInstanceLoopCharacteristics isSequential="true">
        <extensionElements><zeebe:loopCharacteristics inputCollection="=positionen" inputElement="pos"
          outputCollection="bewertungen" outputElement="=bewertung"/></extensionElements>
      </multiInstanceLoopCharacteristics>
    </userTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="bewerten"/>
    <sequenceFlow id="f2" sourceRef="bewerten" targetRef="end"/>
  </process>
</definitions>`

// A person's answer to one round of a multi-instance user task is that round's output.
func TestTimelineNamesWhatAPersonAnsweredInALoopRound(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", miUserTaskModel, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key),
		`{"variables":{"positionen":["Notebook","Maus"]}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("start: status=%d body=%s", code, body)
	}
	pi := onlyInstanceKey(t, ts)
	for _, answer := range []string{"gut", "schlecht"} {
		code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/tasks?processInstance=%d", pi), "", "")
		if code != http.StatusOK {
			t.Fatalf("list tasks: status=%d body=%s", code, body)
		}
		var rows []struct {
			Key uint64 `json:"key"`
		}
		if err := json.Unmarshal(listRows(t, body), &rows); err != nil || len(rows) != 1 {
			t.Fatalf("open tasks = %s (%v), want exactly one", body, err)
		}
		code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/complete", rows[0].Key),
			fmt.Sprintf(`{"variables":{"bewertung":%q}}`, answer), "application/json")
		if code != http.StatusOK {
			t.Fatalf("complete: status=%d body=%s", code, body)
		}
	}
	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/timeline", pi), "", "")
	if code != http.StatusOK {
		t.Fatalf("timeline: status=%d body=%s", code, body)
	}
	var tl struct {
		Steps []scopedStep `json:"steps"`
	}
	if err := json.Unmarshal(body, &tl); err != nil {
		t.Fatalf("decode timeline: %v", err)
	}
	want := map[int]string{1: "gut", 2: "schlecht"}
	for _, s := range tl.Steps {
		if s.ElementID != "bewerten" || s.Iteration == 0 {
			continue
		}
		got, ok := findVar(s.Writes, "bewertung")
		if !ok || got.Value != want[s.Iteration] {
			t.Errorf("round %d: writes = %+v, want bewertung = %q", s.Iteration, s.Writes, want[s.Iteration])
		}
		delete(want, s.Iteration)
	}
	if len(want) != 0 {
		t.Errorf("rounds not seen: %v", want)
	}
}
