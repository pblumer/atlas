package mcp_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/mcp"
)

// newMCPHTTP wires an mcp.Server (backed by a real Atlas server) behind its
// Streamable HTTP transport and returns an httptest server for it.
func newMCPHTTP(t *testing.T) *httptest.Server {
	t.Helper()
	atlas := newAtlas(t)
	srv := mcp.NewServer(mcp.NewClient(atlas.URL))
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts
}

// postRPC sends one JSON-RPC message and returns the HTTP status and decoded
// response body (nil for an empty body).
func postRPC(t *testing.T, url, payload string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if len(strings.TrimSpace(string(data))) == 0 {
		return resp.StatusCode, nil
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
	return resp.StatusCode, m
}

func TestHTTPInitializeAndCall(t *testing.T) {
	ts := newMCPHTTP(t)

	// initialize
	code, resp := postRPC(t, ts.URL, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if code != http.StatusOK {
		t.Fatalf("initialize status = %d", code)
	}
	res := resp["result"].(map[string]any)
	if res["protocolVersion"] != "2025-06-18" {
		t.Fatalf("protocolVersion = %v", res["protocolVersion"])
	}

	// tools/call: deploy a model over HTTP transport.
	req := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "atlas_deploy", "arguments": map[string]any{"xml": sampleBPMN}},
	}
	b, _ := json.Marshal(req)
	code, resp = postRPC(t, ts.URL, string(b))
	if code != http.StatusOK {
		t.Fatalf("deploy status = %d", code)
	}
	res = resp["result"].(map[string]any)
	if isErr, _ := res["isError"].(bool); isErr {
		t.Fatalf("deploy reported tool error: %v", res)
	}
	content := res["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, `"processId":"order"`) {
		t.Fatalf("deploy text = %q, want the order definition", text)
	}
}

func TestHTTPNotificationGets202(t *testing.T) {
	ts := newMCPHTTP(t)
	code, resp := postRPC(t, ts.URL, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if code != http.StatusAccepted {
		t.Fatalf("notification status = %d, want 202", code)
	}
	if resp != nil {
		t.Fatalf("notification body = %v, want empty", resp)
	}
}

func TestHTTPGetIsMethodNotAllowed(t *testing.T) {
	ts := newMCPHTTP(t)
	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); !strings.Contains(allow, "POST") {
		t.Fatalf("Allow header = %q, want it to list POST", allow)
	}
}

func TestHTTPParseErrorIsRPCError(t *testing.T) {
	ts := newMCPHTTP(t)
	code, resp := postRPC(t, ts.URL, `{not json`)
	if code != http.StatusOK {
		t.Fatalf("parse-error status = %d, want 200 with a JSON-RPC error body", code)
	}
	e, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("want a JSON-RPC error, got %v", resp)
	}
	if c, _ := e["code"].(float64); int(c) != -32700 {
		t.Fatalf("error code = %v, want -32700", e["code"])
	}
}
