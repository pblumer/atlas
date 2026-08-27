package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/mcp"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// Atlas as an RFC 9728 protected resource (ADR-0200).
//
// What these cover is the half of that record that does not depend on Atlas
// issuing tokens: a client that is refused learns *what it was refused by* and
// where to read about it, instead of guessing a route. Before this, a hosted MCP
// client read `WWW-Authenticate: Bearer realm="atlas"`, found no pointer, guessed
// /authorize and got a 404 — a failure that says nothing about the cause.

// getJSON fetches a URL with no credential and decodes the body.
func getJSON(t *testing.T, url string) (int, http.Header, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	return resp.StatusCode, resp.Header, m
}

// TestProtectedResourceMetadataIsPublic: the discovery documents must be readable
// by a caller holding nothing, on a server that requires a login for everything
// else. A metadata document behind the credential it exists to help you obtain is
// the one arrangement that cannot work.
func TestProtectedResourceMetadataIsPublic(t *testing.T) {
	ts := newMCPServer(t)

	for _, path := range []string{
		"/.well-known/oauth-protected-resource",
		"/.well-known/oauth-protected-resource/mcp",
	} {
		status, hdr, doc := getJSON(t, ts.URL+path)
		if status != http.StatusOK {
			t.Errorf("GET %s without a credential = %d, want 200", path, status)
		}
		// The media type, not the whole header: the API's JSON helper appends a charset,
		// and a client parses the type.
		if ct := hdr.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("GET %s Content-Type = %q, want application/json", path, ct)
		}
		if doc["resource"] == nil {
			t.Errorf("GET %s served no resource field; RFC 9728 requires it", path)
		}
	}
}

// TestProtectedResourceMetadataNamesItsResource: the two documents describe two
// different resources — the server and the MCP transport — and each must say which
// one it is. A client validates the audience of its token against this value, so a
// document naming the wrong resource is worse than none.
func TestProtectedResourceMetadataNamesItsResource(t *testing.T) {
	ts := newMCPServer(t)

	_, _, root := getJSON(t, ts.URL+"/.well-known/oauth-protected-resource")
	if got := root["resource"]; got != ts.URL {
		t.Errorf("root document resource = %v, want %q", got, ts.URL)
	}

	_, _, mcpDoc := getJSON(t, ts.URL+"/.well-known/oauth-protected-resource/mcp")
	if got, want := mcpDoc["resource"], ts.URL+"/mcp"; got != want {
		t.Errorf("MCP document resource = %v, want %q", got, want)
	}
}

// TestProtectedResourceMetadataNamesItsAuthorizationServer: the field this test
// used to pin as *absent*.
//
// It was absent while Atlas issued no tokens, because naming an authorization
// server it could not honour would send a client through a whole flow to refuse it
// at the end. Atlas issues them now, so the pointer is the truth — and it has to
// be there, because it is the only thing that tells a refused client where to go.
func TestProtectedResourceMetadataNamesItsAuthorizationServer(t *testing.T) {
	ts := newMCPServer(t)

	_, _, doc := getJSON(t, ts.URL+"/.well-known/oauth-protected-resource")
	servers, ok := doc["authorization_servers"].([]any)
	if !ok || len(servers) != 1 || servers[0] != ts.URL {
		t.Errorf("authorization_servers = %v, want [%q]", doc["authorization_servers"], ts.URL)
	}
	// The other fields are what make it a document rather than a pointer.
	if got := doc["bearer_methods_supported"]; got == nil {
		t.Error("the document does not say how a bearer is presented")
	}
}

// TestUnauthorizedPointsAtTheMetadata is the header half of RFC 9728 §5.1, and the
// direct fix for the failure that motivated the record: a refused client must be
// handed the metadata URL rather than left to guess at /authorize.
func TestUnauthorizedPointsAtTheMetadata(t *testing.T) {
	ts := newMCPServer(t)

	// A gated API route points at the document describing the server.
	resp, err := http.Get(ts.URL + "/api/v1/processes")
	if err != nil {
		t.Fatalf("GET /api/v1/processes: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous GET /api/v1/processes = %d, want 401", resp.StatusCode)
	}
	want := `resource_metadata="` + ts.URL + `/.well-known/oauth-protected-resource"`
	if auth := resp.Header.Get("WWW-Authenticate"); !strings.Contains(auth, want) {
		t.Errorf("WWW-Authenticate = %q, want it to contain %s", auth, want)
	}

	// /mcp points at the document describing /mcp, because that is the resource the
	// token will have to be issued for.
	mcpResp, err := http.Post(ts.URL+"/mcp", "application/json", http.NoBody)
	if err != nil {
		t.Fatalf("POST /mcp: %v", err)
	}
	mcpResp.Body.Close()
	if mcpResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous POST /mcp = %d, want 401", mcpResp.StatusCode)
	}
	wantMCP := `resource_metadata="` + ts.URL + `/.well-known/oauth-protected-resource/mcp"`
	if auth := mcpResp.Header.Get("WWW-Authenticate"); !strings.Contains(auth, wantMCP) {
		t.Errorf("/mcp WWW-Authenticate = %q, want it to contain %s", auth, wantMCP)
	}
}

// TestExternalURLOverridesTheRequestOrigin: Atlas serves plain HTTP and terminates
// TLS nowhere, so behind a proxy the origin it would derive from a request is
// http://<whatever Host said> — the wrong scheme for every deployment that has a
// certificate. An operator states the public origin once, and both the documents
// and the header follow it.
func TestExternalURLOverridesTheRequestOrigin(t *testing.T) {
	const external = "https://atlas.example.com"
	ts := newServerWithExternalURL(t, external)

	_, _, doc := getJSON(t, ts.URL+"/.well-known/oauth-protected-resource/mcp")
	if got, want := doc["resource"], external+"/mcp"; got != want {
		t.Errorf("resource = %v, want %q — the configured origin, not the request's", got, want)
	}

	resp, err := http.Get(ts.URL + "/api/v1/processes")
	if err != nil {
		t.Fatalf("GET /api/v1/processes: %v", err)
	}
	resp.Body.Close()
	want := `resource_metadata="` + external + `/.well-known/oauth-protected-resource"`
	if auth := resp.Header.Get("WWW-Authenticate"); !strings.Contains(auth, want) {
		t.Errorf("WWW-Authenticate = %q, want it to contain %s", auth, want)
	}
}

// newServerWithExternalURL starts an authenticated server whose public origin is
// stated rather than derived.
func newServerWithExternalURL(t *testing.T, external string) *httptest.Server {
	t.Helper()
	t.Setenv("ATLAS_ADMIN_USERNAME", "root")
	t.Setenv("ATLAS_ADMIN_PASSWORD", "rootpassword")

	dir := t.TempDir()
	wl, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, wl, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	ts := httptest.NewUnstartedServer(nil)
	loopback := "http://" + ts.Listener.Addr().String()
	srv, err := api.New(proc, store, dir,
		api.WithAuth(),
		api.WithExternalURL(external),
		api.WithMCP(mcp.NewServer(mcp.NewClient(loopback))),
	)
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	ts.Config.Handler = srv.Handler()
	ts.Start()
	t.Cleanup(func() {
		ts.Close()
		srv.Close()
		_ = store.Close()
		_ = wl.Close()
	})
	return ts
}
