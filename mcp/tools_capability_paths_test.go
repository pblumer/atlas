package mcp_test

import (
	"net/http"
	"testing"
)

// TestMeasureCapabilityStatesItsWindow: the windowed figures mean nothing without the
// window, so it is required and travels as the query the endpoint reads.
func TestMeasureCapabilityStatesItsWindow(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusOK, `{"key":"billing"}`)

	text, isErr, c := rec.call(t, "atlas_measure_capability", map[string]any{"key": "billing"})
	wantRefusal(t, "atlas_measure_capability", text, isErr, c, "argument: windowDays")

	text, isErr, c = rec.call(t, "atlas_measure_capability", map[string]any{"key": "billing", "windowDays": 30})
	if isErr || text != `{"key":"billing"}` {
		t.Fatalf("atlas_measure_capability = (%q, isErr=%v), want the API body", text, isErr)
	}
	wantCall(t, "atlas_measure_capability", c, http.MethodGet, "/api/v1/capabilities/billing/measurement", "windowDays=30")
}
