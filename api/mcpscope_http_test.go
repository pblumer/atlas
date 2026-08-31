package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/pblumer/atlas/mcp"
)

// A token approved for /mcp has to be able to *use* /mcp.
//
// The confinement and the adapter arrived from different directions and met here.
// ADR-0200 scopes a grant by the resource the person approved, so a token for the
// transport is refused at /api/v1 — the audience made real. ADR-0196 has the adapter
// forward that same token to the API for every tool call it serves. Together they
// made a hosted MCP client connect, list its tools, and then get
// "this credential's scope (mcp) does not permit GET /api/v1/processes" from every
// one of them: a grant that reached the transport and nothing the transport does.
//
// What separates the two is not the credential — it is the same credential — but
// where the request came from, which is what mcp.TransportHeader says.

// mcpScopedToken walks the authorization code flow for the /mcp resource and returns
// the access token it yields: the credential a hosted MCP client actually holds.
func mcpScopedToken(t *testing.T, base string) string {
	t.Helper()
	admin := signedInClient(t, base)
	clientID, clientSecret := registerClient(t, admin, base)
	code := codeFrom(t, approve(t, admin, base, clientID, base+"/mcp", true))
	if code == "" {
		t.Fatal("approval produced no code")
	}
	status, tok := postToken(t, base, url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {testRedirect}, "code_verifier": {testVerifier},
		"client_id": {clientID}, "client_secret": {clientSecret},
	})
	if status != http.StatusOK {
		t.Fatalf("token exchange = %d: %v", status, tok)
	}
	access, _ := tok["access_token"].(string)
	if access == "" {
		t.Fatalf("exchange returned no access token: %v", tok)
	}
	return access
}

// bearerRPC posts one JSON-RPC message to /mcp with an access token.
func bearerRPC(t *testing.T, base, token, payload string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/mcp", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /mcp: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var m map[string]any
	if len(strings.TrimSpace(string(data))) > 0 {
		_ = json.Unmarshal(data, &m)
	}
	return resp.StatusCode, m
}

// TestMCPScopedTokenDrivesTheTools is the property the two records add up to: the
// tools work for the person who approved them, and the API stays out of reach.
func TestMCPScopedTokenDrivesTheTools(t *testing.T) {
	ts := newMCPServer(t)
	access := mcpScopedToken(t, ts.URL)

	status, resp := bearerRPC(t, ts.URL, access, listProcessesCall)
	if status != http.StatusOK {
		t.Fatalf("tools/call over /mcp = %d, want 200", status)
	}
	if text, isErr := toolText(t, resp); isErr {
		t.Fatalf("a tool call by the person who approved the client failed: %s", text)
	}

	// And the confinement is untouched: the same token driving the API directly is
	// still refused, which is the whole reason the marker exists rather than the
	// scope simply being widened to /api/v1.
	if got := bearerGet(t, ts.URL, "/api/v1/processes", access); got != http.StatusForbidden {
		t.Errorf("an /mcp-scoped token reached /api/v1/processes with %d, want 403", got)
	}
}

// TestMCPTransportMarkerCannotBeForged: the exemption is worth exactly what the
// marker is worth. Whoever holds the confined token can put any header they like on
// a direct request, and must get no further for it — the value is this server's own
// internal secret, and a guess is not it.
func TestMCPTransportMarkerCannotBeForged(t *testing.T) {
	ts := newMCPServer(t)
	access := mcpScopedToken(t, ts.URL)

	for _, forged := range []string{"1", "true", "atlas", strings.Repeat("0", 64)} {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/processes", nil)
		req.Header.Set("Authorization", "Bearer "+access)
		req.Header.Set(mcp.TransportHeader, forged)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /api/v1/processes: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("a forged marker %q got %d, want 403", forged, resp.StatusCode)
		}
	}
}

// TestMCPTransportMarkerIsStampedNotTrusted: what a caller sends under that name is
// replaced on the way in, so a request the adapter forwards can only ever carry the
// value this server put there. Without the overwrite, the header a client controls
// would be the header the boundary later checks.
func TestMCPTransportMarkerIsStampedNotTrusted(t *testing.T) {
	ts := newMCPServer(t)
	access := mcpScopedToken(t, ts.URL)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(listProcessesCall))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set(mcp.TransportHeader, "a value of my own")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /mcp: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	if text, isErr := toolText(t, m); isErr {
		t.Fatalf("a caller-supplied marker displaced the server's own: %s", text)
	}
}
