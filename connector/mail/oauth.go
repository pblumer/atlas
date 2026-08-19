package mail

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// TokenSource yields a valid OAuth2 bearer access token for a provider API,
// acquiring and refreshing it as it nears expiry. Implementations are safe for
// concurrent use by the mail worker.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// OAuth2 grant methods a native mail provider supports (ADR-0093). clientCredentials
// is app-only (a confidential client sends as a fixed mailbox); refreshToken exchanges
// a pre-obtained refresh token (works with consumer accounts); serviceAccount is a
// Google service account with domain-wide delegation (a signed JWT-bearer assertion,
// Workspace only).
const (
	methodClientCredentials = "clientCredentials"
	methodRefreshToken      = "refreshToken"
	methodServiceAccount    = "serviceAccount"
)

// credentialBundle is the JSON an operator stores in the vault under a mail
// connector's credentialsRef (ADR-0093). method selects the OAuth2 grant; the
// remaining fields configure it. Non-secret fields (ids, tenant) and secret fields
// (clientSecret, refreshToken, privateKey) live together in this one vault secret, so
// a model never carries any of them (I6). tokenUrl and scope are optional overrides;
// the provider supplies sensible defaults.
type credentialBundle struct {
	Method       string `json:"method"`
	TokenURL     string `json:"tokenUrl,omitempty"`
	Scope        string `json:"scope,omitempty"`
	TenantID     string `json:"tenantId,omitempty"`
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ClientEmail  string `json:"clientEmail,omitempty"`
	PrivateKey   string `json:"privateKey,omitempty"`
	Subject      string `json:"subject,omitempty"`
}

// tokenExpirySkew is how long before a token's stated expiry the cache refreshes it,
// so a token never expires mid-flight between acquisition and use.
const tokenExpirySkew = 60 * time.Second

// fetcher is the uncached token-acquisition step of one grant; cachedTokenSource
// adds expiry-aware caching around it.
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
	// A response without an expires_in is treated as immediately stale (never cached),
	// so the next send re-fetches rather than reusing a token of unknown lifetime.
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
// misconfigured credential fails the send (the job stays pending) rather than sending
// unauthenticated.
func fetchToken(ctx context.Context, httpc *http.Client, tokenURL string, form url.Values) (string, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("mail: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := httpc.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("mail: token request to %s: %w", tokenURL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("mail: read token response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return "", 0, fmt.Errorf("mail: token endpoint %s returned HTTP %d: %s", tokenURL, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var tr tokenResponse
	if err := json.Unmarshal(raw, &tr); err != nil {
		return "", 0, fmt.Errorf("mail: decode token response: %w", err)
	}
	if tr.AccessToken == "" {
		return "", 0, fmt.Errorf("mail: token endpoint %s returned no access_token", tokenURL)
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

// serviceAccountFetcher signs a JWT-bearer assertion with a Google service account's
// private key and exchanges it for an access token (domain-wide delegation). subject
// is the mailbox the service account impersonates.
type serviceAccountFetcher struct {
	httpc       *http.Client
	tokenURL    string
	clientEmail string
	privateKey  *rsa.PrivateKey
	scope       string
	subject     string
	now         func() time.Time
}

func (f *serviceAccountFetcher) clock() time.Time {
	if f.now != nil {
		return f.now()
	}
	return time.Now()
}

func (f *serviceAccountFetcher) fetch(ctx context.Context) (string, time.Duration, error) {
	assertion, err := f.signAssertion()
	if err != nil {
		return "", 0, err
	}
	return fetchToken(ctx, f.httpc, f.tokenURL, url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	})
}

// signAssertion builds and RS256-signs the JWT-bearer assertion a Google service
// account presents: iss is the SA email, sub the impersonated user, aud the token
// endpoint, with a one-hour validity window.
func (f *serviceAccountFetcher) signAssertion() (string, error) {
	now := f.clock()
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claims := map[string]any{
		"iss":   f.clientEmail,
		"scope": f.scope,
		"aud":   f.tokenURL,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
	if f.subject != "" {
		claims["sub"] = f.subject
	}
	hb, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("mail: encode JWT header: %w", err)
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("mail: encode JWT claims: %w", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("mail: sign JWT assertion: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// parseRSAPrivateKey decodes a PEM-encoded RSA private key (PKCS#8, as Google service
// account JSON carries, or PKCS#1) for JWT signing.
func parseRSAPrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("mail: service-account private key is not valid PEM")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("mail: service-account private key is not an RSA key")
		}
		return rk, nil
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	return nil, fmt.Errorf("mail: cannot parse service-account private key (want PKCS#8 or PKCS#1 RSA)")
}

// newTokenSource builds a cached TokenSource for a fully-resolved credential bundle
// (the provider has already filled in tokenUrl/scope defaults and, for a service
// account, the impersonation subject). It validates the fields the chosen method
// requires, so a misconfigured bundle fails here rather than at send time. httpc and
// now are injected so the token flow is testable without a live OAuth endpoint.
func newTokenSource(b credentialBundle, httpc *http.Client, now func() time.Time) (TokenSource, error) {
	if strings.TrimSpace(b.TokenURL) == "" {
		return nil, fmt.Errorf("mail: credential has no token URL")
	}
	var f fetcher
	switch b.Method {
	case methodClientCredentials:
		if b.ClientID == "" || b.ClientSecret == "" {
			return nil, fmt.Errorf("mail: clientCredentials needs clientId and clientSecret")
		}
		f = &clientCredentialsFetcher{httpc: httpc, tokenURL: b.TokenURL, clientID: b.ClientID, clientSecret: b.ClientSecret, scope: b.Scope}
	case methodRefreshToken:
		if b.ClientID == "" || b.ClientSecret == "" || b.RefreshToken == "" {
			return nil, fmt.Errorf("mail: refreshToken needs clientId, clientSecret and refreshToken")
		}
		f = &refreshTokenFetcher{httpc: httpc, tokenURL: b.TokenURL, clientID: b.ClientID, clientSecret: b.ClientSecret, refreshToken: b.RefreshToken, scope: b.Scope}
	case methodServiceAccount:
		if b.ClientEmail == "" || b.PrivateKey == "" {
			return nil, fmt.Errorf("mail: serviceAccount needs clientEmail and privateKey")
		}
		key, err := parseRSAPrivateKey(b.PrivateKey)
		if err != nil {
			return nil, err
		}
		f = &serviceAccountFetcher{httpc: httpc, tokenURL: b.TokenURL, clientEmail: b.ClientEmail, privateKey: key, scope: b.Scope, subject: b.Subject, now: now}
	default:
		return nil, fmt.Errorf("mail: unknown auth method %q (want %q, %q, or %q)", b.Method, methodClientCredentials, methodRefreshToken, methodServiceAccount)
	}
	return &cachedTokenSource{f: f, now: now}, nil
}
