package oauth2

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// tokenServer is a stand-in OAuth2 token endpoint that records the last form it was
// posted and counts how many times it was asked, so caching is observable.
type tokenServer struct {
	srv      *httptest.Server
	lastForm map[string]string
	calls    atomic.Int64
	status   int
	body     string
	ttl      int64
}

func newTokenServer(t *testing.T, ttl int64) *tokenServer {
	t.Helper()
	ts := &tokenServer{ttl: ttl, lastForm: map[string]string{}}
	ts.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := ts.calls.Add(1)
		_ = r.ParseForm()
		for k := range r.PostForm {
			ts.lastForm[k] = r.PostForm.Get(k)
		}
		if ts.status != 0 {
			w.WriteHeader(ts.status)
		}
		if ts.body != "" {
			_, _ = w.Write([]byte(ts.body))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"tok-` + strconv.Itoa(int(n)) + `","expires_in":` + strconv.FormatInt(ts.ttl, 10) + `}`))
	}))
	t.Cleanup(ts.srv.Close)
	return ts
}

func (ts *tokenServer) cfg(kind string) Config {
	return Config{HTTPClient: http.DefaultClient, Kind: kind, TokenURL: ts.srv.URL}
}

func TestClientCredentialsGrant(t *testing.T) {
	ts := newTokenServer(t, 3600)
	c := ts.cfg("entra")
	c.ClientID, c.ClientSecret, c.Scope = "app-1", "s3cr3t", "https://graph.microsoft.com/.default"

	tok, ttl, err := ClientCredentials(c).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if tok != "tok-1" || ttl != time.Hour {
		t.Errorf("token/ttl = %q/%v", tok, ttl)
	}
	if ts.lastForm["grant_type"] != "client_credentials" || ts.lastForm["client_id"] != "app-1" ||
		ts.lastForm["client_secret"] != "s3cr3t" || ts.lastForm["scope"] != c.Scope {
		t.Errorf("form = %v, want the client-credentials grant", ts.lastForm)
	}
}

func TestRefreshTokenGrant(t *testing.T) {
	ts := newTokenServer(t, 3600)
	c := ts.cfg("mail")
	c.ClientID, c.ClientSecret, c.RefreshToken = "c", "s", "r3fresh"

	if _, _, err := RefreshToken(c).Fetch(context.Background()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if ts.lastForm["grant_type"] != "refresh_token" || ts.lastForm["refresh_token"] != "r3fresh" {
		t.Errorf("form = %v, want the refresh-token grant", ts.lastForm)
	}
	// Scope is optional for this grant, and an unset one must not be sent as empty.
	if _, ok := ts.lastForm["scope"]; ok {
		t.Errorf("an unset scope should not be sent, form = %v", ts.lastForm)
	}

	ts2 := newTokenServer(t, 3600)
	c2 := ts2.cfg("mail")
	c2.ClientID, c2.ClientSecret, c2.RefreshToken, c2.Scope = "c", "s", "r", "Mail.Send"
	if _, _, err := RefreshToken(c2).Fetch(context.Background()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if ts2.lastForm["scope"] != "Mail.Send" {
		t.Errorf("a set scope should be sent, form = %v", ts2.lastForm)
	}
}

// A token is reused until it nears expiry, and refreshed once it does — the whole
// reason this cache exists rather than a token per call.
func TestCachingRefreshesNearExpiry(t *testing.T) {
	ts := newTokenServer(t, 3600)
	c := ts.cfg("entra")
	c.ClientID, c.ClientSecret = "c", "s"

	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	src := NewCached(ClientCredentials(c), func() time.Time { return now })

	first, err := src.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	again, err := src.Token(context.Background())
	if err != nil || again != first {
		t.Fatalf("a cached token should be reused: %q vs %q (%v)", again, first, err)
	}
	if got := ts.calls.Load(); got != 1 {
		t.Errorf("token endpoint called %d times, want 1", got)
	}

	// Move to inside the skew window: the token is still technically valid, and is
	// refreshed anyway so it cannot expire mid-flight.
	now = now.Add(time.Hour - ExpirySkew/2)
	third, err := src.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if third == first {
		t.Error("a token inside the expiry skew should have been refreshed")
	}
	if got := ts.calls.Load(); got != 2 {
		t.Errorf("token endpoint called %d times, want 2", got)
	}
}

// A response with no expires_in is never cached: pretending it lasts forever would
// wedge the worker on the first expiry.
func TestNoTTLIsNeverCached(t *testing.T) {
	ts := newTokenServer(t, 0)
	ts.body = `{"access_token":"tok"}`
	c := ts.cfg("entra")
	c.ClientID, c.ClientSecret = "c", "s"
	src := NewCached(ClientCredentials(c), nil)

	for i := 0; i < 3; i++ {
		if _, err := src.Token(context.Background()); err != nil {
			t.Fatalf("Token: %v", err)
		}
	}
	if got := ts.calls.Load(); got != 3 {
		t.Errorf("token endpoint called %d times, want 3 (never cached)", got)
	}
}

// The default clock is used when none is injected.
func TestCachingWithTheDefaultClock(t *testing.T) {
	ts := newTokenServer(t, 3600)
	c := ts.cfg("entra")
	c.ClientID, c.ClientSecret = "c", "s"
	src := NewCached(ClientCredentials(c), nil)
	a, err := src.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	b, err := src.Token(context.Background())
	if err != nil || a != b {
		t.Errorf("token = %q then %q (%v), want the cached one", a, b, err)
	}
}

// A misconfigured credential must fail the call rather than let the worker act
// unauthenticated, so every unhappy answer from the endpoint is an error.
func TestPostFormErrors(t *testing.T) {
	base := func(t *testing.T) Config {
		ts := newTokenServer(t, 3600)
		c := ts.cfg("entra")
		c.ClientID, c.ClientSecret = "c", "s"
		return c
	}
	t.Run("non-2xx names the status and body", func(t *testing.T) {
		ts := newTokenServer(t, 3600)
		ts.status, ts.body = 401, `{"error":"invalid_client"}`
		c := ts.cfg("entra")
		c.ClientID, c.ClientSecret = "c", "bad"
		_, _, err := ClientCredentials(c).Fetch(context.Background())
		if err == nil {
			t.Fatal("a 401 must be an error")
		}
		if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "invalid_client") {
			t.Errorf("error should carry the status and the body: %v", err)
		}
	})
	t.Run("no access_token", func(t *testing.T) {
		ts := newTokenServer(t, 3600)
		ts.body = `{"expires_in":3600}`
		c := ts.cfg("entra")
		c.ClientID, c.ClientSecret = "c", "s"
		if _, _, err := ClientCredentials(c).Fetch(context.Background()); err == nil {
			t.Error("a response with no access_token must be an error")
		}
	})
	t.Run("malformed JSON", func(t *testing.T) {
		ts := newTokenServer(t, 3600)
		ts.body = `{not json`
		c := ts.cfg("entra")
		c.ClientID, c.ClientSecret = "c", "s"
		if _, _, err := ClientCredentials(c).Fetch(context.Background()); err == nil {
			t.Error("a malformed response must be an error")
		}
	})
	t.Run("unreachable endpoint", func(t *testing.T) {
		c := base(t)
		c.TokenURL = "http://127.0.0.1:1/token"
		if _, _, err := ClientCredentials(c).Fetch(context.Background()); err == nil {
			t.Error("an unreachable endpoint must be an error")
		}
	})
	t.Run("unbuildable request", func(t *testing.T) {
		c := base(t)
		c.TokenURL = "://not a url"
		if _, _, err := ClientCredentials(c).Fetch(context.Background()); err == nil {
			t.Error("an unparsable token URL must be an error")
		}
	})
	t.Run("body that cannot be read", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "50")
			_, _ = w.Write([]byte("short"))
			// Closing early makes the advertised length a lie, so the read fails.
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			srvHijackClose(w)
		}))
		t.Cleanup(srv.Close)
		_, _, err := PostForm(context.Background(), http.DefaultClient, "entra", srv.URL, url.Values{})
		if err == nil {
			t.Error("a truncated body must be an error")
		}
	})
}

// srvHijackClose drops the connection mid-response so the client sees a short read.
func srvHijackClose(w http.ResponseWriter) {
	h, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	conn, _, err := h.Hijack()
	if err == nil {
		_ = conn.Close()
	}
}

// The kind prefixes every message, so an operator reading a log knows which
// worker produced it even though the code is shared.
func TestErrorsNameTheCallingConnector(t *testing.T) {
	ts := newTokenServer(t, 3600)
	ts.status, ts.body = 500, "boom"
	for _, kind := range []string{"mail", "sharepoint", "entra"} {
		c := ts.cfg(kind)
		c.ClientID, c.ClientSecret = "c", "s"
		_, _, err := ClientCredentials(c).Fetch(context.Background())
		if err == nil || !strings.HasPrefix(err.Error(), kind+":") {
			t.Errorf("kind %q: error = %v, want it prefixed with the kind", kind, err)
		}
	}
}

// A fetcher that fails leaves no token cached, so the next call retries rather than
// serving an empty bearer.
func TestCacheDoesNotHoldAFailedFetch(t *testing.T) {
	var calls int
	src := NewCached(fetcherFunc(func(context.Context) (string, time.Duration, error) {
		calls++
		return "", 0, errors.New("nope")
	}), nil)
	for i := 0; i < 2; i++ {
		if _, err := src.Token(context.Background()); err == nil {
			t.Fatal("a failing fetch must surface")
		}
	}
	if calls != 2 {
		t.Errorf("fetch called %d times, want 2", calls)
	}
}

type fetcherFunc func(context.Context) (string, time.Duration, error)

func (f fetcherFunc) Fetch(ctx context.Context) (string, time.Duration, error) { return f(ctx) }
