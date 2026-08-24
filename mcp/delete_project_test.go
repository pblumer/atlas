package mcp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeleteProjectCallsAPIAndConfirmsDeletion(t *testing.T) {
	var gotMethod, gotPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	responses := run(t, backend, callTool(1, "atlas_delete_project", map[string]any{"id": "abc123"}))
	if len(responses) != 1 {
		t.Fatalf("tools/call returned %d responses, want 1", len(responses))
	}
	text, isErr := toolText(t, result(t, responses[0]))
	if isErr {
		t.Fatalf("atlas_delete_project returned tool error: %s", text)
	}
	if gotMethod != http.MethodDelete || gotPath != "/api/v1/projects/abc123" {
		t.Fatalf("request = %s %s, want DELETE /api/v1/projects/abc123", gotMethod, gotPath)
	}
	var confirmation struct {
		Deleted bool   `json:"deleted"`
		ID      string `json:"id"`
	}
	if err := json.Unmarshal([]byte(text), &confirmation); err != nil {
		t.Fatalf("decode confirmation %q: %v", text, err)
	}
	if !confirmation.Deleted || confirmation.ID != "abc123" {
		t.Fatalf("confirmation = %+v, want deleted abc123", confirmation)
	}
}

func TestDeleteProjectIsIdempotentEndToEnd(t *testing.T) {
	atlas := newAtlas(t)
	created := callOne(t, atlas, "atlas_create_project", map[string]any{"name": "Temporary"})
	var project struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(created), &project); err != nil || project.ID == "" {
		t.Fatalf("create project = %q (err=%v), want id", created, err)
	}

	for range 2 {
		confirmation := callOne(t, atlas, "atlas_delete_project", map[string]any{"id": project.ID})
		if !strings.Contains(confirmation, `"deleted":true`) {
			t.Fatalf("delete confirmation = %s, want deleted true", confirmation)
		}
	}
	projects := callOne(t, atlas, "atlas_list_projects", map[string]any{})
	if strings.Contains(projects, project.ID) || strings.Contains(projects, "Temporary") {
		t.Fatalf("list projects = %s, deleted project still visible", projects)
	}
}

func TestDeleteProjectIsAdvertised(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer backend.Close()

	responses := run(t, backend, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools := result(t, responses[0])["tools"].([]any)
	for _, raw := range tools {
		tool := raw.(map[string]any)
		if tool["name"] != "atlas_delete_project" {
			continue
		}
		schema := tool["inputSchema"].(map[string]any)
		required := schema["required"].([]any)
		if len(required) != 1 || required[0] != "id" {
			t.Fatalf("atlas_delete_project required = %v, want [id]", required)
		}
		return
	}
	t.Fatal("tools/list missing atlas_delete_project")
}
