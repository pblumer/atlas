// Package oauth2 is the OAuth2 token machinery every Atlas connector that speaks to
// a token-protected API shares: an expiry-aware cache, the token-endpoint exchange,
// and the two grants that are not specific to any one vendor.
//
// It exists because three connectors needed the same hundred lines. The mail
// connector grew them first (ADR-0093), the SharePoint connector copied them
// (ADR-0141), and a third copy for Entra ID (ADR-0172) would have made a token
// caching or refresh bug something to fix in three places and remember in a fourth.
//
// # What is shared, and what is not
//
// Shared is the *mechanism*: when to refresh, how to post a form grant, how to read
// a token response, and the client-credentials and refresh-token grants themselves.
//
// Not shared is *policy*: which grants a connector accepts, what its credential
// bundle looks like, and what its endpoint defaults are. Those are per-connector
// decisions and they stay in the connector — which is also why a caller supplies its
// own [Fetcher] for a grant nobody else has (the mail connector's Google
// service-account JWT-bearer assertion is the one such case).
//
// Every error is prefixed with the caller's kind, so a message an operator reads
// still names the connector that produced it.
package oauth2

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// TokenSource yields a valid OAuth2 bearer access token, acquiring and refreshing it
// as it nears expiry. Implementations are safe for concurrent use by a worker.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// Fetcher is the uncached token-acquisition step of one grant. [NewCached] adds
// expiry-aware caching around it. Implement it to add a grant this package does not
// carry, and use [PostForm] so the response handling stays identical.
type Fetcher interface {
	Fetch(ctx context.Context) (token string, ttl time.Duration, err error)
}

// ExpirySkew is how long before a token's stated expiry the cache refreshes it, so a
// token never expires mid-flight between acquisition and use.
const ExpirySkew = 60 * time.Second

// cached wraps a Fetcher with expiry-aware caching.
type cached struct {
	f   Fetcher
	now func() time.Time
	mu  sync.Mutex
	tok string
	exp time.Time
}

// NewCached wraps a fetcher in an expiry-aware cache, refreshing when the held token
// is within [ExpirySkew] of expiry. now defaults to time.Now; tests inject a
// controllable clock. The returned source is safe for concurrent use.
func NewCached(f Fetcher, now func() time.Time) TokenSource { return &cached{f: f, now: now} }

func (c *cached) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *cached) Token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock()
	if c.tok != "" && now.Before(c.exp.Add(-ExpirySkew)) {
		return c.tok, nil
	}
	tok, ttl, err := c.f.Fetch(ctx)
	if err != nil {
		return "", err
	}
	c.tok = tok
	// A response without an expires_in yields ttl 0, so the token is treated as
	// immediately stale (never cached) and the next call re-fetches.
	c.exp = now.Add(ttl)
	return tok, nil
}

// tokenResponse is the subset of an OAuth2 token endpoint's JSON this package reads.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
}

// PostForm posts an OAuth2 form grant to tokenURL and returns the access token and
// its lifetime. kind prefixes every error with the calling connector's name.
//
// A non-2xx status or a missing access_token is an error, so a misconfigured
// credential fails the call — leaving the job pending for a retry — rather than
// letting the connector proceed unauthenticated.
func PostForm(ctx context.Context, httpc *http.Client, kind, tokenURL string, form url.Values) (string, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("%s: build token request: %w", kind, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := httpc.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("%s: token request to %s: %w", kind, tokenURL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("%s: read token response: %w", kind, err)
	}
	if resp.StatusCode/100 != 2 {
		return "", 0, fmt.Errorf("%s: token endpoint %s returned HTTP %d: %s", kind, tokenURL, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var tr tokenResponse
	if err := json.Unmarshal(raw, &tr); err != nil {
		return "", 0, fmt.Errorf("%s: decode token response: %w", kind, err)
	}
	if tr.AccessToken == "" {
		return "", 0, fmt.Errorf("%s: token endpoint %s returned no access_token", kind, tokenURL)
	}
	return tr.AccessToken, time.Duration(tr.ExpiresIn) * time.Second, nil
}

// Config is what the two shared grants need. Scope is optional for the refresh-token
// grant and required in practice for client credentials.
type Config struct {
	HTTPClient   *http.Client
	Kind         string
	TokenURL     string
	ClientID     string
	ClientSecret string
	RefreshToken string
	Scope        string
}

// ClientCredentials is the app-only grant: a confidential client acting as itself,
// which is the normal shape for a server-side workflow.
func ClientCredentials(c Config) Fetcher { return &ccFetcher{c: c} }

type ccFetcher struct{ c Config }

func (f *ccFetcher) Fetch(ctx context.Context) (string, time.Duration, error) {
	return PostForm(ctx, f.c.HTTPClient, f.c.Kind, f.c.TokenURL, url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {f.c.ClientID},
		"client_secret": {f.c.ClientSecret},
		"scope":         {f.c.Scope},
	})
}

// RefreshToken exchanges a pre-obtained refresh token for an access token, which is
// how a delegated or consumer-account credential is used without a browser.
func RefreshToken(c Config) Fetcher { return &rtFetcher{c: c} }

type rtFetcher struct{ c Config }

func (f *rtFetcher) Fetch(ctx context.Context) (string, time.Duration, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {f.c.ClientID},
		"client_secret": {f.c.ClientSecret},
		"refresh_token": {f.c.RefreshToken},
	}
	if f.c.Scope != "" {
		form.Set("scope", f.c.Scope)
	}
	return PostForm(ctx, f.c.HTTPClient, f.c.Kind, f.c.TokenURL, form)
}
