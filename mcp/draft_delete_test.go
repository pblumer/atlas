package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// An agent can create drafts (atlas_save_draft), so it needs a way to remove one:
// without it, every diagram an agent generates — a scratch model, a layout test, a
// draft superseded on the next attempt — is permanent litter only a human can
// clear. atlas_delete_draft closes that loop. It is deliberately the one exception
// to "artifact editing is a UI concern" in the MCP surface classification; see
// tool_registry_drift_test.go.

const deletableBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="mcp-throwaway-proc" isExecutable="true">
    <startEvent id="s"/>
    <endEvent id="e"/>
    <sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`

func TestDeleteDraftRemovesItAndIsIdempotent(t *testing.T) {
	atlas := newAtlas(t)

	if saved := callOne(t, atlas, "atlas_save_draft", map[string]any{"xml": deletableBPMN}); !strings.Contains(saved, "mcp-throwaway-proc") {
		t.Fatalf("save draft = %s, want the process id", saved)
	}
	if listed := callOne(t, atlas, "atlas_list_drafts", map[string]any{}); !strings.Contains(listed, "mcp-throwaway-proc") {
		t.Fatalf("list drafts = %s, want the saved draft", listed)
	}

	// Delete → confirmed, and the draft is gone from the listing.
	deleted := callOne(t, atlas, "atlas_delete_draft", map[string]any{"id": "mcp-throwaway-proc"})
	var got struct {
		Deleted bool   `json:"deleted"`
		ID      string `json:"id"`
	}
	if err := json.Unmarshal([]byte(deleted), &got); err != nil {
		t.Fatalf("delete draft = %q, want a JSON confirmation: %v", deleted, err)
	}
	if !got.Deleted || got.ID != "mcp-throwaway-proc" {
		t.Errorf("delete draft = %+v, want {deleted:true id:mcp-throwaway-proc}", got)
	}
	if listed := callOne(t, atlas, "atlas_list_drafts", map[string]any{}); strings.Contains(listed, "mcp-throwaway-proc") {
		t.Errorf("list drafts still has the deleted draft: %s", listed)
	}

	// Idempotent: deleting what is already gone succeeds rather than erroring, so a
	// cleanup pass does not have to check first or special-case a repeat.
	again := callOne(t, atlas, "atlas_delete_draft", map[string]any{"id": "mcp-throwaway-proc"})
	if !strings.Contains(again, `"deleted":true`) {
		t.Errorf("second delete = %s, want the same confirmation", again)
	}
	// Omitting the id is an argument error rather than a silent no-op that could read
	// as "the draft was removed" — covered with the other authoring tools in
	// TestAuthoringToolErrors.
}

// TestDeleteDraftLeavesDeploymentsAlone pins the boundary the tool description
// promises: a draft is design-time only, so removing it must not disturb a
// definition already deployed from it. An agent tidying up its drafts cannot take
// down running processes by accident.
func TestDeleteDraftLeavesDeploymentsAlone(t *testing.T) {
	atlas := newAtlas(t)

	callOne(t, atlas, "atlas_save_draft", map[string]any{"xml": deletableBPMN})
	if deployed := callOne(t, atlas, "atlas_deploy", map[string]any{"xml": deletableBPMN}); !strings.Contains(deployed, "mcp-throwaway-proc") {
		t.Fatalf("deploy = %s, want the deployed definition", deployed)
	}

	callOne(t, atlas, "atlas_delete_draft", map[string]any{"id": "mcp-throwaway-proc"})

	if processes := callOne(t, atlas, "atlas_list_processes", map[string]any{}); !strings.Contains(processes, "mcp-throwaway-proc") {
		t.Errorf("deleting the draft removed the deployed definition too:\n%s", processes)
	}
}
