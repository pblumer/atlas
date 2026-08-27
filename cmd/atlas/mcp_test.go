package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The stdio adapter's credential.
//
// It had none: runMCP built its client with no bearer and offered no flag to give
// it one, so `atlas mcp --server …` could not work against a server with --auth at
// all — every tool call came back 401. `atlas worker` has had --token for some
// time; this is the same thing for the adapter, and it is what the transport
// becoming authenticated makes necessary rather than merely tidy.
//
// These drive the real entry point, so what is under test is the wiring — flag or
// environment, into the client, onto the request — and not just that WithBearer
// works, which the mcp package already covers.

// mcpBackend stands in for an Atlas server and reports the Authorization header
// each request arrived with.
func mcpBackend(t *testing.T) (url string, auth *string) {
	t.Helper()
	var got string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"product":"Atlas","version":"test"}`))
	}))
	t.Cleanup(backend.Close)
	return backend.URL, &got
}

// oneInfoCall is a single JSON-RPC line asking for the cheapest tool there is, so
// the adapter makes exactly one call to the backend and then sees EOF.
const oneInfoCall = `{"jsonrpc":"2.0","id":1,"method":"tools/call",` +
	`"params":{"name":"atlas_info","arguments":{}}}` + "\n"

func TestMCPStdioSendsTheTokenFromTheFlag(t *testing.T) {
	// Set in the environment too, to pin which one wins: an operator who passes the
	// flag has said something more specific than the environment they inherited.
	t.Setenv("ATLAS_TOKEN", "from-the-environment")
	url, gotAuth := mcpBackend(t)

	var out strings.Builder
	if err := runMCPOn([]string{"--server", url, "--token", "from-the-flag"},
		strings.NewReader(oneInfoCall), &out); err != nil {
		t.Fatalf("runMCPOn: %v", err)
	}
	if *gotAuth != "Bearer from-the-flag" {
		t.Errorf("Authorization = %q, want the flag's token", *gotAuth)
	}
}

func TestMCPStdioFallsBackToTheEnvironment(t *testing.T) {
	t.Setenv("ATLAS_TOKEN", "from-the-environment")
	url, gotAuth := mcpBackend(t)

	var out strings.Builder
	if err := runMCPOn([]string{"--server", url}, strings.NewReader(oneInfoCall), &out); err != nil {
		t.Fatalf("runMCPOn: %v", err)
	}
	if *gotAuth != "Bearer from-the-environment" {
		t.Errorf("Authorization = %q, want ATLAS_TOKEN", *gotAuth)
	}
}

// TestMCPStdioTrimsTheToken: a token read from a file or a shell export routinely
// arrives with a trailing newline, and a bearer sent with one is refused for a
// reason nothing in the message explains.
func TestMCPStdioTrimsTheToken(t *testing.T) {
	t.Setenv("ATLAS_TOKEN", "  padded-token\n")
	url, gotAuth := mcpBackend(t)

	var out strings.Builder
	if err := runMCPOn([]string{"--server", url}, strings.NewReader(oneInfoCall), &out); err != nil {
		t.Fatalf("runMCPOn: %v", err)
	}
	if *gotAuth != "Bearer padded-token" {
		t.Errorf("Authorization = %q, want the token without surrounding whitespace", *gotAuth)
	}
}

// TestMCPStdioWithoutATokenSendsNone: an unauthenticated server is still the
// default case, and nothing is invented to fill the gap.
func TestMCPStdioWithoutATokenSendsNone(t *testing.T) {
	t.Setenv("ATLAS_TOKEN", "")
	url, gotAuth := mcpBackend(t)

	var out strings.Builder
	if err := runMCPOn([]string{"--server", url}, strings.NewReader(oneInfoCall), &out); err != nil {
		t.Fatalf("runMCPOn: %v", err)
	}
	if *gotAuth != "" {
		t.Errorf("Authorization = %q, want none", *gotAuth)
	}
}

// TestMCPStdioAnswersOnTheGivenStream is the sanity check that the streams are
// wired at all: the adapter's reply goes to the writer it was handed, which for
// the real command is stdout and must never carry anything else.
func TestMCPStdioAnswersOnTheGivenStream(t *testing.T) {
	url, _ := mcpBackend(t)

	var out strings.Builder
	if err := runMCPOn([]string{"--server", url}, strings.NewReader(oneInfoCall), &out); err != nil {
		t.Fatalf("runMCPOn: %v", err)
	}
	if !strings.Contains(out.String(), `"result"`) {
		t.Errorf("adapter wrote %q, want a JSON-RPC result", out.String())
	}
}
