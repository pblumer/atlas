package mcp_test

import (
	"fmt"
	"strings"
	"testing"
)

// TestGetTaskViaTool fetches one open user task by key — the deep-link read side.
func TestGetTaskViaTool(t *testing.T) {
	atlas := newAtlas(t)
	jobKey := firstTaskKey(t, atlas)

	text, isErr := toolText(t, result(t, run(t, atlas, callTool(4, "atlas_get_task", map[string]any{"key": jobKey}))[0]))
	if isErr ||
		!strings.Contains(text, fmt.Sprintf(`"key":%d`, jobKey)) ||
		!strings.Contains(text, `"processId":"approval"`) {
		t.Fatalf("get_task = (%q, isErr=%v), want the task with its key and definition", text, isErr)
	}
}

// TestGetTaskMissingIsToolError reads a task key that is not an open user task,
// surfacing the server's 404 as a tool error.
func TestGetTaskMissingIsToolError(t *testing.T) {
	atlas := newAtlas(t)
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(1, "atlas_get_task", map[string]any{"key": 999999}))[0]))
	if !isErr || !strings.Contains(text, "no open task") {
		t.Fatalf("get missing task = (%q, isErr=%v), want a not-found tool error", text, isErr)
	}
}

// TestClaimAndUnclaimTaskViaTool assigns a task to a named user and then releases
// it, confirming the assignee is set and then cleared as seen through get_task.
func TestClaimAndUnclaimTaskViaTool(t *testing.T) {
	atlas := newAtlas(t)
	jobKey := firstTaskKey(t, atlas)

	claimText, isErr := toolText(t, result(t, run(t, atlas, callTool(4, "atlas_claim_task", map[string]any{"key": jobKey, "assignee": "alice"}))[0]))
	if isErr ||
		!strings.Contains(claimText, fmt.Sprintf(`"taskKey":%d`, jobKey)) ||
		!strings.Contains(claimText, `"assignee":"alice"`) {
		t.Fatalf("claim_task = (%q, isErr=%v), want the task claimed for alice", claimText, isErr)
	}

	// get_task now reports the assignee.
	got, isErr := toolText(t, result(t, run(t, atlas, callTool(5, "atlas_get_task", map[string]any{"key": jobKey}))[0]))
	if isErr || !strings.Contains(got, `"assignee":"alice"`) {
		t.Fatalf("get_task after claim = (%q, isErr=%v), want assignee alice", got, isErr)
	}

	// Unclaim clears the assignment.
	unclaimText, isErr := toolText(t, result(t, run(t, atlas, callTool(6, "atlas_unclaim_task", map[string]any{"key": jobKey}))[0]))
	if isErr || !strings.Contains(unclaimText, `"assignee":""`) {
		t.Fatalf("unclaim_task = (%q, isErr=%v), want the assignee cleared", unclaimText, isErr)
	}
	got2, isErr := toolText(t, result(t, run(t, atlas, callTool(7, "atlas_get_task", map[string]any{"key": jobKey}))[0]))
	if isErr || strings.Contains(got2, "alice") {
		t.Fatalf("get_task after unclaim = (%q, isErr=%v), want no assignee", got2, isErr)
	}
}

// TestClaimTaskRequiresAssignee covers the empty-assignee branch: with auth off
// (the test server's mode) a claim must name an assignee, surfaced as a tool
// error from the server's 400.
func TestClaimTaskRequiresAssignee(t *testing.T) {
	atlas := newAtlas(t)
	jobKey := firstTaskKey(t, atlas)
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(4, "atlas_claim_task", map[string]any{"key": jobKey}))[0]))
	if !isErr || !strings.Contains(text, "assignee is required") {
		t.Fatalf("claim without assignee = (%q, isErr=%v), want an assignee-required tool error", text, isErr)
	}
}

// TestClaimTaskMissingIsToolError claims a key with no open task → tool error.
func TestClaimTaskMissingIsToolError(t *testing.T) {
	atlas := newAtlas(t)
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(1, "atlas_claim_task", map[string]any{"key": 999999, "assignee": "alice"}))[0]))
	if !isErr || !strings.Contains(text, "no open task") {
		t.Fatalf("claim missing task = (%q, isErr=%v), want a not-found tool error", text, isErr)
	}
}

// TestUnclaimTaskMissingIsToolError unclaims a key with no open task → tool error.
func TestUnclaimTaskMissingIsToolError(t *testing.T) {
	atlas := newAtlas(t)
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(1, "atlas_unclaim_task", map[string]any{"key": 999999}))[0]))
	if !isErr || !strings.Contains(text, "no open task") {
		t.Fatalf("unclaim missing task = (%q, isErr=%v), want a not-found tool error", text, isErr)
	}
}
