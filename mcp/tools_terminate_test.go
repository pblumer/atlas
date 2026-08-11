package mcp_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// deployAndStartInstances deploys sampleBPMN once and starts one instance per
// entry in varsList (a nil entry starts with no variables), returning every
// active instance key. sampleBPMN parks on its service task, so all stay active.
func deployAndStartInstances(t *testing.T, atlas *httptest.Server, varsList []map[string]any) []uint64 {
	t.Helper()
	if _, isErr := toolText(t, result(t, run(t, atlas, callTool(1, "atlas_deploy", map[string]any{"xml": sampleBPMN}))[0])); isErr {
		t.Fatal("deploy failed")
	}
	for i, vars := range varsList {
		args := map[string]any{"key": 1}
		if vars != nil {
			args["variables"] = vars
		}
		if _, isErr := toolText(t, result(t, run(t, atlas, callTool(2, "atlas_create_instance", args))[0])); isErr {
			t.Fatalf("create_instance %d failed", i)
		}
	}
	listText, isErr := toolText(t, result(t, run(t, atlas, callTool(3, "atlas_list_instances", map[string]any{}))[0]))
	if isErr {
		t.Fatal("list_instances failed")
	}
	var instances []struct {
		Key   uint64 `json:"key"`
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(listText), &instances); err != nil {
		t.Fatalf("parse instances %q: %v", listText, err)
	}
	keys := []uint64{}
	for _, in := range instances {
		if in.State == "active" {
			keys = append(keys, in.Key)
		}
	}
	return keys
}

type terminateResult struct {
	Terminated int  `json:"terminated"`
	NotFound   int  `json:"notFound"`
	Remaining  bool `json:"remaining"`
	Stats      struct {
		ActiveProcessInstances int `json:"activeProcessInstances"`
	} `json:"stats"`
}

func terminate(t *testing.T, atlas *httptest.Server, args map[string]any) terminateResult {
	t.Helper()
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(9, "atlas_terminate_instances", args))[0]))
	if isErr {
		t.Fatalf("terminate_instances = (%q, isErr=%v)", text, isErr)
	}
	var res terminateResult
	if err := json.Unmarshal([]byte(text), &res); err != nil {
		t.Fatalf("decode terminate result %q: %v", text, err)
	}
	return res
}

// TestTerminateInstancesByKeysViaTool terminates an explicit key set; a bogus key
// mixed in is reported as notFound rather than failing the call.
func TestTerminateInstancesByKeysViaTool(t *testing.T) {
	atlas := newAtlas(t)
	keys := deployAndStartInstances(t, atlas, []map[string]any{nil, nil})
	if len(keys) != 2 {
		t.Fatalf("want 2 active instances, got %d", len(keys))
	}

	res := terminate(t, atlas, map[string]any{"keys": []any{keys[0], keys[1], uint64(999999)}})
	if res.Terminated != 2 || res.NotFound != 1 || res.Remaining {
		t.Fatalf("terminate result = %+v, want terminated 2 / notFound 1 / remaining false", res)
	}
	if res.Stats.ActiveProcessInstances != 0 {
		t.Fatalf("active after terminate = %d, want 0", res.Stats.ActiveProcessInstances)
	}
}

// TestTerminateInstancesByFilterViaTool drains a definition's instances in bounded
// batches: limit 1 terminates one and reports remaining, a follow-up clears the rest.
func TestTerminateInstancesByFilterViaTool(t *testing.T) {
	atlas := newAtlas(t)
	if keys := deployAndStartInstances(t, atlas, []map[string]any{nil, nil}); len(keys) != 2 {
		t.Fatalf("want 2 active instances, got %d", len(keys))
	}

	first := terminate(t, atlas, map[string]any{"processDefKey": 1, "limit": 1})
	if first.Terminated != 1 || !first.Remaining {
		t.Fatalf("first batch = %+v, want terminated 1 / remaining true", first)
	}
	second := terminate(t, atlas, map[string]any{"processDefKey": 1})
	if second.Terminated != 1 || second.Remaining || second.Stats.ActiveProcessInstances != 0 {
		t.Fatalf("second batch = %+v, want terminated 1 / remaining false / 0 active", second)
	}
}

// TestTerminateInstancesByQueryViaTool narrows filter mode to instances carrying a
// matching variable, leaving the others running.
func TestTerminateInstancesByQueryViaTool(t *testing.T) {
	atlas := newAtlas(t)
	keys := deployAndStartInstances(t, atlas, []map[string]any{
		{"customer": "acme"},
		{"customer": "globex"},
	})
	if len(keys) != 2 {
		t.Fatalf("want 2 active instances, got %d", len(keys))
	}

	res := terminate(t, atlas, map[string]any{"processDefKey": 1, "q": "customer=acme"})
	if res.Terminated != 1 || res.Stats.ActiveProcessInstances != 1 {
		t.Fatalf("query terminate = %+v, want terminated 1 / 1 still active", res)
	}
}

// TestTerminateInstancesRequiresSelector rejects a call with neither keys nor
// processDefKey as a tool error, before any request reaches the server.
func TestTerminateInstancesRequiresSelector(t *testing.T) {
	atlas := newAtlas(t)
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(1, "atlas_terminate_instances", map[string]any{}))[0]))
	if !isErr || !strings.Contains(text, "keys") {
		t.Fatalf("terminate with no selector = (%q, isErr=%v), want a tool error", text, isErr)
	}
}

// TestTerminateInstancesBothSelectorsIsError forwards a both-modes request and
// surfaces the server's mutual-exclusion 400 as a tool error.
func TestTerminateInstancesBothSelectorsIsError(t *testing.T) {
	atlas := newAtlas(t)
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(1, "atlas_terminate_instances", map[string]any{"keys": []any{uint64(1)}, "processDefKey": 1}))[0]))
	if !isErr || !strings.Contains(text, "not both") {
		t.Fatalf("terminate with both selectors = (%q, isErr=%v), want a mutual-exclusion tool error", text, isErr)
	}
}

// TestTerminateInstancesUnknownDefIsToolError filters on a definition that does not
// exist, surfacing the server's 404 as a tool error.
func TestTerminateInstancesUnknownDefIsToolError(t *testing.T) {
	atlas := newAtlas(t)
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(1, "atlas_terminate_instances", map[string]any{"processDefKey": 999999}))[0]))
	if !isErr || !strings.Contains(text, "no deployment") {
		t.Fatalf("terminate unknown def = (%q, isErr=%v), want a not-found tool error", text, isErr)
	}
}

// TestTerminateInstancesRejectsBadKeys covers the keys argument-validation branch:
// a non-array keys value, and a non-integer element, are tool errors.
func TestTerminateInstancesRejectsBadKeys(t *testing.T) {
	atlas := newAtlas(t)
	if text, isErr := toolText(t, result(t, run(t, atlas, callTool(1, "atlas_terminate_instances", map[string]any{"keys": "nope"}))[0])); !isErr || !strings.Contains(text, "array") {
		t.Fatalf("terminate non-array keys = (%q, isErr=%v), want a tool error", text, isErr)
	}
	if text, isErr := toolText(t, result(t, run(t, atlas, callTool(2, "atlas_terminate_instances", map[string]any{"keys": []any{"x"}}))[0])); !isErr || !strings.Contains(text, "integer") {
		t.Fatalf("terminate non-integer key = (%q, isErr=%v), want a tool error", text, isErr)
	}
}

// TestTerminateInstancesRejectsBadFilterArgs covers the processDefKey and limit
// argument-validation branches of the filter-mode selector.
func TestTerminateInstancesRejectsBadFilterArgs(t *testing.T) {
	atlas := newAtlas(t)
	if text, isErr := toolText(t, result(t, run(t, atlas, callTool(1, "atlas_terminate_instances", map[string]any{"processDefKey": "x"}))[0])); !isErr || !strings.Contains(text, "integer") {
		t.Fatalf("terminate bad processDefKey = (%q, isErr=%v), want a tool error", text, isErr)
	}
	if text, isErr := toolText(t, result(t, run(t, atlas, callTool(2, "atlas_terminate_instances", map[string]any{"limit": "x"}))[0])); !isErr || !strings.Contains(text, "integer") {
		t.Fatalf("terminate bad limit = (%q, isErr=%v), want a tool error", text, isErr)
	}
}
