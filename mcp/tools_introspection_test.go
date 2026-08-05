package mcp_test

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

// deployAndStartInstance deploys sampleBPMN and starts one instance with the
// given atlas_create_instance arguments (which must include key:1), returning the
// live instance key read back from atlas_list_instances. sampleBPMN parks a token
// on its service task, so the instance stays active and its state is queryable.
func deployAndStartInstance(t *testing.T, atlas *httptest.Server, createArgs map[string]any) uint64 {
	t.Helper()
	if _, isErr := toolText(t, result(t, run(t, atlas, callTool(1, "atlas_deploy", map[string]any{"xml": sampleBPMN}))[0])); isErr {
		t.Fatal("deploy failed")
	}
	if _, isErr := toolText(t, result(t, run(t, atlas, callTool(2, "atlas_create_instance", createArgs))[0])); isErr {
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
		t.Fatalf("parse instances: err=%v, list=%q", err, listText)
	}
	return instances[0].Key
}

// TestInstanceVariablesViaTool reads back an instance's scope through the tool and
// sees the start variables it was seeded with, typed.
func TestInstanceVariablesViaTool(t *testing.T) {
	atlas := newAtlas(t)
	key := deployAndStartInstance(t, atlas, map[string]any{"key": 1, "variables": map[string]any{"amount": 42, "customer": "acme"}})

	text, isErr := toolText(t, result(t, run(t, atlas, callTool(4, "atlas_instance_variables", map[string]any{"key": key}))[0]))
	if isErr {
		t.Fatalf("instance_variables = (%q, isErr=%v)", text, isErr)
	}
	var vars map[string]any
	if err := json.Unmarshal([]byte(text), &vars); err != nil {
		t.Fatalf("decode variables %q: %v", text, err)
	}
	if fmt.Sprint(vars["amount"]) != "42" || vars["customer"] != "acme" {
		t.Fatalf("variables = %v, want amount 42 and customer acme", vars)
	}
}

// TestInstanceJobsViaTool lists the one activatable job an instance parked on its
// service task exposes.
func TestInstanceJobsViaTool(t *testing.T) {
	atlas := newAtlas(t)
	key := deployAndStartInstance(t, atlas, map[string]any{"key": 1})

	text, isErr := toolText(t, result(t, run(t, atlas, callTool(4, "atlas_instance_jobs", map[string]any{"key": key}))[0]))
	if isErr {
		t.Fatalf("instance_jobs = (%q, isErr=%v)", text, isErr)
	}
	var jobs []struct {
		ProcessInstanceKey uint64 `json:"processInstanceKey"`
		JobType            string `json:"jobType"`
	}
	if err := json.Unmarshal([]byte(text), &jobs); err != nil {
		t.Fatalf("decode jobs %q: %v", text, err)
	}
	if len(jobs) != 1 || jobs[0].ProcessInstanceKey != key || jobs[0].JobType != "payment" {
		t.Fatalf("jobs = %+v, want one payment job for instance %d", jobs, key)
	}
}

// TestInstanceTimelineViaTool reads an instance's replay timeline and sees its
// definition identity and step list.
func TestInstanceTimelineViaTool(t *testing.T) {
	atlas := newAtlas(t)
	key := deployAndStartInstance(t, atlas, map[string]any{"key": 1})

	text, isErr := toolText(t, result(t, run(t, atlas, callTool(4, "atlas_instance_timeline", map[string]any{"key": key}))[0]))
	if isErr {
		t.Fatalf("instance_timeline = (%q, isErr=%v)", text, isErr)
	}
	if !strings.Contains(text, `"processId":"order"`) || !strings.Contains(text, `"steps"`) {
		t.Fatalf("timeline = %q, want the order definition and a steps list", text)
	}
	// The step list records the activated start event and service task.
	var tl struct {
		Steps []json.RawMessage `json:"steps"`
	}
	if err := json.Unmarshal([]byte(text), &tl); err != nil || len(tl.Steps) == 0 {
		t.Fatalf("decode timeline steps: err=%v, want a non-empty step list (%q)", err, text)
	}
}

// TestInstanceTimelineMissingIsToolError reads the timeline of an instance key
// that does not exist, surfacing the server's 404 as a tool error. (variables and
// jobs return an empty result for an unknown key rather than erroring, so the
// error path lives on timeline.)
func TestInstanceTimelineMissingIsToolError(t *testing.T) {
	atlas := newAtlas(t)
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(1, "atlas_instance_timeline", map[string]any{"key": 999999}))[0]))
	if !isErr || !strings.Contains(text, "no instance") {
		t.Fatalf("timeline missing = (%q, isErr=%v), want a not-found tool error", text, isErr)
	}
}

// TestInstanceVariablesUnknownKeyIsEmpty confirms the read tools tolerate an
// unknown key where the API does: variables returns an empty object, not an error.
func TestInstanceVariablesUnknownKeyIsEmpty(t *testing.T) {
	atlas := newAtlas(t)
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(1, "atlas_instance_variables", map[string]any{"key": 999999}))[0]))
	if isErr || strings.TrimSpace(text) != "{}" {
		t.Fatalf("variables of unknown instance = (%q, isErr=%v), want {}", text, isErr)
	}
}
