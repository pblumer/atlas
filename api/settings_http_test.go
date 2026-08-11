package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestThemeEndpointRoundTrip drives the org-wide theme over HTTP end to end: the
// default is empty, a PUT persists and is echoed back by GET, a bad colour is
// rejected, and DELETE resets to the default. Single-user mode (auth off), so the
// admin gate passes and this exercises the store wiring.
func TestThemeEndpointRoundTrip(t *testing.T) {
	ts := newTestServer(t)

	readAccent := func() string {
		code, body := doReq(t, ts, http.MethodGet, "/api/v1/settings/theme", "", "")
		if code != http.StatusOK {
			t.Fatalf("GET theme: status=%d body=%s", code, body)
		}
		var r struct {
			Accent string `json:"accent"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			t.Fatalf("GET theme: decode %s: %v", body, err)
		}
		return r.Accent
	}

	// Default: no override.
	if a := readAccent(); a != "" {
		t.Fatalf("default accent = %q; want empty", a)
	}

	// Set a brand colour.
	code, body := doReq(t, ts, http.MethodPut, "/api/v1/settings/theme", `{"accent":"#7c3aed"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("PUT theme: status=%d body=%s", code, body)
	}
	if a := readAccent(); a != "#7c3aed" {
		t.Fatalf("accent after PUT = %q; want #7c3aed", a)
	}

	// A 3-digit shorthand is normalised to #rrggbb on the way in.
	if code, body := doReq(t, ts, http.MethodPut, "/api/v1/settings/theme", `{"accent":"#abc"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("PUT shorthand: status=%d body=%s", code, body)
	}
	if a := readAccent(); a != "#aabbcc" {
		t.Fatalf("accent after shorthand PUT = %q; want #aabbcc", a)
	}

	// Garbage is rejected and does not change the stored value.
	if code, _ := doReq(t, ts, http.MethodPut, "/api/v1/settings/theme", `{"accent":"not-a-colour"}`, "application/json"); code != http.StatusBadRequest {
		t.Fatalf("PUT invalid: status=%d; want 400", code)
	}
	if a := readAccent(); a != "#aabbcc" {
		t.Fatalf("accent after rejected PUT = %q; want unchanged #aabbcc", a)
	}

	// Reset to default.
	if code, body := doReq(t, ts, http.MethodDelete, "/api/v1/settings/theme", "", ""); code != http.StatusNoContent {
		t.Fatalf("DELETE theme: status=%d body=%s", code, body)
	}
	if a := readAccent(); a != "" {
		t.Fatalf("accent after DELETE = %q; want empty", a)
	}
}
