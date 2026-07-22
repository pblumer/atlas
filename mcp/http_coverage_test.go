package mcp_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pblumer/atlas/mcp"
)

// TestHTTPDeleteAcknowledged covers ServeHTTP's DELETE arm: the stateless
// transport has no session to tear down, so a DELETE is acknowledged with 204.
func TestHTTPDeleteAcknowledged(t *testing.T) {
	ts := newMCPHTTP(t)
	req, err := http.NewRequest(http.MethodDelete, ts.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204", resp.StatusCode)
	}
}

// errBody is a request body whose Read always fails, to drive serveHTTPPost's
// body-read error branch.
type errBody struct{}

func (errBody) Read([]byte) (int, error) { return 0, errors.New("boom") }
func (errBody) Close() error             { return nil }

// TestHTTPReadBodyError covers serveHTTPPost's io.ReadAll error branch: a POST
// whose body fails to read is answered with a JSON-RPC parse error (HTTP 200).
func TestHTTPReadBodyError(t *testing.T) {
	srv := mcp.NewServer(mcp.NewClient("http://example.invalid"))
	req := httptest.NewRequest(http.MethodPost, "/mcp", errBody{})
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("read-error status = %d, want 200 with a JSON-RPC error body", rec.Code)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.Bytes(), err)
	}
	e, ok := m["error"].(map[string]any)
	if !ok {
		t.Fatalf("want a JSON-RPC error, got %v", m)
	}
	if c, _ := e["code"].(float64); int(c) != -32700 {
		t.Fatalf("error code = %v, want -32700 (parse error)", e["code"])
	}
}
