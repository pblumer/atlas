package main

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// The adapter's trust anchor.
//
// `atlas mcp --server https://…` is a client of a remote Atlas like the promotion
// path and `atlas worker` are, and it hit the same wall: where that server's
// certificate comes from an internal CA, verification fails and there was no way
// to name the CA short of touching the host's trust store. ADR-0191 left this as
// the one client it did not cover; this is that flag in its third place.
//
// This drives the real entry point, so what is under test is the wiring — flag or
// environment, into the client, onto the transport.

// mcpTLSBackend stands in for an Atlas server reachable only over TLS, with a
// certificate signed by nothing the host trusts. It reports whether it was
// reached, and writes its certificate where --tls-ca can be pointed at it.
func mcpTLSBackend(t *testing.T) (url, caFile string, reached *bool) {
	t.Helper()
	var got bool
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		got = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"product":"Atlas","version":"test"}`))
	}))
	t.Cleanup(backend.Close)

	caFile = filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: backend.Certificate().Raw})
	if err := os.WriteFile(caFile, pemBytes, 0o600); err != nil {
		t.Fatalf("write CA bundle: %v", err)
	}
	return backend.URL, caFile, &got
}

func TestMCPStdioTrustsTheCAFromTheFlag(t *testing.T) {
	url, caFile, reached := mcpTLSBackend(t)

	var out strings.Builder
	if err := runMCPOn([]string{"--server", url, "--tls-ca", caFile}, strings.NewReader(oneInfoCall), &out); err != nil {
		t.Fatalf("runMCPOn: %v", err)
	}
	if !*reached {
		t.Fatalf("the server was never reached; the adapter answered %q", out.String())
	}
	if !strings.Contains(out.String(), `"result"`) {
		t.Errorf("adapter wrote %q, want a JSON-RPC result", out.String())
	}
}

// Without the CA the call is refused, which is what makes the flag mean
// something: it adds a trust anchor rather than dropping verification.
func TestMCPStdioWithoutTheCAIsRefused(t *testing.T) {
	url, _, reached := mcpTLSBackend(t)

	var out strings.Builder
	if err := runMCPOn([]string{"--server", url}, strings.NewReader(oneInfoCall), &out); err != nil {
		t.Fatalf("runMCPOn: %v", err)
	}
	if *reached {
		t.Error("a certificate signed by nothing the host trusts was accepted")
	}
	if !strings.Contains(out.String(), "certificate") {
		t.Errorf("adapter wrote %q, want the verification failure surfaced to the agent", out.String())
	}
}

// A bad bundle stops the adapter at startup rather than at the first tool call:
// the agent driving it sees a process that did not start, not a tool that fails
// for a reason it cannot act on.
func TestMCPStdioRefusesAnUnreadableCA(t *testing.T) {
	url, _, _ := mcpTLSBackend(t)

	err := runMCPOn([]string{"--server", url, "--tls-ca", filepath.Join(t.TempDir(), "absent.pem")},
		strings.NewReader(oneInfoCall), &strings.Builder{})
	if err == nil {
		t.Fatal("the adapter started with a --tls-ca path that does not exist")
	}
	if !strings.Contains(err.Error(), "--tls-ca") {
		t.Errorf("error %q does not name the flag it is about", err)
	}
}

// The environment is the fallback, as it is for --token: an agent runner that
// cannot pass flags can still say what to trust.
func TestMCPStdioTrustsTheCAFromTheEnvironment(t *testing.T) {
	url, caFile, reached := mcpTLSBackend(t)
	t.Setenv("ATLAS_TLS_CA", caFile)

	var out strings.Builder
	if err := runMCPOn([]string{"--server", url}, strings.NewReader(oneInfoCall), &out); err != nil {
		t.Fatalf("runMCPOn: %v", err)
	}
	if !*reached {
		t.Fatalf("the server was never reached; the adapter answered %q", out.String())
	}
}
