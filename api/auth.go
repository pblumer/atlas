package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
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

// Principal is the authenticated identity of a request. It carries a snapshot of
// the user's roles taken at login, so authorization checks read the context and
// never have to touch the user store off the run-loop goroutine. A role change
// therefore takes effect on the user's next login — an acceptable MVP tradeoff
// (ADR-0044).
type Principal struct {
	UserID   string
	Username string
	Roles    []string
}

// hasRole reports whether the principal carries the given role.
func (p *Principal) hasRole(role string) bool {
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

type principalCtxKey struct{}

func withPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalCtxKey{}, p)
}

// principalFrom returns the request's authenticated principal, or nil when the
// request is unauthenticated (auth disabled, or no valid session).
func principalFrom(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalCtxKey{}).(*Principal)
	return p
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

// principalFor resolves the request's session cookie to a Principal, or nil. It
// reads only the session store (never the user store), so it is safe to call from
// a handler goroutine.
func (s *Server) principalFor(r *http.Request) *Principal {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	sess, ok := s.sessions.lookup(c.Value)
	if !ok {
		return nil
	}
	return &Principal{UserID: sess.userID, Username: sess.username, Roles: sess.roles}
}

// requiresAuth reports whether enforcement gates a path when auth is enabled.
// Only /api/v1 is gated, minus the endpoints that must work before login: the
// login call itself, product info (the UI reads it on the login screen), and the
// OpenAPI document. The static UI and /healthz are never gated so the login
// screen can load at all.
func requiresAuth(path string) bool {
	if path != "/api/v1" && !strings.HasPrefix(path, "/api/v1/") {
		return false
	}
	switch path {
	case "/api/v1/auth/login", "/api/v1/info", "/api/v1/openapi.json":
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
			r = r.WithContext(withPrincipal(r.Context(), p))
		}
		if s.authEnabled && requiresAuth(r.URL.Path) && principalFrom(r.Context()) == nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
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
	p := principalFrom(r.Context())
	if p == nil || !p.hasRole(RoleAdmin) {
		writeError(w, http.StatusForbidden, "admin role required")
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
	if err := s.users.save(u); err != nil {
		return err
	}
	if generated {
		// Logged once, to stderr, so the operator can capture it on first boot.
		// A pre-set ATLAS_ADMIN_PASSWORD is never logged.
		log.Printf("atlas: seeded admin user %q with a generated password: %s", username, password)
	}
	return nil
}
