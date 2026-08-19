package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestStartVarsFromMap documents the shared map→VariableValues contract the CSV
// upload path and the JSON start body both rely on (ADR-0084).
func TestStartVarsFromMap(t *testing.T) {
	if vs, err := startVarsFromMap(nil); err != nil || vs != nil {
		t.Fatalf("empty map: got %v, %v; want nil, nil", vs, err)
	}
	// A raw int is outside the encoding/json-with-UseNumber shape and is rejected.
	if _, err := startVarsFromMap(map[string]any{"x": 42}); err == nil ||
		!strings.Contains(err.Error(), "unsupported value type") {
		t.Fatalf("raw int: want unsupported value type error, got %v", err)
	}
	// Every supported kind converts, including a null and a nested array.
	vs, err := startVarsFromMap(map[string]any{
		"n": json.Number("3"), "s": "hi", "b": true, "z": nil,
		"j": []any{map[string]any{"k": "v"}},
	})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(vs) != 5 {
		t.Fatalf("converted %d variables, want 5", len(vs))
	}
}
