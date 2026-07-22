package mcp_test

import (
	"strings"
	"testing"
)

// TestToolsCallOmittedArguments covers handleToolsCall's nil-arguments branch:
// a tools/call with the "arguments" member omitted must still dispatch, with the
// handler receiving an empty argument map.
func TestToolsCallOmittedArguments(t *testing.T) {
	ts := newAtlas(t)
	resps := run(t, ts, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"atlas_info"}}`)
	text, isErr := toolText(t, result(t, resps[0]))
	if isErr || !strings.Contains(text, `"product":"Atlas"`) {
		t.Fatalf("atlas_info with no arguments = (%q, isErr=%v), want the info body", text, isErr)
	}
}

// TestBadKeyToolErrorsPerTool covers the argUint-error branch of the remaining
// key-taking tool handlers (get_process_xml and create_instance), each surfaced
// as a tool result with isError:true.
func TestBadKeyToolErrorsPerTool(t *testing.T) {
	ts := newAtlas(t)
	for _, tool := range []string{"atlas_get_process_xml", "atlas_create_instance"} {
		t.Run(tool, func(t *testing.T) {
			resps := run(t, ts, callTool(1, tool, map[string]any{"key": "not-a-number"}))
			text, isErr := toolText(t, result(t, resps[0]))
			if !isErr || !strings.Contains(text, "non-negative integer") {
				t.Fatalf("%s bad key = (%q, isErr=%v), want a tool error", tool, text, isErr)
			}
		})
	}
}
