package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestValidateFeel exercises the edit-time FEEL validation endpoint the Modeler
// calls: a well-formed expression is ok (unknown variables are fine — they're
// process variables), a malformed one reports ok:false with a message, and an
// empty expression is a no-op success.
func TestValidateFeel(t *testing.T) {
	ts := newTestServer(t)
	cases := []struct {
		name   string
		body   string
		wantOK bool
	}{
		{"boolean condition", `{"expression":"amount > 100"}`, true},
		{"free variables allowed", `{"expression":"a + b * c"}`, true},
		{"builtin call", `{"expression":"string length(name) > 3"}`, true},
		{"blank is ok", `{"expression":"   "}`, true},
		{"dangling operator", `{"expression":"amount >"}`, false},
		{"unbalanced paren", `{"expression":"(1 + 2"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doReq(t, ts, http.MethodPost, "/api/v1/feel/validate", tc.body, "application/json")
			if code != http.StatusOK {
				t.Fatalf("status=%d body=%s", code, body)
			}
			var resp struct {
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			}
			if err := json.Unmarshal(body, &resp); err != nil {
				t.Fatalf("unmarshal %s: %v", body, err)
			}
			if resp.OK != tc.wantOK {
				t.Fatalf("ok=%v want %v (error=%q)", resp.OK, tc.wantOK, resp.Error)
			}
			if !tc.wantOK && resp.Error == "" {
				t.Fatalf("expected a non-empty error message for an invalid expression")
			}
			if tc.wantOK && resp.Error != "" {
				t.Fatalf("unexpected error for a valid expression: %q", resp.Error)
			}
		})
	}
}

// TestValidateFeelBadRequest rejects a malformed request body (not the FEEL, the
// JSON envelope) as a client error.
func TestValidateFeelBadRequest(t *testing.T) {
	ts := newTestServer(t)
	code, _ := doReq(t, ts, http.MethodPost, "/api/v1/feel/validate", "not json", "application/json")
	if code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", code)
	}
}
