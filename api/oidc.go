package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Atlas as an OpenID Connect relying party
// (ADR-0210).
//
// This file holds what talking to a provider needs — the configuration an
// operator sets, the discovery document, and the signing keys — and nothing about
// the login itself, which is oidclogin.go.
//
// Everything here is inert until an operator configures a provider. That is the
// promise the record makes first and the one an installation feels: with no issuer
// set, no route is mounted, no document is fetched, and the server is the server it
// was before. Atlas gains a dependency on somebody else's availability only when
// somebody decides it should.

const (
	// oidcDiscoveryPath is where an OpenID provider publishes what it is (OpenID
	// Connect Discovery 1.0). It is appended to the issuer, which is why the issuer
	// is configured without a trailing slash.
	oidcDiscoveryPath = "/.well-known/openid-configuration"

	// oidcDefaultScopes is what Atlas asks for: the identity, and enough profile to
	// put a name on the account it creates. Nothing else — a relying party that asks
	// for more than it uses is one an administrator has to justify at the provider.
	oidcDefaultScopes = "openid profile email"

	// oidcFetchTimeout bounds a call to the provider. A login that hangs is a login
	// that fails, and it should fail while the person is still looking at it.
	oidcFetchTimeout = 10 * time.Second

	// oidcDocumentTTL is how long the discovery document and the key set are reused.
	// Keys also refresh out of band when a token names one that is not held, which is
	// what makes a provider's rotation invisible here rather than an outage until
	// this expires.
	oidcDocumentTTL = time.Hour
)

// OIDCConfig is the identity provider an operator configured. An empty Issuer
// means there is none, which is the default.
type OIDCConfig struct {
	// Issuer is the provider's issuer URL, exactly as it appears in the tokens it
	// signs. It is both where discovery starts and what every token is checked
	// against, so a mismatch is a refusal rather than a warning.
	Issuer string

	// ClientID identifies Atlas at the provider, and is the audience every ID token
	// must name.
	ClientID string

	// ClientSecret authenticates the token exchange for a confidential client. Empty
	// is allowed: PKCE covers the flow either way, and a provider that registered
	// Atlas as a public client issues no secret.
	ClientSecret string

	// Scopes is the space-separated scope list, or empty for oidcDefaultScopes.
	Scopes string

	// Name is what the button on the login screen says. Empty falls back to the
	// issuer's host, which is a worse label than an operator would write and a
	// better one than "OIDC".
	Name string
}

// configured reports whether an operator gave this server a provider.
func (c OIDCConfig) configured() bool {
	return strings.TrimSpace(c.Issuer) != "" && strings.TrimSpace(c.ClientID) != ""
}

// label is what the login screen calls this provider.
func (c OIDCConfig) label() string {
	if n := strings.TrimSpace(c.Name); n != "" {
		return n
	}
	host := strings.TrimPrefix(strings.TrimPrefix(c.Issuer, "https://"), "http://")
	if i := strings.IndexAny(host, "/:"); i > 0 {
		host = host[:i]
	}
	if host == "" {
		return "single sign-on"
	}
	return host
}

// scopes is what to ask the provider for.
func (c OIDCConfig) scopes() string {
	if s := strings.TrimSpace(c.Scopes); s != "" {
		return s
	}
	return oidcDefaultScopes
}

// WithOIDC configures an OpenID Connect provider people may sign in with
// (ADR-0210).
//
// Off unless it is given: without it the routes are not mounted and the login
// screen offers nothing but the password form, which is the behaviour every
// installation has today.
func WithOIDC(cfg OIDCConfig) Option {
	return func(s *Server) {
		if !cfg.configured() {
			return
		}
		cfg.Issuer = strings.TrimRight(strings.TrimSpace(cfg.Issuer), "/")
		s.oidc = newOIDCProvider(cfg)
	}
}

// oidcDiscovery is the part of a provider's discovery document Atlas uses.
type oidcDiscovery struct {
	Issuer       string `json:"issuer"`
	AuthorizeURL string `json:"authorization_endpoint"`
	TokenURL     string `json:"token_endpoint"`
	JWKSURL      string `json:"jwks_uri"`
}

// oidcProvider is a configured provider plus what has been learned about it. The
// documents are cached because a login should cost one round trip to the provider,
// not three, and refreshed because a provider is entitled to change its keys.
type oidcProvider struct {
	cfg    OIDCConfig
	client *http.Client

	mu      sync.Mutex
	disco   oidcDiscovery
	discoAt time.Time
	keys    *jwkSet
	keysAt  time.Time
}

func newOIDCProvider(cfg OIDCConfig) *oidcProvider {
	return &oidcProvider{cfg: cfg, client: &http.Client{Timeout: oidcFetchTimeout}}
}

// getJSON fetches a document from the provider and decodes it.
func (p *oidcProvider) getJSON(ctx context.Context, url string, into any) error {
	body, err := p.get(ctx, url)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("oidc: %s is not JSON: %w", url, err)
	}
	return nil
}

// get fetches a document from the provider, bounded in time and in size.
func (p *oidcProvider) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("oidc: build request for %s: %w", url, err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc: fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc: %s answered %d", url, resp.StatusCode)
	}
	// A provider's documents are small; a body that is not is either a mistake or
	// somebody feeding this process a large file over a URL an operator configured.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("oidc: read %s: %w", url, err)
	}
	return body, nil
}

// endpoints returns the provider's discovery document, fetching it when what is
// held has expired.
//
// The issuer in the document must equal the one configured. That check is the
// defence against being pointed at a document describing somebody else: everything
// after it — where to send the person, where to exchange the code, whose keys to
// trust — comes out of this document, so believing the wrong one is believing the
// wrong provider entirely.
func (p *oidcProvider) endpoints(ctx context.Context, now time.Time) (oidcDiscovery, error) {
	p.mu.Lock()
	if !p.discoAt.IsZero() && now.Sub(p.discoAt) < oidcDocumentTTL {
		d := p.disco
		p.mu.Unlock()
		return d, nil
	}
	p.mu.Unlock()

	var d oidcDiscovery
	if err := p.getJSON(ctx, p.cfg.Issuer+oidcDiscoveryPath, &d); err != nil {
		return oidcDiscovery{}, err
	}
	if strings.TrimRight(d.Issuer, "/") != p.cfg.Issuer {
		return oidcDiscovery{}, fmt.Errorf("oidc: discovery document names issuer %q, want %q",
			d.Issuer, p.cfg.Issuer)
	}
	if d.AuthorizeURL == "" || d.TokenURL == "" || d.JWKSURL == "" {
		return oidcDiscovery{}, errors.New("oidc: discovery document is missing an endpoint")
	}
	p.mu.Lock()
	p.disco, p.discoAt = d, now
	p.mu.Unlock()
	return d, nil
}

// keySet returns the provider's signing keys. force refetches even when what is
// held is fresh, which is what a token naming an unknown key id asks for.
//
// The forced refetch is not rate limited, and that is a decision rather than an
// omission. Rotation is a thing providers do on their own schedule and without
// announcing it, so a cache that outlived a rotation would answer every login with
// "signature invalid" until it expired — an outage with no cause visible from
// either end. The refetch it costs is not a lever worth guarding: reaching this
// code at all requires a state this server minted, the cookie that matches it, and
// a code the *provider* accepted and issued a token for, and a token from the
// provider carries the provider's own key id. One refetch per verification is the
// bound, because the caller retries once and then gives up.
func (p *oidcProvider) keySet(ctx context.Context, now time.Time, force bool) (*jwkSet, error) {
	p.mu.Lock()
	if p.keys != nil && !force && now.Sub(p.keysAt) < oidcDocumentTTL {
		k := p.keys
		p.mu.Unlock()
		return k, nil
	}
	p.mu.Unlock()

	d, err := p.endpoints(ctx, now)
	if err != nil {
		return nil, err
	}
	body, err := p.get(ctx, d.JWKSURL)
	if err != nil {
		return nil, err
	}
	keys, err := parseJWKS(body)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.keys, p.keysAt = keys, now
	p.mu.Unlock()
	return keys, nil
}

// exchange trades an authorization code for the provider's token response.
//
// The redirect URI is the one the flow started with rather than one derived here:
// RFC 6749 requires the two to be identical, and a server that recomputes it is a
// server whose logins break the day it sits behind a different proxy.
func (p *oidcProvider) exchange(ctx context.Context, code, verifier, redirectURI string, now time.Time) (string, error) {
	d, err := p.endpoints(ctx, now)
	if err != nil {
		return "", err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
		"client_id":     {p.cfg.ClientID},
	}
	if p.cfg.ClientSecret != "" {
		form.Set("client_secret", p.cfg.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("oidc: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("oidc: token exchange: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("oidc: read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// The provider's own error is worth carrying into the audit line: "invalid_grant"
		// and "invalid_client" send an operator to entirely different places.
		return "", fmt.Errorf("oidc: token endpoint answered %d: %s", resp.StatusCode, tokenError(body))
	}
	var out struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("oidc: token response is not JSON: %w", err)
	}
	if out.IDToken == "" {
		return "", errors.New("oidc: token response carries no id_token")
	}
	return out.IDToken, nil
}

// tokenError pulls the OAuth error code out of a failed token response, or says
// the body carried none.
func tokenError(body []byte) string {
	var e struct {
		Error string `json:"error"`
		Desc  string `json:"error_description"`
	}
	if json.Unmarshal(body, &e) != nil || e.Error == "" {
		return "no error code"
	}
	if e.Desc != "" {
		return e.Error + " (" + e.Desc + ")"
	}
	return e.Error
}
