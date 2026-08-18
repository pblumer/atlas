package sharepoint

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

// TokenSource yields a valid OAuth2 bearer access token for the Graph API,
// acquiring and refreshing it as it nears expiry. Implementations are safe for
// concurrent use by the SharePoint worker.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// OAuth2 grant methods a SharePoint connector supports (ADR-0140). clientCredentials
// is app-only (a confidential client acts as itself, the norm for server workflows);
// refreshToken exchanges a pre-obtained refresh token (works for delegated /
// consumer scenarios). These mirror the native mail providers' grants (ADR-0093);
// the service-account (JWT-bearer) grant is Google-specific and omitted here.
const (
	methodClientCredentials = "clientCredentials"
	methodRefreshToken      = "refreshToken"
)

// credentialBundle is the JSON an operator stores in the vault under a SharePoint
// connector's credentialsRef (ADR-0140). method selects the OAuth2 grant; the
// remaining fields configure it. Non-secret fields (ids, tenant) and secret fields
// (clientSecret, refreshToken) live together in this one vault secret, so a model
// never carries any of them (I6). tokenUrl and scope are optional overrides; the
// connector supplies sensible Graph defaults (from tenantId).
type credentialBundle struct {
	Method       string `json:"method"`
	TokenURL     string `json:"tokenUrl,omitempty"`
	Scope        string `json:"scope,omitempty"`
	TenantID     string `json:"tenantId,omitempty"`
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
	RefreshToken string `json:"refreshToken,omitempty"`
}

// tokenExpirySkew is how long before a token's stated expiry the cache refreshes it,
// so a token never expires mid-flight between acquisition and use.
const tokenExpirySkew = 60 * time.Second

// fetcher is the uncached token-acquisition step of one grant; cachedTokenSource adds
// expiry-aware caching around it.
type fetcher interface {
	fetch(ctx context.Context) (token string, ttl time.Duration, err error)
}

// cachedTokenSource wraps a fetcher with expiry-aware caching, refreshing when the
// cached token is within tokenExpirySkew of expiry (or a fetch returned no TTL, which
// is never cached). now defaults to time.Now; tests inject a controllable clock. The
// mutex makes it safe for concurrent Token calls.
type cachedTokenSource struct {
	f   fetcher
	now func() time.Time
	mu  sync.Mutex
	tok string
	exp time.Time
}

func (c *cachedTokenSource) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *cachedTokenSource) Token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock()
	if c.tok != "" && now.Before(c.exp.Add(-tokenExpirySkew)) {
		return c.tok, nil
	}
	tok, ttl, err := c.f.fetch(ctx)
	if err != nil {
		return "", err
	}
	c.tok = tok
	// A response without an expires_in yields ttl 0, so the token is treated as
	// immediately stale (never cached) and the next call re-fetches.
	c.exp = now.Add(ttl)
	return tok, nil
}

// tokenResponse is the subset of an OAuth2 token endpoint's JSON we consume.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
}

// fetchToken posts an OAuth2 form grant to tokenURL and returns the access token and
// its lifetime. A non-2xx status or a missing access_token is an error, so a
// misconfigured credential fails the call (the job stays pending) rather than acting
// unauthenticated.
func fetchToken(ctx context.Context, httpc *http.Client, tokenURL string, form url.Values) (string, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("sharepoint: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := httpc.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("sharepoint: token request to %s: %w", tokenURL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("sharepoint: read token response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return "", 0, fmt.Errorf("sharepoint: token endpoint %s returned HTTP %d: %s", tokenURL, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var tr tokenResponse
	if err := json.Unmarshal(raw, &tr); err != nil {
		return "", 0, fmt.Errorf("sharepoint: decode token response: %w", err)
	}
	if tr.AccessToken == "" {
		return "", 0, fmt.Errorf("sharepoint: token endpoint %s returned no access_token", tokenURL)
	}
	return tr.AccessToken, time.Duration(tr.ExpiresIn) * time.Second, nil
}

// clientCredentialsFetcher performs the OAuth2 client-credentials grant (app-only).
type clientCredentialsFetcher struct {
	httpc        *http.Client
	tokenURL     string
	clientID     string
	clientSecret string
	scope        string
}

func (f *clientCredentialsFetcher) fetch(ctx context.Context) (string, time.Duration, error) {
	return fetchToken(ctx, f.httpc, f.tokenURL, url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {f.clientID},
		"client_secret": {f.clientSecret},
		"scope":         {f.scope},
	})
}

// refreshTokenFetcher exchanges a pre-obtained refresh token for an access token.
type refreshTokenFetcher struct {
	httpc        *http.Client
	tokenURL     string
	clientID     string
	clientSecret string
	refreshToken string
	scope        string
}

func (f *refreshTokenFetcher) fetch(ctx context.Context) (string, time.Duration, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {f.clientID},
		"client_secret": {f.clientSecret},
		"refresh_token": {f.refreshToken},
	}
	if f.scope != "" {
		form.Set("scope", f.scope)
	}
	return fetchToken(ctx, f.httpc, f.tokenURL, form)
}

// newTokenSource builds a cached TokenSource for a fully-resolved credential bundle
// (the connector has already filled in tokenUrl/scope defaults). It validates the
// fields the chosen method requires, so a misconfigured bundle fails here rather than
// at call time. httpc and now are injected so the token flow is testable without a
// live OAuth endpoint.
func newTokenSource(b credentialBundle, httpc *http.Client, now func() time.Time) (TokenSource, error) {
	if strings.TrimSpace(b.TokenURL) == "" {
		return nil, fmt.Errorf("sharepoint: credential has no token URL (set tokenUrl or tenantId)")
	}
	var f fetcher
	switch b.Method {
	case methodClientCredentials:
		if b.ClientID == "" || b.ClientSecret == "" {
			return nil, fmt.Errorf("sharepoint: clientCredentials needs clientId and clientSecret")
		}
		f = &clientCredentialsFetcher{httpc: httpc, tokenURL: b.TokenURL, clientID: b.ClientID, clientSecret: b.ClientSecret, scope: b.Scope}
	case methodRefreshToken:
		if b.ClientID == "" || b.ClientSecret == "" || b.RefreshToken == "" {
			return nil, fmt.Errorf("sharepoint: refreshToken needs clientId, clientSecret and refreshToken")
		}
		f = &refreshTokenFetcher{httpc: httpc, tokenURL: b.TokenURL, clientID: b.ClientID, clientSecret: b.ClientSecret, refreshToken: b.RefreshToken, scope: b.Scope}
	default:
		return nil, fmt.Errorf("sharepoint: unknown auth method %q (want %q or %q)", b.Method, methodClientCredentials, methodRefreshToken)
	}
	return &cachedTokenSource{f: f, now: now}, nil
}
