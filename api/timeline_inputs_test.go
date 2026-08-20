package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// ioMappingBPMN gives its service task three zeebe:ioMapping inputs — a plain path, an
// arithmetic expression, and a whole object — so the timeline's inputs are checked
// against values only the engine could have produced, not against the sources as
// written.
const ioMappingBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="iomap" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="check">
      <extensionElements>
        <zeebe:taskDefinition type="check"/>
        <zeebe:ioMapping>
          <zeebe:input source="=order.customer" target="customer"/>
          <zeebe:input source="=order.amount * 2" target="doubled"/>
          <zeebe:input source="=order" target="whole"/>
        </zeebe:ioMapping>
      </extensionElements>
    </serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="check"/>
    <sequenceFlow id="f2" sourceRef="check" targetRef="end"/>
  </process>
</definitions>`

// TestTimelineCarriesInputMappings covers what the diagram's in/out card shows on the
// "in" side: an activity's zeebe:ioMapping inputs live in its own activity-local scope
// (ADR-0068), which the timeline's process- and subprocess-scope folds never read, so
// before this the values a task was actually handed were on the log and nowhere in the
// API. The expressions are evaluated, so the assertions are on results, not sources.
func TestTimelineCarriesInputMappings(t *testing.T) {
	ts := newTestServer(t)
	c := newClient(t)

	code, dep := cReqCT(t, c, ts, http.MethodPost, "/api/v1/deployments", ioMappingBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, dep)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(dep, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	if code, raw := cReq(t, c, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key),
		`{"variables":{"order":{"customer":"ACME","amount":50}}}`); code != http.StatusOK {
		t.Fatalf("start: %d (%s)", code, raw)
	}
	key := activeInstanceKey(t, c, ts)

	code, raw := cReq(t, c, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/timeline", key), "")
	if code != http.StatusOK {
		t.Fatalf("timeline: %d (%s)", code, raw)
	}
	var tl struct {
		Steps []struct {
			ElementID string `json:"elementId"`
			Inputs    []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
				Kind  string `json:"kind"`
			} `json:"inputs"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(raw, &tl); err != nil {
		t.Fatalf("decode timeline: %v", err)
	}

	seen := false
	for _, s := range tl.Steps {
		if s.ElementID != "check" {
			// An element that declares no input mapping was handed nothing of its own,
			// so it must carry no inputs at all — otherwise every step would sprout an
			// empty "in" section on the diagram.
			if len(s.Inputs) != 0 {
				t.Errorf("step %s carries inputs %+v, want none — it declares no input mapping", s.ElementID, s.Inputs)
			}
			continue
		}
		seen = true
		got := map[string]string{}
		kinds := map[string]string{}
		for _, v := range s.Inputs {
			got[v.Name] = v.Value
			kinds[v.Name] = v.Kind
		}
		// Sorted by name, so the card reads the same on every replay of the same instance.
		if len(s.Inputs) != 3 || s.Inputs[0].Name != "customer" || s.Inputs[1].Name != "doubled" || s.Inputs[2].Name != "whole" {
			t.Fatalf("inputs = %+v, want customer, doubled, whole in that order", s.Inputs)
		}
		if got["customer"] != "ACME" {
			t.Errorf("customer = %q, want ACME", got["customer"])
		}
		// The point of reading the evaluated local rather than the mapping source: the
		// operator sees 100, not "=order.amount * 2".
		if got["doubled"] != "100" || kinds["doubled"] != "number" {
			t.Errorf("doubled = %q (%s), want the evaluated 100 as a number", got["doubled"], kinds["doubled"])
		}
		if kinds["whole"] != "json" || got["whole"] == "" {
			t.Errorf("whole = %q (%s), want the mapped object as json", got["whole"], kinds["whole"])
		}
	}
	if !seen {
		t.Fatal("the timeline has no step for the io-mapped task")
	}
}

// TestTimelineHasNoInputsWithoutMappings guards the cheap path: an instance whose
// definition declares no input mapping anywhere must not gain an inputs field on any
// step — nothing is scanned, and the diagram card stays out of the way.
func TestTimelineHasNoInputsWithoutMappings(t *testing.T) {
	ts := newTestServer(t)
	c := newClient(t)

	code, dep := cReqCT(t, c, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, dep)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(dep, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	if code, raw := cReq(t, c, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key),
		`{"variables":{"amount":7}}`); code != http.StatusOK {
		t.Fatalf("start: %d (%s)", code, raw)
	}
	key := activeInstanceKey(t, c, ts)
	code, raw := cReq(t, c, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/timeline", key), "")
	if code != http.StatusOK {
		t.Fatalf("timeline: %d (%s)", code, raw)
	}
	var tl struct {
		Steps []map[string]any `json:"steps"`
	}
	if err := json.Unmarshal(raw, &tl); err != nil {
		t.Fatalf("decode timeline: %v", err)
	}
	if len(tl.Steps) == 0 {
		t.Fatal("timeline has no steps")
	}
	for _, s := range tl.Steps {
		if _, ok := s["inputs"]; ok {
			t.Errorf("step %v carries an inputs field with no mapping declared", s["elementId"])
		}
	}
}
