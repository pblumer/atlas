package mcp_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestAssignableUsersViaTool lists the accounts a task can be assigned to. A
// fresh server has no users, so the tool returns an empty array; after an
// (enabled) account is created, that username appears — the pick-an-assignee
// read that precedes atlas_claim_task.
func TestAssignableUsersViaTool(t *testing.T) {
	atlas := newAtlas(t)

	// No users yet: a successful empty list, not an error.
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(1, "atlas_assignable_users", map[string]any{}))[0]))
	if isErr || strings.TrimSpace(text) != "[]" {
		t.Fatalf("assignable_users (empty) = (%q, isErr=%v), want []", text, isErr)
	}

	// Seed an enabled user over HTTP (auth is off in the harness, so the
	// admin-gated create route is open) and confirm it becomes assignable.
	resp, err := http.Post(atlas.URL+"/api/v1/users", "application/json",
		strings.NewReader(`{"username":"alice","displayName":"Alice A.","password":"password1","roles":["user"]}`))
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed user status = %d, want 201", resp.StatusCode)
	}

	text, isErr = toolText(t, result(t, run(t, atlas, callTool(2, "atlas_assignable_users", map[string]any{}))[0]))
	if isErr ||
		!strings.Contains(text, `"username":"alice"`) ||
		!strings.Contains(text, `"displayName":"Alice A."`) {
		t.Fatalf("assignable_users = (%q, isErr=%v), want alice with her display name", text, isErr)
	}
}
