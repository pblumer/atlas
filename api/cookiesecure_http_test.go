package api_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestSessionCookieIsSecureBehindATLSTerminatingProxy: a server behind a proxy that
// ends TLS receives plain HTTP, so r.TLS says nothing about the browser's connection.
// Reading only that left the session cookie without Secure in the one deployment that
// had a certificate, and a browser would then send it over http:// as well, where
// anybody on the path reads it. The proxy's X-Forwarded-Proto is what says how the
// browser arrived.
//
// The server here listens without TLS on purpose: it stands where Atlas stands behind
// the proxy, and the header is all it has to go on.
func TestSessionCookieIsSecureBehindATLSTerminatingProxy(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "s3cret-pass")
	cases := []struct {
		name       string
		forwarded  string
		wantSecure bool
	}{
		{"direct plain HTTP, no proxy in front", "", false},
		{"the proxy ended TLS", "https", true},
		{"a chain whose client-facing hop was HTTPS", "https, http", true},
		{"the proxy served plain HTTP itself", "http", false},
		{"a value that is no scheme", "wss", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/auth/login",
				strings.NewReader(`{"username":"admin","password":"s3cret-pass"}`))
			if err != nil {
				t.Fatalf("building the login request: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")
			if tc.forwarded != "" {
				req.Header.Set("X-Forwarded-Proto", tc.forwarded)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("login: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("login status = %d, want 200", resp.StatusCode)
			}
			var session *http.Cookie
			for _, c := range resp.Cookies() {
				if c.Name == "atlas_session" {
					session = c
				}
			}
			if session == nil {
				t.Fatal("the login set no session cookie")
			}
			if session.Secure != tc.wantSecure {
				t.Errorf("X-Forwarded-Proto %q: session cookie Secure = %v, want %v",
					tc.forwarded, session.Secure, tc.wantSecure)
			}
			// What the cookie carried before must not move with this.
			if !session.HttpOnly || session.SameSite != http.SameSiteLaxMode {
				t.Errorf("session cookie HttpOnly = %v, SameSite = %v; want HttpOnly and Lax",
					session.HttpOnly, session.SameSite)
			}
		})
	}
}
