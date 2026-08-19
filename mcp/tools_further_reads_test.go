package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// dataObjectBPMN parks an instance on a long timer so it stays active, and
// declares two data objects so the data-objects read has something to surface.
const dataObjectBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="withdata" name="With Data" isExecutable="true">
    <dataObject id="DataObject_order" name="order" isCollection="false">
      <dataState name="received"/>
    </dataObject>
    <dataObject id="DataObject_items" isCollection="true"/>
    <startEvent id="s"/>
    <intermediateCatchEvent id="w"><timerEventDefinition><timeDuration>PT3600S</timeDuration></timerEventDefinition></intermediateCatchEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="w"/>
    <sequenceFlow id="f2" sourceRef="w" targetRef="e"/>
  </process>
</definitions>`

// TestInstancesSummaryViaTool reads the per-definition counts and finds the
// deployed definition with an active instance.
func TestInstancesSummaryViaTool(t *testing.T) {
	atlas := newAtlas(t)
	deployAndStartInstance(t, atlas, map[string]any{"key": 1})

	text, isErr := toolText(t, result(t, run(t, atlas, callTool(4, "atlas_instances_summary", map[string]any{}))[0]))
	if isErr {
		t.Fatalf("instances_summary = (%q, isErr=%v)", text, isErr)
	}
	var rows []struct {
		ProcessID string `json:"processId"`
		Active    int    `json:"active"`
	}
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		t.Fatalf("decode summary %q: %v", text, err)
	}
	found := false
	for _, r := range rows {
		if r.ProcessID == "order" {
			found = true
			if r.Active < 1 {
				t.Fatalf("order row active = %d, want >= 1 (%s)", r.Active, text)
			}
		}
	}
	if !found {
		t.Fatalf("summary %q has no row for processId order", text)
	}
}

// TestSearchInstancesViaTool finds a running instance by a variable it carries,
// and returns nothing for a value it does not.
func TestSearchInstancesViaTool(t *testing.T) {
	atlas := newAtlas(t)
	key := deployAndStartInstance(t, atlas, map[string]any{"key": 1, "variables": map[string]any{"customer": "acme"}})

	hitText, isErr := toolText(t, result(t, run(t, atlas, callTool(4, "atlas_search_instances", map[string]any{"q": "customer=acme"}))[0]))
	if isErr {
		t.Fatalf("search (hit) = (%q, isErr=%v)", hitText, isErr)
	}
	var hits []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal([]byte(hitText), &hits); err != nil {
		t.Fatalf("decode search hits %q: %v", hitText, err)
	}
	if len(hits) != 1 || hits[0].Key != key {
		t.Fatalf("search hits = %+v, want the one instance %d", hits, key)
	}

	missText, isErr := toolText(t, result(t, run(t, atlas, callTool(5, "atlas_search_instances", map[string]any{"q": "customer=nobody"}))[0]))
	if isErr || strings.TrimSpace(missText) != "[]" {
		t.Fatalf("search (miss) = (%q, isErr=%v), want []", missText, isErr)
	}
}

// TestSearchInstancesRequiresQ rejects a search with no query as a tool error.
func TestSearchInstancesRequiresQ(t *testing.T) {
	atlas := newAtlas(t)
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(1, "atlas_search_instances", map[string]any{}))[0]))
	if !isErr || !strings.Contains(text, "q") {
		t.Fatalf("search without q = (%q, isErr=%v), want a tool error naming the missing argument", text, isErr)
	}
}

// TestInstanceDataObjectsViaTool reads an instance's declared data objects, each
// with its name and data state.
func TestInstanceDataObjectsViaTool(t *testing.T) {
	atlas := newAtlas(t)
	if _, isErr := toolText(t, result(t, run(t, atlas, callTool(1, "atlas_deploy", map[string]any{"xml": dataObjectBPMN}))[0])); isErr {
		t.Fatal("deploy failed")
	}
	if _, isErr := toolText(t, result(t, run(t, atlas, callTool(2, "atlas_create_instance", map[string]any{"key": 1}))[0])); isErr {
		t.Fatal("create_instance failed")
	}
	listText, isErr := toolText(t, result(t, run(t, atlas, callTool(3, "atlas_list_instances", map[string]any{}))[0]))
	if isErr {
		t.Fatal("list_instances failed")
	}
	var instances []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal([]byte(listText), &instances); err != nil || len(instances) == 0 {
		t.Fatalf("parse instances: err=%v list=%q", err, listText)
	}

	text, isErr := toolText(t, result(t, run(t, atlas, callTool(4, "atlas_instance_data_objects", map[string]any{"key": instances[0].Key}))[0]))
	if isErr {
		t.Fatalf("instance_data_objects = (%q, isErr=%v)", text, isErr)
	}
	var objs []struct {
		Name  string `json:"name"`
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(text), &objs); err != nil {
		t.Fatalf("decode data objects %q: %v", text, err)
	}
	var orderState string
	for _, o := range objs {
		if o.Name == "order" {
			orderState = o.State
		}
	}
	if len(objs) != 2 || orderState != "received" {
		t.Fatalf("data objects = %+v, want 2 including order in state \"received\"", objs)
	}
}
