package mcp_test

import (
	"strings"
	"testing"
)

// An instance whose Workers are ungoverned has no decisions, and an unknown one none
// either; the tool reads them back as an empty list rather than an error, so an agent
// asking "why was this refused" on an ungoverned instance learns that nothing decided.
func TestInstanceAuthorizationsViaTool(t *testing.T) {
	atlas := newAtlas(t)
	key := deployAndStartInstance(t, atlas, map[string]any{"key": 1})
	if got := callOne(t, atlas, "atlas_instance_authorizations", map[string]any{"key": key}); strings.TrimSpace(got) != "[]" {
		t.Fatalf("ungoverned instance: %s", got)
	}
	if got := callOne(t, atlas, "atlas_instance_authorizations", map[string]any{"key": 999999}); strings.TrimSpace(got) != "[]" {
		t.Fatalf("unknown instance: %s", got)
	}
}
