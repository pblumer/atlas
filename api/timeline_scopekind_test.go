package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// The replay's Variables tab sorts what a selected element could reach into the scopes
// it reached it through. A scope label alone cannot do that: a multi-instance body and
// one of its iterations carry the same element id, and a process constant reads like
// any other root variable. These tests hold the two facts the timeline adds for it —
// what kind of scope a variable lives in, and whether a root variable is a constant.

// scopedVar is the part of a timeline variable these tests read.
type scopedVar struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Scope     string `json:"scope"`
	ScopeKind string `json:"scopeKind"`
	Constant  string `json:"constant"`
}

type scopedStep struct {
	ElementID      string      `json:"elementId"`
	Iteration      int         `json:"iteration"`
	Variables      []scopedVar `json:"variables"`
	VariablesAfter []scopedVar `json:"variablesAfter"`
	Writes         []scopedVar `json:"writes"`
}

// scopedTimeline deploys model, starts one instance with start and returns its replay.
// Every model here runs to the end or parks at a user task, so the instance is the only
// one on the server either way.
func scopedTimeline(t *testing.T, model, start string) []scopedStep {
	t.Helper()
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", model, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), start, "application/json")
	if code != http.StatusOK {
		t.Fatalf("start: status=%d body=%s", code, body)
	}
	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/timeline", onlyInstanceKey(t, ts)), "", "")
	if code != http.StatusOK {
		t.Fatalf("timeline: status=%d body=%s", code, body)
	}
	var tl struct {
		Steps []scopedStep `json:"steps"`
	}
	if err := json.Unmarshal(body, &tl); err != nil {
		t.Fatalf("decode timeline: %v (%s)", err, body)
	}
	return tl.Steps
}

func findVar(vars []scopedVar, name string) (scopedVar, bool) {
	for _, v := range vars {
		if v.Name == name {
			return v, true
		}
	}
	return scopedVar{}, false
}

// A subprocess-local says it is one; a process variable carries no scope kind at all.
func TestTimelineNamesASubprocessScope(t *testing.T) {
	steps := scopedTimeline(t, subProcessVarBPMN, `{"variables":{"prozessVar":"meinWert"}}`)
	for _, s := range steps {
		if s.ElementID != "check" {
			continue
		}
		local, ok := findVar(s.Variables, "meineSubprocVar")
		if !ok || local.Scope != "Arbeit-01" || local.ScopeKind != "subprocess" {
			t.Errorf("meineSubprocVar = %+v (found %v), want scope Arbeit-01 of kind subprocess", local, ok)
		}
		root, ok := findVar(s.Variables, "prozessVar")
		if !ok || root.Scope != "" || root.ScopeKind != "" || root.Constant != "" {
			t.Errorf("prozessVar = %+v (found %v), want a plain process variable", root, ok)
		}
		return
	}
	t.Fatalf("no step for the inner script; steps=%+v", steps)
}

// miCollectModel runs a parallel multi-instance over three items and collects what each
// round computed into an output collection — which lives in the body's scope while the
// loop runs (ADR-0077), under the same element id as the rounds themselves.
const miCollectModel = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="micollect" isExecutable="true">
    <startEvent id="start"/>
    <scriptTask id="rechne" scriptFormat="feel">
      <extensionElements><zeebe:script expression="=item * 2" resultVariable="doppelt"/></extensionElements>
      <multiInstanceLoopCharacteristics isSequential="true">
        <extensionElements><zeebe:loopCharacteristics inputCollection="=[1, 2, 3]" inputElement="item"
          outputCollection="ergebnisse" outputElement="=doppelt"/></extensionElements>
      </multiInstanceLoopCharacteristics>
    </scriptTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="rechne"/>
    <sequenceFlow id="f2" sourceRef="rechne" targetRef="end"/>
  </process>
</definitions>`

// The output collection a multi-instance assembles is named as the body's, so it does
// not read as one more local of the round it appears beside.
func TestTimelineNamesAMultiInstanceBodyScope(t *testing.T) {
	steps := scopedTimeline(t, miCollectModel, `{}`)
	seen := false
	for _, s := range steps {
		if s.ElementID != "rechne" || s.Iteration == 0 {
			continue
		}
		v, ok := findVar(s.Variables, "ergebnisse")
		if !ok {
			continue
		}
		seen = true
		if v.Scope != "rechne" || v.ScopeKind != "multiInstanceBody" {
			t.Errorf("round %d: ergebnisse = %+v, want scope rechne of kind multiInstanceBody", s.Iteration, v)
		}
	}
	if !seen {
		t.Fatalf("no round saw the output collection; steps=%+v", steps)
	}
}

// What a standard loop's rounds write is held in the loop body (ADR-0133), and named so.
func TestTimelineNamesAStandardLoopBodyScope(t *testing.T) {
	steps := scopedTimeline(t, standardLoopBPMN, `{}`)
	seen := false
	for _, s := range steps {
		if s.ElementID != "gruss" || s.Iteration == 0 {
			continue
		}
		for _, v := range append(append([]scopedVar{}, s.Variables...), s.VariablesAfter...) {
			if v.Name != "letzterGruss" || v.Scope == "" {
				continue
			}
			seen = true
			if v.Scope != "gruss" || v.ScopeKind != "loopBody" {
				t.Errorf("round %d: letzterGruss = %+v, want scope gruss of kind loopBody", s.Iteration, v)
			}
		}
	}
	if !seen {
		t.Fatalf("no round held the loop body's result; steps=%+v", steps)
	}
}

// constantsScopeBPMN declares a process constant and a server constant with a fallback,
// so an instance carries both the constant's own root variable and the server namespace.
const constantsScopeBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:atlas="http://atlas/schema/1.0">
  <process id="konst" isExecutable="true">
    <extensionElements>
      <atlas:constants>
        <atlas:constant name="MAX_AGENTS" type="number" value="20"/>
        <atlas:constant name="LANGUAGE" type="string" source="server" value="de-CH"/>
      </atlas:constants>
    </extensionElements>
    <startEvent id="s"/>
    <userTask id="u"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="u"/>
    <sequenceFlow id="f2" sourceRef="u" targetRef="e"/>
  </process>
</definitions>`

// A process constant and a namespace the installation fills say where they come from;
// an ordinary start variable beside them does not.
func TestTimelineMarksConstants(t *testing.T) {
	steps := scopedTimeline(t, constantsScopeBPMN, `{"variables":{"antrag":"A-1"}}`)
	for _, s := range steps {
		if s.ElementID != "u" {
			continue
		}
		want := map[string]string{"MAX_AGENTS": "process", "server": "server", "antrag": ""}
		for name, kind := range want {
			v, ok := findVar(s.Variables, name)
			if !ok {
				t.Errorf("%s missing at the user task; vars=%+v", name, s.Variables)
				continue
			}
			if v.Constant != kind {
				t.Errorf("%s constant = %q, want %q", name, v.Constant, kind)
			}
		}
		return
	}
	t.Fatalf("no step for the user task; steps=%+v", steps)
}
