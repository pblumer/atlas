package mcp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/mcp"
)

// The adapter must not carry an identity of its own over the HTTP transport.
//
// It used to: cmd/atlas handed it the server's internal service token and every
// loopback call went out with that, whoever had made the MCP request. Combined
// with a transport nobody authenticated, that made reaching the port enough to
// drive the whole API as a principal the caller never had to present. Gating the
// transport is one half of the fix (the api package); forwarding what the caller
// presented, instead of substituting something stronger, is the other.

// recordingBackend stands in for an Atlas server and reports the credentials each
// request arrived with.
func recordingBackend(t *testing.T) (url string, auth, cookie *string) {
	t.Helper()
	var gotAuth, gotCookie string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCookie = r.Header.Get("Cookie")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"product":"Atlas","version":"test"}`))
	}))
	t.Cleanup(backend.Close)
	return backend.URL, &gotAuth, &gotCookie
}

// callInfo drives one atlas_info tool call over the HTTP transport, with whatever
// credentials decorate sets on the request.
func callInfo(t *testing.T, transport string, decorate func(*http.Request)) map[string]any {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call",` +
		`"params":{"name":"atlas_info","arguments":{}}}`
	req, err := http.NewRequest(http.MethodPost, transport, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if decorate != nil {
		decorate(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tools/call status = %d, want 200", resp.StatusCode)
	}
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

// TestHTTPForwardsTheCallersCredential is the property the transport now has: a
// tool call reaches the API as whoever made it. Both credentials Atlas accepts
// travel — the bearer a remote client holds, and the session cookie a browser
// sends when the UI itself drives a tool.
func TestHTTPForwardsTheCallersCredential(t *testing.T) {
	backendURL, gotAuth, gotCookie := recordingBackend(t)
	ts := httptest.NewServer(mcp.NewServer(mcp.NewClient(backendURL)))
	t.Cleanup(ts.Close)

	resp := callInfo(t, ts.URL, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer alices-token")
		r.AddCookie(&http.Cookie{Name: "atlas_session", Value: "alices-session"})
	})
	if res, ok := resp["result"].(map[string]any); !ok {
		t.Fatalf("no result in %v", resp)
	} else if isErr, _ := res["isError"].(bool); isErr {
		t.Fatalf("tool reported an error: %v", res)
	}

	if *gotAuth != "Bearer alices-token" {
		t.Errorf("Authorization reaching the API = %q, want the caller's own bearer", *gotAuth)
	}
	if !strings.Contains(*gotCookie, "atlas_session=alices-session") {
		t.Errorf("Cookie reaching the API = %q, want the caller's session", *gotCookie)
	}
}

// TestHTTPCallerCredentialBeatsTheAdapters is the half that closes the hole. An
// adapter configured with a token of its own must not lend it to a caller who
// presented a weaker one — otherwise every request is privileged again, just one
// level down from where the transport was open.
func TestHTTPCallerCredentialBeatsTheAdapters(t *testing.T) {
	backendURL, gotAuth, _ := recordingBackend(t)
	ts := httptest.NewServer(mcp.NewServer(mcp.NewClient(backendURL, mcp.WithBearer("the-adapters-own"))))
	t.Cleanup(ts.Close)

	callInfo(t, ts.URL, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer alices-token")
	})
	if *gotAuth != "Bearer alices-token" {
		t.Errorf("Authorization reaching the API = %q, want the caller's bearer, not the adapter's", *gotAuth)
	}
}

// TestHTTPWithoutACallerCredentialSendsNone: an unauthenticated server is still
// unauthenticated. Nothing is invented to fill the gap.
func TestHTTPWithoutACallerCredentialSendsNone(t *testing.T) {
	backendURL, gotAuth, gotCookie := recordingBackend(t)
	ts := httptest.NewServer(mcp.NewServer(mcp.NewClient(backendURL)))
	t.Cleanup(ts.Close)

	callInfo(t, ts.URL, nil)
	if *gotAuth != "" {
		t.Errorf("Authorization = %q, want none", *gotAuth)
	}
	if *gotCookie != "" {
		t.Errorf("Cookie = %q, want none", *gotCookie)
	}
}

// TestStdioUsesItsConfiguredToken: the stdio adapter is a per-agent process with
// one identity for its whole life, so there is no per-request caller to forward
// and the token it was built with is what authenticates it (atlas mcp --token).
func TestStdioUsesItsConfiguredToken(t *testing.T) {
	backendURL, gotAuth, _ := recordingBackend(t)
	srv := mcp.NewServer(mcp.NewClient(backendURL, mcp.WithBearer("the-stdio-token")))

	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call",` +
		`"params":{"name":"atlas_info","arguments":{}}}` + "\n")
	var out strings.Builder
	if err := srv.Serve(in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if *gotAuth != "Bearer the-stdio-token" {
		t.Errorf("Authorization = %q, want the stdio adapter's configured token", *gotAuth)
	}
}
