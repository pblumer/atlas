package mcp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestClientBearer verifies WithBearer attaches the Authorization header and that
// its absence sends none.
func TestClientBearer(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()

	if _, err := NewClient(srv.URL, WithBearer("tok123")).get("/x"); err != nil {
		t.Fatalf("get with bearer: %v", err)
	}
	if gotAuth != "Bearer tok123" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer tok123")
	}

	gotAuth = "unset"
	if _, err := NewClient(srv.URL).get("/x"); err != nil {
		t.Fatalf("get without bearer: %v", err)
	}
	if gotAuth != "" {
		t.Fatalf("Authorization = %q, want empty (no bearer)", gotAuth)
	}

	// An empty bearer token is a no-op, so callers can pass it unconditionally.
	gotAuth = "unset"
	if _, err := NewClient(srv.URL, WithBearer("")).get("/x"); err != nil {
		t.Fatalf("get with empty bearer: %v", err)
	}
	if gotAuth != "" {
		t.Fatalf("empty bearer should send no header, got %q", gotAuth)
	}
}
