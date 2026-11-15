package api

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestArrivedOverHTTPS pins the two ways a browser's HTTPS connection reaches this
// process — the listener's own TLS, and a proxy saying so — and the one thing the
// header must never do: take Secure away from a connection that is TLS itself.
func TestArrivedOverHTTPS(t *testing.T) {
	cases := []struct {
		name      string
		tls       bool
		forwarded string
		want      bool
	}{
		{"plain listener, no proxy", false, "", false},
		{"the listener's own TLS", true, "", true},
		{"a proxy that ended TLS", false, "https", true},
		{"a chain, client-facing hop first", false, " https , http", true},
		{"a chain whose client-facing hop was plain", false, "http, https", false},
		{"a proxy serving plain HTTP", false, "http", false},
		{"not a scheme", false, "HTTPS://example", false},
		{"the header cannot downgrade the listener's TLS", true, "http", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if tc.forwarded != "" {
				r.Header.Set("X-Forwarded-Proto", tc.forwarded)
			}
			if got := arrivedOverHTTPS(r); got != tc.want {
				t.Errorf("arrivedOverHTTPS = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCookiesAreClearedTheWayTheyWereSet: a browser that holds a Secure cookie
// ignores a plain one of the same name, so the cookie that ends a session or a
// sign-on attempt has to be written with the attribute the first one carried.
func TestCookiesAreClearedTheWayTheyWereSet(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	clearSessionCookie(w, r)
	clearOIDCStateCookie(w, r)
	cookies := w.Result().Cookies()
	if len(cookies) != 3 {
		t.Fatalf("cleared %d cookies, want the session and the two sign-on cookies", len(cookies))
	}
	for _, c := range cookies {
		if !c.Secure {
			t.Errorf("the cookie clearing %s is not Secure behind a proxy that ended TLS", c.Name)
		}
		if c.MaxAge >= 0 {
			t.Errorf("the cookie clearing %s has MaxAge %d; it does not expire anything", c.Name, c.MaxAge)
		}
	}
}

// TestNoCookieDecidesSecureFromTheListenerAlone holds every cookie this package
// writes to one question: how did the browser arrive. Seven places answered it with
// r.TLS, which is the listener's side of the connection and plain HTTP behind every
// proxy that ends TLS — so the session cookie, the single sign-on state and the
// account-link state all went out without Secure there. A cookie added later must not
// bring the short answer back, and nothing else would notice if it did: the login
// still works, the attribute is simply missing.
func TestNoCookieDecidesSecureFromTheListenerAlone(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("listing the package's files: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("found no Go files; the test is not running in the package directory")
	}
	fromListener := regexp.MustCompile(`Secure:\s*r\.TLS\b`)
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if fromListener.MatchString(line) {
				t.Errorf("%s:%d sets a cookie's Secure attribute from r.TLS alone; ask arrivedOverHTTPS(r), which also hears a proxy that ended TLS",
					name, i+1)
			}
		}
	}
}
