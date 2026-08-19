package sharepoint

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// tokenServer is a fake OAuth2 token endpoint that records the last form it received
// and counts requests, returning a canned access token with the given TTL.
type tokenServer struct {
	srv      *httptest.Server
	lastForm map[string]string
	calls    int32
	ttl      int64
	status   int
	body     string // when set, returned verbatim instead of a token JSON
}

func newTokenServer(t *testing.T, ttl int64) *tokenServer {
	t.Helper()
	ts := &tokenServer{ttl: ttl, status: 200}
	ts.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&ts.calls, 1)
		_ = r.ParseForm()
		ts.lastForm = map[string]string{}
		for k := range r.PostForm {
			ts.lastForm[k] = r.PostForm.Get(k)
		}
		w.WriteHeader(ts.status)
		if ts.body != "" {
			_, _ = w.Write([]byte(ts.body))
			return
		}
		n := atomic.LoadInt32(&ts.calls)
		_, _ = w.Write([]byte(`{"access_token":"tok-` + strconv.Itoa(int(n)) + `","expires_in":` + strconv.FormatInt(ts.ttl, 10) + `}`))
	}))
	t.Cleanup(ts.srv.Close)
	return ts
}

func TestClientCredentialsFetcher(t *testing.T) {
	ts := newTokenServer(t, 3600)
	src, err := newTokenSource(credentialBundle{
		Method: methodClientCredentials, TokenURL: ts.srv.URL, Scope: graphScope,
		ClientID: "app-1", ClientSecret: "s3cr3t",
	}, http.DefaultClient, nil)
	if err != nil {
		t.Fatalf("newTokenSource: %v", err)
	}
	tok, err := src.Token(context.Background())
	if err != nil || tok == "" {
		t.Fatalf("Token = %q, %v", tok, err)
	}
	if ts.lastForm["grant_type"] != "client_credentials" || ts.lastForm["client_id"] != "app-1" ||
		ts.lastForm["client_secret"] != "s3cr3t" || ts.lastForm["scope"] != graphScope {
		t.Errorf("token form = %v, want the client-credentials grant", ts.lastForm)
	}
}

func TestRefreshTokenFetcher(t *testing.T) {
	ts := newTokenServer(t, 3600)
	src, err := newTokenSource(credentialBundle{
		Method: methodRefreshToken, TokenURL: ts.srv.URL, Scope: graphScope,
		ClientID: "c", ClientSecret: "s", RefreshToken: "r3fresh",
	}, http.DefaultClient, nil)
	if err != nil {
		t.Fatalf("newTokenSource: %v", err)
	}
	if _, err := src.Token(context.Background()); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if ts.lastForm["grant_type"] != "refresh_token" || ts.lastForm["refresh_token"] != "r3fresh" ||
		ts.lastForm["scope"] != graphScope {
		t.Errorf("token form = %v, want the refresh-token grant", ts.lastForm)
	}
}

// TestTokenCaching proves a token is reused within its lifetime and re-fetched once it
// nears expiry (driven by an injected clock, so no wall-clock dependency).
func TestTokenCaching(t *testing.T) {
	ts := newTokenServer(t, 3600) // 1h tokens
	nowT := time.Unix(1_000_000, 0)
	clock := func() time.Time { return nowT }
	src, err := newTokenSource(credentialBundle{
		Method: methodClientCredentials, TokenURL: ts.srv.URL, ClientID: "c", ClientSecret: "s",
	}, http.DefaultClient, clock)
	if err != nil {
		t.Fatalf("newTokenSource: %v", err)
	}
	first, _ := src.Token(context.Background())
	second, _ := src.Token(context.Background())
	if first != second || atomic.LoadInt32(&ts.calls) != 1 {
		t.Fatalf("within lifetime: calls=%d first=%q second=%q, want one fetch reused", ts.calls, first, second)
	}
	// Advance past (expiry - skew) → the next Token refreshes.
	nowT = nowT.Add(3600*time.Second - 30*time.Second)
	third, _ := src.Token(context.Background())
	if atomic.LoadInt32(&ts.calls) != 2 || third == first {
		t.Fatalf("after near-expiry: calls=%d third=%q, want a refresh", ts.calls, third)
	}
}

// TestTokenNoTTLNotCached proves a token with no expires_in is never reused.
func TestTokenNoTTLNotCached(t *testing.T) {
	ts := newTokenServer(t, 0) // no TTL
	src, _ := newTokenSource(credentialBundle{Method: methodClientCredentials, TokenURL: ts.srv.URL, ClientID: "c", ClientSecret: "s"}, http.DefaultClient, nil)
	_, _ = src.Token(context.Background())
	_, _ = src.Token(context.Background())
	if atomic.LoadInt32(&ts.calls) != 2 {
		t.Errorf("no-TTL token: calls=%d, want a fetch each time", ts.calls)
	}
}

func TestFetchTokenErrors(t *testing.T) {
	t.Run("non-2xx", func(t *testing.T) {
		ts := newTokenServer(t, 3600)
		ts.status = 401
		ts.body = `{"error":"invalid_client"}`
		src, _ := newTokenSource(credentialBundle{Method: methodClientCredentials, TokenURL: ts.srv.URL, ClientID: "c", ClientSecret: "bad"}, http.DefaultClient, nil)
		if _, err := src.Token(context.Background()); err == nil {
			t.Error("want an error for a 401 token endpoint")
		}
	})
	t.Run("no access_token", func(t *testing.T) {
		ts := newTokenServer(t, 3600)
		ts.body = `{"expires_in":3600}`
		src, _ := newTokenSource(credentialBundle{Method: methodRefreshToken, TokenURL: ts.srv.URL, ClientID: "c", ClientSecret: "s", RefreshToken: "r"}, http.DefaultClient, nil)
		if _, err := src.Token(context.Background()); err == nil {
			t.Error("want an error when no access_token is returned")
		}
	})
	t.Run("malformed json", func(t *testing.T) {
		ts := newTokenServer(t, 3600)
		ts.body = `not json`
		src, _ := newTokenSource(credentialBundle{Method: methodClientCredentials, TokenURL: ts.srv.URL, ClientID: "c", ClientSecret: "s"}, http.DefaultClient, nil)
		if _, err := src.Token(context.Background()); err == nil {
			t.Error("want an error for a malformed token response")
		}
	})
	t.Run("unreachable", func(t *testing.T) {
		src, _ := newTokenSource(credentialBundle{Method: methodClientCredentials, TokenURL: "http://127.0.0.1:0/token", ClientID: "c", ClientSecret: "s"}, http.DefaultClient, nil)
		if _, err := src.Token(context.Background()); err == nil {
			t.Error("want an error for an unreachable token endpoint")
		}
	})
	t.Run("build error", func(t *testing.T) {
		src, _ := newTokenSource(credentialBundle{Method: methodClientCredentials, TokenURL: "://not a url", ClientID: "c", ClientSecret: "s"}, http.DefaultClient, nil)
		if _, err := src.Token(context.Background()); err == nil {
			t.Error("malformed token URL: want a build error")
		}
	})
}

func TestNewTokenSourceValidation(t *testing.T) {
	cases := map[string]credentialBundle{
		"no token url":         {Method: methodClientCredentials, ClientID: "c", ClientSecret: "s"},
		"client-creds missing": {Method: methodClientCredentials, TokenURL: "http://x"},
		"refresh missing":      {Method: methodRefreshToken, TokenURL: "http://x", ClientID: "c"},
		"unknown method":       {Method: "magic", TokenURL: "http://x"},
	}
	for name, b := range cases {
		if _, err := newTokenSource(b, http.DefaultClient, nil); err == nil {
			t.Errorf("%s: want an error, got nil", name)
		}
	}
}
