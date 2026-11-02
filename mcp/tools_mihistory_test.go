package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"
)

const miToolBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="d">
  <process id="mitool" isExecutable="true">
    <startEvent id="s"/>
    <scriptTask id="calc">
      <extensionElements><zeebe:script resultVariable="r" expression="= loopCounter * 2"/></extensionElements>
      <multiInstanceLoopCharacteristics isSequential="true"><loopCardinality>5</loopCardinality></multiInstanceLoopCharacteristics>
    </scriptTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="calc"/>
    <sequenceFlow id="f2" sourceRef="calc" targetRef="e"/>
  </process>
</definitions>`

// TestMultiInstanceHistoryViaTools walks the bounded MI history the way an agent
// would: list the bodies, page the iterations with the cursor handed back, jump
// to one iteration and read its detail.
func TestMultiInstanceHistoryViaTools(t *testing.T) {
	atlas := newAtlas(t)
	if text, isErr := toolText(t, result(t, run(t, atlas, callTool(1, "atlas_deploy", map[string]any{"xml": miToolBPMN}))[0])); isErr {
		t.Fatalf("deploy: %s", text)
	}
	if text, isErr := toolText(t, result(t, run(t, atlas, callTool(2, "atlas_create_instance", map[string]any{"key": 1}))[0])); isErr {
		t.Fatalf("create: %s", text)
	}
	key := firstInstanceKey(t, atlas, 3, map[string]any{"state": "finished"})
	call := func(id int, name string, args map[string]any, out any) {
		t.Helper()
		text, isErr := toolText(t, result(t, run(t, atlas, callTool(id, name, args))[0]))
		if isErr {
			t.Fatalf("%s: %s", name, text)
		}
		if err := json.Unmarshal([]byte(text), out); err != nil {
			t.Fatalf("%s: %v (%s)", name, err, text)
		}
	}
	var bodies struct {
		Bodies []struct {
			BodyElementInstanceKey uint64 `json:"bodyElementInstanceKey"`
			Mode                   string `json:"mode"`
			Completed              int    `json:"completed"`
		} `json:"bodies"`
	}
	call(4, "atlas_instance_multi_instances", map[string]any{"key": key}, &bodies)
	if len(bodies.Bodies) != 1 || bodies.Bodies[0].Mode != "sequential" || bodies.Bodies[0].Completed != 5 {
		t.Fatalf("bodies: %+v", bodies)
	}
	body := bodies.Bodies[0].BodyElementInstanceKey
	var body1 struct {
		Body struct {
			OpenIncidents int `json:"openIncidents"`
		} `json:"body"`
	}
	call(5, "atlas_multi_instance_body", map[string]any{"key": key, "bodyKey": body}, &body1)
	type page struct {
		Iterations []struct {
			ElementInstanceKey uint64 `json:"elementInstanceKey"`
			Iteration          int    `json:"iteration"`
		} `json:"iterations"`
		NextCursor string `json:"nextCursor"`
	}
	var p1, p2 page
	call(6, "atlas_multi_instance_iterations", map[string]any{"key": key, "bodyKey": body, "limit": 3, "state": "completed"}, &p1)
	if len(p1.Iterations) != 3 || p1.NextCursor == "" {
		t.Fatalf("first page: %+v", p1)
	}
	call(7, "atlas_multi_instance_iterations", map[string]any{"key": key, "bodyKey": body, "limit": 3, "state": "completed", "cursor": p1.NextCursor}, &p2)
	if len(p2.Iterations) != 2 || p2.Iterations[0].Iteration != 4 || p2.NextCursor != "" {
		t.Fatalf("second page: %+v", p2)
	}
	var detail struct {
		Iteration struct {
			Iteration int    `json:"iteration"`
			State     string `json:"state"`
		} `json:"iteration"`
	}
	call(8, "atlas_multi_instance_iteration", map[string]any{"key": key, "bodyKey": body, "iterationKey": p2.Iterations[1].ElementInstanceKey}, &detail)
	if detail.Iteration.Iteration != 5 || detail.Iteration.State != "completed" {
		t.Fatalf("detail: %+v", detail)
	}
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(9, "atlas_multi_instance_iterations", map[string]any{"key": key, "bodyKey": body, "cursor": 7}))[0]))
	if !isErr || !strings.Contains(text, "cursor") {
		t.Fatalf("non-string cursor: %q %v", text, isErr)
	}
	text, isErr = toolText(t, result(t, run(t, atlas, callTool(10, "atlas_multi_instance_iterations", map[string]any{"key": key, "bodyKey": body, "incidents": "yes"}))[0]))
	if !isErr || !strings.Contains(text, "incidents") {
		t.Fatalf("non-boolean incidents: %q %v", text, isErr)
	}
}

func TestInstanceOutlineViaTool(t *testing.T) {
	atlas := newAtlas(t)
	if text, isErr := toolText(t, result(t, run(t, atlas, callTool(1, "atlas_deploy", map[string]any{"xml": miToolBPMN}))[0])); isErr {
		t.Fatalf("deploy: %s", text)
	}
	if text, isErr := toolText(t, result(t, run(t, atlas, callTool(2, "atlas_create_instance", map[string]any{"key": 1}))[0])); isErr {
		t.Fatalf("create: %s", text)
	}
	key := firstInstanceKey(t, atlas, 3, map[string]any{"state": "finished"})
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(4, "atlas_instance_outline", map[string]any{"key": key, "limit": 3}))[0]))
	if isErr {
		t.Fatalf("outline: %s", text)
	}
	var page struct {
		Steps []struct {
			ElementID         string `json:"elementId"`
			MultiInstanceBody bool   `json:"multiInstanceBody"`
		} `json:"steps"`
		NextCursor string `json:"nextCursor"`
	}
	if err := json.Unmarshal([]byte(text), &page); err != nil || len(page.Steps) != 3 || page.NextCursor == "" {
		t.Fatalf("outline page: %v %s", err, text)
	}
	text, isErr = toolText(t, result(t, run(t, atlas, callTool(5, "atlas_instance_outline", map[string]any{"key": key, "cursor": page.NextCursor}))[0]))
	if isErr || !strings.Contains(text, `"elementId":"e"`) {
		t.Fatalf("second page: %v %s", isErr, text)
	}
}
