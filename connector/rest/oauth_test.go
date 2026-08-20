package rest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestTokenProviderFetchAndCache proves the client-credentials POST carries
// grant_type + scope and HTTP Basic client auth, and that a fetched token is cached
// until it nears expiry (one fetch for a burst) then refetched once it lapses.
func TestTokenProviderFetchAndCache(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "client_credentials" {
			t.Errorf("grant_type = %q, want client_credentials", got)
		}
		if got := r.Form.Get("scope"); got != "read write" {
			t.Errorf("scope = %q, want the requested scope", got)
		}
		if u, p, ok := r.BasicAuth(); !ok || u != "cid" || p != "csecret" {
			t.Errorf("basic auth = %q/%q ok=%v, want cid/csecret", u, p, ok)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":"T%d","token_type":"Bearer","expires_in":3600}`, calls)
	}))
	defer srv.Close()

	now := time.Unix(1000, 0)
	p := &tokenProvider{http: srv.Client(), cache: map[string]cachedToken{}, now: func() time.Time { return now }}
	cfg := OAuthConfig{TokenURL: srv.URL, ClientID: "cid", ClientSecret: "csecret", Scope: "read write"}

	tok, err := p.Token(context.Background(), cfg)
	if err != nil || tok != "T1" {
		t.Fatalf("first Token = %q,%v, want T1", tok, err)
	}
	// A second call at the same instant is served from cache — no new fetch.
	if tok, _ := p.Token(context.Background(), cfg); tok != "T1" || calls != 1 {
		t.Errorf("cached Token = %q, calls = %d, want T1 and 1 fetch", tok, calls)
	}
	// Past the token's lifetime, the next call refetches.
	now = now.Add(2 * time.Hour)
	if tok, _ := p.Token(context.Background(), cfg); tok != "T2" || calls != 2 {
		t.Errorf("refetched Token = %q, calls = %d, want T2 and 2 fetches", tok, calls)
	}
}

// TestTokenProviderDefaultTTL proves a response without expires_in still yields a
// token and caches it (the assumed default lifetime), so the next call is a hit.
func TestTokenProviderDefaultTTL(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"TOK"}`)
	}))
	defer srv.Close()
	now := time.Unix(0, 0)
	p := &tokenProvider{http: srv.Client(), cache: map[string]cachedToken{}, now: func() time.Time { return now }}
	cfg := OAuthConfig{TokenURL: srv.URL, ClientID: "c", ClientSecret: "s"}
	if tok, err := p.Token(context.Background(), cfg); err != nil || tok != "TOK" {
		t.Fatalf("Token = %q,%v, want TOK", tok, err)
	}
	if tok, _ := p.Token(context.Background(), cfg); tok != "TOK" || calls != 1 {
		t.Errorf("second Token = %q calls=%d, want a cache hit", tok, calls)
	}
}

// TestTokenProviderShortLivedNotCached proves a token whose lifetime is under the
// refresh skew is used but not cached (its cache entry would already be stale).
func TestTokenProviderShortLivedNotCached(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"S","expires_in":5}`)
	}))
	defer srv.Close()
	now := time.Unix(0, 0)
	p := &tokenProvider{http: srv.Client(), cache: map[string]cachedToken{}, now: func() time.Time { return now }}
	cfg := OAuthConfig{TokenURL: srv.URL, ClientID: "c", ClientSecret: "s"}
	if tok, _ := p.Token(context.Background(), cfg); tok != "S" {
		t.Fatalf("Token = %q, want S", tok)
	}
	// expires_in (5s) < tokenSkew (30s), so nothing was cached: the next call refetches.
	if tok, _ := p.Token(context.Background(), cfg); tok != "S" || calls != 2 {
		t.Errorf("short-lived token calls = %d, want 2 (not cached)", calls)
	}
}

func TestTokenProviderErrors(t *testing.T) {
	// A non-2xx token endpoint is an error.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer bad.Close()
	p := NewTokenProvider()
	if _, err := p.Token(context.Background(), OAuthConfig{TokenURL: bad.URL, ClientID: "c", ClientSecret: "s"}); err == nil {
		t.Error("Token on HTTP 401: err = nil, want error")
	}

	// A 2xx body with no access_token is an error.
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"token_type":"Bearer"}`)
	}))
	defer empty.Close()
	if _, err := p.Token(context.Background(), OAuthConfig{TokenURL: empty.URL, ClientID: "c", ClientSecret: "s"}); err == nil {
		t.Error("Token with no access_token: err = nil, want error")
	}

	// A malformed JSON body is an error.
	garbled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{not json`)
	}))
	defer garbled.Close()
	if _, err := p.Token(context.Background(), OAuthConfig{TokenURL: garbled.URL, ClientID: "c", ClientSecret: "s"}); err == nil {
		t.Error("Token with malformed body: err = nil, want error")
	}

	// An unreachable token endpoint is a transport error.
	if _, err := p.Token(context.Background(), OAuthConfig{TokenURL: "http://127.0.0.1:1/token", ClientID: "c", ClientSecret: "s"}); err == nil {
		t.Error("Token to an unreachable endpoint: err = nil, want error")
	}
}
