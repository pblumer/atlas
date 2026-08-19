package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
)

// This file is the authentication boundary (ADR-0044). It is deliberately thin
// and swappable: handlers depend on a resolved *Principal in the request context,
// never on "a cookie" or "a session", so the mechanism that produces the
// Principal (local password + server session today; OIDC/JWT bearer tokens later)
// can change without touching a single handler.
//
// Enforcement is opt-in. WithAuth turns it on; by default the server behaves
// exactly as it always has — fully open, single-user — mirroring how --docs gates
// the API explorer (ADR-0043). Enabling auth is an operator decision, not a
// breaking change forced on every existing deployment.

// minPasswordLen is the shortest local password we accept. A floor, not a policy
// engine — richer password rules are an enterprise concern deferred to ADR-0044's
// trajectory, not the MVP.
const minPasswordLen = 8

// hashPassword returns a bcrypt hash of a plaintext password. bcrypt embeds its
// cost and salt in the digest, so a future cost bump or algorithm swap is a
// verify-time detail, not a schema change.
func hashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("auth: hash password: %w", err)
	}
	return string(b), nil
}

// checkPassword reports whether plain matches a stored bcrypt hash. An empty hash
// (an external-identity user with no local password) never matches.
func checkPassword(hash, plain string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// randomHex returns n cryptographically random bytes as a hex string. It backs
// both session tokens and generated bootstrap passwords.
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: random: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// newUserID mints a stable, opaque, unguessable user id. The "usr_" prefix keeps
// ids self-describing in logs and URLs; the random suffix guarantees uniqueness
// without a central counter.
func newUserID() (string, error) {
	suffix, err := randomHex(12)
	if err != nil {
		return "", err
	}
	return "usr_" + suffix, nil
}

// sessionCookie is the name of the opaque session cookie.
const sessionCookie = "atlas_session"

// defaultSessionTTL is how long a login lasts. Sessions are ephemeral (in memory
// only), so a server restart logs everyone out — a documented MVP limitation, not
// a durability bug (ADR-0044).
const defaultSessionTTL = 12 * time.Hour

// session is one logged-in identity. Roles are snapshotted here so a request can
// be authorized from the session alone (see Principal).
type session struct {
	userID   string
	username string
	roles    []string
	expires  time.Time
}

// sessionStore holds live sessions in memory. Unlike the durable sidecar stores,
// it is touched from concurrent HTTP handler goroutines (not the run loop), so it
// guards itself with a mutex. It is not engine state and never persists, so it
// sits outside the single-writer invariant entirely.
type sessionStore struct {
	mu      sync.Mutex
	byToken map[string]session
	ttl     time.Duration
	now     func() time.Time
}

func newSessionStore(ttl time.Duration) *sessionStore {
	return &sessionStore{byToken: map[string]session{}, ttl: ttl, now: time.Now}
}

// create opens a session for a user and returns its opaque token.
func (s *sessionStore) create(u User) (string, error) {
	token, err := randomHex(32)
	if err != nil {
		return "", err
	}
	roles := append([]string(nil), u.Roles...)
	s.mu.Lock()
	s.byToken[token] = session{userID: u.ID, username: u.Username, roles: roles, expires: s.now().Add(s.ttl)}
	s.mu.Unlock()
	return token, nil
}

// lookup returns the session for a token if it exists and has not expired. An
// expired session is dropped on read so the map self-cleans on use.
func (s *sessionStore) lookup(token string) (session, bool) {
	if token == "" {
		return session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.byToken[token]
	if !ok {
		return session{}, false
	}
	if !s.now().Before(sess.expires) {
		delete(s.byToken, token)
		return session{}, false
	}
	return sess, true
}

// destroy removes a session (logout). Removing a missing token is a no-op.
func (s *sessionStore) destroy(token string) {
	if token == "" {
		return
	}
	s.mu.Lock()
	delete(s.byToken, token)
	s.mu.Unlock()
}

// destroyUser drops every live session for a user id. Deleting or disabling a
// user calls this so the change takes effect immediately rather than lingering
// until the snapshotted session expires.
func (s *sessionStore) destroyUser(userID string) {
	s.mu.Lock()
	for tok, sess := range s.byToken {
		if sess.userID == userID {
			delete(s.byToken, tok)
		}
	}
	s.mu.Unlock()
}

// WithAuth turns on authentication enforcement: /api/v1 requires a valid session
// (except the login call, product info, and the OpenAPI doc), and managing users
// requires the admin role. Off by default, so an existing single-binary
// deployment is unaffected until an operator opts in (ADR-0044).
func WithAuth() Option { return func(s *Server) { s.authEnabled = true } }

// setSessionCookie writes the session cookie. It is HttpOnly and SameSite=Lax,
// and Secure whenever the request arrived over TLS (so it still works on a plain
// http:// dev server while never leaking over http on a real TLS deployment).
func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		MaxAge:   int(ttl.Seconds()),
	})
}

// clearSessionCookie expires the session cookie in the client.
func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		MaxAge:   -1,
	})
}

// InternalToken returns the internal service token used by the in-process MCP
// adapter to authenticate its loopback calls (ADR-0049). It is empty unless auth
// is enabled. It is never served over any endpoint; only the constructing process
// reads it, to hand to its own MCP client.
func (s *Server) InternalToken() string { return s.internalToken }

// servicePrincipalName is the identity a valid internal token resolves to. It is
// deliberately not an admin: the MCP adapter drives deploy/run/query, never user
// administration, so a leaked token cannot manage accounts.
const servicePrincipalName = "system:mcp"

// RoleDeployAgent marks the principal a deploy token resolves to: a peer Atlas
// publishing a bundle here (ADR-0129). It is deliberately not a user role — no
// account carries it, it cannot be assigned, and it grants nothing on its own.
// What it may reach is decided by deployAgentAllowed below.
const RoleDeployAgent = "deploy-agent"

// deployAgentPrincipalPrefix namespaces the synthetic user id a deploy token
// resolves to, so an audit trail says which token acted.
const deployAgentPrincipalPrefix = "system:deploy:"

// deployAgentAllowed is the complete set of operations a deploy token may reach:
// push a bundle, and read back what this server now runs for that application.
// Both are what ADR-0129 scoped the credential to — a publisher has to see the
// result of what it shipped, or its own per-target view is blind.
//
// This is a fail-closed allowlist rather than per-handler rejection, and that is
// the point: a deploy token is a credential handed to another machine, so the
// blast radius of it leaking must be provable by reading one short list, not by
// auditing every handler for a role check somebody might have forgotten to add.
//
// Matching goes through an http.ServeMux rather than hand-rolled path comparison,
// so wildcards are resolved by the same matcher that routes the real request —
// hand-written path parsing is exactly where an allowlist springs a leak.
var deployAgentAllowed = []string{
	"POST /api/v1/applications/import",
	"GET /api/v1/applications/{id}/deployments",
}

// deployAgentMux resolves a request against deployAgentAllowed. It carries no
// handlers; only whether a pattern matched is consulted.
var deployAgentMux = func() *http.ServeMux {
	m := http.NewServeMux()
	nop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for _, pattern := range deployAgentAllowed {
		m.Handle(pattern, nop)
	}
	return m
}()

// deployAgentMayReach reports whether a deploy token may perform this request.
// An unmatched request yields an empty pattern, so anything not listed is refused.
func deployAgentMayReach(r *http.Request) bool {
	_, pattern := deployAgentMux.Handler(r)
	return pattern != ""
}

// isDeployAgent reports whether a principal is a peer authenticated by a deploy
// token rather than a person or the in-process MCP adapter.
func isDeployAgent(p *httpapi.Principal) bool { return p != nil && p.HasRole(RoleDeployAgent) }

// principalFor resolves a request to a Principal, or nil. It first honors a valid
// internal bearer token (the MCP adapter's service identity), then a session
// cookie. It reads only the session store and the in-memory token (never the user
// store), so it is safe to call from a handler goroutine.
func (s *Server) principalFor(r *http.Request) *httpapi.Principal {
	if tok, ok := bearerToken(r); ok {
		if s.internalToken != "" &&
			subtle.ConstantTimeCompare([]byte(tok), []byte(s.internalToken)) == 1 {
			return &httpapi.Principal{UserID: servicePrincipalName, Username: servicePrincipalName}
		}
		// A deploy token identifies a peer Atlas publishing here (ADR-0129). The
		// index is an in-memory, mutex-guarded mirror of the durable records, so this
		// stays safe to call from a handler goroutine and costs no disk read.
		if rec, ok := s.deployTokens.match(tok); ok {
			return &httpapi.Principal{
				UserID:   deployAgentPrincipalPrefix + rec.ID,
				Username: rec.Name,
				Roles:    []string{RoleDeployAgent},
			}
		}
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	sess, ok := s.sessions.lookup(c.Value)
	if !ok {
		return nil
	}
	return &httpapi.Principal{UserID: sess.userID, Username: sess.username, Roles: sess.roles}
}

// bearerToken extracts the token from an "Authorization: Bearer <token>" header,
// reporting ok=false when the header is absent or not a bearer credential.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	return h[len(prefix):], true
}

// requiresAuth reports whether enforcement gates a path when auth is enabled.
// Only /api/v1 is gated, minus the endpoints that must work before login: the
// login call itself, product info (the UI reads it on the login screen), and the
// OpenAPI document. The static UI, /healthz and /readyz are never gated so the
// login screen can load at all and a probe that carries no session still works.
func requiresAuth(path string) bool {
	if path != "/api/v1" && !strings.HasPrefix(path, "/api/v1/") {
		return false
	}
	switch path {
	case "/api/v1/auth/login", "/api/v1/info", "/api/v1/openapi.json":
		return false
	case "/api/v1/settings/theme", "/api/v1/settings/registration":
		// The brand accent (theme) and the self-service registration link
		// (ADR-0126) are both read by the login screen before authentication. Only
		// GET is served pre-auth; PUT/DELETE re-check the admin role in the handler,
		// so writes stay gated even though the path is public here.
		return false
	}
	return true
}

// withAuth is the middleware that resolves a Principal for every request and, when
// enforcement is on, rejects a gated request that carries none. Resolution is
// best-effort and unconditional so that even public endpoints (e.g. /auth/me) can
// see who is calling.
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := s.principalFor(r); p != nil {
			r = r.WithContext(httpapi.WithPrincipal(r.Context(), p))
		}
		if s.authEnabled && requiresAuth(r.URL.Path) && httpapi.PrincipalFrom(r.Context()) == nil {
			httpapi.Error(w, http.StatusUnauthorized, "authentication required")
			return
		}
		// A deploy token authenticates a peer, not a user: it may reach exactly the
		// operations on the allowlist and nothing else, whatever the path's own rules
		// would otherwise permit. Enforced here, in one place, so the credential's
		// reach is provable by reading deployAgentAllowed (ADR-0129).
		if p := httpapi.PrincipalFrom(r.Context()); isDeployAgent(p) && !deployAgentMayReach(r) {
			httpapi.Error(w, http.StatusForbidden, "a deploy token may only publish an application bundle and read its deployments")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireAdmin enforces the one authorization rule the MVP ships: managing users
// needs the admin role. When auth is disabled the server is open (single-user
// mode), so there is nothing to check and it returns true. When enabled it
// demands an admin principal, writing 403 and returning false otherwise.
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !s.authEnabled {
		return true
	}
	p := httpapi.PrincipalFrom(r.Context())
	if p == nil || !p.HasRole(RoleAdmin) {
		httpapi.Error(w, http.StatusForbidden, "admin role required")
		return false
	}
	return true
}

// bootstrapAdmin seeds an initial admin on a fresh instance (no users yet) so an
// operator who turns on auth can actually log in. The username comes from
// ATLAS_ADMIN_USERNAME (default "admin"); the password from ATLAS_ADMIN_PASSWORD,
// or, if that is unset, a strong random password generated and logged once — we
// never ship a hardcoded default credential. It runs on the constructing
// goroutine before the run loop starts, so it may touch the store directly, the
// same discipline as loadDeployments.
func (s *Server) bootstrapAdmin(now int64) error {
	n, err := s.users.count()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	username := strings.TrimSpace(os.Getenv("ATLAS_ADMIN_USERNAME"))
	if username == "" {
		username = "admin"
	}
	password := os.Getenv("ATLAS_ADMIN_PASSWORD")
	generated := false
	if password == "" {
		password, err = randomHex(12)
		if err != nil {
			return err
		}
		generated = true
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	id, err := newUserID()
	if err != nil {
		return err
	}
	u := User{
		ID:           id,
		Username:     username,
		Roles:        []string{RoleAdmin},
		Source:       SourceLocal,
		PasswordHash: hash,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.users.Save(u); err != nil {
		return err
	}
	if generated {
		// Logged once so the operator can capture it on first boot; a pre-set
		// ATLAS_ADMIN_PASSWORD is never logged. The password deliberately stays inside
		// the message rather than becoming an attribute: an attribute is what a log
		// shipper extracts, indexes and keeps (ADR-0142 — no secret becomes a field).
		logging.Warn(logging.AuthAdminSeeded,
			fmt.Sprintf("seeded admin user with a generated password: %s — capture it now, "+
				"it is not shown again", password),
			slog.String("username", username))
	}
	return nil
}
