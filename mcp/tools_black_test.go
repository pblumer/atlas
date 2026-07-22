package mcp_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/mcp"
)

// callTool builds a tools/call request literal.
func callTool(id int, name string, args map[string]any) string {
	req := map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args},
	}
	b, _ := json.Marshal(req)
	return string(b)
}

// TestNoArgToolHandlers exercises every no-argument tool handler (info,
// list_processes, list_instances, stats) so defaultTools is fully covered.
func TestNoArgToolHandlers(t *testing.T) {
	ts := newAtlas(t)
	cases := []struct {
		tool     string
		contains string
	}{
		{"atlas_info", `"product":"Atlas"`},
		{"atlas_list_processes", "["},
		{"atlas_list_instances", "["},
		{"atlas_stats", `"activeProcessInstances"`},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			resps := run(t, ts, callTool(1, tc.tool, map[string]any{}))
			text, isErr := toolText(t, result(t, resps[0]))
			if isErr || !strings.Contains(text, tc.contains) {
				t.Fatalf("%s = (%q, isErr=%v), want contains %q", tc.tool, text, isErr, tc.contains)
			}
		})
	}
}

// TestGetProcessXMLStringKey deploys a process then fetches its XML using a
// string-typed key argument, exercising argUint's string branch (and parseUint).
func TestGetProcessXMLStringKey(t *testing.T) {
	ts := newAtlas(t)
	resps := run(t, ts,
		callTool(1, "atlas_deploy", map[string]any{"xml": sampleBPMN}),
		callTool(2, "atlas_get_process_xml", map[string]any{"key": "1"}),
	)
	if _, isErr := toolText(t, result(t, resps[0])); isErr {
		t.Fatal("deploy failed")
	}
	text, isErr := toolText(t, result(t, resps[1]))
	if isErr || !strings.Contains(text, `id="order"`) {
		t.Fatalf("get_process_xml = (%q, isErr=%v), want the order XML", text, isErr)
	}
}

// TestBadKeyArgumentIsToolError sends an out-of-range key so the tool handler's
// argUint returns an error, surfaced as a tool result with isError:true.
func TestBadKeyArgumentIsToolError(t *testing.T) {
	ts := newAtlas(t)
	resps := run(t, ts, callTool(3, "atlas_process_runtime", map[string]any{"key": "not-a-number"}))
	text, isErr := toolText(t, result(t, resps[0]))
	if !isErr || !strings.Contains(text, "non-negative integer") {
		t.Fatalf("bad key = (%q, isErr=%v), want a tool error", text, isErr)
	}
}

// TestToolsCallInvalidParams covers handleToolsCall's params-unmarshal error
// branch by sending a non-object params value.
func TestToolsCallInvalidParams(t *testing.T) {
	ts := newAtlas(t)
	resps := run(t, ts, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":"oops"}`)
	e, ok := resps[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("want a JSON-RPC error for non-object params, got %v", resps[0])
	}
	if code, _ := e["code"].(float64); int(code) != -32602 {
		t.Fatalf("code = %v, want -32602 (invalid params)", e["code"])
	}
}

// TestUnknownNotificationGetsNoReply covers handle's default branch for a
// message with no id (a notification): it must produce no response.
func TestUnknownNotificationGetsNoReply(t *testing.T) {
	ts := newAtlas(t)
	resps := run(t, ts, `{"jsonrpc":"2.0","method":"some/notification"}`)
	if len(resps) != 0 {
		t.Fatalf("notification produced %d responses, want 0", len(resps))
	}
}

// TestCancelledNotificationGetsNoReply covers the explicit notifications/cancelled
// branch of handle.
func TestCancelledNotificationGetsNoReply(t *testing.T) {
	ts := newAtlas(t)
	resps := run(t, ts, `{"jsonrpc":"2.0","method":"notifications/cancelled"}`)
	if len(resps) != 0 {
		t.Fatalf("cancelled notification produced %d responses, want 0", len(resps))
	}
}

// failWriter fails every Write, to drive Serve's encode-error return path.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

// TestServeEncodeError makes the output encoder fail on a valid request so Serve
// returns the write error rather than swallowing it.
func TestServeEncodeError(t *testing.T) {
	ts := newAtlas(t)
	srv := mcp.NewServer(mcp.NewClient(ts.URL))
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n")
	if err := srv.Serve(in, failWriter{}); err == nil {
		t.Fatal("Serve should return the encoder's write error")
	}
}

// TestServeSkipsBlankLines confirms Serve's empty-line skip: a blank line between
// two requests is ignored and both requests are answered.
func TestServeSkipsBlankLines(t *testing.T) {
	ts := newAtlas(t)
	resps := run(t, ts,
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
		``,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
	)
	if len(resps) != 2 {
		t.Fatalf("got %d responses, want 2 (blank line skipped)", len(resps))
	}
}
