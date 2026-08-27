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

// session is one logged-in identity. Roles and group ids are snapshotted here so a
// request can be authorized from the session alone (see Principal). Group ids are
// then kept live: a membership change pushes into the snapshot
// (ADR-0185), so it takes effect without a re-login. Roles
// remain a login-time snapshot.
type session struct {
	userID   string
	username string
	roles    []string
	groupIDs []string
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

// create opens a session for a user and returns its opaque token. Roles and group
// ids are snapshotted here so a request can be authorized from the session alone
// (see Principal). A role change takes effect on the user's next login; a group
// membership change takes effect live, pushed into the snapshot by the group
// handlers (ADR-0185).
func (s *sessionStore) create(u User, groupIDs []string) (string, error) {
	token, err := randomHex(32)
	if err != nil {
		return "", err
	}
	roles := append([]string(nil), u.Roles...)
	groups := append([]string(nil), groupIDs...)
	s.mu.Lock()
	s.byToken[token] = session{userID: u.ID, username: u.Username, roles: roles, groupIDs: groups, expires: s.now().Add(s.ttl)}
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

// setUserGroupMembership reflects a group-membership change into every live session
// of a user, so adding or removing them from a group takes effect on their next
// request without a re-login (ADR-0185). Sessions are the only
// place group ids live on the access path — principalFor never reads the group store
// — so keeping the snapshot current here is what makes membership live while
// effectiveRole stays pure. A user with no live session is a no-op: their next login
// snapshots the now-current membership.
func (s *sessionStore) setUserGroupMembership(userID, groupID string, member bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for tok, sess := range s.byToken {
		if sess.userID != userID {
			continue
		}
		if next, changed := applyGroup(sess.groupIDs, groupID, member); changed {
			sess.groupIDs = next
			s.byToken[tok] = sess
		}
	}
}

// dropGroupFromSessions removes a group id from every live session, whoever it
// belongs to — used when the group is deleted, so its grants stop applying for
// everyone at once (ADR-0185).
func (s *sessionStore) dropGroupFromSessions(groupID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for tok, sess := range s.byToken {
		if next, changed := applyGroup(sess.groupIDs, groupID, false); changed {
			sess.groupIDs = next
			s.byToken[tok] = sess
		}
	}
}

// applyGroup returns ids with groupID present (member) or absent (!member), and
// whether that changed anything. On a change it always allocates a fresh slice, so a
// session's snapshot is never aliased into another session's backing array.
func applyGroup(ids []string, groupID string, member bool) ([]string, bool) {
	has := false
	for _, id := range ids {
		if id == groupID {
			has = true
			break
		}
	}
	if member {
		if has {
			return ids, false
		}
		return append(append([]string(nil), ids...), groupID), true
	}
	if !has {
		return ids, false
	}
	next := make([]string, 0, len(ids)-1)
	for _, id := range ids {
		if id != groupID {
			next = append(next, id)
		}
	}
	return next, true
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

// InternalToken returns the internal service token a process this server started
// itself authenticates with (ADR-0049) — the supervised workers, which are handed
// it at spawn. It is empty unless auth is enabled, and is never served over any
// endpoint; only the constructing process reads it.
func (s *Server) InternalToken() string { return s.internalToken }

// servicePrincipalName is the identity a valid internal token resolves to. It is
// deliberately not an admin: what holds this token leases and settles jobs, never
// administers users, so a leaked token cannot manage accounts.
//
// The name is historical — the MCP adapter was the first holder — and is left
// alone because it is a wire value: it appears in job attribution and in an
// operator's logs, so renaming it would rewrite records that already exist.
const servicePrincipalName = "system:mcp"

// RoleDeployAgent marks the principal a deploy token resolves to: a peer Atlas
// publishing a bundle here (ADR-0129). It is deliberately not a user role — no
// account carries it, it cannot be assigned, and it grants nothing on its own.
// What it may reach is decided by deployAgentAllowed below.
const RoleDeployAgent = "deploy-agent"

// deployAgentPrincipalPrefix namespaces the synthetic user id a deploy token
// resolves to, so an audit trail says which token acted.
const deployAgentPrincipalPrefix = "system:deploy:"

// apiTokenPrincipalPrefix does the same for an API token, and is distinct so a
// trail says which kind of credential acted as well as which one.
const apiTokenPrincipalPrefix = "system:token:"

// principalFor resolves a request to a Principal, or nil. It first honors a valid
// internal bearer token (the service identity of a process this server started),
// then a deploy token, then a session cookie. It reads only the session store and
// the in-memory token indexes (never the user store), so it is safe to call from a
// handler goroutine.
//
// Every credential Atlas accepts is resolved here and nowhere else, which is what
// lets the MCP transport forward a caller's bearer or cookie without knowing
// anything about either.
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
				Scope:    apiScopeDeploy,
			}
		}
		// An API token identifies a machine an administrator issued a credential to:
		// a worker on another host, a stdio MCP adapter, a CI job
		// (ADR-0194). Same index discipline, and deliberately never an
		// admin — a machine that administers accounts is not a case Atlas has, and a
		// leaked token that could would be a much worse leak.
		if rec, ok := s.apiTokens.match(tok, time.Now().Unix()); ok {
			return &httpapi.Principal{
				UserID:   apiTokenPrincipalPrefix + rec.ID,
				Username: rec.Name,
				Scope:    rec.scope(),
			}
		}
		// An OAuth access token identifies a *person* who approved an application to
		// act as them (ADR-0200) — the one credential here that is not a machine. So
		// unlike the three above it resolves to the human's own principal, roles and
		// groups included, which is what keeps ADR-0196's property true through a
		// hosted client: a tool call is exactly as privileged as whoever made it.
		//
		// Roles come from the grant rather than from the user store, because this runs
		// on a handler goroutine and that store belongs to the run loop. What keeps
		// them honest is maintenance, not freshness: disabling or deleting the account
		// revokes its grants and a role change rewrites them (revokeUserGrants,
		// refreshUserGrants), at the same call sites that already do this for sessions.
		if g, ok := s.oauthGrants.matchAccess(tok, time.Now().Unix()); ok {
			return &httpapi.Principal{
				UserID:   g.UserID,
				Username: g.Username,
				Roles:    g.Roles,
				GroupIDs: g.GroupIDs,
				Scope:    g.scope(),
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
	return &httpapi.Principal{UserID: sess.userID, Username: sess.username, Roles: sess.roles, GroupIDs: sess.groupIDs}
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

// withAuth is the middleware that resolves a Principal for every request and, when
// enforcement is on, rejects a gated request that carries none. Resolution is
// best-effort and unconditional so that even public endpoints (e.g. /auth/me) can
// see who is calling.
//
// Which requests are gated is the policy's answer, not a rule written here. It
// used to be a path-prefix test — gated iff the path started with /api/v1 — which
// meant a route mounted anywhere else was public because of where it sat rather
// than because anyone decided it should be. access.go carries the replacement and
// the reasoning.
func (s *Server) withAuth(policy *accessPolicy, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := s.principalFor(r); p != nil {
			r = r.WithContext(httpapi.WithPrincipal(r.Context(), p))
		}
		if s.authEnabled && policy.classify(r) != accessPublic && httpapi.PrincipalFrom(r.Context()) == nil {
			// Name the scheme, so a client that holds a token knows to send it rather
			// than reading the 401 as "this endpoint is broken" — an MCP client above
			// all, now that /mcp is gated like everything else. Bearer and not Basic
			// on purpose: Basic is what makes a browser open its own credential
			// dialog over the top of the login screen.
			//
			// The challenge also points at this resource's RFC 9728 metadata, which is
			// what turns "refused, and now guess" into "refused, and here is what
			// refused you" (ADR-0200). Without it a hosted MCP client has nothing to go
			// on and tries /authorize, a route Atlas does not serve — so the operator
			// sees a 404 whose cause is nowhere in the response.
			challenge := `Bearer realm="atlas"`
			if metadata := s.resourceMetadataURL(r); metadata != "" {
				challenge += `, resource_metadata="` + metadata + `"`
			}
			w.Header().Set("WWW-Authenticate", challenge)
			httpapi.Error(w, http.StatusUnauthorized, "authentication required")
			return
		}
		// A machine credential authenticates a machine, not a user: it reaches exactly
		// the operations its scope names and nothing else, whatever the path's own
		// rules would otherwise permit. Enforced here, in one place and for every
		// scoped credential there is, so the reach of all of them is provable by
		// reading apiScopeAllowed (ADR-0129, ADR-0194).
		if p := httpapi.PrincipalFrom(r.Context()); p != nil && p.Scope != "" && !apiScopeMayReach(p.Scope, r) {
			auditRefusal(r, logging.AuthDenied, "refused: outside this credential's scope",
				slog.String("scope", p.Scope),
				slog.String("method", r.Method), slog.String("path", r.URL.Path))
			httpapi.Error(w, http.StatusForbidden,
				"this credential's scope ("+p.Scope+") does not permit "+r.Method+" "+r.URL.Path)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isAdmin reports whether this request carries the admin role, writing nothing and
// auditing nothing. It is for a handler that answers *everyone* but keeps one field
// back — a refusal there would be wrong, because nothing was refused: the caller asked
// a question they are allowed to ask and got the part of the answer that is theirs.
//
// Like requireAdmin it is true when auth is disabled: the server is then single-user
// and there is no one to keep anything from.
func (s *Server) isAdmin(r *http.Request) bool {
	if !s.authEnabled {
		return true
	}
	p := httpapi.PrincipalFrom(r.Context())
	return p != nil && p.HasRole(RoleAdmin)
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
		// Somebody who is signed in reaching for something they may not have is worth
		// a line; an anonymous request being asked to log in is not, or the signal
		// drowns in every unauthenticated probe that finds the port.
		auditRefusal(r, logging.AuthDenied, "refused: admin role required",
			slog.String("method", r.Method), slog.String("path", r.URL.Path))
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
