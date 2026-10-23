package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/identity"
	"github.com/pblumer/atlas/logging"
)

// Linking a Microsoft account to an Atlas user and authorizing a delegated connection
// (ADR-0475 §4).
//
// It is the login flow of ADR-0210 run for a different purpose and held to the same
// checks — Authorization Code with PKCE, a single-use state, a nonce, and the ID token
// verified against the tenant's keys before any claim is believed — plus the ones a
// link needs that a login does not: the person must already be signed in to Atlas, the
// reply must come back to the session that started the flow (otherwise an attacker
// could link their own Microsoft account to a victim's Atlas user), the token must name
// the configured tenant and an object id, and a refresh token must come back, because a
// connection without one cannot act.
//
// The person signs in at Microsoft in their own browser, so the tenant's consent,
// Conditional Access and MFA apply as they do to any sign-in. Atlas never sees a
// password.

const (
	linkMicrosoftStartPath    = "/auth/link/microsoft/start"
	linkMicrosoftCallbackPath = "/auth/link/microsoft/callback"
	linkStateCookie           = "atlas_link"
	linkWindow                = 10 * time.Minute
	linkMaxPending            = 1024
	// linkDonePage is where the browser lands afterwards, with ?linked= or ?link=failed.
	linkDonePage = "/#/console/connections"
)

// MicrosoftLinkConfig is the app registration people link their Microsoft accounts
// through. It is a confidential client of one tenant: the issuer of every token is
// that tenant's, so a token from another tenant fails the issuer check before its tid
// is read.
type MicrosoftLinkConfig struct {
	TenantID     string
	ClientID     string
	ClientSecret string
	// Scopes are the delegated resource scopes the connection is for, space-separated
	// ("https://graph.microsoft.com/Sites.ReadWrite.All"). openid, profile and
	// offline_access are always added.
	Scopes string
	// Authority is the identity platform's base, "https://login.microsoftonline.com"
	// when empty; a national cloud, or a test server, names its own.
	Authority string
}

func (c MicrosoftLinkConfig) configured() bool {
	return strings.TrimSpace(c.TenantID) != "" && strings.TrimSpace(c.ClientID) != "" && strings.TrimSpace(c.Scopes) != ""
}

func (c MicrosoftLinkConfig) issuer() string {
	authority := strings.TrimRight(strings.TrimSpace(c.Authority), "/")
	if authority == "" {
		authority = "https://login.microsoftonline.com"
	}
	return authority + "/" + strings.TrimSpace(c.TenantID) + "/v2.0"
}

func (c MicrosoftLinkConfig) scopes() string {
	return "openid profile offline_access " + strings.TrimSpace(c.Scopes)
}

// microsoftLink is the configured flow and its links in flight.
type microsoftLink struct {
	cfg      MicrosoftLinkConfig
	provider *oidcProvider
	mu       sync.Mutex
	pending  map[string]linkPending
}

// linkPending is one link in flight: what the callback needs, and whose it is.
type linkPending struct {
	userID      string
	nonce       string
	verifier    string
	redirectURI string
	expires     time.Time
}

// WithMicrosoftLinking lets signed-in users link Microsoft accounts and authorize
// delegated connections through cfg's app registration. Off unless it is given.
func WithMicrosoftLinking(cfg MicrosoftLinkConfig) Option {
	return func(s *Server) {
		if !cfg.configured() {
			return
		}
		s.msLink = &microsoftLink{
			cfg: cfg,
			provider: newOIDCProvider(OIDCConfig{Issuer: cfg.issuer(), ClientID: strings.TrimSpace(cfg.ClientID),
				ClientSecret: cfg.ClientSecret, Scopes: cfg.scopes()}, s.budgets()),
			pending: map[string]linkPending{},
		}
	}
}

func (l *microsoftLink) begin(state string, p linkPending, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, v := range l.pending {
		if now.After(v.expires) {
			delete(l.pending, k)
		}
	}
	for len(l.pending) >= linkMaxPending {
		oldest, at := "", time.Time{}
		for k, v := range l.pending {
			if at.IsZero() || v.expires.Before(at) {
				oldest, at = k, v.expires
			}
		}
		delete(l.pending, oldest)
	}
	l.pending[state] = p
}

func (l *microsoftLink) spend(state string, now time.Time) (linkPending, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p, ok := l.pending[state]
	if !ok || state == "" {
		return linkPending{}, false
	}
	delete(l.pending, state)
	return p, !now.After(p.expires)
}

// handleLinkMicrosoftStart sends a signed-in person to Microsoft to link an account.
func (s *Server) handleLinkMicrosoftStart(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	if s.msLink == nil || s.identity == nil {
		httpapi.Error(w, http.StatusNotFound, "linking a Microsoft account is not configured on this server")
		return
	}
	if p == nil || !strings.HasPrefix(p.UserID, "usr_") {
		httpapi.Error(w, http.StatusForbidden, "only a person signed in to Atlas can link an account")
		return
	}
	now := time.Now()
	d, err := s.msLink.provider.endpoints(r.Context(), now)
	if err != nil {
		s.linkRefuse(w, r, "provider discovery failed", err)
		return
	}
	state, err1 := randomHex(16)
	nonce, err2 := randomHex(16)
	verifier, err3 := randomHex(32)
	base := s.externalBase(r)
	if err := errors.Join(err1, err2, err3); err != nil || base == "" {
		s.linkRefuse(w, r, "could not start the flow", err)
		return
	}
	redirectURI := base + linkMicrosoftCallbackPath
	s.msLink.begin(state, linkPending{userID: p.UserID, nonce: nonce, verifier: verifier,
		redirectURI: redirectURI, expires: now.Add(linkWindow)}, now)
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {s.msLink.provider.cfg.ClientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {s.msLink.cfg.scopes()},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		// Always ask: linking is a deliberate act, and silently reusing whichever
		// account the browser is signed in to would link the wrong one.
		"prompt": {"select_account"},
	}
	http.SetCookie(w, &http.Cookie{Name: linkStateCookie, Value: state, Path: linkMicrosoftCallbackPath,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil, MaxAge: int(linkWindow.Seconds())})
	sep := "?"
	if strings.Contains(d.AuthorizeURL, "?") {
		sep = "&"
	}
	http.Redirect(w, r, d.AuthorizeURL+sep+q.Encode(), http.StatusFound)
}

// handleLinkMicrosoftCallback finishes a link. Every failure lands on the same page
// with nothing said about why; which check failed is in the audit log.
func (s *Server) handleLinkMicrosoftCallback(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	http.SetCookie(w, &http.Cookie{Name: linkStateCookie, Value: "", Path: linkMicrosoftCallbackPath,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil, MaxAge: -1})
	if s.msLink == nil || s.identity == nil {
		httpapi.Error(w, http.StatusNotFound, "linking a Microsoft account is not configured on this server")
		return
	}
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		s.linkRefuse(w, r, "the provider refused: "+e, nil)
		return
	}
	state := q.Get("state")
	if c, err := r.Cookie(linkStateCookie); err != nil || c.Value == "" || c.Value != state {
		s.linkRefuse(w, r, "state does not match this browser", nil)
		return
	}
	pending, ok := s.msLink.spend(state, now)
	if !ok {
		s.linkRefuse(w, r, "no link in flight for this state", nil)
		return
	}
	// The session finishing the flow must be the one that started it. Without this, a
	// victim lured to the callback with an attacker's code would have the attacker's
	// Microsoft account linked to their own Atlas user — and every Worker the victim
	// allowed would then act as the attacker, in the victim's name.
	if p := httpapi.PrincipalFrom(r.Context()); p == nil || p.UserID != pending.userID {
		s.linkRefuse(w, r, "the reply came back to a different Atlas session than the one that started it", nil)
		return
	}
	code := q.Get("code")
	if code == "" {
		s.linkRefuse(w, r, "the provider returned no code", nil)
		return
	}
	tokens, err := s.msLink.provider.tokenRequest(r.Context(), url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {pending.redirectURI}, "code_verifier": {pending.verifier},
	}, now)
	if err != nil {
		s.linkRefuse(w, r, "code exchange failed", err)
		return
	}
	claims, err := s.verifyLinkToken(r.Context(), tokens.IDToken, pending.nonce, now)
	if err != nil {
		s.linkRefuse(w, r, "the ID token did not verify", err)
		return
	}
	tid, _ := claims.Raw["tid"].(string)
	oid, _ := claims.Raw["oid"].(string)
	if tid != strings.TrimSpace(s.msLink.cfg.TenantID) || oid == "" {
		s.linkRefuse(w, r, "the ID token names no object id in the configured tenant", nil)
		return
	}
	conn, err := s.identity.Link(identity.LinkRequest{
		UserID: pending.userID, Provider: identity.ProviderMicrosoft, Issuer: s.msLink.cfg.issuer(),
		TenantID: tid, Subject: oid, Username: claims.PreferredUsername, DisplayName: claims.Name,
		Scopes: strings.Fields(tokens.Scope), RefreshToken: tokens.RefreshToken,
	})
	if err != nil {
		s.linkRefuse(w, r, "the link was refused", err)
		return
	}
	audit(r, logging.ConnectionLinked, "linked a Microsoft account and authorized a delegated connection",
		slog.String("connection", conn.ID), slog.String("identity", conn.IdentityKey))
	http.Redirect(w, r, linkDonePage+"?linked="+url.QueryEscape(conn.ID), http.StatusFound)
}

// verifyLinkToken verifies an ID token against the tenant's keys, refetching them once
// when the token names a key that is not held — the same retry the login makes.
func (s *Server) verifyLinkToken(ctx context.Context, raw, nonce string, now time.Time) (oidcClaims, error) {
	want := oidcExpect{issuer: s.msLink.cfg.issuer(), clientID: s.msLink.provider.cfg.ClientID, nonce: nonce, now: now}
	keys, err := s.msLink.provider.keySet(ctx, now, false)
	if err != nil {
		return oidcClaims{}, err
	}
	claims, err := verifyIDToken(raw, keys, want)
	if err != nil && strings.Contains(err.Error(), "signing key") {
		if keys, kerr := s.msLink.provider.keySet(ctx, now, true); kerr == nil {
			claims, err = verifyIDToken(raw, keys, want)
		}
	}
	return claims, err
}

func (s *Server) linkRefuse(w http.ResponseWriter, r *http.Request, what string, err error) {
	attrs := []slog.Attr{slog.String("reason", what)}
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	auditRefusal(r, logging.ConnectionLinkFailed, "an account link failed", attrs...)
	http.Redirect(w, r, linkDonePage+"?link=failed", http.StatusFound)
}

// refreshDelegatedToken is the identity service's refresher for Microsoft
// connections: a refresh-token grant at the tenant's token endpoint, for the scopes the
// connection was authorized for.
func (s *Server) refreshDelegatedToken(ctx context.Context, c identity.Connection, refreshToken string) (identity.Tokens, error) {
	if s.msLink == nil {
		return identity.Tokens{}, errors.New("linking a Microsoft account is no longer configured on this server")
	}
	if c.Provider != identity.ProviderMicrosoft || c.TenantID != strings.TrimSpace(s.msLink.cfg.TenantID) {
		return identity.Tokens{}, fmt.Errorf("connection %s belongs to a tenant this server is not configured for", c.ID)
	}
	tokens, err := s.msLink.provider.tokenRequest(ctx, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}, "scope": {s.msLink.cfg.scopes()},
	}, time.Now())
	if errors.Is(err, errProviderInvalidGrant) {
		return identity.Tokens{}, fmt.Errorf("%w: %v", identity.ErrInvalidGrant, err)
	}
	if err != nil {
		return identity.Tokens{}, err
	}
	return identity.Tokens{AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken,
		ExpiresIn: tokens.ExpiresIn, Scope: tokens.Scope}, nil
}

// configuredWorkerExists says whether a configured Worker of that name exists. Called
// on the loop by the identity service.
func (s *Server) configuredWorkerExists(name string) bool {
	if s.connectors == nil {
		return false
	}
	recs, err := s.connectors.LoadAll()
	if err != nil {
		return false
	}
	for _, r := range recs {
		if r.Name == name {
			return true
		}
	}
	return false
}
