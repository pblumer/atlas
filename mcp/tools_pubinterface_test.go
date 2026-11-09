package mcp_test

import (
	"strings"
	"testing"
)

// The published-interface reads (ADR-0373), driven the way an agent would: list what
// it may see, read one, read one version, and ask a definition for its candidates.
// The server here has no published interface, so every read answers what an empty
// installation answers — which is what proves each tool reaches its own route.
func TestPublishedInterfaceToolsReachTheirRoutes(t *testing.T) {
	ts := newAtlas(t)

	text, isErr := callText(t, ts, "atlas_list_published_interfaces", map[string]any{})
	if isErr || !strings.Contains(text, `"total":0`) {
		t.Fatalf("list = (%q, isErr=%v)", text, isErr)
	}
	text, isErr = callText(t, ts, "atlas_get_published_interface", map[string]any{"id": "kreditpruefung"})
	if !isErr || !strings.Contains(text, "no published interface") {
		t.Fatalf("get = (%q, isErr=%v)", text, isErr)
	}
	text, isErr = callText(t, ts, "atlas_get_published_interface", map[string]any{"id": "kreditpruefung", "version": 2})
	if !isErr || !strings.Contains(text, "no published interface") {
		t.Fatalf("get version = (%q, isErr=%v)", text, isErr)
	}
	text, isErr = callText(t, ts, "atlas_interface_candidates", map[string]any{"key": 4242})
	if !isErr || !strings.Contains(text, "no deployed definition") {
		t.Fatalf("candidates = (%q, isErr=%v)", text, isErr)
	}

	// Missing or malformed arguments are refused before anything is sent.
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"atlas_get_published_interface", map[string]any{}},
		{"atlas_get_published_interface", map[string]any{"id": "x", "version": "two"}},
		{"atlas_interface_candidates", map[string]any{}},
	} {
		if _, isErr := callText(t, ts, c.tool, c.args); !isErr {
			t.Errorf("%s %v was not refused", c.tool, c.args)
		}
	}
}
