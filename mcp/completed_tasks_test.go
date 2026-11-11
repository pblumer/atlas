package mcp_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// The completed-task tools forward their arguments to the routes they proxy and hand
// back the server's own answer (ADR-0554).
func TestCompletedTaskToolsForwardTheirArguments(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer backend.Close()

	resp := callToolAgainstBackend(t, backend, "atlas_list_completed_tasks", map[string]any{
		"limit": 20, "cursor": "5.6", "process": "freigabe", "from": "2026-01-01", "to": "2026-12-31",
	})
	if text, isErr := toolText(t, result(t, resp)); isErr {
		t.Fatalf("list returned a tool error: %s", text)
	}
	if gotPath != "/api/v1/tasks/completed" {
		t.Fatalf("path = %q", gotPath)
	}
	for name, want := range map[string]string{"limit": "20", "cursor": "5.6", "process": "freigabe", "from": "2026-01-01", "to": "2026-12-31"} {
		if got := gotQuery.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}

	resp = callToolAgainstBackend(t, backend, "atlas_list_completed_tasks", map[string]any{})
	if _, isErr := toolText(t, result(t, resp)); isErr || gotPath != "/api/v1/tasks/completed" || len(gotQuery) != 0 {
		t.Errorf("no arguments = %s?%v", gotPath, gotQuery)
	}
	resp = callToolAgainstBackend(t, backend, "atlas_list_completed_tasks", map[string]any{"limit": -1})
	if _, isErr := toolText(t, result(t, resp)); !isErr {
		t.Error("a negative limit was forwarded")
	}

	resp = callToolAgainstBackend(t, backend, "atlas_get_completed_task", map[string]any{"key": 42})
	if text, isErr := toolText(t, result(t, resp)); isErr || gotPath != "/api/v1/tasks/completed/42" {
		t.Fatalf("get = %s (%s, error %v)", gotPath, text, isErr)
	}
	resp = callToolAgainstBackend(t, backend, "atlas_get_completed_task", map[string]any{})
	if _, isErr := toolText(t, result(t, resp)); !isErr {
		t.Error("get without a key was forwarded")
	}
}
