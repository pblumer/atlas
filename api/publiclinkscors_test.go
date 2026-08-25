package api_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
)

// reqOrigin performs a request with an Origin header (a cross-origin browser call)
// and returns the status and response headers, so the CORS assertions can read
// Access-Control-* without a body round-trip.
func reqOrigin(t *testing.T, url, method, origin, body, contentType string) (int, http.Header) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	return res.StatusCode, res.Header
}

// With an allow-listed origin, the public form endpoints echo it in
// Access-Control-Allow-Origin, and the POST /start preflight is answered — the two
// things a cross-origin embedded widget needs (ADR-draft-embed-public-forms-cross-origin). Credentials are never
// allowed, so the header must be absent.
func TestPublicFormsCORSAllowedOrigin(t *testing.T) {
	const origin = "https://embed.example"
	ts := newTestServerWith(t, api.WithPublicFormsCORS([]string{origin}))
	token := publish(t, ts)

	// Simple GET /schema: no preflight, but the response must carry the header so a
	// cross-origin reader may see it.
	code, h := reqOrigin(t, ts.URL+"/public/forms/"+token+"/schema", http.MethodGet, origin, "", "")
	if code != http.StatusOK {
		t.Fatalf("schema: %d", code)
	}
	if got := h.Get("Access-Control-Allow-Origin"); got != origin {
		t.Errorf("schema ACAO = %q, want %q", got, origin)
	}
	if h.Get("Access-Control-Allow-Credentials") != "" {
		t.Error("credentials must never be allowed on the cookieless public surface")
	}

	// Preflight for POST /start (application/json is not a simple request).
	code, h = reqOrigin(t, ts.URL+"/public/forms/"+token+"/start", http.MethodOptions, origin, "", "")
	if code != http.StatusNoContent {
		t.Fatalf("preflight: %d, want 204", code)
	}
	if got := h.Get("Access-Control-Allow-Origin"); got != origin {
		t.Errorf("preflight ACAO = %q, want %q", got, origin)
	}
	if m := h.Get("Access-Control-Allow-Methods"); !strings.Contains(m, "POST") {
		t.Errorf("preflight methods = %q, want it to allow POST", m)
	}
	if hd := h.Get("Access-Control-Allow-Headers"); !strings.Contains(hd, "Content-Type") {
		t.Errorf("preflight headers = %q, want Content-Type", hd)
	}

	// The actual cross-origin POST also carries the header.
	code, h = reqOrigin(t, ts.URL+"/public/forms/"+token+"/start", http.MethodPost, origin, `{"variables":{"customer":"Acme"}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("start: %d", code)
	}
	if got := h.Get("Access-Control-Allow-Origin"); got != origin {
		t.Errorf("start ACAO = %q, want %q", got, origin)
	}
}

// An origin that is not on the allow-list gets no CORS headers, so the browser
// blocks the cross-origin read — the request still succeeds server-side (it is the
// same public endpoint), only the header that would let another origin read it is
// withheld.
func TestPublicFormsCORSDisallowedOrigin(t *testing.T) {
	ts := newTestServerWith(t, api.WithPublicFormsCORS([]string{"https://embed.example"}))
	token := publish(t, ts)
	code, h := reqOrigin(t, ts.URL+"/public/forms/"+token+"/schema", http.MethodGet, "https://evil.example", "", "")
	if code != http.StatusOK {
		t.Fatalf("schema: %d", code)
	}
	if got := h.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("a non-allow-listed origin got ACAO %q, want none", got)
	}
}

// The default server configures no CORS, so even a plausible origin gets no header:
// cross-origin embedding is opt-in, zero blast radius on existing deployments.
func TestPublicFormsCORSOffByDefault(t *testing.T) {
	ts := newTestServer(t)
	token := publish(t, ts)
	_, h := reqOrigin(t, ts.URL+"/public/forms/"+token+"/schema", http.MethodGet, "https://embed.example", "", "")
	if got := h.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("CORS is on with no configuration: ACAO = %q", got)
	}
	// A preflight with no CORS configured still routes (OPTIONS is registered) but
	// carries no headers, which the browser treats as blocked.
	code, h := reqOrigin(t, ts.URL+"/public/forms/"+token+"/start", http.MethodOptions, "https://embed.example", "", "")
	if code != http.StatusNoContent {
		t.Fatalf("preflight: %d, want 204", code)
	}
	if got := h.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("preflight ACAO with no config = %q, want none", got)
	}
}

// The "*" sentinel echoes whatever origin calls, so the form embeds anywhere. It
// echoes the caller rather than a literal "*" — and still sends no credentials, so
// "any origin" grants only what a visitor to the public link already has.
func TestPublicFormsCORSWildcard(t *testing.T) {
	ts := newTestServerWith(t, api.WithPublicFormsCORS([]string{"*"}))
	token := publish(t, ts)
	for _, origin := range []string{"https://a.example", "https://b.example"} {
		_, h := reqOrigin(t, ts.URL+"/public/forms/"+token+"/schema", http.MethodGet, origin, "", "")
		if got := h.Get("Access-Control-Allow-Origin"); got != origin {
			t.Errorf("wildcard ACAO = %q, want the caller origin %q", got, origin)
		}
		if h.Get("Access-Control-Allow-Credentials") != "" {
			t.Error("wildcard must not allow credentials")
		}
	}
}
