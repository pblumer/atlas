package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestArgUint(t *testing.T) {
	cases := []struct {
		name    string
		args    map[string]any
		want    uint64
		wantErr bool
	}{
		{"float64 valid", map[string]any{"key": float64(7)}, 7, false},
		{"float64 negative", map[string]any{"key": float64(-1)}, 0, true},
		{"float64 fractional", map[string]any{"key": float64(1.5)}, 0, true},
		{"json.Number valid", map[string]any{"key": json.Number("42")}, 42, false},
		{"json.Number invalid", map[string]any{"key": json.Number("nope")}, 0, true},
		{"string valid", map[string]any{"key": "13"}, 13, false},
		{"string invalid", map[string]any{"key": "xyz"}, 0, true},
		{"wrong type", map[string]any{"key": true}, 0, true},
		{"missing", map[string]any{}, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := argUint(tc.args, "key")
			if (err != nil) != tc.wantErr {
				t.Fatalf("argUint err = %v, wantErr = %v", err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Fatalf("argUint = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestArgString(t *testing.T) {
	cases := []struct {
		name    string
		args    map[string]any
		want    string
		wantErr bool
	}{
		{"valid", map[string]any{"xml": "hi"}, "hi", false},
		{"missing", map[string]any{}, "", true},
		{"wrong type", map[string]any{"xml": 3}, "", true},
		{"empty", map[string]any{"xml": ""}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := argString(tc.args, "xml")
			if (err != nil) != tc.wantErr {
				t.Fatalf("argString err = %v, wantErr = %v", err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Fatalf("argString = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseUint(t *testing.T) {
	if got, err := parseUint("100"); err != nil || got != 100 {
		t.Fatalf("parseUint(100) = (%d, %v), want (100, nil)", got, err)
	}
	if _, err := parseUint("nope"); err == nil {
		t.Fatal("parseUint(nope) should error")
	}
}

// TestClientDoUnreachable covers the do path where the HTTP request itself fails
// (server closed), which must be wrapped as a reach-atlas error.
func TestClientDoUnreachable(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	url := ts.URL
	ts.Close() // now nothing is listening

	c := NewClient(url)
	if _, err := c.get("/api/v1/info"); err == nil || !strings.Contains(err.Error(), "reach atlas server") {
		t.Fatalf("get on closed server err = %v, want a reach-atlas error", err)
	}
}

// TestClientErrorBodies covers extractError's fallback branch (non-JSON body)
// and the apiError message rendering, plus a well-formed JSON error body.
func TestClientErrorBodies(t *testing.T) {
	t.Run("plain text body falls back", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("  boom  "))
		}))
		defer ts.Close()
		_, err := NewClient(ts.URL).get("/x")
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("err = %v, want the trimmed plain-text body", err)
		}
	})

	t.Run("json error body", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"bad thing"}`))
		}))
		defer ts.Close()
		_, err := NewClient(ts.URL).get("/x")
		if err == nil || !strings.Contains(err.Error(), "bad thing") {
			t.Fatalf("err = %v, want the JSON error message", err)
		}
	})

	t.Run("empty body uses status only", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer ts.Close()
		_, err := NewClient(ts.URL).get("/x")
		if err == nil || !strings.Contains(err.Error(), "status 503") {
			t.Fatalf("err = %v, want a status-only message", err)
		}
	})
}

// TestExtractError exercises the helper directly for both shapes.
func TestExtractError(t *testing.T) {
	if got := extractError([]byte(`{"error":"x"}`)); got != "x" {
		t.Fatalf("extractError json = %q, want x", got)
	}
	if got := extractError([]byte("  raw  ")); got != "raw" {
		t.Fatalf("extractError fallback = %q, want raw", got)
	}
	if got := extractError([]byte(`{"error":""}`)); got != `{"error":""}` {
		t.Fatalf("extractError empty-error = %q, want the raw body", got)
	}
}
