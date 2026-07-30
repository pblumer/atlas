package mcp_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func listedToolNames(t *testing.T, resp map[string]any) []string {
	t.Helper()
	res := result(t, resp)
	tools, ok := res["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("tools/list returned no tools: %v", res)
	}

	seen := make(map[string]bool, len(tools))
	names := make([]string, 0, len(tools))
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("tools/list entry is not an object: %v", raw)
		}
		name, ok := tool["name"].(string)
		if !ok || name == "" {
			t.Fatalf("tools/list entry has no name: %v", tool)
		}
		if seen[name] {
			t.Fatalf("tools/list contains duplicate tool name %q", name)
		}
		seen[name] = true
		names = append(names, name)
	}

	for _, name := range []string{"atlas_save_form", "atlas_deploy", "atlas_deploy_project"} {
		if !seen[name] {
			t.Fatalf("tools/list missing incident regression tool %q", name)
		}
	}
	return names
}

func toolCallRequest(t *testing.T, name string) string {
	t.Helper()
	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": map[string]any{},
		},
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal tools/call for %q: %v", name, err)
	}
	return string(payload)
}

func assertToolResult(t *testing.T, name string, resp map[string]any) {
	t.Helper()
	if rpcErr, ok := resp["error"]; ok {
		t.Fatalf("advertised tool %q returned a JSON-RPC error: %v", name, rpcErr)
	}
	// Required arguments may deliberately produce isError:true. Reaching a valid
	// CallToolResult proves the advertised name resolved to its handler.
	toolText(t, result(t, resp))
}

func TestEveryAdvertisedToolIsDispatchableOverStdio(t *testing.T) {
	atlas := newAtlas(t)
	listed := run(t, atlas, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if len(listed) != 1 {
		t.Fatalf("tools/list returned %d responses, want 1", len(listed))
	}

	for _, name := range listedToolNames(t, listed[0]) {
		t.Run(name, func(t *testing.T) {
			called := run(t, atlas, toolCallRequest(t, name))
			if len(called) != 1 {
				t.Fatalf("tools/call returned %d responses, want 1", len(called))
			}
			assertToolResult(t, name, called[0])
		})
	}
}

func TestEveryAdvertisedToolIsDispatchableOverHTTP(t *testing.T) {
	server := newMCPHTTP(t)
	status, listed := postRPC(t, server.URL, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if status != http.StatusOK {
		t.Fatalf("tools/list status = %d, want 200", status)
	}

	for _, name := range listedToolNames(t, listed) {
		t.Run(name, func(t *testing.T) {
			status, called := postRPC(t, server.URL, toolCallRequest(t, name))
			if status != http.StatusOK {
				t.Fatalf("tools/call status = %d, want 200", status)
			}
			assertToolResult(t, name, called)
		})
	}
}
