package api_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestWhyADocumentCouldNotBeFetchedIsNotToldToAStranger: naming a client by the URL
// of its metadata document makes Atlas fetch that URL, on a route that answers
// without a session. The fetch is bounded — only public addresses, no redirect, a
// size, a time, a throttle — and what it found was then told to the caller word for
// word: the address a name resolved to, the resolver's own error, the dial or TLS
// failure. That turns the server into a way of asking what an internal name resolves
// to and which ports answer, from where the server stands.
//
// A caller who is not an administrator is now told that the document could not be
// read and nothing about why; the cause goes to the log, and an administrator, who
// is the one setting such a client up, still reads it on the screen.
func TestWhyADocumentCouldNotBeFetchedIsNotToldToAStranger(t *testing.T) {
	// Not connectedAtlas: that server may read private addresses, and the refusal of
	// one is the cause this test needs.
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	q := url.Values{
		"client_id": {"https://localhost:1/client.json"}, "redirect_uri": {"https://localhost:1/cb"},
		"response_type": {"code"}, "code_challenge": {testChallenge()}, "code_challenge_method": {"S256"},
		"resource": {ts.URL + "/mcp"}, "state": {"xyz"},
	}
	contextURL := ts.URL + "/api/v1/oauth/authorize-context?" + q.Encode()

	_, anon := csGetJSON(t, &http.Client{}, contextURL)
	said, _ := anon["error"].(string)
	if said == "" {
		t.Fatalf("a document that cannot be read was not refused: %v", anon)
	}
	for _, leak := range []string{"127.0.0.1", "::1", "resolves to", "public address", "connection refused", "dial", "lookup"} {
		if strings.Contains(said, leak) {
			t.Errorf("a caller without a session was told %q, which says %q about what the server found", said, leak)
		}
	}

	_, forAdmin := csGetJSON(t, admin, contextURL)
	detail, _ := forAdmin["error"].(string)
	if !strings.Contains(detail, "public address") {
		t.Errorf("an administrator was told %q, want the reason the document could not be read", detail)
	}
}
