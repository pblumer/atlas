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

// TestEvaluateFeel exercises the "Test expression" endpoint: it compiles and
// evaluates a FEEL expression against sample variables and returns the result
// with its kind. A FEEL type error (e.g. number + string) yields null rather
// than an error, which the endpoint reports faithfully.
func TestEvaluateFeel(t *testing.T) {
	ts := newTestServer(t)
	cases := []struct {
		name       string
		body       string
		wantOK     bool
		wantResult string
		wantKind   string
	}{
		{"arithmetic", `{"expression":"1 + 1"}`, true, "2", "number"},
		{"with variables", `{"expression":"amount * 2","variables":{"amount":21}}`, true, "42", "number"},
		{"string builtin", `{"expression":"upper case(name)","variables":{"name":"bob"}}`, true, "BOB", "string"},
		{"boolean", `{"expression":"a and b","variables":{"a":true,"b":false}}`, true, "false", "boolean"},
		{"decimal variable", `{"expression":"amount * (1 + rate)","variables":{"amount":100,"rate":0.19}}`, true, "119", "number"},
		{"null variable", `{"expression":"x","variables":{"x":null}}`, true, "null", "null"},
		{"type error is null", `{"expression":"1 + \"x\""}`, true, "null", "null"},
		{"syntax error", `{"expression":"1 +"}`, false, "", ""},
		{"empty is not ok", `{"expression":"  "}`, false, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doReq(t, ts, http.MethodPost, "/api/v1/feel/evaluate", tc.body, "application/json")
			if code != http.StatusOK {
				t.Fatalf("status=%d body=%s", code, body)
			}
			var resp struct {
				OK     bool   `json:"ok"`
				Result string `json:"result"`
				Kind   string `json:"kind"`
				Error  string `json:"error"`
			}
			if err := json.Unmarshal(body, &resp); err != nil {
				t.Fatalf("unmarshal %s: %v", body, err)
			}
			if resp.OK != tc.wantOK {
				t.Fatalf("ok=%v want %v (error=%q)", resp.OK, tc.wantOK, resp.Error)
			}
			if !tc.wantOK {
				if resp.Error == "" {
					t.Fatalf("expected an error message when not ok")
				}
				return
			}
			if resp.Result != tc.wantResult || resp.Kind != tc.wantKind {
				t.Fatalf("result=%q kind=%q, want %q/%q", resp.Result, resp.Kind, tc.wantResult, tc.wantKind)
			}
		})
	}
}

// TestEvaluateFeelStructuredVariable binds a structured variable (an object) and
// reads one of its members through FEEL, proving objects/arrays are usable — not
// rejected — as evaluation inputs (ADR-0037).
func TestEvaluateFeelStructuredVariable(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/feel/evaluate",
		`{"expression":"x.nested + 1","variables":{"x":{"nested":1}}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var resp struct {
		OK     bool   `json:"ok"`
		Result string `json:"result"`
		Kind   string `json:"kind"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.OK || resp.Result != "2" || resp.Kind != "number" {
		t.Fatalf("x.nested + 1 = ok=%v result=%q kind=%q err=%q, want 2/number", resp.OK, resp.Result, resp.Kind, resp.Error)
	}
}

// TestEvaluateFeelReturnsJSON evaluates to a whole object and checks it renders as
// canonical JSON under the "json" kind.
func TestEvaluateFeelReturnsJSON(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/feel/evaluate",
		`{"expression":"{b: 2, a: 1}"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var resp struct {
		OK     bool   `json:"ok"`
		Result string `json:"result"`
		Kind   string `json:"kind"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.OK || resp.Kind != "json" || resp.Result != `{"a":1,"b":2}` {
		t.Fatalf("got ok=%v kind=%q result=%q, want json/{\"a\":1,\"b\":2}", resp.OK, resp.Kind, resp.Result)
	}
}

// TestEvaluateFeelBadRequest rejects a malformed JSON envelope.
func TestEvaluateFeelBadRequest(t *testing.T) {
	ts := newTestServer(t)
	code, _ := doReq(t, ts, http.MethodPost, "/api/v1/feel/evaluate", "not json", "application/json")
	if code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", code)
	}
}
