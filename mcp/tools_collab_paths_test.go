package mcp_test

import (
	"net/http"
	"testing"
)

// TestSessionToolsNeedTheElement: a lock or a change is about one element, so neither
// may reach the session without naming it — and a lock without a participant would be
// held by nobody who could release it.
func TestSessionToolsNeedTheElement(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusOK, `{}`)
	for _, tc := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"atlas_session_lock", map[string]any{"draftId": "d", "action": "acquire"}, "argument: participantId"},
		{"atlas_session_lock", map[string]any{"draftId": "d", "participantId": "p", "action": "acquire"}, "argument: elementId"},
		{"atlas_session_change", map[string]any{"draftId": "d", "elementId": "e"}, "argument: participantId"},
		{"atlas_session_change", map[string]any{"draftId": "d", "participantId": "p"}, "argument: elementId"},
	} {
		t.Run(tc.tool+"/"+tc.want, func(t *testing.T) {
			text, isErr, c := rec.call(t, tc.tool, tc.args)
			wantRefusal(t, tc.tool, text, isErr, c, tc.want)
		})
	}
}

// TestSessionLockPostsToTheDraftsSession pins the request a lock becomes.
func TestSessionLockPostsToTheDraftsSession(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusOK, `{"held":true}`)
	_, isErr, c := rec.call(t, "atlas_session_lock",
		map[string]any{"draftId": "order", "participantId": "p1", "elementId": "Task_1", "action": "release"})
	if isErr {
		t.Fatal("atlas_session_lock reported an error")
	}
	wantCall(t, "atlas_session_lock", c, http.MethodPost, "/api/v1/drafts/order/session/lock", "")
	body := bodyObject(t, c)
	if body["participantId"] != "p1" || body["elementId"] != "Task_1" || body["action"] != "release" {
		t.Fatalf("lock body = %v, want participant p1 releasing Task_1", body)
	}
}
