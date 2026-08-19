package mcp_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestVariableAuditViaTool reads the operator-override audit trail of a running
// instance. The overrides are seeded through the admin-gated set-variables
// endpoint directly (that write is deliberately not an MCP tool); the tool then
// reads the "who changed it" history back. An unknown key returns [].
func TestVariableAuditViaTool(t *testing.T) {
	atlas := newAtlas(t)
	key := deployAndStartInstance(t, atlas, map[string]any{"key": 1})

	// Seed an override via the HTTP API (auth off on the test server), the source
	// the audit trail records.
	resp, err := http.Post(
		fmt.Sprintf("%s/api/v1/instances/%d/variables", atlas.URL, key),
		"application/json",
		strings.NewReader(`{"variables":{"amount":200,"reviewer":"sam"}}`),
	)
	if err != nil {
		t.Fatalf("set variables: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set variables: status=%d body=%s", resp.StatusCode, body)
	}

	auditJSON := callOne(t, atlas, "atlas_variable_audit", map[string]any{"key": key})
	var rows []struct {
		Name  string `json:"name"`
		Value any    `json:"value"`
		Kind  string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(auditJSON), &rows); err != nil {
		t.Fatalf("decode audit %q: %v", auditJSON, err)
	}
	if len(rows) != 2 {
		t.Fatalf("audit rows = %d, want 2 (%s)", len(rows), auditJSON)
	}
	byName := map[string]struct {
		Name  string `json:"name"`
		Value any    `json:"value"`
		Kind  string `json:"kind"`
	}{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	if got := byName["amount"]; got.Kind != "number" || fmt.Sprint(got.Value) != "200" {
		t.Fatalf("amount row = %+v, want number 200", got)
	}
	if got := byName["reviewer"]; got.Kind != "string" || got.Value != "sam" {
		t.Fatalf("reviewer row = %+v, want string sam", got)
	}

	// An instance with no overrides (or an unknown key) returns an empty array.
	if empty := callOne(t, atlas, "atlas_variable_audit", map[string]any{"key": 999999}); strings.TrimSpace(empty) != "[]" {
		t.Fatalf("audit of unknown instance = %q, want []", empty)
	}
}
