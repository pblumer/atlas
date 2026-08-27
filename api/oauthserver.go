package api

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
)

// The authorization server (ADR-0200).
//
// This is the half that lets a person say yes. The resource-server half
// (oauthmeta.go) says what Atlas is and how a bearer reaches it; this one turns a
// person's approval, made in their browser while signed in, into a token a hosted
// client can present.
//
// Crossing into being an authorization server is a threshold the record is
// explicit about, so the shape here is deliberately the smallest one a compliant
// client will talk to, and nothing more:
//
//   - **Authorization code with PKCE (S256), and no other grant type.** No implicit
//     flow, no resource-owner password grant, no client-credentials grant. Each of
//     those either puts a token in a URL or asks an application to handle somebody's
//     password, and neither is a thing to add without a reason to.
//   - **Pre-registered clients by default.** Registration is an admin act unless an
//     operator opens RFC 7591 self-registration (oauthregister.go), which is off
//     until they do. Client ID Metadata Documents are not built.
//   - **The token carries a person.** Not a role, not a service identity — the human
//     who approved it, so ADR-0196's property survives: a tool call is exactly as
//     privileged as whoever made it.
//
// A note on what this is *not* protecting against. The client secret authenticates
// the token exchange, but the MCP clients this exists for are hosted applications
// that hold their secret server-side, which is the case where a secret means
// something. A public client (one that cannot keep a secret) is exactly what PKCE
// covers, and PKCE is required here regardless.

const (
	// oauthAuthorizePath is where the person's browser lands, and
	// oauthTokenPath where the client exchanges. Both are published in the
	// authorization-server metadata; nothing should hard-code them elsewhere.
	oauthAuthorizePath = "/oauth/authorize"
	oauthTokenPath     = "/oauth/token"

	// oauthMetadataPath is RFC 8414's well-known location.
	oauthMetadataPath = "/.well-known/oauth-authorization-server"

	// oauthAccessLifetime is how long an access token is good for. Short because
	// the client can silently renew: what a copied token is worth is bounded by
	// this, and nothing about the person's experience depends on it being longer.
	oauthAccessLifetime = 2 * time.Hour
)

// authorizationServerMetadata is the RFC 8414 document.
//
// code_challenge_methods_supported is not decoration: the MCP specification tells
// a client to verify PKCE support here and to **refuse to proceed** if the field
// is absent. A server that omits it is not merely less good, it is one no
// compliant client will talk to.
func (s *Server) handleAuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	base := s.externalBase(r)
	doc := map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + oauthAuthorizePath,
		"token_endpoint":                        base + oauthTokenPath,
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_post"},
	}
	// Advertised only when it is served. A registration_endpoint in this document is
	// a promise, and a client that takes it and gets a 404 has been told a lie about
	// the server rather than the truth about its policy (RFC 7591, ADR-0200).
	if s.dynamicRegistration {
		doc["registration_endpoint"] = base + oauthRegisterPath
	}
	w.Header().Set("Vary", "X-Forwarded-Proto")
	httpapi.JSON(w, http.StatusOK, doc)
}

// oauthRequest is a validated authorization request.
type oauthRequest struct {
	client      oauthClient
	redirectURI string
	state       string
	challenge   string
	resource    string
}

// parseAuthorizeRequest validates the query of an authorization request.
//
// The order matters and is the one RFC 6749 §4.1.2.1 requires. Anything wrong with
// the *client or the redirect URI* is shown to the person here and never
// redirected: bouncing to an unvalidated redirect_uri is how an authorization
// endpoint becomes an open redirector, and an open redirector on this endpoint
// hands the code to whoever supplied the URI. Everything after that is the
// client's own mistake and travels back to it as an error, which is what lets a
// client show its user something better than a blank page.
func (s *Server) parseAuthorizeRequest(r *http.Request, q url.Values) (oauthRequest, string, string) {
	clientID := strings.TrimSpace(q.Get("client_id"))
	client, ok := s.oauthClients.lookup(clientID)
	if !ok {
		return oauthRequest{}, "", "unknown client_id"
	}
	redirectURI := strings.TrimSpace(q.Get("redirect_uri"))
	if redirectURI == "" || !client.allowsRedirect(redirectURI) {
		return oauthRequest{}, "", "redirect_uri is not registered for this client"
	}

	req := oauthRequest{
		client:      client,
		redirectURI: redirectURI,
		state:       q.Get("state"),
		challenge:   strings.TrimSpace(q.Get("code_challenge")),
		resource:    strings.TrimSpace(q.Get("resource")),
	}
	if q.Get("response_type") != "code" {
		return req, "unsupported_response_type", ""
	}
	// PKCE is required, not offered. RFC 7636's "plain" is not accepted; see
	// verifyPKCE for why supporting it would buy nothing.
	if req.challenge == "" || q.Get("code_challenge_method") != "S256" {
		return req, "invalid_request", ""
	}
	// RFC 8707: the client names the resource it wants the token for, and it has to
	// be one this server actually publishes metadata for. Checking it here, at
	// issuance, is what makes the audience real — see oauthGrant.Resource for why it
	// is not re-derived on every later request.
	if !s.isCanonicalResource(r, req.resource) {
		return req, "invalid_target", ""
	}
	return req, "", ""
}

// isCanonicalResource reports whether uri is one of the two this server publishes
// protected-resource metadata for: the server itself, and the MCP transport.
//
// The origin comes from externalBase, so it is the configured one when an operator
// stated it and the request's otherwise — the same answer the discovery documents
// give, which is the point: a client reads the resource out of those documents, so
// what it sends back has to be what they said.
func (s *Server) isCanonicalResource(r *http.Request, uri string) bool {
	base := s.externalBase(r)
	if uri == "" || base == "" {
		return false
	}
	if uri == base {
		return true
	}
	return s.mcpHandler != nil && uri == base+"/mcp"
}

// handleAuthorize serves the consent page to the person's browser.
//
// It is public because an unauthenticated browser has to be able to land here and
// be told to sign in — a 401 is not an answer a person can act on. The page itself
// reveals nothing until it asks the API below, and what it then reveals is the name
// of a client the person was already being sent to.
func (s *Server) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	page, err := webFS.ReadFile("web/oauth-consent.html")
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "consent page missing")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// A consent decision must be made on a page this server served, freshly: a
	// cached one could name a client the operator has since removed.
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(page)
}

// handleAuthorizeContext tells the consent page what it is asking about: whether
// the request is valid at all, who is signed in, and what the client is called.
//
// Public, because the page has to render for somebody not yet signed in. It
// discloses a registered client's display name to anyone who can guess a client
// id — which is the name that client is about to show the person anyway, and the
// id travels in the URL the client itself constructed.
func (s *Server) handleAuthorizeContext(w http.ResponseWriter, r *http.Request) {
	req, clientErr, fatal := s.parseAuthorizeRequest(r, r.URL.Query())
	if fatal != "" {
		httpapi.JSON(w, http.StatusOK, map[string]any{"error": fatal})
		return
	}
	out := map[string]any{
		"clientId":   req.client.ID,
		"clientName": req.client.Name,
		"resource":   req.resource,
		// Who vouched for this application. With self-registration open, the name above
		// is one the client chose for itself thirty seconds ago and nobody checked, and
		// the person about to decide has to be told that — see oauthregister.go. Always
		// present, so the page can rely on it rather than infer from an absent field.
		"selfRegistered": req.client.Dynamic,
	}
	if clientErr != "" {
		out["error"] = "the client's request is not valid (" + clientErr + ")"
		out["redirect"] = errorRedirect(req.redirectURI, clientErr, req.state)
	}
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		out["signedInAs"] = p.Username
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// handleApprove mints an authorization code for a signed-in person's approval, and
// returns the URL their browser should go to.
//
// Authenticated: the principal resolved here *is* the identity the grant will
// carry. The decision arrives as a POST with the request's parameters repeated,
// rather than being remembered between the GET and the POST, so there is no
// server-side flow state to expire, collide, or be confused between two tabs.
func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	if p == nil {
		httpapi.Error(w, http.StatusUnauthorized, "sign in to approve")
		return
	}
	var payload struct {
		ClientID     string `json:"clientId"`
		RedirectURI  string `json:"redirectUri"`
		State        string `json:"state"`
		Challenge    string `json:"codeChallenge"`
		Resource     string `json:"resource"`
		ResponseType string `json:"responseType"`
		Approve      bool   `json:"approve"`
	}
	if !decodeJSONBody(w, r, &payload) {
		return
	}
	q := url.Values{
		"client_id":             {payload.ClientID},
		"redirect_uri":          {payload.RedirectURI},
		"state":                 {payload.State},
		"code_challenge":        {payload.Challenge},
		"code_challenge_method": {"S256"},
		"resource":              {payload.Resource},
		"response_type":         {"code"},
	}
	req, clientErr, fatal := s.parseAuthorizeRequest(r, q)
	if fatal != "" {
		httpapi.Error(w, http.StatusBadRequest, fatal)
		return
	}
	if clientErr != "" {
		httpapi.Error(w, http.StatusBadRequest, "the client's request is not valid ("+clientErr+")")
		return
	}

	if !payload.Approve {
		auditRefusal(r, logging.AuthOAuthDenied, "oauth: the person declined",
			slog.String("client_id", req.client.ID), slog.String("resource", req.resource))
		httpapi.JSON(w, http.StatusOK, map[string]any{
			"redirect": errorRedirect(req.redirectURI, "access_denied", req.state),
		})
		return
	}

	secret, err := randomHex(32)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "generate code: "+err.Error())
		return
	}
	code := oauthCodePrefix + secret
	s.oauthCodes.issue(oauthCode{
		hash:        hashAPIToken(code),
		clientID:    req.client.ID,
		userID:      p.UserID,
		username:    p.Username,
		roles:       append([]string(nil), p.Roles...),
		groupIDs:    append([]string(nil), p.GroupIDs...),
		redirectURI: req.redirectURI,
		challenge:   req.challenge,
		resource:    req.resource,
		expires:     time.Now().Add(oauthCodeLifetime),
	})
	audit(r, logging.AuthOAuthGranted, "oauth: the person approved a client",
		slog.String("client_id", req.client.ID), slog.String("client_name", req.client.Name),
		slog.String("resource", req.resource))

	target, _ := url.Parse(req.redirectURI)
	rq := target.Query()
	rq.Set("code", code)
	if req.state != "" {
		rq.Set("state", req.state)
	}
	target.RawQuery = rq.Encode()
	httpapi.JSON(w, http.StatusOK, map[string]any{"redirect": target.String()})
}

// oauthCodePrefix marks an authorization code, so one in a log or a bug report is
// recognizable as what it is.
const oauthCodePrefix = "atlasoc_"

// errorRedirect builds the redirect that carries an OAuth error back to a client.
// The redirect URI is only ever one parseAuthorizeRequest already matched against
// the client's registered set.
func errorRedirect(redirectURI, code, state string) string {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set("error", code)
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// handleToken is the token endpoint: it exchanges an authorization code, or a
// refresh token, for a fresh pair.
//
// Public as a route, because the caller is a client authenticating with its own
// credentials rather than a person with a session. Rate-limited on the client
// address like the login is, because it takes a secret and is therefore somewhere
// to guess one (ADR-0197).
func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	if !s.publicRate.allow(httpapi.ClientIP(r)) {
		oauthError(w, http.StatusTooManyRequests, "temporarily_unavailable", "too many requests")
		return
	}
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "malformed form body")
		return
	}
	client, ok := s.oauthClients.authenticate(r.PostFormValue("client_id"), r.PostFormValue("client_secret"))
	if !ok {
		// 401 with the OAuth error code, not the API's envelope: the caller here is a
		// client library that parses this shape.
		oauthError(w, http.StatusUnauthorized, "invalid_client", "client authentication failed")
		return
	}
	switch r.PostFormValue("grant_type") {
	case "authorization_code":
		s.exchangeCode(w, r, client)
	case "refresh_token":
		s.refreshGrant(w, r, client)
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type",
			"only authorization_code and refresh_token are supported")
	}
}

// exchangeCode spends an authorization code and issues the first token pair.
func (s *Server) exchangeCode(w http.ResponseWriter, r *http.Request, client oauthClient) {
	rec, ok := s.oauthCodes.spend(r.PostFormValue("code"), time.Now())
	if !ok {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "the code is unknown, spent or expired")
		return
	}
	// Every one of these was fixed when the person approved, and is re-checked here
	// because the exchange arrives from a different party on a different connection.
	if rec.clientID != client.ID {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "the code was issued to another client")
		return
	}
	if rec.redirectURI != r.PostFormValue("redirect_uri") {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri does not match the authorization")
		return
	}
	if res := r.PostFormValue("resource"); res != "" && res != rec.resource {
		oauthError(w, http.StatusBadRequest, "invalid_target", "resource does not match the authorization")
		return
	}
	if !verifyPKCE(rec.challenge, r.PostFormValue("code_verifier")) {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "code_verifier does not match the challenge")
		return
	}

	id, err := newID()
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "generate id")
		return
	}
	grant := oauthGrant{
		ID: id, ClientID: client.ID, ClientName: client.Name,
		UserID: rec.userID, Username: rec.username,
		Roles: rec.roles, GroupIDs: rec.groupIDs,
		Resource: rec.resource, CreatedAt: time.Now().Unix(),
	}
	s.issueTokens(w, r, grant, "authorization_code")
}

// refreshGrant rotates a grant's tokens.
//
// Rotation, not reuse: the refresh token presented is replaced by a new one, so a
// copy of the old one is worthless after its first use. That is what makes a
// stolen refresh token a bounded loss rather than a standing one.
func (s *Server) refreshGrant(w http.ResponseWriter, r *http.Request, client oauthClient) {
	grant, ok := s.oauthGrants.matchRefresh(r.PostFormValue("refresh_token"))
	if !ok || grant.ClientID != client.ID {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "the refresh token is unknown or revoked")
		return
	}
	s.issueTokens(w, r, grant, "refresh_token")
}

// issueTokens mints a fresh pair onto a grant and saves it.
//
// The durable write goes first and the index second, inside one run-loop turn: a
// failure between them leaves the *old* tokens still resolving rather than a grant
// nobody can use, which is the recoverable direction — the client refreshes again.
func (s *Server) issueTokens(w http.ResponseWriter, r *http.Request, grant oauthGrant, how string) {
	accessSecret, err := randomHex(32)
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "generate token")
		return
	}
	refreshSecret, err := randomHex(32)
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "generate token")
		return
	}
	access := oauthAccessPrefix + accessSecret
	refresh := oauthRefreshPrefix + refreshSecret

	grant.AccessHash = hashAPIToken(access)
	grant.AccessExpires = time.Now().Add(oauthAccessLifetime).Unix()
	grant.RefreshHash = hashAPIToken(refresh)

	var saveErr error
	s.do(func() {
		if saveErr = s.oauthGrantStore.Save(grant); saveErr != nil {
			return
		}
		s.oauthGrants.put(grant)
	})
	if saveErr != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "save grant")
		return
	}
	audit(r, logging.AuthOAuthTokenIssued, "oauth: token issued",
		slog.String("grant_id", grant.ID), slog.String("client_id", grant.ClientID),
		slog.String("subject", grant.Username), slog.String("resource", grant.Resource),
		slog.String("how", how))

	w.Header().Set("Cache-Control", "no-store")
	httpapi.JSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    int(oauthAccessLifetime.Seconds()),
		"refresh_token": refresh,
	})
}

// oauthError writes the error shape RFC 6749 §5.2 defines, which is not the API's
// own envelope: the reader here is a client library, and it parses this one.
func oauthError(w http.ResponseWriter, status int, code, describe string) {
	w.Header().Set("Cache-Control", "no-store")
	httpapi.JSON(w, status, map[string]string{
		"error":             code,
		"error_description": describe,
	})
}
