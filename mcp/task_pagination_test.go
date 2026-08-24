package mcp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func callToolAgainstBackend(t *testing.T, backend *httptest.Server, name string, args map[string]any) map[string]any {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	})
	if err != nil {
		t.Fatalf("marshal tools/call: %v", err)
	}
	responses := run(t, backend, string(payload))
	if len(responses) != 1 {
		t.Fatalf("tools/call returned %d responses, want 1", len(responses))
	}
	return responses[0]
}

func TestListTasksForwardsPaginationAndReturnsMetadata(t *testing.T) {
	var gotMethod, gotPath string
	var gotQuery url.Values
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.Query()
		w.Header().Set("X-Tasks-Truncated", "true")
		w.Header().Set("X-Tasks-Next-Cursor", "77")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"key":88}]`))
	}))
	defer backend.Close()

	response := callToolAgainstBackend(t, backend, "atlas_list_tasks", map[string]any{
		"limit":           25,
		"before":          99,
		"processInstance": 123,
	})
	text, isErr := toolText(t, result(t, response))
	if isErr {
		t.Fatalf("atlas_list_tasks returned tool error: %s", text)
	}

	if gotMethod != http.MethodGet || gotPath != "/api/v1/tasks" {
		t.Fatalf("request = %s %s, want GET /api/v1/tasks", gotMethod, gotPath)
	}
	if got := gotQuery.Get("limit"); got != "25" {
		t.Fatalf("limit query = %q, want 25", got)
	}
	if got := gotQuery.Get("before"); got != "99" {
		t.Fatalf("before query = %q, want 99", got)
	}
	if got := gotQuery.Get("processInstance"); got != "123" {
		t.Fatalf("processInstance query = %q, want 123", got)
	}

	var page struct {
		Items []struct {
			Key uint64 `json:"key"`
		} `json:"items"`
		Truncated  bool   `json:"truncated"`
		NextCursor uint64 `json:"nextCursor"`
	}
	if err := json.Unmarshal([]byte(text), &page); err != nil {
		t.Fatalf("decode paginated result %q: %v", text, err)
	}
	if len(page.Items) != 1 || page.Items[0].Key != 88 {
		t.Fatalf("items = %+v, want task key 88", page.Items)
	}
	if !page.Truncated || page.NextCursor != 77 {
		t.Fatalf("pagination = truncated:%v nextCursor:%d, want true/77", page.Truncated, page.NextCursor)
	}
}

func TestListTasksOmitsCursorWhenAPIHasNone(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer backend.Close()

	response := callToolAgainstBackend(t, backend, "atlas_list_tasks", map[string]any{})
	text, isErr := toolText(t, result(t, response))
	if isErr {
		t.Fatalf("atlas_list_tasks returned tool error: %s", text)
	}
	var page map[string]any
	if err := json.Unmarshal([]byte(text), &page); err != nil {
		t.Fatalf("decode paginated result %q: %v", text, err)
	}
	if _, ok := page["nextCursor"]; ok {
		t.Fatalf("result unexpectedly contains nextCursor: %v", page)
	}
	if truncated, ok := page["truncated"].(bool); !ok || truncated {
		t.Fatalf("truncated = %v, want false", page["truncated"])
	}
}

func TestListTasksAdvertisesPaginationArguments(t *testing.T) {
	called := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer backend.Close()

	responses := run(t, backend, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if called {
		t.Fatal("tools/list called the Atlas HTTP API")
	}
	tools := result(t, responses[0])["tools"].([]any)
	for _, raw := range tools {
		tool := raw.(map[string]any)
		if tool["name"] != "atlas_list_tasks" {
			continue
		}
		schema := tool["inputSchema"].(map[string]any)
		properties := schema["properties"].(map[string]any)
		for _, name := range []string{"limit", "before", "processInstance"} {
			if _, ok := properties[name]; !ok {
				t.Fatalf("atlas_list_tasks schema missing %q: %v", name, schema)
			}
		}
		return
	}
	t.Fatal("tools/list missing atlas_list_tasks")
}
