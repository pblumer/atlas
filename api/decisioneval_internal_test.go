package api

import "testing"

// TestRawJSONOr covers the decision-view JSON passthrough: a stored canonical JSON
// string is emitted verbatim, and an empty string falls back to the given document
// so a view field declared as JSON never carries an invalid empty value (ADR-0066).
func TestRawJSONOr(t *testing.T) {
	if got := string(rawJSONOr("", "{}")); got != "{}" {
		t.Errorf("rawJSONOr(empty) = %q, want {}", got)
	}
	if got := string(rawJSONOr(`{"Dish":"Roastbeef"}`, "{}")); got != `{"Dish":"Roastbeef"}` {
		t.Errorf("rawJSONOr(json) = %q, want it verbatim", got)
	}
}
