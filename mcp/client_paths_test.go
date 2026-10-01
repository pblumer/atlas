package mcp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/pblumer/atlas/mcp"
)

// TestHTTPForwardsTheTransportMarker: the marker that says an API request is a tool
// call must survive the hop to the API. A token approved for the MCP transport alone is
// confined to tool calls by it; dropped here, every tool call would look like a direct
// API call and that token would be refused for the very calls it was approved for.
func TestHTTPForwardsTheTransportMarker(t *testing.T) {
	var mu sync.Mutex
	var got string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = r.Header.Get(mcp.TransportHeader)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"product":"Atlas"}`))
	}))
	t.Cleanup(backend.Close)
	ts := httptest.NewServer(mcp.NewServer(mcp.NewClient(backend.URL)))
	t.Cleanup(ts.Close)

	callInfo(t, ts.URL, func(r *http.Request) { r.Header.Set(mcp.TransportHeader, "1") })
	mu.Lock()
	defer mu.Unlock()
	if got != "1" {
		t.Fatalf("%s reaching the API = %q, want the marker the tool call arrived with", mcp.TransportHeader, got)
	}
}

// TestToolCallToAnUnusableServerURLIsAToolError: a server address that is not a URL is
// a configuration mistake the model can report, not a crash of the adapter.
func TestToolCallToAnUnusableServerURLIsAToolError(t *testing.T) {
	srv := mcp.NewServer(mcp.NewClient("http://bad host"))
	var out strings.Builder
	in := strings.NewReader(callTool(1, "atlas_info", nil) + "\n")
	if err := srv.Serve(in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &resp); err != nil {
		t.Fatalf("decode %q: %v", out.String(), err)
	}
	text, isErr := toolText(t, result(t, resp))
	if !isErr || !strings.Contains(text, "invalid character") {
		t.Fatalf("atlas_info against %q = (%q, isErr=%v), want a tool error about the URL", "http://bad host", text, isErr)
	}
}
