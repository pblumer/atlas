package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func get(t *testing.T, h http.Handler, path string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func TestHealthAlwaysOn(t *testing.T) {
	h := New(Config{Web: false, MCP: false}, nil).Handler()
	if resp := get(t, h, "/healthz"); resp.StatusCode != http.StatusOK {
		t.Errorf("healthz status = %d, want 200", resp.StatusCode)
	}
}

func TestInfoReportsSurfaceState(t *testing.T) {
	h := New(Config{Web: true, MCP: false}, nil).Handler()
	resp := get(t, h, "/api/v1/info")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("info status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Version  string          `json:"version"`
		Surfaces map[string]bool `json:"surfaces"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Surfaces["web"] || body.Surfaces["mcp"] {
		t.Errorf("surfaces = %v, want web:true mcp:false", body.Surfaces)
	}
	if body.Version == "" {
		t.Error("version should not be empty")
	}
}

func TestQueryAPIStubsReturn501(t *testing.T) {
	h := New(Config{Web: true, MCP: true}, nil).Handler()
	for _, p := range []string{"instances", "incidents", "jobs"} {
		if resp := get(t, h, "/api/v1/"+p); resp.StatusCode != http.StatusNotImplemented {
			t.Errorf("%s status = %d, want 501", p, resp.StatusCode)
		}
	}
}

func TestWebToggle(t *testing.T) {
	on := New(Config{Web: true}, nil).Handler()
	if resp := get(t, on, "/"); resp.StatusCode != http.StatusOK {
		t.Errorf("web on: / status = %d, want 200", resp.StatusCode)
	}

	off := New(Config{Web: false}, nil).Handler()
	if resp := get(t, off, "/"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("web off: / status = %d, want 404", resp.StatusCode)
	}
}

func TestMCPToggle(t *testing.T) {
	on := New(Config{MCP: true}, nil).Handler()
	if resp := get(t, on, "/mcp"); resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("mcp on: /mcp status = %d, want 501", resp.StatusCode)
	}

	off := New(Config{MCP: false}, nil).Handler()
	if resp := get(t, off, "/mcp"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("mcp off: /mcp status = %d, want 404", resp.StatusCode)
	}
}
