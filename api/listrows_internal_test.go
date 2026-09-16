package api

import (
	"encoding/json"
	"testing"
)

// listRows unwraps a capped listing's envelope down to its rows
// (ADR-0378), for the
// tests inside package api. api_test has its own copy in server_test.go: the two
// packages cannot share a test helper, and duplicating six lines beats exporting a
// test-only function from production code.
func listRows(t *testing.T, body []byte) []byte {
	t.Helper()
	var page struct {
		Items json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode listing envelope: %v (%s)", err, truncateBody(body))
	}
	if page.Items == nil {
		t.Fatalf("listing carried no items: %s", truncateBody(body))
	}
	return page.Items
}

func truncateBody(body []byte) string {
	if len(body) > 400 {
		return string(body[:400]) + "…"
	}
	return string(body)
}
