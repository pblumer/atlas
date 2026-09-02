package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
)

// This file is the HTTP surface for identity (ADR-0044): the auth endpoints
// (login, logout, me) and the user-management CRUD. Every store access funnels
// through s.do onto the run-loop goroutine, the same discipline the other sidecar
// handlers follow, so the user store is only ever touched by a single owner.

// maxUserBytes caps a user request body. User records are tiny; this refuses a
// runaway upload without constraining any real request.
const maxUserBytes = 64 << 10 // 64 KiB

// decodeJSONBody reads a size-limited JSON body into dst, writing a 400 and
// returning false on a read or parse error. Centralizing it keeps every identity
// handler's body handling identical and in one place.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxUserBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return false
	}
	if err := json.Unmarshal(body, dst); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

// normalizeRoles trims, drops empties, and de-duplicates a role list, preserving
// order. An empty result defaults to the base user role, so every account has at
// least one role.
func normalizeRoles(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, r := range in {
		r = strings.TrimSpace(r)
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	if len(out) == 0 {
		return []string{RoleUser}
	}
	return out
}

// enabledAdminCount counts enabled admins in all, excluding excludeID (the user
// being changed). It backs the lockout guard: an operation that would drop this
// to zero — deleting, disabling, or de-admining the last admin — is refused so an
// instance can never lock every operator out.
func enabledAdminCount(all []User, excludeID string) int {
	n := 0
	for _, u := range all {
		if u.ID == excludeID || u.Disabled {
			continue
		}
		if u.hasRole(RoleAdmin) {
			n++
		}
	}
	return n
}

// handleLogin verifies a username/password and opens a session, setting the
// session cookie. The failure response is uniform ("invalid credentials") whether
// the user is unknown, disabled, or the password is wrong, so it does not leak
// which usernames exist.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSONBody(w, r, &payload) {
		return
	}
	username := strings.TrimSpace(payload.Username)
	if username == "" || payload.Password == "" {
		httpapi.Error(w, http.StatusBadRequest, "username and password are required")
		return
	}
	// Throttled before the store is touched and before bcrypt runs, so a flood
	// costs the caller a request and this server almost nothing — the ordering is
	// half the point of the throttle, because verifying a password is the expensive
	// operation an unauthenticated caller would otherwise get to trigger at will.
	if !s.logins.allow(httpapi.ClientIP(r), username) {
		auditRefusal(r, logging.AuthLoginThrottled,
			"login throttled: too many attempts for this account or from this address",
			slog.String("username", username))
		httpapi.Error(w, http.StatusTooManyRequests, "too many login attempts; try again shortly")
		return
	}
	var (
		u       User
		ok      bool
		lookErr error
	)
	s.do(func() { u, ok, lookErr = s.users.byUsername(username) })
	if lookErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "login: "+lookErr.Error())
		return
	}
	if !ok || u.Disabled || !checkPassword(u.PasswordHash, payload.Password) {
		// The reason is recorded but not returned: the response stays the one
		// uniform message, so the log tells an operator what happened without the
		// wire telling an attacker which of their guesses was closer.
		auditRefusal(r, logging.AuthLoginFailed, "failed login",
			slog.String("username", username),
			slog.String("reason", loginFailure(ok, u.Disabled)))
		httpapi.Error(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	// Snapshot the user's group ids into the session (ADR-0180),
	// alongside roles, so scope group grants resolve without a store read.
	var (
		groupIDs []string
		grpErr   error
	)
	s.do(func() { groupIDs, grpErr = s.groups.idsForUser(u.ID) })
	if grpErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "login: "+grpErr.Error())
		return
	}
	token, err := s.sessions.create(u, groupIDs)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "login: "+err.Error())
		return
	}
	setSessionCookie(w, r, token, s.sessions.ttl)
	// Proving you are this account clears the failures counted against it, so a
	// couple of mistyped passwords are not carried around for the next quarter of
	// an hour.
	s.logins.forgive(username)
	audit(r, logging.AuthLogin, "login",
		slog.String("username", u.Username), slog.String("user_id", u.ID))
	httpapi.JSON(w, http.StatusOK, u.toPublic())
}

// loginFailure names why a login was refused, for the audit line only. The
// response never distinguishes these — an unknown account and a wrong password
// answer identically, which is what stops the login from being a directory.
func loginFailure(found, disabled bool) string {
	switch {
	case !found:
		return "no such account"
	case disabled:
		return "account disabled"
	default:
		return "wrong password"
	}
}

// handleLogout ends the caller's session and clears the cookie. It is idempotent:
// logging out without a session still succeeds.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.sessions.destroy(c.Value)
	}
	// Before the cookie is cleared, while the principal is still resolvable —
	// otherwise the line would say somebody logged out without saying who.
	if httpapi.PrincipalFrom(r.Context()) != nil {
		audit(r, logging.AuthLogout, "logout")
	}
	clearSessionCookie(w, r)
	httpapi.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleMe reports whether auth is enforced and who, if anyone, is logged in. The
// UI reads it on load to decide between the login screen and the app. When auth
// is enabled the route is gated, so a nil principal only occurs with auth off (or
// a mid-session deletion), reported as {authEnabled, user:null}.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	if p == nil {
		httpapi.JSON(w, http.StatusOK, map[string]any{"authEnabled": s.authEnabled, "user": nil})
		return
	}
	var (
		u       User
		ok      bool
		loadErr error
	)
	s.do(func() { u, ok, loadErr = s.users.Get(p.UserID) })
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "me: "+loadErr.Error())
		return
	}
	if !ok {
		httpapi.JSON(w, http.StatusOK, map[string]any{"authEnabled": s.authEnabled, "user": nil})
		return
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{"authEnabled": s.authEnabled, "user": u.toPublic()})
}

// handleListUsers returns every account (public projection), oldest first, each
// annotated with whether somebody is signed in as it right now
// (ADR-0228). The annotation rides along here rather than being a
// second call the page has to make, so the roster's first paint is already
// current; /api/v1/users/presence is the same answer without the roster, for the
// refresh.
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	list := []publicUser{}
	var loadErr error
	s.do(func() {
		var recs []User
		recs, loadErr = s.users.LoadAll()
		for _, u := range recs {
			list = append(list, u.toPublic())
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list users: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, withPresence(list, s.sessions.presenceByUser()))
}

// handleListAssignableUsers returns the accounts a task can be assigned to — a
// minimal projection (username + display name) of every enabled user. Unlike the
// admin-gated management list, it is available to any authenticated caller (and
// open when auth is off): assigning work is an everyday Tasks action, not user
// administration (ADR-0045).
func (s *Server) handleListAssignableUsers(w http.ResponseWriter, _ *http.Request) {
	type assignable struct {
		Username    string `json:"username"`
		DisplayName string `json:"displayName,omitempty"`
	}
	list := []assignable{}
	var loadErr error
	s.do(func() {
		var recs []User
		recs, loadErr = s.users.LoadAll()
		for _, u := range recs {
			if u.Disabled {
				continue
			}
			list = append(list, assignable{Username: u.Username, DisplayName: u.DisplayName})
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list assignable users: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, list)
}

// handleCreateUser creates a local user. Body:
// {"username","email","displayName","password","roles":[...]}. Username is
// required and unique (case-insensitive); email, if given, is unique too; the
// password must meet the minimum length. Roles default to ["user"].
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Username    string   `json:"username"`
		Email       string   `json:"email"`
		DisplayName string   `json:"displayName"`
		Password    string   `json:"password"`
		Roles       []string `json:"roles"`
	}
	if !decodeJSONBody(w, r, &payload) {
		return
	}
	username := strings.TrimSpace(payload.Username)
	email := strings.TrimSpace(payload.Email)
	if username == "" {
		httpapi.Error(w, http.StatusBadRequest, "username is required")
		return
	}
	if len(payload.Password) < minPasswordLen {
		httpapi.Error(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	hash, err := hashPassword(payload.Password)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "create user: "+err.Error())
		return
	}
	id, err := newUserID()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "create user: "+err.Error())
		return
	}
	now := time.Now().Unix()
	rec := User{
		ID:              id,
		Username:        username,
		Email:           email,
		DisplayName:     strings.TrimSpace(payload.DisplayName),
		Roles:           normalizeRoles(payload.Roles),
		Source:          SourceLocal,
		PasswordHash:    hash,
		CreatedAt:       now,
		UpdatedAt:       now,
		RolesUpgradedAt: now,
	}
	// The uniqueness check and the write happen in one run-loop turn, so no
	// concurrent create can slip a duplicate username/email between them.
	var (
		conflict string
		saveErr  error
	)
	s.do(func() {
		if _, ok, e := s.users.byUsername(username); e != nil {
			saveErr = e
			return
		} else if ok {
			conflict = "username already taken"
			return
		}
		if email != "" {
			if _, ok, e := s.users.byEmail(email); e != nil {
				saveErr = e
				return
			} else if ok {
				conflict = "email already taken"
				return
			}
		}
		saveErr = s.users.Save(rec)
	})
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "create user: "+saveErr.Error())
		return
	}
	if conflict != "" {
		httpapi.Error(w, http.StatusConflict, conflict)
		return
	}
	audit(r, logging.AuthUserCreated, "user created", append([]slog.Attr{
		slog.String("username", rec.Username), slog.String("user_id", rec.ID),
		slog.Any("roles", rec.Roles),
	}, unenforcedAttr(rec.Roles)...)...)
	httpapi.JSON(w, http.StatusCreated, rec.toPublic())
}

// handleGetUser returns one account by id, or 404.
func (s *Server) handleGetUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var (
		u       User
		ok      bool
		loadErr error
	)
	s.do(func() { u, ok, loadErr = s.users.Get(id) })
	switch {
	case loadErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read user: "+loadErr.Error())
	case !ok:
		httpapi.Error(w, http.StatusNotFound, "no user with that id")
	default:
		httpapi.JSON(w, http.StatusOK, u.toPublic())
	}
}

// handlePatchUser updates mutable fields of a user. Absent fields are left
// unchanged (pointers distinguish "omitted" from "set to empty"). Username and
// source are immutable here; the password has its own endpoint. Changes that
// would remove the last enabled admin are refused (409) so no instance locks
// itself out; disabling a user also ends their live sessions.
func (s *Server) handlePatchUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var payload struct {
		Email       *string   `json:"email"`
		DisplayName *string   `json:"displayName"`
		Roles       *[]string `json:"roles"`
		Disabled    *bool     `json:"disabled"`
	}
	if !decodeJSONBody(w, r, &payload) {
		return
	}
	var (
		updated   User
		found     bool
		conflict  string
		lockout   bool
		opErr     error
		disabling bool
	)
	s.do(func() {
		u, ok, e := s.users.Get(id)
		if e != nil {
			opErr = e
			return
		}
		if !ok {
			return
		}
		found = true
		if payload.Email != nil {
			email := strings.TrimSpace(*payload.Email)
			if email != "" {
				if other, ok, e := s.users.byEmail(email); e != nil {
					opErr = e
					return
				} else if ok && other.ID != id {
					conflict = "email already taken"
					return
				}
			}
			u.Email = email
		}
		if payload.DisplayName != nil {
			u.DisplayName = strings.TrimSpace(*payload.DisplayName)
		}
		if payload.Roles != nil {
			u.Roles = normalizeRoles(*payload.Roles)
			// This list is now a statement under the role model, so mark it as one. Without
			// this, narrowing an account that has not yet been through upgradeLegacyRoles
			// would last exactly until the next restart, which would widen it straight back.
			u.RolesUpgradedAt = time.Now().Unix()
		}
		if payload.Disabled != nil {
			u.Disabled = *payload.Disabled
		}
		// Guard against locking everyone out: if the result is not an enabled admin
		// and no other enabled admin remains, refuse.
		if !(u.hasRole(RoleAdmin) && !u.Disabled) {
			all, e := s.users.LoadAll()
			if e != nil {
				opErr = e
				return
			}
			if enabledAdminCount(all, id) == 0 {
				// Only a problem if this user *was* the last enabled admin.
				if prev, _, _ := s.users.Get(id); prev.hasRole(RoleAdmin) && !prev.Disabled {
					lockout = true
					return
				}
			}
		}
		u.UpdatedAt = time.Now().Unix()
		if opErr = s.users.Save(u); opErr != nil {
			return
		}
		updated = u
		disabling = payload.Disabled != nil && *payload.Disabled
	})
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "update user: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no user with that id")
	case conflict != "":
		httpapi.Error(w, http.StatusConflict, conflict)
	case lockout:
		httpapi.Error(w, http.StatusConflict, "cannot remove the last enabled admin")
	default:
		if disabling {
			s.sessions.destroyUser(id)
			// A disabled account must stop acting everywhere, not only where it holds a
			// session: an OAuth grant can stand for months (ADR-0200).
			s.revokeUserGrants(id)
		} else {
			// Roles changed but the account stands: rewrite what its grants may do
			// rather than dropping them, so an administrative edit does not knock a
			// person's connector over.
			s.setUserGrantRoles(id, updated.Roles)
		}
		// Roles and the disabled flag are the two fields that change what an account
		// can do, so they are the two the line carries; the rest is a display change.
		audit(r, logging.AuthUserUpdated, "user updated", append([]slog.Attr{
			slog.String("username", updated.Username), slog.String("user_id", updated.ID),
			slog.Any("roles", updated.Roles), slog.Bool("disabled", updated.Disabled),
		}, unenforcedAttr(updated.Roles)...)...)
		httpapi.JSON(w, http.StatusOK, updated.toPublic())
	}
}

// handleSetUserPassword replaces a user's password. Body: {"password":"..."}.
func (s *Server) handleSetUserPassword(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var payload struct {
		Password string `json:"password"`
	}
	if !decodeJSONBody(w, r, &payload) {
		return
	}
	if len(payload.Password) < minPasswordLen {
		httpapi.Error(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	hash, err := hashPassword(payload.Password)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "set password: "+err.Error())
		return
	}
	var (
		found bool
		opErr error
	)
	s.do(func() {
		u, ok, e := s.users.Get(id)
		if e != nil {
			opErr = e
			return
		}
		if !ok {
			return
		}
		found = true
		u.PasswordHash = hash
		u.UpdatedAt = time.Now().Unix()
		opErr = s.users.Save(u)
	})
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "set password: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no user with that id")
	default:
		// That it happened and to whom. Never the password, and never a hash — a
		// hash in a log is a credential in a log with an extra step.
		audit(r, logging.AuthPasswordSet, "password set for user", slog.String("user_id", id))
		httpapi.JSON(w, http.StatusOK, map[string]any{"id": id})
	}
}

// handleDeleteUser removes a user. Deleting the last enabled admin is refused
// (409) so an instance can't lock itself out; a successful delete also ends the
// user's live sessions.
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var (
		found   bool
		lockout bool
		opErr   error
	)
	s.do(func() {
		u, ok, e := s.users.Get(id)
		if e != nil {
			opErr = e
			return
		}
		if !ok {
			return
		}
		found = true
		if u.hasRole(RoleAdmin) && !u.Disabled {
			all, e := s.users.LoadAll()
			if e != nil {
				opErr = e
				return
			}
			if enabledAdminCount(all, id) == 0 {
				lockout = true
				return
			}
		}
		opErr = s.users.Delete(id)
	})
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "delete user: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no user with that id")
	case lockout:
		httpapi.Error(w, http.StatusConflict, "cannot delete the last enabled admin")
	default:
		s.sessions.destroyUser(id)
		s.revokeUserGrants(id)
		audit(r, logging.AuthUserDeleted, "user deleted", slog.String("user_id", id))
		w.WriteHeader(http.StatusNoContent)
	}
}
