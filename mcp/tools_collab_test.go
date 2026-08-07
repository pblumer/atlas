package mcp_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// callMaybeErr calls a tool and returns its text and whether it was a tool
// error, without failing the test on an error (unlike callOne).
func callMaybeErr(t *testing.T, ts *httptest.Server, name string, args map[string]any) (string, bool) {
	t.Helper()
	resps := run(t, ts, callTool(1, name, args))
	return toolText(t, result(t, resps[0]))
}

// A minimal, non-executable draft the agent can join a session on.
const collabDraftBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="wip-collab" name="Co-edited order">
    <bpmn:startEvent id="StartEvent_1" name="Start"/>
  </bpmn:process>
</bpmn:definitions>`

// TestCollabSessionToolScenario drives the ADR-0103 M2 flow entirely through MCP
// tools: save a draft, join its live session as an agent, poll, lock, change,
// announce presence, and leave.
func TestCollabSessionToolScenario(t *testing.T) {
	ts := newAtlas(t)

	// Save a draft to co-edit.
	callOne(t, ts, "atlas_save_draft", map[string]any{"xml": collabDraftBPMN})

	// Join the session; the reply carries our own participant id.
	joinText := callOne(t, ts, "atlas_join_session", map[string]any{"draftId": "wip-collab", "name": "Claude"})
	var join struct {
		Self         string `json:"self"`
		Participants []struct {
			Name string `json:"name"`
		} `json:"participants"`
	}
	if err := json.Unmarshal([]byte(joinText), &join); err != nil {
		t.Fatalf("decode join: %v (%s)", err, joinText)
	}
	if join.Self == "" {
		t.Fatalf("join returned no self id: %s", joinText)
	}
	if len(join.Participants) != 1 || join.Participants[0].Name != "Claude" {
		t.Fatalf("join roster = %+v, want just Claude", join.Participants)
	}
	self := join.Self

	// Lock an element, edit it, announce presence — all should succeed.
	callOne(t, ts, "atlas_session_lock", map[string]any{
		"draftId": "wip-collab", "participantId": self, "elementId": "StartEvent_1", "action": "acquire",
	})
	callOne(t, ts, "atlas_session_change", map[string]any{
		"draftId": "wip-collab", "participantId": self, "elementId": "StartEvent_1", "xml": "<startEvent/>",
	})
	callOne(t, ts, "atlas_session_presence", map[string]any{
		"draftId": "wip-collab", "participantId": self, "selection": "StartEvent_1",
	})

	// Poll reflects our own lock and selection.
	pollText := callOne(t, ts, "atlas_session_poll", map[string]any{"draftId": "wip-collab", "participantId": self})
	var poll struct {
		Locks []struct {
			ElementID string `json:"elementId"`
		} `json:"locks"`
	}
	if err := json.Unmarshal([]byte(pollText), &poll); err != nil {
		t.Fatalf("decode poll: %v (%s)", err, pollText)
	}
	if len(poll.Locks) != 1 || poll.Locks[0].ElementID != "StartEvent_1" {
		t.Fatalf("poll locks = %+v", poll.Locks)
	}

	// Release the lock and leave.
	callOne(t, ts, "atlas_session_lock", map[string]any{
		"draftId": "wip-collab", "participantId": self, "elementId": "StartEvent_1", "action": "release",
	})
	callOne(t, ts, "atlas_leave_session", map[string]any{"draftId": "wip-collab", "participantId": self})

	// After leaving, a poll is a not-found tool error.
	if _, isErr := callMaybeErr(t, ts, "atlas_session_poll", map[string]any{"draftId": "wip-collab", "participantId": self}); !isErr {
		t.Fatal("poll after leave should error")
	}
}

// TestCollabSessionLockConflictTool confirms the 409 surfaces as a tool error to
// the agent when it targets an element another participant holds.
func TestCollabSessionLockConflictTool(t *testing.T) {
	ts := newAtlas(t)
	callOne(t, ts, "atlas_save_draft", map[string]any{"xml": collabDraftBPMN})

	// Two agent participants.
	self1 := joinSelf(t, ts, "wip-collab", "One")
	self2 := joinSelf(t, ts, "wip-collab", "Two")

	callOne(t, ts, "atlas_session_lock", map[string]any{
		"draftId": "wip-collab", "participantId": self1, "elementId": "StartEvent_1", "action": "acquire",
	})
	text, isErr := callMaybeErr(t, ts, "atlas_session_lock", map[string]any{
		"draftId": "wip-collab", "participantId": self2, "elementId": "StartEvent_1", "action": "acquire",
	})
	if !isErr || !strings.Contains(text, "locked by another") {
		t.Fatalf("conflicting lock = (isErr=%v) %s", isErr, text)
	}
}

// TestCollabToolErrors covers each session tool's required-argument validation.
func TestCollabToolErrors(t *testing.T) {
	ts := newAtlas(t)
	cases := []struct {
		name string
		args map[string]any
	}{
		{"atlas_join_session", map[string]any{}},
		{"atlas_session_poll", map[string]any{"draftId": "d"}},
		{"atlas_session_lock", map[string]any{"draftId": "d", "participantId": "p", "elementId": "e"}},
		{"atlas_session_change", map[string]any{"draftId": "d", "participantId": "p"}},
		{"atlas_session_presence", map[string]any{"draftId": "d"}},
		{"atlas_leave_session", map[string]any{"draftId": "d"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, isErr := callMaybeErr(t, ts, tc.name, tc.args); !isErr {
				t.Fatalf("%s with missing args did not error", tc.name)
			}
		})
	}
}

// joinSelf joins a session and returns the participant's own id.
func joinSelf(t *testing.T, ts *httptest.Server, draftID, name string) string {
	t.Helper()
	text := callOne(t, ts, "atlas_join_session", map[string]any{"draftId": draftID, "name": name})
	var j struct {
		Self string `json:"self"`
	}
	if err := json.Unmarshal([]byte(text), &j); err != nil {
		t.Fatalf("decode join: %v (%s)", err, text)
	}
	return j.Self
}
