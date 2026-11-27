package mcp_test

import (
	"strings"
	"testing"
)

// An instance's model calls are read over MCP through the one route the HTTP surface
// has for them (ADR-0016, ADR-0566). An instance that asked no model
// answers an empty list, and a key that is not a number is the caller's error.
func TestInstanceAgentRunsViaTool(t *testing.T) {
	atlas := newAtlas(t)
	if got := strings.TrimSpace(callOne(t, atlas, "atlas_instance_agent_runs", map[string]any{"key": 1})); got != "[]" {
		t.Errorf("an instance that asked no model = %q, want []", got)
	}
	if _, isErr := toolText(t, result(t, run(t, atlas, callTool(2, "atlas_instance_agent_runs", map[string]any{"key": "x"}))[0])); !isErr {
		t.Error("a key that is not a number was accepted")
	}
}
