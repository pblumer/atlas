package rest

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

	"github.com/pblumer/atlas/connector/nettimeout"
)

// OAuthConfig identifies one OAuth2 client-credentials token request (ADR-0152).
// TokenURL is the token endpoint; ClientID/ClientSecret authenticate the client;
// Scope is the optional space-delimited scope list. ClientSecret is the resolved
// value (from a secret reference, ADR-0041), never a reference here.
type OAuthConfig struct {
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scope        string
}

// TokenProvider fetches an OAuth2 access token for a client-credentials grant. It
// is an interface so the worker is testable without a live token endpoint.
type TokenProvider interface {
	Token(ctx context.Context, cfg OAuthConfig) (string, error)
}

// tokenProvider fetches client-credentials tokens over HTTP and caches each by
// (tokenURL, clientID, scope) until shortly before it expires, so a run of jobs
// against one API reuses one token rather than calling the token endpoint for every
// job. REST connector jobs are driven serially on the run-loop goroutine, so the
// cache needs no concurrency, but the mutex keeps it correct if that ever changes.
type tokenProvider struct {
	http *http.Client
	mu   sync.Mutex
	// cache maps a config key to a fetched token and the instant it should no longer
	// be served (expiry minus a safety skew).
	cache map[string]cachedToken
	now   func() time.Time
}

type cachedToken struct {
	token   string
	goodTil time.Time
}

// NewTokenProvider builds a token provider bounded by the shared connector call
// budget (nettimeout.HTTPClient), like the REST client itself.
func NewTokenProvider() *tokenProvider {
	return &tokenProvider{http: nettimeout.HTTPClient(), cache: map[string]cachedToken{}, now: time.Now}
}

// tokenSkew is subtracted from a token's lifetime before it is cached, so a token
// is refreshed slightly early rather than used in the moment it expires.
const tokenSkew = 30 * time.Second

// Token returns a valid access token for cfg, serving a cached one when it has not
// yet reached its refresh point and otherwise fetching a fresh one. A non-2xx token
// response or a body without an access_token is an error, so the job fails (retry,
// then an incident) rather than calling the API with no credential.
func (p *tokenProvider) Token(ctx context.Context, cfg OAuthConfig) (string, error) {
	key := cfg.TokenURL + "\x00" + cfg.ClientID + "\x00" + cfg.Scope
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.cache[key]; ok && p.now().Before(c.goodTil) {
		return c.token, nil
	}
	tok, ttl, err := p.fetch(ctx, cfg)
	if err != nil {
		return "", err
	}
	goodTil := p.now().Add(ttl - tokenSkew)
	if !goodTil.After(p.now()) { // a very short-lived token: don't cache a stale entry
		return tok, nil
	}
	p.cache[key] = cachedToken{token: tok, goodTil: goodTil}
	return tok, nil
}

// defaultTokenTTL is the lifetime assumed when a token response omits expires_in.
const defaultTokenTTL = 5 * time.Minute

// fetch performs the client-credentials POST: an application/x-www-form-urlencoded
// body carrying grant_type and scope, with the client authenticated via HTTP Basic
// (RFC 6749 §2.3.1). It returns the access token and its lifetime.
func (p *tokenProvider) fetch(ctx context.Context, cfg OAuthConfig) (string, time.Duration, error) {
	form := url.Values{"grant_type": {"client_credentials"}}
	if cfg.Scope != "" {
		form.Set("scope", cfg.Scope)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("rest: build oauth2 token request: %w", err)
	}
	req.SetBasicAuth(cfg.ClientID, cfg.ClientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("rest: oauth2 token request to %s: %w", cfg.TokenURL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("rest: read oauth2 token response from %s: %w", cfg.TokenURL, err)
	}
	if resp.StatusCode/100 != 2 {
		return "", 0, fmt.Errorf("rest: oauth2 token endpoint %s returned HTTP %d", cfg.TokenURL, resp.StatusCode)
	}
	var body struct {
		AccessToken string      `json:"access_token"`
		ExpiresIn   json.Number `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", 0, fmt.Errorf("rest: decode oauth2 token response from %s: %w", cfg.TokenURL, err)
	}
	if body.AccessToken == "" {
		return "", 0, fmt.Errorf("rest: oauth2 token response from %s has no access_token", cfg.TokenURL)
	}
	ttl := defaultTokenTTL
	if secs, err := body.ExpiresIn.Int64(); err == nil && secs > 0 {
		ttl = time.Duration(secs) * time.Second
	}
	return body.AccessToken, ttl, nil
}
